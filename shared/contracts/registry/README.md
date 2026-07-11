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

删除后的编号必须进入 `reserved`，不得重新分配。新增 owner 或扩大范围必须先修改 OpenSpec 与长期协议文档。

## 路由规则

- 每个 `messageId` 在三个 registry 中引用一致，并且只有一条 route。
- `control` 只允许 `WSS`；后续业务消息必须由所属 protocol change 分配 owner 范围并登记唯一通道。
- request 使用 `REQUEST_ID`，command 使用 `COMMAND_ID`，response 跟随其来源，error 使用 `CORRELATION_ID`，push 使用 `NONE`。
- route `maxSize` 限制完整编码 envelope，不得超过全局 1 MiB frame 上限。
- command schema 禁止声明 `actor_id`、`account_id`、`player_id` 或 `user_id` 等操作者字段。

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
