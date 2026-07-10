# Redis Key 规范

## 原则

Redis 只保存可恢复或可失效运行态。任何 key 在实现前必须通过对应 OpenSpec change 确认 owner、TTL、value schema、恢复来源、清理触发和故障语义。

## 命名

```text
ih:{env}:{owner}:{kind}:{identity...}
```

要求：

- `env` 明确隔离 local、test、staging、production。
- owner 与代码模块一致。
- identity 使用稳定 ID，不使用未经处理的账号名或 token。
- 动态片段必须校验长度和字符集。
- key builder 集中实现并测试。

## 第一阶段规划

下表是允许的第一阶段 key 类型；实际创建必须由实现 change 固化 TTL 和 value。

| Key Pattern | Owner | 用途 | 恢复/失效原则 |
|---|---|---|---|
| `ih:{env}:session:token:{tokenHash}` | session | access/session metadata | MySQL account 仍在；丢失后要求登录或刷新 |
| `ih:{env}:session:ticket:{ticketHash}` | session | 一次性 connection ticket | 不恢复，短 TTL，使用后删除 |
| `ih:{env}:presence:player:{playerID}` | session | 在线连接摘要 | 从 connection registry 恢复 |
| `ih:{env}:world:assignment:{personalWorldID}` | placement | 当前 WorldInstance assignment generation | 从 PersonalWorld 持久事实和 placement policy 重建 |
| `ih:{env}:world:lease:{personalWorldID}` | placement | active writable instance lease/fencing | 不恢复；TTL 到期后重新竞争并拒绝旧 fencing token |
| `ih:{env}:visit:session:{visitSessionID}` | visit | Visitor membership、revision、expiry 与 Owner grace | 丢失后访问安全结束或按权威事实重建，不是持久世界事实 |
| `ih:{env}:visit:player:{playerID}` | visit | 玩家当前 visit membership 索引 | 从有效 VisitSession 重建，必须有 TTL 与 cleanup owner |
| `ih:{env}:rate:{scope}:{identity}` | owning adapter | 限流窗口 | 丢失后最多放宽一个窗口 |
| `ih:{env}:lock:{owner}:{resourceID}` | owning module | 必要短租约 | 不恢复，必须有 TTL 与 fencing/idempotency |

## Value 规则

- Value 使用明确 schema，不存任意 `map[string]any`。
- JSON 字段使用稳定英文名；文档用中文说明含义。
- 时间字段包含单位。
- Value 必须有版本或能够兼容新增字段。
- 不存密码、完整 token、ticket 明文或 TLS key。
- 大对象和高频热 key 需要大小/带宽预算。

## TTL 规则

- 每个运行态 key 必须有 TTL 或明确的主动清理与恢复路径。
- ticket TTL 必须短且一次性。
- session TTL 与 token expiry 一致或更短。
- presence TTL 必须能容忍心跳抖动但不能无限延长。
- lock 禁止无过期时间。
- TTL refresh 由明确 owner 执行，不允许多个模块竞争续期。

## 清理与恢复

必须测试：

- key miss
- TTL 到期
- Redis flush
- Redis 暂时不可用
- value 损坏或版本未知
- 重复写入与乱序刷新
- 连接关闭、登出、VisitSession 关闭和进程重启

Redis 故障不能使已失败的持久操作对客户端返回成功。

## 文档模板

新增 key 时记录：

```text
Pattern:
Owner:
用途:
TTL:
Value schema:
写入触发:
读取触发:
恢复来源:
清理触发:
故障行为:
指标:
```

## 禁止事项

- 无 owner key
- 无 TTL 且无恢复路径的运行态 key
- 将 Redis 作为账号、资产、PersonalWorld 或结算等唯一事实源
- 使用 `KEYS` 扫描生产 namespace
- 在日志中打印完整敏感 key/value
- 多个模块写同一 key 但没有并发与版本策略
