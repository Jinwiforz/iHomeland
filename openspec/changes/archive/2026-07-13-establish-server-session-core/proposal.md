## Why

公开 HTTPS、WSS 与 TLS/TCP 在实现前必须共享同一套服务端身份事实，否则 token、ticket、session epoch 和连接身份会在不同 adapter 中各自演化，产生重放、越权和失效不一致。服务端运行基础已经稳定，现在需要先建立不依赖网络与存储实现的 session/security core，作为账号领域和后续全部 transport 的唯一认证边界。

## What Changes

- 建立 session、session epoch、principal、auth context 与 scope 的领域模型和不变量。
- 建立高熵 opaque access/refresh token 生命周期，只保存不可逆 digest，并支持原子 refresh rotation、过期和重放拒绝。
- 建立短期一次性 connection ticket 的签发与消费流程，绑定 session epoch、目标 channel、受信 endpoint、scope、16-byte nonce 和 expiry，并为协议 adapter 返回完整领域投影。
- 固定 WSS 与 TLS/TCP 的 channel/scope 签发矩阵，客户端输入只能选择允许的 channel，不能扩大 endpoint 或 scope。
- 建立 session invalidation、logout、forced logout 与 ban 输入的 epoch 递增语义，以及通知旧连接失效的窄接口。
- 定义存储、endpoint provider、连接失效通知、clock、ID 与 secret generator 边界，复用 runtime 的生产 clock/ID 实现，并提供仅用于测试的确定性 fake/in-memory 实现。
- 增加 token/ticket 脱敏、并发、expiry、rotation、replay、wrong-channel、wrong-epoch 和失效传播测试。
- 不实现账号凭据校验、HTTP/WSS/TCP listener、Redis/MySQL adapter、实际连接 registry、个人世界/访客会话业务、UDP/KCP 或 Unity 代码。

## Capabilities

### New Capabilities

- `server-session`：定义服务端 session 身份事实、opaque token、一次性 connection ticket、auth context、epoch 失效与 transport/storage-independent 安全行为。

### Modified Capabilities

无。现有 `server-contracts` 与 `network-transport` 已规定外部 session/ticket 契约和多通道身份边界，本 change 在不改变协议的前提下实现对应核心行为。

## Impact

- 新增服务端 session/security 核心 package、窄 repository/provider interfaces 及测试实现。
- 新增可验证的 session/token/ticket policy 值对象；在真实存储和 adapter 接线前不向启动配置加入无人消费的字段。
- 后续账号 application service 使用 session core 创建和轮换 token；HTTPS/WSS/TCP adapters 只负责提取凭据并消费 auth/ticket 结果。
- 后续 storage change 为 session store 与一次性消费接口提供 Redis 实现，transport changes 为连接失效接口提供真实 registry adapter。
