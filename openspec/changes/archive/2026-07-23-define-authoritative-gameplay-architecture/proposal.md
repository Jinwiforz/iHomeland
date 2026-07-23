## Why

服务端 v1 与客户端 v1 已通过资格验收，但项目仍没有可玩的实时战斗闭环；如果继续在未冻结权威模型、Tick 语义和跨端所有权的前提下堆叠通用基础设施，UDP/KCP、物理、技能与客户端预测会相互反向定义并造成返工。现在需要先冻结一套面向个人世界协作探索的服务器权威 gameplay 架构，使后续 change 能按“可移动、可战斗、可联机、可验收”的竖切顺序实施。

## What Changes

- 定义独立 C++ `Game Simulation Server` 作为个人世界实例内移动、物理、AI、技能、伤害、死亡和实时状态的唯一权威；现有 Go 服务继续拥有账号、Session、PersonalWorld、VisitSession、匹配/放置、准入、持久化与结算事实。
- 定义服务器权威状态同步与帧同步技术的融合模型：固定 `SimulationTick`/`InputTick`、带帧号输入、客户端本地预测、确认帧校正、未确认输入回滚重演、远端插值、服务器历史帧延迟补偿与可重放命令流；第一阶段不采用要求 Unity/C++ 全世界跨平台确定性的 Lockstep。
- 定义 C++ 模拟核心采用项目自研、范围受限的 ECS，并在其上构建 GAS-like Attribute、GameplayTag、Ability、Effect、Cooldown 与 Gameplay Cue 语义；物理、导航和网络 I/O 分别通过 Jolt、Recast/Detour 与 Asio 的窄 adapter 接入。
- 定义实时网络为同一安全 UDP session 下的裸 UDP 与 KCP 双 lane：高频可覆盖输入/快照走不可靠时序 lane，仍具时效且不可丢失的数据走 KCP；首次实现必须与 ticket、cookie、AEAD、重放保护、限流、抗放大、endpoint 校验、网络模拟和带宽预算同时交付。
- 定义 Unity 只实现客户端侧 gameplay replica、输入采样、预测/校正、插值、Actor View、动画、音效、UI 与镜头表现，不引入完整客户端 ECS/GAS、Netcode for GameObjects、Mirror、FishNet 或 Unity Dedicated Server。
- 定义 UI 延续 UI Toolkit 页面与 uGUI 战斗 HUD 的双框架边界；Cinemachine 仅作为 Scene Scope 镜头表现工具，通过窄 `CameraIntent`/Host 接入，不拥有瞄准、命中、技能或网络事实。
- 将后续实施拆为 simulation model、network profile、C++ simulation core、Go/C++ control、安全 UDP/KCP、网络资格、Unity gameplay runtime 与内容验收等有进入条件的独立 change，避免本 change 直接实现巨型跨端竖切。

## Capabilities

### New Capabilities

- `authoritative-gameplay-simulation`: 定义服务器权威实时模拟、Tick/帧语义、自研 ECS 与 GAS-like 战斗模型、跨端状态复制、预测校正、历史帧和首个 gameplay 竖切边界。

### Modified Capabilities

- `server-architecture`: 增加 Go 控制/数据面与独立 C++ Game Simulation Server 的进程所有权、内部控制协议和持久事实边界。
- `network-transport`: 冻结状态同步与帧同步技术在裸 UDP/KCP lane 上的消息语义、安全进入条件和禁止静默降级规则。
- `client-runtime`: 增加客户端 gameplay replica、预测/校正、表现 Transform、战斗 HUD 与 Cinemachine Scene Scope Host 边界。

## Impact

- 影响长期架构、路线图、网络传输、客户端运行时、文件结构、技术版本和协议治理文档。
- 后续将新增独立 C++ 工程、CMake/CMake Presets、Asio、Jolt、Recast/Detour、KCP core，以及 Unity Cinemachine 包；具体版本和依赖来源由对应实现 change 锁定，本 change 不安装依赖。
- 后续需要新增 Go↔C++ 内部控制契约、HTTPS 签发的 battle ticket、安全 UDP wire contract、跨 C++/C#/Go fixtures、网络模拟与 battle qualification group。
- 现有 HTTPS/WSS/TLS-TCP、PersonalWorld、VisitSession、Go 服务端 v1 与 Unity 客户端 v1 行为保持兼容；本 change 不开放 UDP 端口、不改变现有生产 listener，也不实现武器、怪物、Boss 或奖励结算。
