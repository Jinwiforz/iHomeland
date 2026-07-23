## ADDED Requirements

### Requirement: 实时协议必须显式携带状态同步与帧同步语义
Battle realtime registry MUST 为输入、snapshot、可靠战斗事件和 resync 消息登记唯一 owner、direction、allowed lane、auth scope、QoS、max size、rate、expiry、Tick/sequence 与幂等语义。帧输入 MUST 携带 `InputTick` 与 command sequence；snapshot MUST 携带 `ServerTick`、snapshot/baseline identity 与已处理输入确认。协议 MUST 支持客户端预测校正、远端插值和有界历史帧查询，但 MUST NOT 声明所有客户端执行完整确定性 Lockstep。

#### Scenario: 接收可用于校正的 snapshot
- **WHEN** 客户端收到通过 AEAD、replay、sequence 和 baseline 校验的 snapshot
- **THEN** 它能从消息中确定权威 ServerTick、已处理本地输入边界和 delta baseline，并按 gameplay simulation spec 执行本地校正或远端插值

#### Scenario: 消息缺失登记的 Tick 语义
- **WHEN** 一个 battle message 声明为帧输入或 snapshot，但 registry/schema 未提供其 Tick、sequence、expiry 或 baseline policy
- **THEN** contract validation 失败，消息不得进入 C++/C# production route

### Requirement: 裸 UDP 与 KCP 必须按数据时效唯一分 lane
同一认证 UDP session MUST 通过受认证 channel kind 区分 raw unreliable-sequenced lane 与 KCP reliable-ordered lane。连续输入 bundle、可覆盖 snapshot delta、transform/aim delta 与 probe MUST 使用 raw lane；只有丢失不可接受且迟到后仍有价值的 entity lifecycle、重要状态、ability grant/revoke 或 resync 数据 MAY 使用 KCP。连续 snapshot MUST NOT 通过 KCP 发送，同一 message id MUST NOT 跨 raw/KCP/TCP/WSS 双写。

#### Scenario: 连续 snapshot 丢失
- **WHEN** 一个 raw UDP snapshot delta 在网络中丢失且后续 snapshot 已可基于有效 baseline 解码
- **THEN** 客户端使用后续状态继续收敛，系统不通过 KCP 重传已经被覆盖的旧 snapshot

#### Scenario: entity spawn 消息丢失
- **WHEN** registry 将 entity spawn 登记为 KCP 且首次 segment 丢失
- **THEN** KCP 在消息仍未过期时重传并保持该 lane 顺序，应用在交付后仍校验 instance generation、Tick 和重复 identity

### Requirement: Battle UDP 首次启用必须与完整安全和网络资格同时交付
客户端与 C++ 模拟服首次开放 battle UDP 时 MUST 同时实现 HTTPS 短期 ticket、cookie challenge、AEAD、nonce/key epoch、replay window、endpoint binding/rebinding、每 IP/session 限流、畸形包快速拒绝、抗放大、MTU/分片 policy、网络模拟、KCP 参数验证和上下行带宽预算。KCP MUST 复用同一安全 UDP session且不得被当作 UDP 阻断时的 fallback；失败 MUST 产生稳定可恢复或 terminal 结果，不得把相同 battle message 静默转发到现有 TLS/TCP gameplay channel。

#### Scenario: 未验证地址发送放大请求
- **WHEN** 未完成 cookie/endpoint 验证的来源发送会诱发大 response 的 datagram
- **THEN** 服务端在抗放大预算内只返回有界 challenge 或直接丢弃，不分配完整 session、KCP state 或 snapshot

#### Scenario: UDP 被网络阻断
- **WHEN** HTTPS/WSS/TLS-TCP 仍健康但 battle UDP handshake 在冻结 deadline 内无法完成
- **THEN** 客户端显示登记的 battle transport failure 并执行玩法定义的重试/退出流程，不把 KCP 或 battle snapshot 静默改走 TCP
