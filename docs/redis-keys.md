# Redis Key 规则

## 基本原则

Redis 用于短期运行态数据，不是持久事实来源。所有 key 必须有 owner、用途、TTL 或重建路径。

## Key 命名

推荐格式：

```text
ih:{env}:{module}:{type}:{id}
```

示例：

```text
ih:dev:session:player:{playerID}
ih:dev:presence:player:{playerID}
ih:dev:room:index:{roomID}
ih:dev:room:reconnect:{roomID}:{playerID}
ih:dev:match:queue:{mode}
ih:dev:lock:room:{roomID}
```

## Namespace 规则

- `session`：会话，必须有 TTL。
- `presence`：在线状态，必须可从连接状态重建。
- `room:index`：房间索引，必须可从 room service 或持久摘要重建。
- `room:reconnect`：断线重连资格，必须有较短 TTL。
- `match:queue`：匹配队列，必须定义清理策略。
- `lock`：锁，必须有 TTL，禁止无过期锁。
- `rate`：限流，必须有窗口 TTL。

## 文档模板

新增 Redis key 时记录：

```text
Key:
Owner:
Purpose:
TTL:
Value:
Rebuild source:
Cleanup trigger:
```

## 禁止事项

- 禁止新增无 TTL 且无重建路径的运行态 key。
- 禁止把 Redis 当作玩家进度或对局结果的唯一存储。
- 禁止多个模块写同一 namespace 而没有 owner。
