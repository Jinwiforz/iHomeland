package session

import redisclient "github.com/redis/go-redis/v9"

// luaPrelude 以纯string decimal避免uint64 epoch经过Lua double丢失精度。
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
local function decimal_increment(value)
  local carry = 1
  local output = {}
  for index = #value, 1, -1 do
    local digit = string.byte(value, index) - 48 + carry
    if digit >= 10 then digit = digit - 10; carry = 1 else carry = 0 end
    table.insert(output, 1, string.char(48 + digit))
  end
  if carry == 1 then table.insert(output, 1, '1') end
  return table.concat(output)
end
local function valid_id(value, prefix)
  if not value or #value <= #prefix or #value > 128 then return false end
  if string.sub(value, 1, #prefix) ~= prefix then return false end
  local suffix = string.sub(value, #prefix + 1)
  if prefix == 'acc_' or prefix == 'ply_' then return string.match(suffix, '^[A-Za-z0-9]+$') ~= nil end
  return string.match(suffix, '^[A-Za-z0-9_.-]+$') ~= nil
end
local function valid_reason(value)
  return value == 'logout' or value == 'forced_logout' or value == 'principal_ban' or value == 'refresh_replay'
end
local function session_complete(key)
  if redis.call('HLEN', key) ~= 8 or redis.call('PTTL', key) < 0 then return false end
  local status = redis.call('HGET', key, 'status')
  local epoch = redis.call('HGET', key, 'epoch')
  local invalid_epoch = redis.call('HGET', key, 'invalidation_epoch')
  local reason = redis.call('HGET', key, 'invalidation_reason')
  if redis.call('HGET', key, 'v') ~= '1' or
    not valid_id(redis.call('HGET', key, 'account'), 'acc_') or
    not valid_id(redis.call('HGET', key, 'player'), 'ply_') or
    not canonical_decimal(epoch) or epoch == '0' or
    not canonical_decimal(redis.call('HGET', key, 'expires_us')) then return false end
  if status == 'active' then return invalid_epoch == '0' and reason == 'none' end
  return status == 'invalidated' and invalid_epoch == epoch and valid_reason(reason)
end
local function access_complete(key)
  return redis.call('HLEN', key) == 4 and redis.call('PTTL', key) >= 0 and
    redis.call('HGET', key, 'v') == '1' and valid_id(redis.call('HGET', key, 'session'), 'ses_') and
    canonical_decimal(redis.call('HGET', key, 'epoch')) and redis.call('HGET', key, 'epoch') ~= '0' and
    canonical_decimal(redis.call('HGET', key, 'expires_us'))
end
local function refresh_complete(key)
  if redis.call('HLEN', key) ~= 9 or redis.call('PTTL', key) < 0 or redis.call('HGET', key, 'v') ~= '1' or
    not valid_id(redis.call('HGET', key, 'session'), 'ses_') or
    not canonical_decimal(redis.call('HGET', key, 'epoch')) or redis.call('HGET', key, 'epoch') == '0' or
    string.match(redis.call('HGET', key, 'access') or '', '^[0-9a-f]+$') == nil or
    #(redis.call('HGET', key, 'access') or '') ~= 64 or
    not canonical_decimal(redis.call('HGET', key, 'expires_us')) or
    not canonical_decimal(redis.call('HGET', key, 'session_expires_us')) then return false end
  local consumed = redis.call('HGET', key, 'consumed')
  local replay_epoch = redis.call('HGET', key, 'replay_epoch')
  local replay_reason = redis.call('HGET', key, 'replay_reason')
  if consumed == '0' then return replay_epoch == '0' and replay_reason == 'none' end
  return consumed == '1' and ((replay_epoch == '0' and replay_reason == 'none') or
    (canonical_decimal(replay_epoch) and replay_epoch ~= '0' and valid_reason(replay_reason)))
end
local function ticket_complete(key)
  if redis.call('HLEN', key) ~= 9 or redis.call('PTTL', key) < 0 or redis.call('HGET', key, 'v') ~= '1' or
    not valid_id(redis.call('HGET', key, 'session'), 'ses_') or
    not canonical_decimal(redis.call('HGET', key, 'epoch')) or redis.call('HGET', key, 'epoch') == '0' or
    not canonical_decimal(redis.call('HGET', key, 'port')) or
    not canonical_decimal(redis.call('HGET', key, 'expires_us')) then return false end
  local channel = redis.call('HGET', key, 'channel')
  local scopes = redis.call('HGET', key, 'scopes')
  local host = redis.call('HGET', key, 'host')
  local port = tonumber(redis.call('HGET', key, 'port'))
  local consumed = redis.call('HGET', key, 'consumed')
  return (channel == 'wss' or channel == 'tls_tcp') and
    ((channel == 'wss' and scopes == 'control') or (channel == 'tls_tcp' and scopes == 'gameplay')) and
    host and #host > 0 and #host <= 253 and string.match(host, '^[A-Za-z0-9%.:%-]+$') and
    port and port >= 1 and port <= 65535 and (consumed == '0' or consumed == '1')
end
local function principal_complete(key)
  if redis.call('HLEN', key) ~= 5 or redis.call('PTTL', key) < 0 or
    redis.call('HGET', key, 'v') ~= '1' or not valid_id(redis.call('HGET', key, 'account'), 'acc_') or
    not valid_id(redis.call('HGET', key, 'player'), 'ply_') or
    not canonical_decimal(redis.call('HGET', key, 'expires_us')) then return false end
  local sessions = redis.call('HGET', key, 'sessions')
  if not sessions or sessions == '' or string.sub(sessions, 1, 1) == ',' or string.sub(sessions, -1) == ',' then return false end
  local seen = {}; local count = 0
  for current in string.gmatch(sessions, '([^,]+)') do
    count = count + 1
    if count > 64 or not valid_id(current, 'ses_') or seen[current] then return false end
    seen[current] = true
  end
  return count > 0
end
local function auth_reply(code, session_key)
  return {code, redis.call('HGET', session_key, 'account'), redis.call('HGET', session_key, 'player'),
    string.match(session_key, '([^:]+)$'), redis.call('HGET', session_key, 'epoch')}
end
local function access_auth_reply(code, session_key, access_key)
  local reply = auth_reply(code, session_key)
  table.insert(reply, redis.call('HGET', access_key, 'expires_us'))
  table.insert(reply, redis.call('HGET', session_key, 'expires_us'))
  return reply
end
local function session_outcome(session_key, epoch, now_us)
  if redis.call('EXISTS', session_key) == 0 then return 'not_found' end
  if not session_complete(session_key) then return 'defect' end
  if decimal_compare(redis.call('HGET', session_key, 'expires_us'), now_us) <= 0 then return 'expired' end
  if redis.call('HGET', session_key, 'status') ~= 'active' then return 'invalidated' end
  if redis.call('HGET', session_key, 'epoch') ~= epoch then return 'epoch_mismatch' end
  return 'applied'
end
`

const createScriptSource = luaPrelude + `
if not valid_id(ARGV[1], 'ses_') or not valid_id(ARGV[2], 'acc_') or not valid_id(ARGV[3], 'ply_') or
  not canonical_decimal(ARGV[4]) or ARGV[4] == '0' or not canonical_decimal(ARGV[5]) or
  not canonical_decimal(ARGV[6]) or not canonical_decimal(ARGV[7]) or not canonical_decimal(ARGV[8]) or
  not canonical_decimal(ARGV[9]) or not canonical_decimal(ARGV[10]) or
  string.match(ARGV[11] or '', '^[0-9a-f]+$') == nil or #(ARGV[11] or '') ~= 64 or
  not canonical_decimal(ARGV[12]) or not canonical_decimal(ARGV[13]) or
  decimal_compare(ARGV[7], ARGV[12]) >= 0 or decimal_compare(ARGV[12], ARGV[5]) > 0 or ARGV[9] ~= ARGV[5] then
  return {'defect'}
end
if redis.call('EXISTS', KEYS[1]) == 1 or redis.call('EXISTS', KEYS[2]) == 1 or redis.call('EXISTS', KEYS[3]) == 1 then return {'conflict'} end
local sessions = ARGV[1]
local principal_expiry_us = ARGV[9]
local principal_expiry_ms = ARGV[10]
if redis.call('EXISTS', KEYS[4]) == 1 then
  if not principal_complete(KEYS[4]) then return {'defect'} end
  if redis.call('HGET', KEYS[4], 'account') ~= ARGV[2] or redis.call('HGET', KEYS[4], 'player') ~= ARGV[3] then return {'defect'} end
  local active = {}
  local count = 0
  for current in string.gmatch(redis.call('HGET', KEYS[4], 'sessions'), '([^,]+)') do
    local current_key = ARGV[14] .. current
    if redis.call('EXISTS', current_key) == 1 then
      if not session_complete(current_key) or redis.call('HGET', current_key, 'account') ~= ARGV[2] or
        redis.call('HGET', current_key, 'player') ~= ARGV[3] then return {'defect'} end
      if redis.call('HGET', current_key, 'status') == 'active' then
        count = count + 1; table.insert(active, current)
        local current_expiry = redis.call('HGET', current_key, 'expires_us')
        if decimal_compare(current_expiry, principal_expiry_us) > 0 then
          principal_expiry_us = current_expiry
          principal_expiry_ms = tostring(math.floor((tonumber(current_expiry) + 999) / 1000))
        end
      end
    end
  end
  if count >= 64 then return {'conflict'} end
  table.insert(active, ARGV[1]); sessions = table.concat(active, ',')
end
redis.call('HSET', KEYS[1], 'v','1','account',ARGV[2],'player',ARGV[3],'epoch',ARGV[4],'status','active',
  'expires_us',ARGV[5],'invalidation_epoch','0','invalidation_reason','none')
redis.call('PEXPIREAT', KEYS[1], ARGV[6])
redis.call('HSET', KEYS[2], 'v','1','session',ARGV[1],'epoch',ARGV[4],'expires_us',ARGV[7])
redis.call('PEXPIREAT', KEYS[2], ARGV[8])
redis.call('HSET', KEYS[3], 'v','1','session',ARGV[1],'epoch',ARGV[4],'access',ARGV[11],
  'expires_us',ARGV[12],'session_expires_us',ARGV[5],'consumed','0','replay_epoch','0','replay_reason','none')
redis.call('PEXPIREAT', KEYS[3], ARGV[13])
redis.call('HSET', KEYS[4], 'v','1','account',ARGV[2],'player',ARGV[3],'expires_us',principal_expiry_us,'sessions',sessions)
redis.call('PEXPIREAT', KEYS[4], principal_expiry_ms)
return {'applied'}
`

const resolveAccessScriptSource = luaPrelude + `
if redis.call('EXISTS', KEYS[1]) == 0 then return {'not_found'} end
if not access_complete(KEYS[1]) then return {'defect'} end
if decimal_compare(redis.call('HGET', KEYS[1], 'expires_us'), ARGV[1]) <= 0 then return {'expired'} end
local session_key = ARGV[2] .. redis.call('HGET', KEYS[1], 'session')
local outcome = session_outcome(session_key, redis.call('HGET', KEYS[1], 'epoch'), ARGV[1])
if outcome ~= 'applied' then return {outcome} end
if decimal_compare(redis.call('HGET', KEYS[1], 'expires_us'), redis.call('HGET', session_key, 'expires_us')) > 0 then return {'defect'} end
return access_auth_reply('applied', session_key, KEYS[1])
`

const rotateRefreshScriptSource = luaPrelude + `
if redis.call('EXISTS', KEYS[1]) == 0 then return {'not_found'} end
if not refresh_complete(KEYS[1]) then return {'defect'} end
if decimal_compare(redis.call('HGET', KEYS[1], 'expires_us'), ARGV[1]) <= 0 then return {'expired'} end
local session_id = redis.call('HGET', KEYS[1], 'session')
local session_key = ARGV[2] .. session_id
if redis.call('HGET', KEYS[1], 'consumed') == '1' then
  if redis.call('EXISTS', session_key) == 0 then return {'not_found'} end
  if not session_complete(session_key) then return {'defect'} end
  if redis.call('HGET', KEYS[1], 'replay_epoch') == '0' then
    if redis.call('HGET', session_key, 'status') == 'active' then
      local next_epoch = decimal_increment(redis.call('HGET', session_key, 'epoch'))
      redis.call('HSET', session_key, 'epoch',next_epoch,'status','invalidated','invalidation_epoch',next_epoch,'invalidation_reason','refresh_replay')
    end
    redis.call('HSET', KEYS[1], 'replay_epoch',redis.call('HGET', session_key, 'epoch'),
      'replay_reason',redis.call('HGET', session_key, 'invalidation_reason'))
  end
  return {'replayed', session_id, redis.call('HGET', KEYS[1], 'replay_epoch'), redis.call('HGET', KEYS[1], 'replay_reason')}
end
local epoch = redis.call('HGET', KEYS[1], 'epoch')
local outcome = session_outcome(session_key, epoch, ARGV[1])
if outcome ~= 'applied' then return {outcome} end
if redis.call('HGET', KEYS[1], 'session_expires_us') ~= redis.call('HGET', session_key, 'expires_us') or
  decimal_compare(redis.call('HGET', KEYS[1], 'expires_us'), redis.call('HGET', session_key, 'expires_us')) > 0 then return {'defect'} end
if redis.call('EXISTS', KEYS[2]) == 1 or redis.call('EXISTS', KEYS[3]) == 1 then return {'conflict'} end
local access_expiry = ARGV[4]
local refresh_expiry = ARGV[6]
local session_expiry = redis.call('HGET', session_key, 'expires_us')
if decimal_compare(access_expiry, session_expiry) > 0 then access_expiry = session_expiry end
if decimal_compare(refresh_expiry, session_expiry) > 0 then refresh_expiry = session_expiry end
local access_ms = tostring(math.floor((tonumber(access_expiry) + 999) / 1000))
local refresh_ms = tostring(math.floor((tonumber(refresh_expiry) + 999) / 1000))
redis.call('DEL', ARGV[3] .. redis.call('HGET', KEYS[1], 'access'))
redis.call('HSET', KEYS[1], 'consumed','1','replay_epoch','0','replay_reason','none')
redis.call('PEXPIREAT', KEYS[1], tostring(math.floor((tonumber(session_expiry) + 999) / 1000)))
redis.call('HSET', KEYS[2], 'v','1','session',session_id,'epoch',epoch,'expires_us',access_expiry)
redis.call('PEXPIREAT', KEYS[2], access_ms)
redis.call('HSET', KEYS[3], 'v','1','session',session_id,'epoch',epoch,'access',ARGV[5],
  'expires_us',refresh_expiry,'session_expires_us',session_expiry,'consumed','0','replay_epoch','0','replay_reason','none')
redis.call('PEXPIREAT', KEYS[3], refresh_ms)
local reply = auth_reply('applied', session_key)
table.insert(reply, access_expiry); table.insert(reply, refresh_expiry)
return reply
`

const issueTicketScriptSource = luaPrelude + `
if redis.call('EXISTS', KEYS[1]) == 1 then return {'conflict'} end
local outcome = session_outcome(KEYS[2], ARGV[2], ARGV[1])
if outcome ~= 'applied' then return {outcome} end
if decimal_compare(ARGV[7], redis.call('HGET', KEYS[2], 'expires_us')) > 0 then return {'defect'} end
redis.call('HSET', KEYS[1], 'v','1','session',ARGV[3],'epoch',ARGV[2],'channel',ARGV[4],
  'host',ARGV[5],'port',ARGV[6],'scopes',ARGV[8],'expires_us',ARGV[7],'consumed','0')
redis.call('PEXPIREAT', KEYS[1], ARGV[9])
return {'applied'}
`

const consumeTicketScriptSource = luaPrelude + `
if redis.call('EXISTS', KEYS[1]) == 0 then return {'not_found'} end
if not ticket_complete(KEYS[1]) then return {'defect'} end
if redis.call('HGET', KEYS[1], 'consumed') == '1' then return {'replayed'} end
if decimal_compare(redis.call('HGET', KEYS[1], 'expires_us'), ARGV[1]) <= 0 then return {'expired'} end
if redis.call('HGET', KEYS[1], 'channel') ~= ARGV[2] or redis.call('HGET', KEYS[1], 'host') ~= ARGV[3] or redis.call('HGET', KEYS[1], 'port') ~= ARGV[4] then return {'not_found'} end
local session_key = ARGV[5] .. redis.call('HGET', KEYS[1], 'session')
local outcome = session_outcome(session_key, redis.call('HGET', KEYS[1], 'epoch'), ARGV[1])
if outcome ~= 'applied' then return {outcome} end
if decimal_compare(redis.call('HGET', KEYS[1], 'expires_us'), redis.call('HGET', session_key, 'expires_us')) > 0 then return {'defect'} end
redis.call('HSET', KEYS[1], 'consumed','1')
local reply = auth_reply('applied', session_key); table.insert(reply, redis.call('HGET', KEYS[1], 'scopes')); return reply
`

const invalidateSessionScriptSource = luaPrelude + `
if redis.call('EXISTS', KEYS[1]) == 0 then return {'not_found'} end
if not session_complete(KEYS[1]) then return {'defect'} end
if redis.call('HGET', KEYS[1], 'status') == 'invalidated' then
  return {'invalidated', ARGV[1], redis.call('HGET', KEYS[1], 'epoch'), redis.call('HGET', KEYS[1], 'invalidation_reason')}
end
local next_epoch = decimal_increment(redis.call('HGET', KEYS[1], 'epoch'))
redis.call('HSET', KEYS[1], 'epoch',next_epoch,'status','invalidated','invalidation_epoch',next_epoch,'invalidation_reason',ARGV[2])
return {'applied', ARGV[1], next_epoch, ARGV[2]}
`

const invalidatePrincipalScriptSource = luaPrelude + `
if redis.call('EXISTS', KEYS[1]) == 0 then return {'applied'} end
if not principal_complete(KEYS[1]) then return {'defect'} end
if redis.call('HGET', KEYS[1], 'account') ~= ARGV[1] or redis.call('HGET', KEYS[1], 'player') ~= ARGV[2] then return {'defect'} end
local sessions = {}
for current in string.gmatch(redis.call('HGET', KEYS[1], 'sessions'), '([^,]+)') do
  table.insert(sessions, current)
  if #sessions > 64 then return {'defect'} end
end
table.sort(sessions)
local records = {}
for _, id in ipairs(sessions) do
  if not valid_id(id, 'ses_') then return {'defect'} end
  local key = ARGV[4] .. id
  if redis.call('EXISTS', key) == 1 then
    if not session_complete(key) then return {'defect'} end
    if redis.call('HGET', key, 'account') ~= ARGV[1] or redis.call('HGET', key, 'player') ~= ARGV[2] then return {'defect'} end
    table.insert(records, {id, key})
  end
end
local reply = {'applied'}
for _, record in ipairs(records) do
  local id = record[1]; local key = record[2]
  if redis.call('HGET', key, 'status') == 'active' then
    local next_epoch = decimal_increment(redis.call('HGET', key, 'epoch'))
    redis.call('HSET', key, 'epoch',next_epoch,'status','invalidated','invalidation_epoch',next_epoch,'invalidation_reason',ARGV[3])
  end
  table.insert(reply, id); table.insert(reply, redis.call('HGET', key, 'epoch')); table.insert(reply, redis.call('HGET', key, 'invalidation_reason'))
end
return reply
`

// ownerScripts 在 package 初始化时只缓存不可变脚本文本与SHA，不创建连接或后台任务。
var (
	createScript              = redisclient.NewScript(createScriptSource)
	resolveAccessScript       = redisclient.NewScript(resolveAccessScriptSource)
	rotateRefreshScript       = redisclient.NewScript(rotateRefreshScriptSource)
	issueTicketScript         = redisclient.NewScript(issueTicketScriptSource)
	consumeTicketScript       = redisclient.NewScript(consumeTicketScriptSource)
	invalidateSessionScript   = redisclient.NewScript(invalidateSessionScriptSource)
	invalidatePrincipalScript = redisclient.NewScript(invalidatePrincipalScriptSource)
)
