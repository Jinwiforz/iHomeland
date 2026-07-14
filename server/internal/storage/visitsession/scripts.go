package visitsession

import redisclient "github.com/redis/go-redis/v9"

// luaHelpers 统一校验owner hashes的schema、字段数、累计编码预算与正TTL，损坏数据一律fail closed。
const luaHelpers = `
local function valid_ttl(key)
  return redis.call('PTTL', key) > 0
end

local function within_budget(names, values, maximum)
  local encoded = 0
  for index, name in ipairs(names) do
    encoded = encoded + string.len(name) + string.len(values[index])
  end
  return encoded > 0 and encoded <= tonumber(maximum)
end

local function read_active(key, version, world, maximum)
  if redis.call('EXISTS', key) == 0 then return nil, 'not_found' end
  if redis.call('HLEN', key) ~= 4 or not valid_ttl(key) then return nil, 'defect' end
  local values = redis.call('HMGET', key, 'v', 'visit_id', 'world', 'expires_us')
  if not values[1] or values[1] ~= version or not values[2] or not values[3] or values[3] ~= world or not values[4] then
    return nil, 'defect'
  end
  if not within_budget({'v', 'visit_id', 'world', 'expires_us'}, values, maximum) then return nil, 'defect' end
  return values, nil
end

local function read_session(key, version, maximum)
  if redis.call('EXISTS', key) == 0 then return nil, 'not_found' end
  if redis.call('HLEN', key) ~= 8 or not valid_ttl(key) then return nil, 'defect' end
  local values = redis.call('HMGET', key, 'v', 'visit_id', 'world', 'revision', 'lifecycle', 'expires_us', 'facts', 'payload')
  if not values[1] or values[1] ~= version or not values[2] or not values[3] or not values[4] or not values[5] or not values[6] or not values[7] or not values[8] then
    return nil, 'defect'
  end
  if not within_budget({'v', 'visit_id', 'world', 'revision', 'lifecycle', 'expires_us', 'facts', 'payload'}, values, maximum) then return nil, 'defect' end
  return values, nil
end

local function read_command(key, version, maximum)
  if redis.call('EXISTS', key) == 0 then return nil, 'not_found' end
  if redis.call('HLEN', key) ~= 8 or not valid_ttl(key) then return nil, 'defect' end
  local values = redis.call('HMGET', key, 'v', 'kind', 'fingerprint', 'visit_id', 'world', 'revision', 'expires_us', 'payload')
  if not values[1] or values[1] ~= version or not values[2] or not values[3] or not values[4] or not values[5] or not values[6] or not values[7] or not values[8] then
    return nil, 'defect'
  end
  if not within_budget({'v', 'kind', 'fingerprint', 'visit_id', 'world', 'revision', 'expires_us', 'payload'}, values, maximum) then return nil, 'defect' end
  return values, nil
end
`

// createScript 先决议全局command，再原子创建world active index和candidate。
//
// KEYS依次为command、active、candidate session；ARGV依次为schema version、fingerprint、
// VisitSessionID、PersonalWorldID、revision、lifecycle、领域expiry(UTC Unix微秒)、immutable
// facts、snapshot payload、物理expiry(UTC Unix毫秒)、session key前缀、完整create result，
// 以及command/active/session Hash的累计字节预算。
// 位置顺序是Go/Lua共同协议，调整时必须同步Store调用、reply parser与integration tests。
var createScript = redisclient.NewScript(luaHelpers + `
local command, command_error = read_command(KEYS[1], ARGV[1], ARGV[13])
if command_error == 'defect' then return {'defect'} end
if command then
  if command[2] ~= 'create' or command[3] ~= ARGV[2] then return {'idempotency_conflict'} end
  return {'replay', command[8], command[4], command[5], command[6], command[7]}
end

local active, active_error = read_active(KEYS[2], ARGV[1], ARGV[4], ARGV[14])
if active_error == 'defect' then return {'defect'} end
if active then
  local existing_key = ARGV[11] .. active[2]
  local existing, existing_error = read_session(existing_key, ARGV[1], ARGV[15])
  if existing_error or existing[2] ~= active[2] or existing[3] ~= ARGV[4] or existing[5] == 'closed' or existing[6] ~= active[4] then
    return {'defect'}
  end
  return {'existing', existing[8], existing[2], existing[3], existing[4], existing[5], existing[6], existing[7]}
end
if redis.call('EXISTS', KEYS[3]) ~= 0 then return {'defect'} end

redis.call('HSET', KEYS[3], 'v', ARGV[1], 'visit_id', ARGV[3], 'world', ARGV[4], 'revision', ARGV[5], 'lifecycle', ARGV[6], 'expires_us', ARGV[7], 'facts', ARGV[8], 'payload', ARGV[9])
redis.call('PEXPIREAT', KEYS[3], ARGV[10])
redis.call('HSET', KEYS[2], 'v', ARGV[1], 'visit_id', ARGV[3], 'world', ARGV[4], 'expires_us', ARGV[7])
redis.call('PEXPIREAT', KEYS[2], ARGV[10])
redis.call('HSET', KEYS[1], 'v', ARGV[1], 'kind', 'create', 'fingerprint', ARGV[2], 'visit_id', ARGV[3], 'world', ARGV[4], 'revision', ARGV[5], 'expires_us', ARGV[7], 'payload', ARGV[12])
redis.call('PEXPIREAT', KEYS[1], ARGV[10])
return {'created', ARGV[12]}
`)

// resolveActiveScript 在同一执行点读取index与完整session，避免close竞态产生混合投影。
//
// KEYS只含active key；ARGV依次为schema version、PersonalWorldID、session key前缀，
// 以及active/session Hash的累计字节预算。
var resolveActiveScript = redisclient.NewScript(luaHelpers + `
local active, active_error = read_active(KEYS[1], ARGV[1], ARGV[2], ARGV[4])
if active_error then return {active_error} end
local session_key = ARGV[3] .. active[2]
local current, current_error = read_session(session_key, ARGV[1], ARGV[5])
if current_error or current[2] ~= active[2] or current[3] ~= ARGV[2] or current[5] == 'closed' or current[6] ~= active[4] then
  return {'defect'}
end
return {'found', current[8], current[2], current[3], current[4], current[5], current[6], current[7]}
`)

// findScript 原子读取payload与TTL，缺失TTL不会被降级为合法snapshot。
//
// KEYS只含session key，ARGV依次为schema version与session Hash累计字节预算；
// 领域deadline仍由application解释。
var findScript = redisclient.NewScript(luaHelpers + `
local current, current_error = read_session(KEYS[1], ARGV[1], ARGV[2])
if current_error then return {current_error} end
return {'found', current[8], current[2], current[3], current[4], current[5], current[6], current[7]}
`)

// commitScript 原子决议command/revision并保存target、result与active index变化。
//
// KEYS依次为command与current session；ARGV依次为schema version、fingerprint、
// VisitSessionID、expected revision、是否携带target、target ID/world/revision/lifecycle、领域
// expiry(UTC Unix微秒)、immutable facts、target payload、完整mutation result、物理expiry
// (UTC Unix毫秒)、active key前缀，以及command/session/active Hash的累计字节预算。
// 位置顺序是Go/Lua共同协议，禁止只改单侧索引。
var commitScript = redisclient.NewScript(luaHelpers + `
local command, command_error = read_command(KEYS[1], ARGV[1], ARGV[16])
if command_error == 'defect' then return {'defect'} end
if command then
  if command[2] ~= 'mutation' or command[3] ~= ARGV[2] then return {'idempotency_conflict'} end
  return {'replay', command[8], command[4], command[5], command[6], command[7]}
end

local current, current_error = read_session(KEYS[2], ARGV[1], ARGV[17])
if current_error == 'not_found' then return {'not_found'} end
if current_error or current[2] ~= ARGV[3] then return {'defect'} end
if current[4] ~= ARGV[4] then return {'revision_conflict'} end
if ARGV[5] ~= '1' then return {'invalid_state'} end

local active_key = ARGV[15] .. current[3]
local active, active_error = read_active(active_key, ARGV[1], current[3], ARGV[18])
if active_error or active[2] ~= ARGV[3] or active[4] ~= current[6] then return {'defect'} end
if ARGV[6] ~= ARGV[3] or ARGV[7] ~= current[3] or ARGV[8] == ARGV[4] or ARGV[10] ~= current[6] or ARGV[11] ~= current[7] then return {'defect'} end

redis.call('HSET', KEYS[2], 'v', ARGV[1], 'visit_id', ARGV[6], 'world', ARGV[7], 'revision', ARGV[8], 'lifecycle', ARGV[9], 'expires_us', ARGV[10], 'facts', ARGV[11], 'payload', ARGV[12])
redis.call('PEXPIREAT', KEYS[2], ARGV[14])
redis.call('HSET', KEYS[1], 'v', ARGV[1], 'kind', 'mutation', 'fingerprint', ARGV[2], 'visit_id', ARGV[6], 'world', ARGV[7], 'revision', ARGV[8], 'expires_us', ARGV[10], 'payload', ARGV[13])
redis.call('PEXPIREAT', KEYS[1], ARGV[14])
if ARGV[9] == 'closed' then
  redis.call('DEL', active_key)
else
  redis.call('PEXPIREAT', active_key, ARGV[14])
end
return {'applied', ARGV[13]}
`)
