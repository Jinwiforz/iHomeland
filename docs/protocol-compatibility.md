# 协议兼容规则

## 基本原则

- 实时通信使用 Protobuf。
- WebSocket 和 TCP 共享同一 envelope。
- 协议版本必须显式携带。
- 服务端必须拒绝不支持的协议版本，并返回结构化错误。
- 生成代码不得手工修改。

## 字段规则

- 新增字段必须使用新的 field number。
- 已发布 field number 不得复用。
- 删除字段必须使用 `reserved` 保留编号和名称。
- 不得随意改变字段语义。
- 不得在不升级协议版本的情况下改变必填行为。

## 消息规则

- 每个消息必须有稳定 message id。
- message id 不得复用。
- 废弃消息应保留兼容期。
- 破坏性变更必须更新 protocol version。

## Envelope 建议字段

```text
protocol_version
message_id
request_id
sequence
timestamp_ms
payload
```

## 版本拒绝策略

当客户端协议版本低于服务端支持范围时：

- 服务端返回 `PROTOCOL_VERSION_UNSUPPORTED`
- 错误响应包含服务端支持范围
- 网关按策略关闭连接或限制会话

当客户端协议版本高于服务端支持范围时：

- 服务端返回 `PROTOCOL_VERSION_UNSUPPORTED`
- 客户端应提示版本不匹配

## 文档要求

每次协议变更必须说明：

- 新增、修改或废弃的消息
- 是否破坏兼容
- 客户端升级要求
- 服务端拒绝策略是否变化
