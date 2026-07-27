# 协议 Registry

## 所有权

- `messages.json`：实时消息编号、Protobuf full name、kind、direction 与 owner。
- `errors.json`：稳定错误编号、owner、类别、安全 message key、重试语义与 HTTP status。
- `routes.json`：每条实时消息唯一的 channel、auth scope、QoS、大小、限流、幂等与 timeout。
- runtime projection：由 `messages.json` 与 `routes.json` 在内存中构建，不保存重复文件。

## Message ID 范围

| 范围 | Owner |
|---|---|
| `1-99` | `common` |
| `100-499` | `session` |
| `500-999` | `control` |
| `1000-1999` | `account` |
| `2000-2099` | `world` |
| `2100-2299` | `visit` |

删除后的编号必须进入 `reserved`，不得重新分配。新增 owner 或扩大范围必须先修改 OpenSpec 与长期协议文档。

## 路由规则

- 每个 `messageId` 在三个 registry 中引用一致，并且只有一条 route。
- `control` 与 world/visit control notice 只允许 `WSS`；common gameplay heartbeat 与 world/visit authoritative request、command、response、safe-return 只允许 `TLS_TCP`。
- gameplay heartbeat 使用独立 `gameplay_heartbeat` rate policy，只证明已认证 active connection 存活，不读取或修改任何业务状态。
- request 使用 `REQUEST_ID`，command 使用 `COMMAND_ID`，response 使用 `CORRELATION_ID` 并在 envelope 中恰好携带来源 request id 或 command id，push 使用 `NONE`。
- route `maxSize` 限制完整编码 envelope，不得超过全局 1 MiB frame 上限。
- command schema 禁止声明 actor/account/player/user、session/epoch、world/instance、role、endpoint、fencing 或 assignment stamp 等可覆盖受信上下文的字段；Owner 控制面的 `target_visitor_id` 是明确允许的业务目标。
- 未登记 world/visit interaction 必须在 registry gate 默认拒绝，不能转交通用 action/mutation handler。
- Battle snapshot 的 `acknowledgementPolicy` 要求 full/delta payload 显式携带当前 authenticated actor、当前 mapping generation 的连续终结前沿；字段缺失、旧 generation 或同一 partition set 内确认漂移必须拒绝，不能按 protobuf 默认零值继续处理。

## 示例

```json
{
  "id": 500,
  "name": "CONTROL_MAINTENANCE_PUSH",
  "owner": "control",
  "protobuf": "ihomeland.control.v1.MaintenancePush",
  "kind": "PUSH",
  "direction": "SERVER_TO_CLIENT"
}
```

统一入口为 `tools/proto/proto.ps1`，其中 `verify` 会交叉校验生成代码注册的 descriptor、OpenAPI、registry、版本目录、fixtures 与生成摘要。
