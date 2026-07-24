## Why

B0.3 已交付通过资格验收的离线 C++ `SimulationInstance`，但当前 Go 服务端仍用进程内逻辑 runtime 占位，无法实际启动、监督或回收独立模拟进程，也没有跨进程结果幂等边界。现在需要建立有界、可恢复的 Go↔C++ 控制面，才能让现有 `AssignmentStamp` 真正约束权威模拟，并为后续安全 UDP/KCP transport 提供可信的 `SimulationNode` 与 admission target。

## What Changes

- 以受监督的独立 `ihomeland-sim-server` 进程替换 production `processWorldRuntime` 占位，通过私有、认证、有界且不开放公网 listener 的控制通道完成 node hello、健康、容量、start、drain、stop 与状态查询。
- 建立 `SimulationNode` registry 和调度策略；只有 build/model/profile digest 匹配、健康且有容量的 node 才能接收 placement，node identity 与容量不得由客户端选择。
- 将完整 `AssignmentStamp`、新生成的 `SimulationInstanceID`、mapping generation、配置/地图/物理/导航 identity 和启动幂等 identity 绑定到每次 C++ lifecycle；旧 generation、响应丢失、重复请求和 C++ crash/restart 必须 fail closed 或收敛到唯一结果。
- 扩展 Go `placement.RuntimeController` 接缝和 WorldInstance 编排，使 remote ready、drain、revoke-before-stop、replacement、节点丢失与 shutdown 顺序保持现有 lease/fencing 语义，并暴露不含 credential/公开 endpoint 的内部 simulation admission target。
- 建立有界 `ResultProposal -> committed/rejected/replayed` 握手和持久 receipt；Go 在提交前重新验证 assignment/fence、proposal identity、Tick 范围、owner policy 与 evidence digest，C++ 不直连 MySQL/Redis，也不能把 proposal 或 replay evidence解释为奖励/资产已结算。
- 增加 closed control schema、Go/C++ fixtures、进程级 contract/integration/fault tests、低敏 logs/metrics/evidence，以及 C++ crash、Go restart、stale assignment、capacity、重复 result、响应丢失和逆序关闭验收。
- 更新总体架构、Gameplay 模拟、文件结构、网络端口、技术版本与路线图文档；本 change 不创建 battle wire/message ID、HTTPS battle ticket、UDP/KCP/Asio listener、production UDP 端口或 Unity gameplay runtime。

## Capabilities

### New Capabilities

- `go-simulation-control`: 定义 Go 对独立 C++ `SimulationNode`/`SimulationInstance` 的认证控制、健康容量、生命周期、admission target、结果握手、故障恢复与资格边界。

### Modified Capabilities

- `server-world-instance-placement`: 将 runtime ready/cleanup 接缝扩展为健康 node 选择、远程 start/drain/stop、simulation target 与节点丢失恢复，同时保持完整 AssignmentStamp、lease/fencing 和 break-before-make 不变量。
- `delivery-sequencing`: 将通过 Go/C++ control、进程故障、结果幂等和现有 v1 回归门规定为安全 battle transport 的进入 evidence，并继续阻止 UDP/KCP、公开端口与 Unity gameplay 提前实现。

## Impact

- 主要影响 `server/internal/placement/`、`server/internal/app/`、新增的 Go simulation control adapter/receipt storage、`simulation/` 的 node/control composition、跨语言 control fixtures 和自动化工具。
- 新增一个由 Go Composition Root 监督的 C++ 子进程及其私有 IPC 生命周期；不新增公网或部署级 TCP/UDP listener，不改变现有 HTTPS/WSS/TLS-TCP 客户端 API。
- 可能新增 simulation result receipt 的 MySQL migration，但不新增奖励、资产、PlayerState 或 PersonalWorld mutation schema；现有 MySQL/Redis owner 和迁移规则保持不变。
- 不引入 gRPC、Asio、KCP 或新的运行时序列化依赖；控制契约复用 Go 标准库与 B0.3 已锁定的 C++ JSON 能力，并保持后续 transport 可独立替换。
- 回滚时恢复进程内 `processWorldRuntime` production wiring 并移除未部署的 control 增量；已应用的 additive result receipt table 保留且不执行 destructive down migration，PersonalWorld、VisitSession、placement 数据与公开协议无需迁移。
