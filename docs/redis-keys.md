# Redis Key 规则

## 基本原则

Redis 用于短期运行态数据，不是持久事实来源。所有 key 必须有 owner、用途、TTL 或重建路径。新增或修改 key 时，必须用中文记录 owner、用途、TTL、value、重建来源和清理触发；value 字段名可以保留协议或 JSON 中的英文标识符，但字段含义必须用中文说明。

## Key 命名

推荐格式：

```text
ih:{env}:{module}:{type}:{id}
```

示例：

```text
ih:dev:session:connection:{connectionID}
ih:dev:account:session:{sessionToken}
ih:dev:presence:player:{playerID}
ih:dev:room:index:{roomID}
ih:dev:room:reconnect:{roomID}:{playerID}
ih:dev:lock:room:{roomID}
ih:dev:rate:gateway:{identity}
```

## Namespace 规则

- `session`：连接会话，必须有 TTL。
- `account:session`：账号 session token，必须有 TTL，丢失后要求重新登录。
- `presence`：在线状态，必须可从连接状态重建。
- `room:index`：房间索引，必须可从 room service 或持久摘要重建。
- `room:reconnect`：断线重连资格，必须有较短 TTL。
- `match:queue`：匹配队列，必须定义清理策略。
- `lock`：锁，必须有 TTL，禁止无过期锁。
- `rate`：限流，必须有窗口 TTL。

## 第一阶段 Key 清单

| Key | Owner | 用途 | TTL | Value | 重建来源 | 清理触发 |
| --- | --- | --- | --- | --- | --- | --- |
| `ih:{env}:session:connection:{connectionID}` | `gateway` | 连接级 session 运行态 | 30 分钟，连接存活时刷新 | `connectionID`、协议版本、建立时间、可选玩家身份 | 当前网关连接表 | 连接关闭、空闲超时或 TTL 到期 |
| `ih:{env}:account:session:{sessionToken}` | `account` | 账号登录后的短期会话凭证 | 第一阶段默认 24 小时，恢复或刷新会话时延长 | JSON：`session_token`、`player_id`、`account_name`、`issued_at_ms`、`expires_at_ms`、`connection_id` | MySQL `account_player`；Redis 丢失后要求重新登录 | 主动登出、session 过期、服务端失效或 TTL 到期 |
| `ih:{env}:presence:player:{playerID}` | `gateway` / `room` | 玩家在线状态 | 2 分钟，心跳或业务消息刷新 | `playerID`、`connectionID`、`roomID`、`online`、更新时间 | 网关连接状态和 room service 成员状态 | 连接关闭、退出房间或 TTL 到期 |
| `ih:{env}:room:index:{roomID}` | `room` | 房间列表/索引缓存 | 5 分钟，房间变更时刷新 | `roomID`、房间名、状态、容量、成员数、更新时间 | room service 当前状态或 MySQL `room_summary` | 房间关闭、成员变更刷新或 TTL 到期 |
| `ih:{env}:room:reconnect:{roomID}:{playerID}` | `room` | 断线重连短期资格 | 30 秒，重复断线覆盖同一 key | `roomID`、`playerID`、断线时间、截止时间 | room service 断线事件；Redis 丢失后只能要求重新建立会话，不能作为持久事实 | 重连成功、主动退出、房间关闭或 TTL 到期 |
| `ih:{env}:lock:room:{roomID}` | `room` | 房间短临界区保护 | 10 秒，禁止无过期锁 | 锁 owner、过期时间、随机 token | 不重建；锁丢失后依靠幂等写入和状态校验兜底 | 操作完成释放或 TTL 到期 |
| `ih:{env}:rate:gateway:{identity}` | `gateway` | 网关连接或消息限流窗口 | 1 分钟窗口 | 计数、窗口开始时间 | 不重建；丢失后最多放宽一个窗口 | 窗口结束或 TTL 到期 |

Redis 丢失只影响运行态体验，不得丢失玩家进度、房间摘要或对局摘要等持久事实。房间索引可以由 room service 内存状态或 MySQL `room_summary` 重建；presence 可以由当前连接表重建；reconnect token 丢失后视为短期资格失效，业务持久事实仍以 room service/MySQL 为准。

## 文档模板

新增 Redis key 时记录：

```text
Key:
Owner:
用途:
TTL:
Value:
重建来源:
清理触发:
```

## 禁止事项

- 禁止新增无 TTL 且无重建路径的运行态 key。
- 禁止把 Redis 当作玩家进度或对局结果的唯一存储。
- 禁止多个模块写同一 namespace 而没有 owner。
