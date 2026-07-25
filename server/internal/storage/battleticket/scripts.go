package battleticket

import (
	"strconv"
	"strings"

	redisclient "github.com/redis/go-redis/v9"
)

// luaPreludeTemplate 提供 owner scripts 共享的 strict Hash、TTL 与编码校验。
const luaPreludeTemplate = `
local function decimal(value)
  return value and string.match(value, '^%d+$') ~= nil and (value == '0' or string.sub(value,1,1) ~= '0')
end
local function positive(value)
  return decimal(value) and value ~= '0'
end
local function hex64(value)
  return value and #value == 64 and string.match(value, '^[0-9a-f]+$') ~= nil
end
local function id(value, prefix)
  return value and string.sub(value,1,#prefix) == prefix and #value > #prefix and #value <= 128 and
    string.match(string.sub(value,#prefix+1),'^[A-Za-z0-9_.-]+$') ~= nil
end
local function hash_bytes(key)
  local values = redis.call('HGETALL', key)
  local total = 0
  for _, value in ipairs(values) do total = total + #value end
  return total
end
local function complete(key)
  if redis.call('HLEN', key) ~= 30 or redis.call('PTTL', key) < 0 or
    hash_bytes(key) > __MAX_ISSUE_BYTES__ or redis.call('HGET',key,'v') ~= '1' then return false end
  local role = redis.call('HGET',key,'role')
  local visit = redis.call('HGET',key,'visit')
  local actor_slot = tonumber(redis.call('HGET',key,'actor_slot'))
  local port = tonumber(redis.call('HGET',key,'port'))
  local issued = tonumber(redis.call('HGET',key,'issued_us'))
  local expires = tonumber(redis.call('HGET',key,'expires_us'))
  local role_binding = (role == 'owner' and visit == 'none') or (role == 'visitor' and id(visit,'vses_'))
  return role_binding and hex64(redis.call('HGET',key,'fingerprint')) and
    hex64(redis.call('HGET',key,'secret_digest')) and hex64(redis.call('HGET',key,'proof_digest')) and
    id(redis.call('HGET',key,'ticket_id'),'btk1_') and id(redis.call('HGET',key,'player'),'ply_') and
    id(redis.call('HGET',key,'session'),'ses_') and positive(redis.call('HGET',key,'epoch')) and
    id(redis.call('HGET',key,'world'),'pworld_') and id(redis.call('HGET',key,'assignment_instance'),'winst_') and
    id(redis.call('HGET',key,'assignment_node'),'rnode_') and positive(redis.call('HGET',key,'assignment_generation')) and
    positive(redis.call('HGET',key,'assignment_fence')) and hex64(redis.call('HGET',key,'assignment_fingerprint')) and
    id(redis.call('HGET',key,'runtime_node'),'rnode_') and id(redis.call('HGET',key,'simulation_node'),'snode_') and
    id(redis.call('HGET',key,'simulation_instance'),'sinst_') and positive(redis.call('HGET',key,'mapping_generation')) and
    positive(redis.call('HGET',key,'target_revision')) and hex64(redis.call('HGET',key,'model_identity')) and
    hex64(redis.call('HGET',key,'profile_identity')) and hex64(redis.call('HGET',key,'config_identity')) and
    hex64(redis.call('HGET',key,'wire_identity')) and decimal(redis.call('HGET',key,'actor_slot')) and
    actor_slot and actor_slot >= 0 and actor_slot < 8 and redis.call('HGET',key,'host') ~= false and
    #redis.call('HGET',key,'host') >= 1 and #redis.call('HGET',key,'host') <= 253 and
    redis.call('HGET',key,'host') == string.lower(redis.call('HGET',key,'host')) and
    decimal(redis.call('HGET',key,'port')) and port and port >= 1 and port <= 65535 and
    decimal(redis.call('HGET',key,'issued_us')) and decimal(redis.call('HGET',key,'expires_us')) and
    issued and expires and issued < expires
end
local function reply(key)
  return {'found',
    redis.call('HGET',key,'fingerprint'),redis.call('HGET',key,'secret_digest'),redis.call('HGET',key,'proof_digest'),
    redis.call('HGET',key,'ticket_id'),redis.call('HGET',key,'player'),redis.call('HGET',key,'session'),
    redis.call('HGET',key,'epoch'),redis.call('HGET',key,'role'),redis.call('HGET',key,'world'),redis.call('HGET',key,'visit'),
    redis.call('HGET',key,'assignment_instance'),redis.call('HGET',key,'assignment_node'),
    redis.call('HGET',key,'assignment_generation'),redis.call('HGET',key,'assignment_fence'),
    redis.call('HGET',key,'assignment_fingerprint'),redis.call('HGET',key,'runtime_node'),
    redis.call('HGET',key,'simulation_node'),redis.call('HGET',key,'simulation_instance'),
    redis.call('HGET',key,'mapping_generation'),redis.call('HGET',key,'target_revision'),
    redis.call('HGET',key,'model_identity'),redis.call('HGET',key,'profile_identity'),
    redis.call('HGET',key,'config_identity'),redis.call('HGET',key,'wire_identity'),
    redis.call('HGET',key,'actor_slot'),redis.call('HGET',key,'host'),redis.call('HGET',key,'port'),
    redis.call('HGET',key,'issued_us'),redis.call('HGET',key,'expires_us')}
end
`

// luaPrelude 注入 definition 的 encoded budget，防止 metadata 与执行门禁漂移。
var luaPrelude = strings.ReplaceAll(luaPreludeTemplate, "__MAX_ISSUE_BYTES__", strconv.Itoa(maximumIssueBytes))

// issueScriptSource 原子创建 exact record 或按全部 fields 返回 replay/conflict。
var issueScriptSource = luaPrelude + `
if redis.call('EXISTS',KEYS[1]) == 1 then
  if not complete(KEYS[1]) then return {'defect'} end
  local fields = {'fingerprint','secret_digest','proof_digest','ticket_id','player','session','epoch','role','world','visit',
    'assignment_instance','assignment_node','assignment_generation','assignment_fence','assignment_fingerprint',
    'runtime_node','simulation_node','simulation_instance','mapping_generation','target_revision',
    'model_identity','profile_identity','config_identity','wire_identity','actor_slot','host','port','issued_us','expires_us'}
  for index, field in ipairs(fields) do
    if redis.call('HGET',KEYS[1],field) ~= ARGV[index] then return {'conflict'} end
  end
  return {'replay'}
end
redis.call('HSET',KEYS[1],'v','1',
  'fingerprint',ARGV[1],'secret_digest',ARGV[2],'proof_digest',ARGV[3],'ticket_id',ARGV[4],
  'player',ARGV[5],'session',ARGV[6],'epoch',ARGV[7],'role',ARGV[8],'world',ARGV[9],'visit',ARGV[10],
  'assignment_instance',ARGV[11],'assignment_node',ARGV[12],'assignment_generation',ARGV[13],
  'assignment_fence',ARGV[14],'assignment_fingerprint',ARGV[15],'runtime_node',ARGV[16],
  'simulation_node',ARGV[17],'simulation_instance',ARGV[18],'mapping_generation',ARGV[19],
  'target_revision',ARGV[20],'model_identity',ARGV[21],'profile_identity',ARGV[22],
  'config_identity',ARGV[23],'wire_identity',ARGV[24],'actor_slot',ARGV[25],
  'host',ARGV[26],'port',ARGV[27],'issued_us',ARGV[28],'expires_us',ARGV[29])
redis.call('PEXPIREAT',KEYS[1],ARGV[30])
return {'created'}
`

// resolveScriptSource 只读返回完整首次 record；缺失、损坏或缺 TTL 不做修复。
var resolveScriptSource = luaPrelude + `
if redis.call('EXISTS',KEYS[1]) == 0 then return {'not_found'} end
if not complete(KEYS[1]) then return {'defect'} end
return reply(KEYS[1])
`

var (
	// issueScript 缓存 immutable owner mutation Lua 与 SHA。
	issueScript = redisclient.NewScript(issueScriptSource)
	// resolveScript 缓存 immutable owner read Lua 与 SHA。
	resolveScript = redisclient.NewScript(resolveScriptSource)
)
