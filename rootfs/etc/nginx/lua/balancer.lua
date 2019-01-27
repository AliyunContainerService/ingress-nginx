local ngx_balancer = require("ngx.balancer")
local json = require("cjson")
local util = require("util")
local dns_util = require("util.dns")
local configuration = require("configuration")
local round_robin = require("balancer.round_robin")
local chash = require("balancer.chash")
local chashsubset = require("balancer.chashsubset")
local sticky = require("balancer.sticky")
local ewma = require("balancer.ewma")
local ck = require("resty.cookie")

-- measured in seconds
-- for an Nginx worker to pick up the new list of upstream peers
-- it will take <the delay until controller POSTed the backend object to the Nginx endpoint> + BACKENDS_SYNC_INTERVAL
local BACKENDS_SYNC_INTERVAL = 1

local DEFAULT_LB_ALG = "round_robin"
local IMPLEMENTATIONS = {
  round_robin = round_robin,
  chash = chash,
  chashsubset = chashsubset,
  sticky = sticky,
  ewma = ewma,
}

local _M = {}
local balancers = {}
local alternative_backends = {}
local servers = {}

local function get_implementation(backend)
  local name = backend["load-balance"] or DEFAULT_LB_ALG

  if backend["sessionAffinityConfig"] and backend["sessionAffinityConfig"]["name"] == "cookie" then
    name = "sticky"
  elseif backend["upstreamHashByConfig"] and backend["upstreamHashByConfig"]["upstream-hash-by"] then
    if backend["upstreamHashByConfig"]["upstream-hash-by-subset"] then
      name = "chashsubset"
    else
      name = "chash"
    end
  end

  local implementation = IMPLEMENTATIONS[name]
  if not implementation then
    ngx.log(ngx.WARN, string.format("%s is not supported, falling back to %s", backend["load-balance"], DEFAULT_LB_ALG))
    implementation = IMPLEMENTATIONS[DEFAULT_LB_ALG]
  end

  return implementation
end

local function resolve_external_names(original_backend)
  local backend = util.deepcopy(original_backend)
  local endpoints = {}
  for _, endpoint in ipairs(backend.endpoints) do
    local ips = dns_util.resolve(endpoint.address)
    for _, ip in ipairs(ips) do
      table.insert(endpoints, { address = ip, port = endpoint.port })
    end
  end
  backend.endpoints = endpoints
  return backend
end

local function format_ipv6_endpoints(endpoints)
  local formatted_endpoints = {}
  for _, endpoint in ipairs(endpoints) do
    local formatted_endpoint = endpoint
    if not endpoint.address:match("^%d+.%d+.%d+.%d+$") then
      formatted_endpoint.address = string.format("[%s]", endpoint.address)
    end
    table.insert(formatted_endpoints, formatted_endpoint)
  end
  return formatted_endpoints
end

local function sync_backend(backend)
  if not backend.endpoints or #backend.endpoints == 0 then
    ngx.log(ngx.INFO, string.format("there is no endpoint for backend %s. Skipping...", backend.name))
    return
  end

  local implementation = get_implementation(backend)
  local balancer = balancers[backend.name]

  if not balancer then
    balancers[backend.name] = implementation:new(backend)
    return
  end

  -- every implementation is the metatable of its instances (see .new(...) functions)
  -- here we check if `balancer` is the instance of `implementation`
  -- if it is not then we deduce LB algorithm has changed for the backend
  if getmetatable(balancer) ~= implementation then
    ngx.log(ngx.INFO,
      string.format("LB algorithm changed from %s to %s, resetting the instance", balancer.name, implementation.name))
    balancers[backend.name] = implementation:new(backend)
    return
  end

  local service_type = backend.service and backend.service.spec and backend.service.spec["type"]
  if service_type == "ExternalName" then
    backend = resolve_external_names(backend)
  end

  backend.endpoints = format_ipv6_endpoints(backend.endpoints)

  balancer:sync(backend)
end

local function sync_servers()
  local servers_data = configuration.get_servers_data()
  if not servers_data then
    servers = {}
    return
  end

  local ok, new_servers = pcall(json.decode, servers_data)
  if not ok then
    ngx.log(ngx.ERR, "could not parse servers data: " .. tostring(new_servers))
    return
  end

  local servers_to_keep = {}
  for _, new_server in ipairs(new_servers) do
    local server_name = new_server.hostname

    local new_locations = {}
    for _, location in ipairs(new_server.locations) do
      local new_location = {
        path = location.path or "",
        backend = location.backend or "",
        namespace = location.luaBackend.namespace or "",
        ingress_name = location.luaBackend.ingressName or "",
        service_name = location.luaBackend.serviceName or "",
        service_port = location.luaBackend.servicePort or ""
      }
      new_locations[new_location.path] = new_location
    end

    local server_conf = {
      hostname = server_name or "",
      alias = new_server.alias or "",
      locations = new_locations,
    }

    servers[server_name] = server_conf
    servers_to_keep[server_name] = server_conf

    -- handle alias server name
    if new_server.alias then
      servers[new_server.alias] = server_conf
      servers_to_keep[new_server.alias] = server_conf
    end
  end

  for server_name, _ in pairs(servers) do
    if not servers_to_keep[server_name] then
      servers[server_name] = nil
    end
  end
end

local function sync_backends()
  local backends_data = configuration.get_backends_data()
  if not backends_data then
    balancers = {}
    alternative_backends = {}
    return
  end

  local ok, new_backends = pcall(json.decode, backends_data)
  if not ok then
    ngx.log(ngx.ERR,  "could not parse backends data: " .. tostring(new_backends))
    return
  end

  local balancers_to_keep = {}
  for _, new_backend in ipairs(new_backends) do
    sync_backend(new_backend)

    if new_backend.alternativeBackends then
      alternative_backends[new_backend.name] = new_backend.alternativeBackends
    else
      alternative_backends[new_backend.name] = nil
    end

    if new_backend.endpoints and #new_backend.endpoints > 0 then
      balancers_to_keep[new_backend.name] = balancers[new_backend.name]
    end
  end

  for backend_name, _ in pairs(balancers) do
    if not balancers_to_keep[backend_name] then
      balancers[backend_name] = nil
      alternative_backends[backend_name] = nil
    end
  end
end

local function route_to_alternative_balancer(balancer)
  if not balancer.alternative_backends then
    return false
  end

  -- TODO: support traffic shaping for n > 1 alternative backends
  local backend_name = balancer.alternative_backends[1]
  if not backend_name then
    ngx.log(ngx.ERR, "empty alternative backend")
    return false
  end

  local alternative_balancer = balancers[backend_name]
  if not alternative_balancer then
    ngx.log(ngx.ERR, "no alternative balancer for backend: " .. tostring(backend_name))
    return false
  end

  local traffic_shaping_policy =  alternative_balancer.traffic_shaping_policy
  if not traffic_shaping_policy then
    ngx.log(ngx.ERR, "traffic shaping policy is not set for balanacer of backend: " .. tostring(backend_name))
    return false
  end

  local target_header = util.replace_special_char(traffic_shaping_policy.header, "-", "_")
  local header = ngx.var["http_" .. target_header]
  if header then
    if header == "always" then
      return true
    elseif header == "never" then
      return false
    end
  end

  local target_cookie = traffic_shaping_policy.cookie
  local cookie = ngx.var["cookie_" .. target_cookie]
  if cookie then
    if cookie == "always" then
      return true
    elseif cookie == "never" then
      return false
    end
  end

  if math.random(100) <= traffic_shaping_policy.weight then
    return true
  end

  return false
end

local function set_alternative_release_backend_cookie(cookie_name, cookie_value)
  if not cookie_name or not cookie_value then
    return
  end

  local current_phase = ngx.get_phase()
  if current_phase ~= "balancer" then
    return
  end

  local cookie, err = ck:new()
  if not cookie then
    ngx.log(ngx.ERR, "error while initializing cookie: " .. tostring(err))
  end

  local ok
  ok, err = cookie:set({
    key = cookie_name,
    value = cookie_value,
    path = ngx.var.location_path,
    httponly = true,
    secure = ngx.var.https == "on",
    max_age = tonumber("28800"),
  })

  if not ok then
    ngx.log(ngx.ERR, err)
  end
end

local function shuffle_alternative_release_balancer(primary_backend_name, alternative_backend_name)
  local primary_balancer = balancers[primary_backend_name]
  local alternative_balancer = balancers[alternative_backend_name]

  if primary_balancer then
    return primary_balancer, primary_backend_name
  end

  if alternative_balancer then
    return alternative_balancer, alternative_backend_name
  end

  return nil, nil
end

local function route_to_alternative_release_balancer(balancer, current_backend_name)
  if not balancer.alternative_backends then
    return nil, nil
  end

  -- TODO: support traffic shaping for n > 1 alternative backends
  local alternative_backend = balancer.alternative_backends[1]
  if not alternative_backend then
    ngx.log(ngx.ERR, "empty alternative backends for " .. current_backend_name)
    return nil, nil
  end

  local traffic_shaping_policy = balancer.traffic_shaping_policy
  if not traffic_shaping_policy then
    ngx.log(ngx.ERR, "traffic shaping policy is not set for " .. current_backend_name)
    return nil, nil
  end

  -- parse alternative traffic shaping policy
  local alternative_weight_enabled = false
  local alternative_weight_percent = -1
  if traffic_shaping_policy.serviceWeight then
    alternative_weight_enabled = true
    if traffic_shaping_policy.serviceWeight[alternative_backend] then
      alternative_weight_percent = traffic_shaping_policy.serviceWeight[alternative_backend]
    elseif traffic_shaping_policy.serviceWeight[current_backend_name] then
      alternative_weight_percent = 100 - traffic_shaping_policy.serviceWeight[current_backend_name]
    end
  end

  local alternative_match_enabled = false
  local alternative_match_config, current_match_config
  if traffic_shaping_policy.serviceMatch then
    alternative_match_enabled = true
    if traffic_shaping_policy.serviceMatch[alternative_backend] then
      alternative_match_config = traffic_shaping_policy.serviceMatch[alternative_backend]
    elseif traffic_shaping_policy.serviceMatch[current_backend_name] then
      current_match_config = traffic_shaping_policy.serviceMatch[current_backend_name]
    end
  end

  -- check the target balancer cookie
  local cookie, err = ck:new()
  if not cookie then
    ngx.log(ngx.ERR, "error while initializing cookie: " .. tostring(err))
  end

  local backend_cookie_name, backend_cookie_value
  if traffic_shaping_policy.hostPath then
    backend_cookie_name, err = util.md5_digest(traffic_shaping_policy.hostPath)
    backend_cookie_value, err = cookie:get(backend_cookie_name)
    if backend_cookie_value then -- specify the upstream backend
      local target_backend_name = backend_cookie_value
      local target_balancer = balancer

      -- check whether the target_backend_name is valid
      if target_backend_name == current_backend_name then
        target_balancer, target_backend_name = shuffle_alternative_release_balancer(current_backend_name, alternative_backend)
      elseif target_backend_name == alternative_backend then
        target_balancer, target_backend_name = shuffle_alternative_release_balancer(alternative_backend, current_backend_name)
      else -- invalid upstream backend name
        return nil, nil
      end

      if alternative_weight_enabled then
        set_alternative_release_backend_cookie(backend_cookie_name, target_backend_name)
      end

      return target_balancer, target_backend_name
    end
  end

  -- check alternative backend service match
  if alternative_match_enabled then
    local match_config = alternative_match_config
    if not match_config then
      match_config = current_match_config
    end

    local request_value
    if match_config.ticket == "header" then
      local target_header = util.replace_special_char(match_config.key, "-", "_")
      request_value = ngx.var["http_" .. target_header]
    elseif match_config.ticket == "cookie" then
      local target_cookie = match_config.key
      request_value = ngx.var["cookie_" .. target_cookie]
    elseif match_config.ticket == "query" then
      local target_query = match_config.key
      request_value = ngx.var["arg_" .. target_query]
    end

    if not request_value then
      request_value = "" -- empty string
    end

    local match_success = false
    if match_config.pattern == "exact" then
      match_success = (request_value == match_config.value)
    elseif match_config.pattern == "regex" then
      local match, _ = ngx.re.match(request_value, match_config.value)
      match_success = (match ~= nil)
    end

    if match_config == alternative_match_config then
      if not match_success then
        return shuffle_alternative_release_balancer(current_backend_name, alternative_backend)
      end

      if not alternative_weight_enabled then
        return shuffle_alternative_release_balancer(alternative_backend, current_backend_name)
      end
    elseif match_config == current_match_config then
      if not match_success then
        return shuffle_alternative_release_balancer(alternative_backend, current_backend_name)
      end

      if not alternative_weight_enabled then
        return shuffle_alternative_release_balancer(current_backend_name, alternative_backend)
      end
    end
  end

  -- check alternative backend service weight
  if alternative_weight_enabled then
    local target_backend_name = current_backend_name
    local target_balancer = balancer

    if alternative_weight_percent <= 0 then
      target_balancer, target_backend_name = shuffle_alternative_release_balancer(current_backend_name, alternative_backend)
    elseif alternative_weight_percent >= 100 then
      target_balancer, target_backend_name = shuffle_alternative_release_balancer(alternative_backend, current_backend_name)
    elseif math.random(100) <= alternative_weight_percent then
      target_balancer, target_backend_name = shuffle_alternative_release_balancer(alternative_backend, current_backend_name)
    end

    set_alternative_release_backend_cookie(backend_cookie_name, target_backend_name)
    return target_balancer, target_backend_name
  end

  return nil, nil
end

local function get_balancer()
  local backend_name = ngx.var.proxy_upstream_name

  local balancer = balancers[backend_name]
  if not balancer then
    local alternatives = alternative_backends[backend_name]
    if not alternatives or #alternatives == 0 then
      return nil, nil
    end

    -- TODO: support traffic shaping for n > 1 alternative backends
    local alternative_backend = alternatives[1]
    if balancers[alternative_backend] then
      return balancers[alternative_backend], alternative_backend
    end

    return nil, nil
  end

  local release_balancer, release_backend_name = route_to_alternative_release_balancer(balancer, backend_name)
  if release_balancer then
    return release_balancer, release_backend_name
  end

  if route_to_alternative_balancer(balancer) then
    local alternative_balancer = balancers[balancer.alternative_backends[1]]
    return alternative_balancer, balancer.alternative_backends[1]
  end

  return balancer, backend_name
end

function _M.init_worker()
  sync_backends() -- when worker starts, sync backends without delay
  sync_servers() -- when worker starts, sync servers without delay
  local _, err = ngx.timer.every(BACKENDS_SYNC_INTERVAL, sync_backends)
  if err then
    ngx.log(ngx.ERR, string.format("error when setting up timer.every for sync_backends: %s", tostring(err)))
  end
  _, err = ngx.timer.every(BACKENDS_SYNC_INTERVAL, sync_servers)
  if err then
    ngx.log(ngx.ERR, string.format("error when setting up timer.every for sync_servers: %s", tostring(err)))
  end
end

function _M.rewrite()
  --local balancer = get_balancer()
  --if not balancer then
  --  ngx.status = ngx.HTTP_SERVICE_UNAVAILABLE
  --  return ngx.exit(ngx.status)
  --end
end

function _M.balance()
  -- get the current proxy upstream balancer
  local balancer = balancers[ngx.var.proxy_upstream_name]
  if not balancer then
    return
  end

  local peer = balancer:balance()
  if not peer then
    ngx.log(ngx.WARN, "no peer was returned, balancer: " .. balancer.name)
    return
  end

  ngx_balancer.set_more_tries(1)

  local ok, err = ngx_balancer.set_current_peer(peer)
  if not ok then
    ngx.log(ngx.ERR, string.format("error while setting current upstream peer %s: %s", peer, err))
  end
end

function _M.log()
  -- get the current proxy upstream balancer
  local balancer = balancers[ngx.var.proxy_upstream_name]
  if not balancer then
    return
  end

  if not balancer.after_balance then
    return
  end

  balancer:after_balance()
end

local function shuffle_server()
  -- TODO support regex match for hostname
  local server_conf = servers[ngx.var.host]
  if not server_conf then
    return
  end

  local target_location_object = nil
  local target_location_priority = 0
  for path, location in pairs(server_conf.locations) do
    local match, _ = ngx.re.match(ngx.var.request_uri, path)
    if match ~= nil then
      if string.len(path) >= target_location_priority then
        target_location_priority = string.len(path)
        target_location_object = location
      end
    end
  end

  if not target_location_object then
    return
  end

  if target_location_object.backend then
    ngx.var.proxy_upstream_name = target_location_object.backend
    ngx.var.location_path = target_location_object.path
    ngx.var.namespace = target_location_object.namespace
    ngx.var.ingress_name = target_location_object.ingress_name
    ngx.var.service_name = target_location_object.service_name
    ngx.var.service_port = target_location_object.service_port
  end
end

function _M.access()
  shuffle_server()

  local balancer, balancer_name = get_balancer()
  if not balancer then -- not fond endpoints
    ngx.status = ngx.HTTP_SERVICE_UNAVAILABLE
    return ngx.exit(ngx.status)
  end

  -- set the current proxy upstream name
  ngx.var.proxy_upstream_name = balancer_name
end

if _TEST then
  _M.get_implementation = get_implementation
  _M.sync_backend = sync_backend
end

return _M
