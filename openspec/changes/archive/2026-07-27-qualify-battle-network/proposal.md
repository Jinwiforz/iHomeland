## Why

B0.5 已证明 Windows x64 上的安全 UDP/KCP 实现正确，但现有 loopback 与定点负例不能回答冻结 battle network profile 在真实进程、受控弱网、NAT 变化、长时重传和 1/5/8 actor 负载下是否仍满足预算。本 change 建立可重复调用的 B0.6 资格能力；开发期用代表性场景证明工具可用，完整发布结论只在用户显式冻结最终候选时生成。

## What Changes

- 建立 `tools/battle-qualification/` 唯一资格入口，以真实 Go parent、精确 B0.5 C++ child 和独立协议测试客户端运行安全 BattleTicket、握手、raw/KCP 与 simulation 流程。
- 将冻结 profile 的 latency、jitter、loss、burst loss、reorder、duplicate、bandwidth、pause/resume、MTU 与 queue pressure 组合成版本化 fault manifest；所有随机源、时钟、workload、拓扑和执行环境均可重放。
- 增加受控网络故障注入 adapter，明确 Windows 本机资格使用的流量路径、方向规则、NAT/rebind 模型、权限探测和不支持环境的 fail-closed 语义；不得向 production transport 注入隐藏测试分支。
- 分别测量 1、5、8 actor 的吞吐、丢包、延迟、jitter、CPU、内存、queue、KCP 重传/带宽放大、snapshot baseline 恢复与 simulation Tick debt，并对默认 5 actor 和 8 actor 上限独立作出结论。
- 覆盖 own-world、Visitor join/reconnect/leave、Owner grace、endpoint rebind、assignment replacement、child crash/restart、Go restart、pause/resume 和长时 soak；旧 session、endpoint、target 与 Tick evidence 均不得恢复资格。
- 扩展安全 negative corpus 和攻击流量场景，验证伪造、重放、反射/放大、malformed flood、rebind hijack、过期 KCP 与资源压力不会绕过 B0.5 安全边界或污染 gameplay。
- 提供生成低敏、机器可读且可连续复现资格报告的能力，绑定 source、binary、toolchain、model/profile/control/wire/config、fault manifest、workload 和环境 identity；缺失、跳过、漂移或不支持的 mandatory gate 均不得声明 qualified。
- 保持 B0.1/B0.2 source corpus 与 B0.5 wire corpus 只读；不修改冻结 lane、MTU、KCP 参数或安全字段来迎合结果。B0.6 tooling 完成只证明资格能力与代表性 development readiness，不实现 Unity gameplay runtime 或产品 combat slice；完整 B0.6 结论属于显式最终验收。

## Capabilities

### New Capabilities

- `battle-network-qualification`: 定义真实进程故障注入、网络/容量/安全矩阵、可重放 evidence、资格报告和 B0.6 发布门。

### Modified Capabilities

- `battle-network-profile`: 规定 B0.6 如何以真实实现补齐冻结 profile 的 network/capacity `implementation_required` 项，并在不改写 source corpus的前提下给出 5/8 actor 分层结论。
- `go-simulation-control`: 增加仅在显式 qualification mode 可读取的低敏 battle transport/simulation 计数快照，用受控内部契约补齐 queue、KCP、Tick debt 与 close reason 证据，不开放新 listener 或外部管理面。

## Impact

- 影响 `tools/battle-qualification/`、`shared/contracts/fixtures/battle/qualification/`、测试协议客户端、simulation-control qualification frame、C++ qualification/test adapters、Go 测试编排、CI/本地脚本、ignored `.local/battle-qualification/` evidence 以及网络、Gameplay、文件结构和路线图文档。
- 不新增 production API、message ID、listener、端口、MySQL/Redis schema 或 gameplay 规则；现有 HTTPS/WSS/TLS-TCP PersonalWorld/VisitSession 与 B0.5 UDP/KCP wire 保持兼容。
- 资格目标仍为 Windows x64；Linux production、公网运营商实验室、Unity/IL2CPP 客户端、内容、奖励结算、跨主机 SimulationNode、Room/Party/ActivityInstance 均不在本 change 范围。
