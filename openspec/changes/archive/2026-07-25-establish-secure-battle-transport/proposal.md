## Why

B0.4 已以连续一致的 `control-qualified-windows-x64` 报告证明 Go 能安全监督并解析 C++ `SimulationTarget`，但 battle 仍没有客户端可用、可认证、可加密且可撤销的 wire。现在需要在不提前进入网络资格或 Unity gameplay runtime 的前提下，交付 B0.5 安全 UDP/KCP transport，并以真实实现补齐 network profile 中的 wire/KCP implementation evidence。

## What Changes

- 建立 versioned battle wire schema、numeric message registry 与 Go/C++/C# canonical fixtures，将冻结的 8 个 logical message kind 一一映射到唯一 raw UDP 或 KCP lane；未知 message、错误 direction/lane、超限 payload 和 TLS/TCP/WSS fallback 均 fail closed。
- 新增 HTTPS `battle-ticket` operation 与独立 BattleTicket owner；凭据绑定当前 SessionID/epoch、PlayerID、Owner/Visitor role、完整 AssignmentStamp、SimulationTarget/SimulationInstanceID、8-actor capacity slot、受信 advertised UDP endpoint、协议/配置 identity、一次性 ticket identity 和绝对 expiry，不复用 WSS/TCP `ConnectionTicket` 或 WorldAdmission。
- 在 C++ `ihomeland-sim-server` 内建立由 Go lifecycle 管理的单 UDP listener 与 authenticated multiplexer；raw/KCP 共享同一 socket、安全 session 和 route policy，部署 bind 与 advertised endpoint 显式分离，首次为 Game Simulation UDP 分配可覆盖的本地推荐端口。
- 冻结 cookie retry、ticket proof、key agreement/derivation、AEAD、key epoch、nonce、replay window、endpoint binding/rebinding、session generation 与 key rollover；地址验证前严格执行 stateless、无 session allocation 的抗放大预算。
- 接入冻结的 1200-byte datagram、raw/KCP header、10 ms KCP update、64-window、500 ms application expiry、MTU/fragmentation、per-IP/per-ticket/per-session/per-message/per-instance rate limit，以及有界 ingress/egress/KCP queue 与 backpressure policy。
- 将 battle input 只作为经过 actor binding、tick/sequence/expiry 校验的命令送入现有 `SimulationInstance`，将 snapshot/event/resync 投影送往唯一登记 lane；客户端字段不得声明最终 transform、hit、damage、death、reward 或替换连接身份。
- 组合 Session invalidation、assignment replacement、SimulationNode/child failure、ticket replay、endpoint rebind、drain 与 shutdown，使旧 endpoint、旧 key epoch、旧 session generation 和旧 SimulationTarget 立即失去后续收发资格。
- 新增 wire/crypto/KCP parity、真实 loopback listener、伪造/重放/放大/乱序/重复/过期/MTU/backpressure/rebind/key-rollover/shutdown 与 scope/secret tests；B0.5 只生成 implementation-qualified transport evidence，不代替 B0.6 的完整 network fault qualification。

## Capabilities

### New Capabilities

- `secure-battle-transport`: 定义 BattleTicket、安全握手与 session、raw/KCP authenticated multiplexer、battle wire/route、限流/抗放大、生命周期接线和 B0.5 implementation qualification。

### Modified Capabilities

- `battle-network-profile`: 用真实 wire encoded-size 与 KCP adapter parity evidence 补齐 B0.5 负责的 `implementation_required` 项，同时保持完整 fault matrix 的 B0.6 发布门。
- `delivery-sequencing`: 将通过安全 transport 实现资格门规定为 `qualify-battle-network` 的唯一进入 evidence，并继续阻止 Unity gameplay runtime 提前实现。
- `go-simulation-control`: 让 BattleSession 只消费 current `SimulationTarget` 并参与 target 撤销、child failure 与 shutdown ordering，不改变 Go/C++ stdio control owner。
- `network-transport`: 冻结 battle UDP/KCP endpoint、握手、AEAD、replay、rebinding、lane multiplexer、资源预算与故障语义。
- `server-contracts`: 为 8 个冻结 logical battle kind 分配唯一 numeric message ID、direction、channel、QoS、大小、速率、幂等、tick 与 assignment binding，并增加 BattleTicket HTTP contract/fixtures。
- `server-http-bootstrap`: 在唯一 HTTPS production graph 增加认证 BattleTicket issuance operation，并保持 handler 只负责 transport adapter 职责。
- `server-session`: 将 session epoch 失效传播到 battle session，并明确 BattleTicket 与既有 WSS/TCP ConnectionTicket、WorldAdmission 的不可互换边界。

## Impact

- 影响 `simulation/`、`server/internal/`、`server/cmd/server`、`shared/contracts/`、`api/`、`tools/`、配置 schema、部署示例、`versions.yaml` 与网络/协议/文件结构/路线图文档。
- 新增 C++ UDP/crypto/KCP adapters 与经版本、来源、checksum、许可证、owner、回滚规则锁定的依赖；Go 继续拥有 HTTPS issuance、Session/Placement policy、child lifecycle 和 production Composition Root，C++ 只拥有 battle socket/session 与 simulation ingress/replication。
- Redis 新增 BattleTicket 的 digest/handle-only、一次性 consume 与短期 replay schema；不新增 MySQL 持久事实，不把账号凭据、长期 token、资产、奖励、结算或完整 replay 写入 UDP、日志或 evidence。
- 现有 HTTPS/WSS/TLS-TCP PersonalWorld/VisitSession v1 契约保持兼容；battle 不通过已有 gameplay TCP 双写或降级。B0.6、Unity battle runtime、正式内容/奖励结算、跨主机 SimulationNode 和 Linux production 仍不在本 change 范围。
