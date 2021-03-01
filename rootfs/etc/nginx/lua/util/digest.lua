local resty_str = require("resty.string")
local resty_sha1 = require("resty.sha1")
local resty_md5 = require("resty.md5")

local _M = {}

local function hash_digest(hash_factory, message)
  local hash = hash_factory:new()
  if not hash then
    return nil, "failed to create object"
  end
  local ok = hash:update(message)
  if not ok then
    return nil, "failed to add data"
  end
  local binary_digest = hash:final()
  if binary_digest == nil then
    return nil, "failed to create digest"
  end
  return resty_str.to_hex(binary_digest), nil
end

function _M.sha1_digest(message)
  return hash_digest(resty_sha1, message)
end

function _M.md5_digest(message)
  return hash_digest(resty_md5, message)
end

return _M