## 1. 权威边界与总体架构

- [x] 1.1 更新 `docs/architecture.md`，加入 Go Control/Data Plane、C++ Game Simulation Server 与 Unity Client 的进程拓扑、唯一 owner 表和内部控制/result handoff 边界。
- [x] 1.2 在总体架构中明确 `Game Simulation Server`/`ihomeland-sim-server` 正式命名、Battle Server 讨论别名，以及 PersonalWorld 首个实例与未来 ActivityInstance 的非前置关系。
- [x] 1.3 更新 `docs/file-structure.md`，登记未来 C++ 模拟服、跨端 battle contract/fixture、网络资格与 Unity gameplay 模块的目标目录所有权，但不创建空实现目录。

## 2. 状态同步与帧同步模型

- [x] 2.1 新增或更新战斗模拟设计文档，完整描述 `SimulationTick`、`InputTick`、输入 history、确认帧、客户端预测、权威校正、未确认输入回滚重演、远端插值和服务器历史帧。
- [x] 2.2 增加“服务器权威状态同步 + 帧同步技术”与“完整确定性 Lockstep”的术语对照、采用项和禁止误解，并给出输入到 snapshot 再到校正的时序图。
- [x] 2.3 记录 replay evidence 所需的命令流、模拟版本、content hash、随机种子和 Tick 元数据边界，明确其不等于持久化或结算事实。

## 3. C++ 模拟核心技术边界

- [x] 3.1 记录自研 ECS 的最小功能、固定 system pipeline、单写 simulation worker、deferred structural command 和明确禁止扩张项。
- [x] 3.2 记录 GAS-like Attribute、GameplayTag、Ability、Effect、Cooldown、Cost 与 GameplayCue 如何建立在 ECS 上，以及 Unity 只实现客户端半边的约束。
- [x] 3.3 在架构/技术文档中记录 Asio、Jolt、Recast/Detour、KCP core、CMake/CMake Presets 的窄 adapter 职责、第三方类型隔离和首次实现 change 的版本/许可证门禁。

## 4. UDP/KCP 与安全网络边界

- [x] 4.1 更新 `docs/network-transport-architecture.md`，登记 raw unreliable-sequenced 与 KCP reliable-ordered lane 的初始消息分类、snapshot 禁止走 KCP 和同一 message id 禁止跨 lane 双写。
- [x] 4.2 在网络架构中加入 `InputTick`、`ServerTick`、snapshot/baseline、`LastProcessedInputTick`、expiry 与 history query 的 registry/协议必填语义。
- [x] 4.3 确认 `docs/network-port-allocation.md` 在 network profile 和安全资格完成前仍不分配 production UDP 端口，并登记 ticket、cookie、AEAD、replay、限流、抗放大、endpoint、网络模拟与带宽预算的联合进入条件。

## 5. Unity gameplay、UI 与镜头边界

- [x] 5.1 更新 `docs/client-architecture.md`，加入 `BattleNetworkClient`、纯 C# gameplay replica、Input/PredictedState history、reconciliation/interpolation、Actor View 和 Scene/App Scope 所有权。
- [x] 5.2 更新 `docs/client-ui-architecture.md`，登记 UI Toolkit 页面与 scene-bound uGUI 战斗 HUD 的复用规则，禁止第二个 Router/UI Manager 和 transport 进入 View。
- [x] 5.3 记录 Cinemachine 的 `CameraIntent -> Scene Scope Camera Host` 边界、探索/近战/远程瞄准/演出模式，以及 Camera/Animator transform 不得覆盖瞄准、命中或权威状态。

## 6. 后续竖切路线与验证

- [x] 6.1 更新 `docs/roadmap.md` 的 B0，按 simulation model、network profile、C++ core、Go/C++ control、安全 UDP/KCP、network qualification、Unity gameplay slice、剑/扇子/怪物/Boss 内容验收拆分后续独立 change，并为每项写进入条件、产出和完成条件。
- [x] 6.2 检查所有受影响文档对 Go/C++/Unity owner、状态同步、帧同步、raw UDP、KCP、ECS/GAS-like 与 Cinemachine 的术语一致性，修正与现有 v1 规格冲突的描述。
- [x] 6.3 运行仓库文档/链接门禁、`openspec validate define-authoritative-gameplay-architecture --strict` 和现有 OpenSpec strict 验证，确认本 change 不安装依赖、不开放 UDP listener 且不改变 v1 runtime。

## 7. 文档收尾清理

- [x] 7.1 盘点 README、AGENTS、架构、路线图、网络、客户端、文件结构、技术版本与长期 specs 中的阶段状态、owner、命名和文档入口，记录有证据的过时、错误、歧义与重复描述。
- [x] 7.2 按唯一 owner 文档原则清理发现的问题：正文保留规范定义，其他入口改为摘要和链接；同步修正当前实现状态、未来目标与尚未决策项的措辞。
- [x] 7.3 运行全仓 Markdown 本地链接/重复入口/术语检查、`git diff --check`、当前 change strict 与全仓 OpenSpec strict，并确认没有 runtime、依赖或 listener 变更。
