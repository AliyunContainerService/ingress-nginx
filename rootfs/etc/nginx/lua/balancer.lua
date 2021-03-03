local ngx_balancer = require("ngx.balancer")
local cjson = require("cjson.safe")
local util = require("util")
local digest_util = require("util.digest")
local dns_lookup = require("util.dns").lookup
local configuration = require("configuration")
local round_robin = require("balancer.round_robin")
local chash = require("balancer.chash")
local chashsubset = require("balancer.chashsubset")
local sticky_balanced = require("balancer.sticky_balanced")
local sticky_persistent = require("balancer.sticky_persistent")
local ewma = require("balancer.ewma")
local ck = require("resty.cookie")
local ip_util = require("resty.iputils")
local string = string
local ipairs = ipairs
local table = table
local getmetatable = getmetatable
local tostring = tostring
local pairs = pairs
local math = math
local ngx = ngx

-- measured in seconds
-- for an Nginx worker to pick up the new list of upstream peers
-- it will take <the delay until controller POSTed the backend object to the
-- Nginx endpoint> + BACKENDS_SYNC_INTERVAL
local BACKENDS_SYNC_INTERVAL = 1
local BACKENDS_FORCE_SYNC_INTERVAL = 30

local DEFAULT_LB_ALG = "round_robin"
local IMPLEMENTATIONS = {
  round_robin = round_robin,
  chash = chash,
  chashsubset = chashsubset,
  sticky_balanced = sticky_balanced,
  sticky_persistent = sticky_persistent,
  ewma = ewma,
}

local _M = {}
local balancers = {}
local backends_with_external_name = {}
local backends_last_synced_at = 0

local alternative_backends = {}
local servers = {}

local function get_implementation(backend)
  local name = backend["load-balance"] or DEFAULT_LB_ALG

  if backend["sessionAffinityConfig"] and
     backend["sessionAffinityConfig"]["name"] == "cookie" then
    if backend["sessionAffinityConfig"]["mode"] == 'persistent' then
      name = "sticky_persistent"
    else
      name = "sticky_balanced"
    end

  elseif backend["upstreamHashByConfig"] and
         backend["upstreamHashByConfig"]["upstream-hash-by"] then
    if backend["upstreamHashByConfig"]["upstream-hash-by-subset"] then
      name = "chashsubset"
    else
      name = "chash"
    end
  end

  local implementation = IMPLEMENTATIONS[name]
  if not implementation then
    ngx.log(ngx.WARN, backend["load-balance"], "is not supported, ",
            "falling back to ", DEFAULT_LB_ALG)
    implementation = IMPLEMENTATIONS[DEFAULT_LB_ALG]
  end

  return implementation
end

local function sync_server(server)
  local server_locations = {}

  for _, location in ipairs(server.locations) do
    local server_location = {
      path = location.path,
      backend = location.backend,
      luaBackend = location.luaBackend,
      rewrite = location.rewrite,
      redirect = location.redirect,
      whitelist = location.whitelist
    }

    server_locations[server_location.path] = server_location
  end

  local server_conf = {
    hostname = server.hostname or "",
    aliases = server.aliases or "",
    locations = server_locations,
  }

  return server_conf
end

local function sync_servers()
  local servers_data = configuration.get_servers_data()
  if not servers_data then
    servers = {}
    return
  end

  local new_servers, err = cjson.decode(servers_data)
  if not new_servers then
    ngx.log(ngx.ERR, "could not parse vservers data: ", err)
    return
  end

  local servers_to_keep = {}
  for _, new_server in ipairs(new_servers) do
    local server_name = new_server.hostname
    local server_conf = sync_server(new_server)

    servers[server_name] = server_conf
    servers_to_keep[server_name] = server_conf

    -- handle aliases server name
    if new_server.aliases then
      for _, alias in ipairs(new_server.aliases) do
        servers[alias] = server_conf
        servers_to_keep[alias] = server_conf
      end
    end
  end

  for server_name, _ in pairs(servers) do
    if not servers_to_keep[server_name] then
      servers[server_name] = nil
    end
  end
end

local function resolve_external_names(original_backend)
  local backend = util.deepcopy(original_backend)
  local endpoints = {}
  for _, endpoint in ipairs(backend.endpoints) do
    local ips = dns_lookup(endpoint.address)
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

local function is_backend_with_external_name(backend)
  local serv_type = backend.service and backend.service.spec
                      and backend.service.spec["type"]
  return serv_type == "ExternalName"
end

local function sync_backend(backend)
  if not backend.endpoints or #backend.endpoints == 0 then
    balancers[backend.name] = nil
    return
  end

  if is_backend_with_external_name(backend) then
    backend = resolve_external_names(backend)
  end

  backend.endpoints = format_ipv6_endpoints(backend.endpoints)

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
        string.format("LB algorithm changed from %s to %s, resetting the instance",
                      balancer.name, implementation.name))
    balancers[backend.name] = implementation:new(backend)
    return
  end

  balancer:sync(backend)
end

local function sync_backends_with_external_name()
  for _, backend_with_external_name in pairs(backends_with_external_name) do
    sync_backend(backend_with_external_name)
  end
end

local function sync_backends()
  local raw_backends_last_synced_at = configuration.get_raw_backends_last_synced_at()
  ngx.update_time()
  local current_timestamp = ngx.time()
  if current_timestamp - backends_last_synced_at < BACKENDS_FORCE_SYNC_INTERVAL
      and raw_backends_last_synced_at <= backends_last_synced_at then
    return
  end

  local backends_data = configuration.get_backends_data()
  if not backends_data then
    balancers = {}
    alternative_backends = {}
    return
  end

  local new_backends, err = cjson.decode(backends_data)
  if not new_backends then
    ngx.log(ngx.ERR, "could not parse backends data: ", err)
    return
  end

  local balancers_to_keep = {}
  for _, new_backend in ipairs(new_backends) do
    if is_backend_with_external_name(new_backend) then
      local backend_with_external_name = util.deepcopy(new_backend)
      backends_with_external_name[backend_with_external_name.name] = backend_with_external_name
    else
      sync_backend(new_backend)
      if new_backend.alternativeBackends then
        alternative_backends[new_backend.name] = new_backend.alternativeBackends
      else
        alternative_backends[new_backend.name] = nil
      end
    end
    if new_backend.endpoints and #new_backend.endpoints > 0 then
      balancers_to_keep[new_backend.name] = true
    end
  end

  for backend_name, _ in pairs(balancers) do
    if not balancers_to_keep[backend_name] then
      balancers[backend_name] = nil
      backends_with_external_name[backend_name] = nil
      alternative_backends[backend_name] = nil
    end
  end
  backends_last_synced_at = raw_backends_last_synced_at
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
    ngx.log(ngx.ERR, "no alternative balancer for backend: ",
            tostring(backend_name))
    return false
  end

  local traffic_shaping_policy =  alternative_balancer.traffic_shaping_policy
  if not traffic_shaping_policy then
    ngx.log(ngx.ERR, "traffic shaping policy is not set for balancer ",
            "of backend: ", tostring(backend_name))
    return false
  end

  local target_header = util.replace_special_char(traffic_shaping_policy.header,
                                                  "-", "_")
  local header = ngx.var["http_" .. target_header]
  if header then
    if traffic_shaping_policy.headerValue
	   and #traffic_shaping_policy.headerValue > 0 then
      if traffic_shaping_policy.headerValue == header then
        return true
      end
    elseif traffic_shaping_policy.headerPattern
       and #traffic_shaping_policy.headerPattern > 0 then
      local m, err = ngx.re.match(header, traffic_shaping_policy.headerPattern)
      if m then
        return true
      elseif  err then
          ngx.log(ngx.ERR, "error when matching canary-by-header-pattern: '",
                  traffic_shaping_policy.headerPattern, "', error: ", err)
          return false
      end
    elseif header == "always" then
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
    backend_cookie_name, err = digest_util.md5_digest(traffic_shaping_policy.hostPath)
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
  if ngx.ctx.balancer then
    return ngx.ctx.balancer
  end

  local backend_name = ngx.var.proxy_upstream_name

  local balancer = balancers[backend_name]
  if not balancer then
    local alternatives = alternative_backends[backend_name]
    if not alternatives or #alternatives == 0 then
      return nil
    end

    -- TODO: support traffic shaping for n > 1 alternative backends
    local alternative_backend = alternatives[1]
    if balancers[alternative_backend] then
      ngx.var.proxy_alternative_upstream_name = alternative_backend
      ngx.ctx.balancer = balancers[alternative_backend]
      return balancers[alternative_backend]
    end

    return nil
  end

  local release_balancer, release_backend_name = route_to_alternative_release_balancer(balancer, backend_name)
  if release_balancer then
    ngx.var.proxy_alternative_upstream_name = release_backend_name
    ngx.ctx.balancer = release_balancer
    return release_balancer
  end

  if route_to_alternative_balancer(balancer) then
    local alternative_backend_name = balancer.alternative_backends[1]
    ngx.var.proxy_alternative_upstream_name = alternative_backend_name

    balancer = balancers[alternative_backend_name]
  end

  ngx.ctx.balancer = balancer

  return balancer
end

local function handle_server_request()
  local server_conf = servers[ngx.var.host]
  if not server_conf then -- check wildcard host name --
    local wildcard_host, _, err = ngx.re.sub(ngx.var.host, "^[^\\.]+\\.", "*.", "jo")
    if err then
      ngx.log(ngx.ERR, "error when handle server request: ", tostring(err))
      return
    end

    if wildcard_host then
      server_conf = servers[wildcard_host]
    end

    if not server_conf then
      return
    end
  end


  ngx.log(ngx.INFO, "dynamic server hostname: ", server_conf.hostname)
  local target_location_object = nil
  local target_location_priority = 0
  for path, location in pairs(server_conf.locations) do
    -- TODO regex match and maybe need to check useRegex
    local match, _ = ngx.re.match(ngx.var.request_uri, path)
    if match ~= nil then -- find the longest matching path
      if string.len(path) >= target_location_priority then
        target_location_priority = string.len(path)
        target_location_object = location
      end
    end
  end

  if not target_location_object then
    return -- not found location
  end

  -- configure nginx variables --
  if target_location_object.backend then
    ngx.var.proxy_upstream_name = target_location_object.backend
    ngx.var.location_path = target_location_object.path
  end
  ngx.log(ngx.INFO, "dynamic server location: ", target_location_object.path)
  ngx.log(ngx.INFO, "dynamic server upstream: ", ngx.var.proxy_upstream_name)

  if target_location_object.luaBackend then
    ngx.var.namespace = target_location_object.luaBackend["namespace"]
    ngx.var.ingress_name = target_location_object.luaBackend["ingressName"]
    ngx.var.service_name = target_location_object.luaBackend["serviceName"]
    ngx.var.service_port = target_location_object.luaBackend["servicePort"]
  end

  -- process ip white list configuration --
  if target_location_object.whitelist then
    local location_whitelist = target_location_object.whitelist["cidr"] or {}
    if location_whitelist and #location_whitelist > 0 then
      local parsed_whitelist = ip_util.parse_cidrs(location_whitelist)
      if not ip_util.ip_in_cidrs(ngx.var.the_real_ip, parsed_whitelist) then
        return ngx.exit(ngx.HTTP_FORBIDDEN)
      end
    end
  end

  -- process redirect configuration --
  if target_location_object.redirect then
    local redirect_url = target_location_object.redirect["url"] or ""
    if redirect_url ~= "" then
      local redirect_code = target_location_object.redirect["code"] or 0
      if redirect_code == 0 then
        redirect_code = ngx.HTTP_MOVED_TEMPORARILY
      end
      return ngx.redirect(redirect_url, redirect_code)
    end
  end

  -- process rewrite configuration --
  if target_location_object.rewrite then
    local rewrite_app_root = target_location_object.rewrite["appRoot"] or ""
    if rewrite_app_root ~= "" and ("/" == ngx.var.uri) then
      return ngx.redirect(rewrite_app_root, ngx.HTTP_MOVED_TEMPORARILY)
    end

    -- TODO check no-tls-redirect-locations list
    local rewrite_force_ssl_redirect = target_location_object.rewrite["forceSSLRedirect"] or false
    if rewrite_force_ssl_redirect and ngx.var.scheme ~= "https" then
      local redirect_uri = "https://" .. ngx.var.best_http_host .. ngx.var.request_uri
      return ngx.redirect(redirect_uri, ngx.HTTP_PERMANENT_REDIRECT)
    end

    -- TODO check no-tls-redirect-locations list
    local rewrite_ssl_redirect = target_location_object.rewrite["sslRedirect"] or false
    if rewrite_ssl_redirect and ngx.var.scheme ~= "https" then
      local redirect_uri = "https://" .. ngx.var.best_http_host .. ngx.var.request_uri
      return ngx.redirect(redirect_uri, ngx.HTTP_PERMANENT_REDIRECT)
    end

    local rewrite_target = target_location_object.rewrite["target"] or ""
    if rewrite_target ~= "" then
      local location_path = "^" .. target_location_object.path
      local rewrite_uri = ngx.re.sub(ngx.var.uri, location_path, rewrite_target, "o")
      ngx.req.set_uri(rewrite_uri)
    end
  end
end

function _M.init_worker()
  -- when worker starts, sync non ExternalName backends without delay
  sync_backends()
  -- when worker starts, sync servers without delay
  sync_servers()
  -- we call sync_backends_with_external_name in timer because for endpoints that require
  -- DNS resolution it needs to use socket which is not available in
  -- init_worker phase
  local ok, err = ngx.timer.at(0, sync_backends_with_external_name)
  if not ok then
    ngx.log(ngx.ERR, "failed to create timer: ", err)
  end

  ok, err = ngx.timer.every(BACKENDS_SYNC_INTERVAL, sync_backends)
  if not ok then
    ngx.log(ngx.ERR, "error when setting up timer.every for sync_backends: ", err)
  end
  ok, err = ngx.timer.every(BACKENDS_SYNC_INTERVAL, sync_servers)
  if not ok then
    ngx.log(ngx.ERR, "error when setting up timer.every for sync_servers: ", err)
  end
  ok, err = ngx.timer.every(BACKENDS_SYNC_INTERVAL, sync_backends_with_external_name)
  if not ok then
    ngx.log(ngx.ERR, "error when setting up timer.every for sync_backends_with_external_name: ",
            err)
  end

  ip_util.enable_lrucache() -- initialize ip white list cache
end

function _M.rewrite()
  -- support dynamic server update
  handle_server_request()

  local balancer = get_balancer()
  if not balancer then
    ngx.status = ngx.HTTP_SERVICE_UNAVAILABLE
    return ngx.exit(ngx.status)
  end
end

function _M.balance()
  local balancer = get_balancer()
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
    ngx.log(ngx.ERR, "error while setting current upstream peer ", peer,
            ": ", err)
  end
end

function _M.log()
  local balancer = get_balancer()
  if not balancer then
    return
  end

  if not balancer.after_balance then
    return
  end

  balancer:after_balance()
end

setmetatable(_M, {__index = {
  get_implementation = get_implementation,
  sync_backend = sync_backend,
  route_to_alternative_balancer = route_to_alternative_balancer,
  get_balancer = get_balancer,
}})

return _M
