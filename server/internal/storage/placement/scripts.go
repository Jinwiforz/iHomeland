package placement

// luaPrelude 提供纯字符串 decimal/stamp/schema 校验；generation、fence 与 microseconds
// 永不经过 tonumber，避免 Redis Lua 的 IEEE-754 double 破坏 uint64 identity。
const luaPrelude = `
local function canonical_decimal(value)
  if not value or value == '' then return false end
  if #value > 1 and string.sub(value, 1, 1) == '0' then return false end
  return string.match(value, '^%d+$') ~= nil
end
local function decimal_compare(left, right)
  if #left < #right then return -1 end
  if #left > #right then return 1 end
  if left == right then return 0 end
  if left < right then return -1 end
  return 1
end
local function valid_id(value, prefix)
  if not value or #value <= #prefix or #value > 128 then return false end
  if string.sub(value, 1, #prefix) ~= prefix then return false end
  return string.match(string.sub(value, #prefix + 1), '^[A-Za-z0-9]+$') ~= nil
end
local function current_complete(key)
  if redis.call('HLEN', key) ~= 9 then return false end
  if redis.call('PTTL', key) < 0 then return false end
  local version = redis.call('HGET', key, 'v')
  local world = redis.call('HGET', key, 'world')
  local instance = redis.call('HGET', key, 'instance')
  local node = redis.call('HGET', key, 'node')
  local generation = redis.call('HGET', key, 'generation')
  local fence = redis.call('HGET', key, 'fence')
  local phase = redis.call('HGET', key, 'phase')
  local created = redis.call('HGET', key, 'created_us')
  local expires = redis.call('HGET', key, 'expires_us')
  return version == '1' and valid_id(world, 'pworld_') and valid_id(instance, 'winst_') and
    valid_id(node, 'rnode_') and canonical_decimal(generation) and generation ~= '0' and
    canonical_decimal(fence) and fence ~= '0' and (phase == 'starting' or phase == 'active') and
    canonical_decimal(created) and created ~= '0' and canonical_decimal(expires) and expires ~= '0' and
    decimal_compare(expires, created) > 0
end
local function replay_complete(key)
  if redis.call('HLEN', key) ~= 12 then return false end
  if redis.call('PTTL', key) < 0 then return false end
  local operation = redis.call('HGET', key, 'operation')
  local fingerprint = redis.call('HGET', key, 'fingerprint')
  local outcome = redis.call('HGET', key, 'outcome')
  if not (operation == 'acquire' or operation == 'activate' or operation == 'renew' or operation == 'revoke' or operation == 'replace') then return false end
  if not fingerprint or #fingerprint ~= 64 or string.match(fingerprint, '^[0-9a-f]+$') == nil then return false end
  if outcome ~= 'applied' then return false end
  return redis.call('HGET', key, 'v') == '1' and
    valid_id(redis.call('HGET', key, 'world'), 'pworld_') and
    valid_id(redis.call('HGET', key, 'instance'), 'winst_') and
    valid_id(redis.call('HGET', key, 'node'), 'rnode_') and
    canonical_decimal(redis.call('HGET', key, 'generation')) and
    canonical_decimal(redis.call('HGET', key, 'fence')) and
    (redis.call('HGET', key, 'phase') == 'starting' or redis.call('HGET', key, 'phase') == 'active') and
    canonical_decimal(redis.call('HGET', key, 'created_us')) and
    canonical_decimal(redis.call('HGET', key, 'expires_us'))
end
local function same_stamp(key, world, instance, node, generation, fence)
  return redis.call('HGET', key, 'world') == world and
    redis.call('HGET', key, 'instance') == instance and
    redis.call('HGET', key, 'node') == node and
    redis.call('HGET', key, 'generation') == generation and
    redis.call('HGET', key, 'fence') == fence
end
local function reply_hash(code, key)
  local values = redis.call('HGETALL', key)
  table.insert(values, 1, code)
  return values
end
local function write_current(key, world, instance, node, generation, fence, phase, created, expires, expiry_ms)
  redis.call('HSET', key,
    'v', '1', 'world', world, 'instance', instance, 'node', node,
    'generation', generation, 'fence', fence, 'phase', phase,
    'created_us', created, 'expires_us', expires)
  redis.call('PEXPIREAT', key, tonumber(expiry_ms))
end
local function write_replay(key, operation, fingerprint, source, ttl_ms)
  redis.call('HSET', key,
    'v', '1', 'operation', operation, 'fingerprint', fingerprint, 'outcome', 'applied',
    'world', redis.call('HGET', source, 'world'),
    'instance', redis.call('HGET', source, 'instance'),
    'node', redis.call('HGET', source, 'node'),
    'generation', redis.call('HGET', source, 'generation'),
    'fence', redis.call('HGET', source, 'fence'),
    'phase', redis.call('HGET', source, 'phase'),
    'created_us', redis.call('HGET', source, 'created_us'),
    'expires_us', redis.call('HGET', source, 'expires_us'))
  redis.call('PEXPIRE', key, tonumber(ttl_ms))
end
`

// acquireScript 原子解析有效 current，或用本次新 allocation 发布 starting assignment。
const acquireScript = luaPrelude + `
if redis.call('EXISTS', KEYS[2]) == 1 then
  if not replay_complete(KEYS[2]) then return {'defect'} end
  if redis.call('HGET', KEYS[2], 'operation') ~= 'acquire' then return {'defect'} end
  if redis.call('HGET', KEYS[2], 'fingerprint') ~= ARGV[12] then return {'conflict'} end
  return reply_hash('replay', KEYS[2])
end
if redis.call('EXISTS', KEYS[1]) == 1 then
  if not current_complete(KEYS[1]) then return {'defect'} end
  if decimal_compare(redis.call('HGET', KEYS[1], 'expires_us'), ARGV[1]) > 0 then
    if redis.call('HGET', KEYS[1], 'phase') == 'active' then return reply_hash('existing', KEYS[1]) end
    return reply_hash('in_progress', KEYS[1])
  end
end
write_current(KEYS[1], ARGV[2], ARGV[3], ARGV[4], ARGV[5], ARGV[6], ARGV[7], ARGV[8], ARGV[9], ARGV[10])
write_replay(KEYS[2], 'acquire', ARGV[12], KEYS[1], ARGV[11])
return reply_hash('applied', KEYS[1])
`

// activateScript 只把完整匹配且未到期的 starting assignment 发布为 active。
const activateScript = luaPrelude + `
if redis.call('EXISTS', KEYS[2]) == 1 then
  if not replay_complete(KEYS[2]) then return {'defect'} end
  if redis.call('HGET', KEYS[2], 'operation') ~= 'activate' then return {'defect'} end
  if redis.call('HGET', KEYS[2], 'fingerprint') ~= ARGV[8] then return {'conflict'} end
  return reply_hash('replay', KEYS[2])
end
if redis.call('EXISTS', KEYS[1]) == 0 then return {'not_found'} end
if not current_complete(KEYS[1]) then return {'defect'} end
if not same_stamp(KEYS[1], ARGV[2], ARGV[3], ARGV[4], ARGV[5], ARGV[6]) then return {'conflict'} end
if decimal_compare(redis.call('HGET', KEYS[1], 'expires_us'), ARGV[1]) <= 0 then return {'expired'} end
if redis.call('HGET', KEYS[1], 'phase') == 'active' then return reply_hash('replay', KEYS[1]) end
redis.call('HSET', KEYS[1], 'phase', 'active')
write_replay(KEYS[2], 'activate', ARGV[8], KEYS[1], ARGV[7])
return reply_hash('applied', KEYS[1])
`

// renewScript 在完整 stamp 仍 current 且未到期时严格推进 lease expiry。
const renewScript = luaPrelude + `
if redis.call('EXISTS', KEYS[2]) == 1 then
  if not replay_complete(KEYS[2]) then return {'defect'} end
  if redis.call('HGET', KEYS[2], 'operation') ~= 'renew' then return {'defect'} end
  if redis.call('HGET', KEYS[2], 'fingerprint') ~= ARGV[10] then return {'conflict'} end
  return reply_hash('replay', KEYS[2])
end
if redis.call('EXISTS', KEYS[1]) == 0 then return {'not_found'} end
if not current_complete(KEYS[1]) then return {'defect'} end
if not same_stamp(KEYS[1], ARGV[2], ARGV[3], ARGV[4], ARGV[5], ARGV[6]) then return {'conflict'} end
local current_expiry = redis.call('HGET', KEYS[1], 'expires_us')
if decimal_compare(current_expiry, ARGV[1]) <= 0 then return {'expired'} end
if ARGV[7] == current_expiry then return reply_hash('replay', KEYS[1]) end
if decimal_compare(ARGV[7], current_expiry) <= 0 then return {'conflict'} end
redis.call('HSET', KEYS[1], 'expires_us', ARGV[7])
redis.call('PEXPIREAT', KEYS[1], tonumber(ARGV[8]))
write_replay(KEYS[2], 'renew', ARGV[10], KEYS[1], ARGV[9])
return reply_hash('applied', KEYS[1])
`

// revokeScript 删除完整匹配 current，并在独立 key 保存旧 snapshot 的有界 replay。
const revokeScript = luaPrelude + `
if redis.call('EXISTS', KEYS[2]) == 1 then
  if not replay_complete(KEYS[2]) then return {'defect'} end
  if redis.call('HGET', KEYS[2], 'operation') ~= 'revoke' then return {'defect'} end
  if redis.call('HGET', KEYS[2], 'fingerprint') ~= ARGV[8] then return {'conflict'} end
  return reply_hash('replay', KEYS[2])
end
if redis.call('EXISTS', KEYS[1]) == 0 then return {'not_found'} end
if not current_complete(KEYS[1]) then return {'defect'} end
if not same_stamp(KEYS[1], ARGV[2], ARGV[3], ARGV[4], ARGV[5], ARGV[6]) then return {'conflict'} end
write_replay(KEYS[2], 'revoke', ARGV[8], KEYS[1], ARGV[7])
local result = reply_hash('applied', KEYS[2])
redis.call('DEL', KEYS[1])
return result
`

// replaceScript 以 break-before-make 线性化点把 predecessor 替换为已分配 successor。
const replaceScript = luaPrelude + `
if redis.call('EXISTS', KEYS[2]) == 1 then
  if not replay_complete(KEYS[2]) then return {'defect'} end
  if redis.call('HGET', KEYS[2], 'operation') ~= 'replace' then return {'defect'} end
  if redis.call('HGET', KEYS[2], 'fingerprint') ~= ARGV[16] then return {'conflict'} end
  return reply_hash('replay', KEYS[2])
end
if redis.call('EXISTS', KEYS[1]) == 0 then return {'not_found'} end
if not current_complete(KEYS[1]) then return {'defect'} end
if not same_stamp(KEYS[1], ARGV[2], ARGV[3], ARGV[4], ARGV[5], ARGV[6]) then
  if same_stamp(KEYS[1], ARGV[7], ARGV[8], ARGV[9], ARGV[10], ARGV[11]) then return reply_hash('replay', KEYS[1]) end
  return {'conflict'}
end
write_current(KEYS[1], ARGV[7], ARGV[8], ARGV[9], ARGV[10], ARGV[11], 'starting', ARGV[12], ARGV[13], ARGV[14])
write_replay(KEYS[2], 'replace', ARGV[16], KEYS[1], ARGV[15])
return reply_hash('applied', KEYS[1])
`

// qualifyWriteScript 只读取并确认 current active、未过期且完整 stamp 匹配。
const qualifyWriteScript = luaPrelude + `
if redis.call('EXISTS', KEYS[1]) == 0 then return {'not_found'} end
if not current_complete(KEYS[1]) then return {'defect'} end
if not same_stamp(KEYS[1], ARGV[2], ARGV[3], ARGV[4], ARGV[5], ARGV[6]) then return {'conflict'} end
if decimal_compare(redis.call('HGET', KEYS[1], 'expires_us'), ARGV[1]) <= 0 then return {'expired'} end
if redis.call('HGET', KEYS[1], 'phase') ~= 'active' then return {'in_progress'} end
return reply_hash('applied', KEYS[1])
`
