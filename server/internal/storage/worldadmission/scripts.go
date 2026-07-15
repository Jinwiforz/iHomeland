package worldadmission

import (
	"strconv"
	"strings"

	redisclient "github.com/redis/go-redis/v9"
)

// luaPreludeTemplate 提供两条 owner script 共享的严格 Hash 完整性与返回编码。
const luaPreludeTemplate = `
local function decimal(value)
  return value and string.match(value, '^%d+$') ~= nil and (value == '0' or string.sub(value,1,1) ~= '0')
end
local function hex64(value)
  return value and #value == 64 and string.match(value, '^[0-9a-f]+$') ~= nil
end
local function id(value, prefix)
  return value and string.sub(value,1,#prefix) == prefix and #value > #prefix and #value <= 128 and string.match(string.sub(value,#prefix+1),'^[A-Za-z0-9_.-]+$') ~= nil
end
local function safe_id(value)
  return value and #value >= 1 and #value <= 128 and string.match(value,'^[A-Za-z0-9_.-]+$') ~= nil
end
local function hash_bytes(key)
  local values = redis.call('HGETALL', key)
  local total = 0
  for _, value in ipairs(values) do total = total + #value end
  return total
end
local function issue_complete(key)
  return redis.call('HLEN', key) == 4 and redis.call('PTTL', key) >= 0 and hash_bytes(key) <= __MAX_ISSUE_BYTES__ and
    redis.call('HGET', key, 'v') == '1' and hex64(redis.call('HGET', key, 'fingerprint')) and
    hex64(redis.call('HGET', key, 'digest')) and decimal(redis.call('HGET', key, 'expires_us'))
end
local function credential_complete(key)
  if redis.call('HLEN', key) ~= 20 or redis.call('PTTL', key) < 0 or hash_bytes(key) > __MAX_CREDENTIAL_BYTES__ or redis.call('HGET', key, 'v') ~= '1' then return false end
  local status = redis.call('HGET', key, 'status')
  local consume_id = redis.call('HGET', key, 'consume_id')
  local consume_fp = redis.call('HGET', key, 'consume_fp')
  if status == 'issued' and (consume_id ~= 'none' or consume_fp ~= 'none') then return false end
  if status == 'consumed' and (not safe_id(consume_id) or consume_id == 'none' or not hex64(consume_fp)) then return false end
  local role = redis.call('HGET', key, 'role')
  local purpose = redis.call('HGET', key, 'purpose')
  local visit = redis.call('HGET', key, 'visit')
  local host = redis.call('HGET', key, 'host')
  local port = tonumber(redis.call('HGET', key, 'port'))
  local issued = tonumber(redis.call('HGET', key, 'issued_us'))
  local expires = tonumber(redis.call('HGET', key, 'expires_us'))
  local role_binding = (role == 'owner' and purpose == 'own_world' and visit == 'none') or
    (role == 'visitor' and (purpose == 'join' or purpose == 'reconnect') and id(visit,'vses_'))
  return (status == 'issued' or status == 'consumed') and role_binding and
    id(redis.call('HGET', key, 'player'),'ply_') and id(redis.call('HGET', key, 'session'),'ses_') and
    decimal(redis.call('HGET', key, 'epoch')) and redis.call('HGET', key, 'epoch') ~= '0' and
    id(redis.call('HGET', key, 'world'),'pworld_') and id(redis.call('HGET', key, 'instance'),'winst_') and
    id(redis.call('HGET', key, 'node'),'rnode_') and
    decimal(redis.call('HGET', key, 'generation')) and redis.call('HGET', key, 'generation') ~= '0' and
    decimal(redis.call('HGET', key, 'fence')) and redis.call('HGET', key, 'fence') ~= '0' and
    redis.call('HGET', key, 'channel') == 'tls_tcp' and host ~= false and
    #host >= 1 and #host <= 253 and host == string.lower(host) and
    string.match(host,'^[A-Za-z0-9%.:%-]+$') ~= nil and
    decimal(redis.call('HGET', key, 'port')) and port and port >= 1 and port <= 65535 and
    decimal(redis.call('HGET', key, 'issued_us')) and decimal(redis.call('HGET', key, 'expires_us')) and
    issued and expires and issued < expires
end
local function binding_reply(code, key)
  return {code, redis.call('HGET',key,'player'),redis.call('HGET',key,'session'),redis.call('HGET',key,'epoch'),
    redis.call('HGET',key,'role'),redis.call('HGET',key,'world'),redis.call('HGET',key,'visit'),redis.call('HGET',key,'purpose'),
    redis.call('HGET',key,'instance'),redis.call('HGET',key,'node'),redis.call('HGET',key,'generation'),redis.call('HGET',key,'fence'),
    redis.call('HGET',key,'channel'),redis.call('HGET',key,'host'),redis.call('HGET',key,'port'),
    redis.call('HGET',key,'issued_us'),redis.call('HGET',key,'expires_us')}
end
`

// luaPrelude 把 Go registry 的编码预算注入脚本，避免治理 metadata 与执行门禁漂移。
var luaPrelude = strings.NewReplacer(
	"__MAX_ISSUE_BYTES__", strconv.Itoa(maximumIssueBytes),
	"__MAX_CREDENTIAL_BYTES__", strconv.Itoa(maximumCredentialBytes),
).Replace(luaPreludeTemplate)

// issueScriptSource 原子创建或重放issuance与credential两类Hash。
var issueScriptSource = luaPrelude + `
if redis.call('EXISTS', KEYS[1]) == 1 then
  if not issue_complete(KEYS[1]) then return {'defect'} end
  if redis.call('HGET',KEYS[1],'fingerprint') ~= ARGV[1] or redis.call('HGET',KEYS[1],'digest') ~= ARGV[2] or redis.call('HGET',KEYS[1],'expires_us') ~= ARGV[17] then return {'conflict'} end
  if redis.call('EXISTS', KEYS[2]) == 0 or not credential_complete(KEYS[2]) then return {'defect'} end
  if redis.call('HGET',KEYS[2],'player') ~= ARGV[3] or redis.call('HGET',KEYS[2],'session') ~= ARGV[4] or
    redis.call('HGET',KEYS[2],'epoch') ~= ARGV[5] or redis.call('HGET',KEYS[2],'role') ~= ARGV[6] or
    redis.call('HGET',KEYS[2],'world') ~= ARGV[7] or redis.call('HGET',KEYS[2],'visit') ~= ARGV[8] or
    redis.call('HGET',KEYS[2],'purpose') ~= ARGV[9] or redis.call('HGET',KEYS[2],'instance') ~= ARGV[10] or
    redis.call('HGET',KEYS[2],'node') ~= ARGV[11] or redis.call('HGET',KEYS[2],'generation') ~= ARGV[12] or
    redis.call('HGET',KEYS[2],'fence') ~= ARGV[13] or redis.call('HGET',KEYS[2],'channel') ~= ARGV[14] or
    redis.call('HGET',KEYS[2],'host') ~= ARGV[15] or redis.call('HGET',KEYS[2],'port') ~= ARGV[16] or
    redis.call('HGET',KEYS[2],'expires_us') ~= ARGV[17] or redis.call('HGET',KEYS[2],'issued_us') ~= ARGV[19] then return {'defect'} end
  if redis.call('HGET',KEYS[2],'status') == 'consumed' then return {'consumed'} end
  return {'replay'}
end
if redis.call('EXISTS', KEYS[2]) == 1 then return {'defect'} end
redis.call('HSET',KEYS[1],'v','1','fingerprint',ARGV[1],'digest',ARGV[2],'expires_us',ARGV[17])
redis.call('PEXPIREAT',KEYS[1],ARGV[18])
redis.call('HSET',KEYS[2],'v','1','status','issued','consume_id','none','consume_fp','none',
  'player',ARGV[3],'session',ARGV[4],'epoch',ARGV[5],'role',ARGV[6],'world',ARGV[7],'visit',ARGV[8],
  'purpose',ARGV[9],'instance',ARGV[10],'node',ARGV[11],'generation',ARGV[12],'fence',ARGV[13],
  'channel',ARGV[14],'host',ARGV[15],'port',ARGV[16],'issued_us',ARGV[19],'expires_us',ARGV[17])
redis.call('PEXPIREAT',KEYS[2],ARGV[18])
return {'created'}
`

// resolveIssueScriptSource 交叉读取issue与credential并返回首次完整绑定。
var resolveIssueScriptSource = luaPrelude + `
if redis.call('EXISTS', KEYS[1]) == 0 then return {'not_found'} end
if not issue_complete(KEYS[1]) then return {'defect'} end
local credential_key = ARGV[1] .. redis.call('HGET', KEYS[1], 'digest')
if redis.call('EXISTS', credential_key) == 0 or not credential_complete(credential_key) then return {'defect'} end
if redis.call('HGET', KEYS[1], 'expires_us') ~= redis.call('HGET', credential_key, 'expires_us') then return {'defect'} end
local reply = binding_reply('found', credential_key)
table.insert(reply, 2, redis.call('HGET', KEYS[1], 'fingerprint'))
table.insert(reply, 3, redis.call('HGET', KEYS[1], 'digest'))
table.insert(reply, 4, redis.call('HGET', credential_key, 'status'))
return reply
`

// consumeScriptSource 在写入tombstone前比较全部静态连接binding。
var consumeScriptSource = luaPrelude + `
if redis.call('EXISTS', KEYS[1]) == 0 then return {'not_found'} end
if not credential_complete(KEYS[1]) then return {'defect'} end
if redis.call('HGET',KEYS[1],'status') == 'consumed' then
  if redis.call('HGET',KEYS[1],'consume_id') == ARGV[1] and redis.call('HGET',KEYS[1],'consume_fp') == ARGV[2] then return binding_reply('replay',KEYS[1]) end
  return {'replayed'}
end
if tonumber(redis.call('HGET',KEYS[1],'expires_us')) <= tonumber(ARGV[9]) then return {'expired'} end
if redis.call('HGET',KEYS[1],'player') ~= ARGV[3] or redis.call('HGET',KEYS[1],'session') ~= ARGV[4] or
  redis.call('HGET',KEYS[1],'epoch') ~= ARGV[5] or redis.call('HGET',KEYS[1],'purpose') ~= ARGV[6] or
  redis.call('HGET',KEYS[1],'channel') ~= ARGV[7] or redis.call('HGET',KEYS[1],'host') ~= ARGV[8] or
  redis.call('HGET',KEYS[1],'port') ~= ARGV[10] then return {'mismatch'} end
redis.call('HSET',KEYS[1],'status','consumed','consume_id',ARGV[1],'consume_fp',ARGV[2])
return binding_reply('applied',KEYS[1])
`

var (
	// issueScript 缓存不可变脚本文本与SHA，不创建连接。
	issueScript = redisclient.NewScript(issueScriptSource)
	// resolveIssueScript 缓存按IssueID解析首次签发事实的只读脚本。
	resolveIssueScript = redisclient.NewScript(resolveIssueScriptSource)
	// consumeScript 缓存不可变脚本文本与SHA，不创建连接。
	consumeScript = redisclient.NewScript(consumeScriptSource)
)
