## Why

`battle-model-v1` 与 `battle-network-profile-v1` 已冻结并通过 strict 验证，但项目仍只有模型与网络画像，没有能消费这些契约的权威 C++ 模拟实现。现在需要交付无公网依赖的离线/loopback 核心，以实测补齐 CPU、内存、history、确定性和第三方 adapter 证据，并为后续 Go/C++ control 解锁稳定的 `SimulationInstance` 生命周期边界。

## What Changes

- 建立唯一 `simulation/` C++ 工程、可重复的 CMake Presets、项目包装入口与依赖恢复/校验流程；先在 `versions.yaml` 锁定 CMake、MSVC、Jolt Physics、Recast/Detour 和 JSON fixture parser 的精确版本、来源、source identity/checksum、许可证、adapter owner 与回滚规则。
- 实现无网络 `ihomeland-sim-server` 离线/loopback harness，以及绑定完整 `AssignmentStamp`、单 worker、50 ms fixed Tick、显式 start/drain/stop/reset 的 `SimulationInstance` owner；不开放 listener，不接入 Go 或 Unity。
- 实现 generation-safe `EntityID`、项目专用最小 ECS、typed deferred structural command buffer、固定单写 pipeline 和所有容量上限；不引入通用 archetype、反射、脚本 VM、Job Scheduler、EventBus 或 Service Locator。
- 按冻结模型实现移动/跳跃、AI intent、剑/扇子 Ability、Effect/Attribute/Death、投射物、稳定 PRNG、overload/drain 与 canonical rejection/event/state projection。
- 通过窄 `PhysicsWorld` 与 `NavigationWorld` value contracts 接入 Jolt 和 Detour；fixture adapter、live adapter 与 gameplay components 均不得暴露第三方类型或依赖 callback/容器迭代顺序。
- 实现 16-Tick 有界 history、只读延迟补偿查询与低敏 replay evidence；以 model/profile manifest digest、build/config identity、AssignmentStamp 和 Tick 范围绑定每次运行。
- 对 10 个冻结 model cases、记录化与 live adapter parity、连续重放、stale generation、容量/overload、sanitizer、benchmark 和 1/5/8 actor workloads 建立自动化门；真实 CPU、memory 与 history 指标只有经本 change 测量后才能从 `implementation_required` 转为已补证。
- 更新 gameplay、技术版本、文件结构与路线图文档；B0.3 通过只解锁 `establish-go-simulation-control`，安全 UDP/KCP、production 端口、wire、battle ticket 和 Unity gameplay runtime 继续关闭。

## Capabilities

### New Capabilities

- `game-simulation-core`: 定义 C++ `SimulationInstance` 生命周期、最小 ECS、固定权威 pipeline、Jolt/Detour adapter、有界 history/evidence、离线 harness、依赖治理和实现资格门。

### Modified Capabilities

- `delivery-sequencing`: 将通过 model/profile parity、确定性、sanitizer 与预算补证的离线 C++ core 规定为 Go/C++ control 的进入 evidence，并继续阻止 production transport 与 Unity gameplay runtime 提前实现。

## Impact

- 新增 `simulation/`、`tools/cpp/` 与被忽略的 `.local/cpp/`/build cache；更新 `versions.yaml`、`docs/technology-versions.md`、`docs/gameplay-simulation-architecture.md`、`docs/file-structure.md` 和 `docs/roadmap.md`。
- 新增 C++20/CMake 构建与测试，不修改现有 Go/Unity v1 API、MySQL/Redis schema、Protobuf、message registry、listener 或端口。
- 第三方边界限于 Jolt Physics 物理 adapter、Detour runtime navigation adapter 与离线 fixture JSON parser；Asio、KCP、Recast 离线烘焙工具和 production codec 不在本 change 引入。
- 回滚点为当前 B0.2 已归档提交；移除新 C++ 工程、局部依赖缓存声明和文档增量即可恢复，现有持久数据与 Go/Unity runtime 无迁移。
