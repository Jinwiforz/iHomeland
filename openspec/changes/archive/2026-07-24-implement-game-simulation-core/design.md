## Context

B0.1 已用 `shared/contracts/fixtures/battle/model/` 冻结 gameplay command/state/query/event、固定 pipeline、容量与 10 个 deterministic cases；B0.2 又用 `shared/contracts/fixtures/battle/network-profile/` 冻结 20 Hz simulation、40 Hz input、10 Hz snapshot、16-Tick history、8-actor qualified maximum、2.5 ms/Tick CPU target、64 MiB/instance memory target和 8 MiB history target。两套 corpus 均有只读 validator、失败回归和 canonical digest。

当前缺口不是继续设计模型，而是建立消费这些 source of truth 的唯一 C++ runtime，并用真实实现替换 `implementation_required` 占位。该 runtime 后续会被 Go `placement.RuntimeController` 的远程 adapter 驱动，但本 change 只允许文件/内存 command source 和离线/loopback harness；任何 socket、listener、ticket、wire codec、Go repository 或 Unity 类型都不进入工程。

## Goals / Non-Goals

**Goals:**

- 交付可重复恢复、构建、测试和回滚的 C++20/CMake 工程与精确依赖治理。
- 实现绑定完整 `AssignmentStamp` 的 `SimulationInstance`、单 worker fixed-tick lifecycle、自研最小 ECS、固定 pipeline 与有界队列。
- 让移动/跳跃、剑/扇子、Ability/Effect/Attribute/Death、AI、物理/导航、history/evidence 按冻结 model corpus 形成唯一权威实现。
- 以记录化 adapter 跑完全部 model fixtures，以 Jolt/Detour smoke/parity cases 证明第三方边界不改写规范结果。
- 实测 1/5/8 actor workload 的 CPU、allocator/memory、history、queue 与连续确定性，并对预算超限 fail closed。
- 产出稳定、低敏、可机器读取的 build/run/benchmark evidence，作为 B0.4 的进入证据。

**Non-Goals:**

- 不实现 Go/C++ RPC、node registration、placement control、result settlement 或 process supervision。
- 不创建 battle `.proto`、numeric message ID、wire codec、Asio、KCP、UDP listener、端口、ticket、cookie、AEAD 或 replay window。
- 不实现 Unity prediction/interpolation、Actor View、输入动作或内容资源管线。
- 不实现 Recast 离线 navmesh 烘焙 target；本 change 只消费版本化 Detour nav data fixture。
- 不实现 Party、Room、ActivityInstance、匹配、奖励、持久化、完整录像或任意脚本 VM。
- 不建立通用 ECS、通用 physics engine wrapper、通用 task scheduler 或未来网络的占位目录/接口。

## Decisions

### 1. 先锁定工具链和唯一必要依赖，再创建 target

`versions.yaml` 必须在任何 `simulation/` target 或依赖恢复前增加以下明确 owner；实现时还要记录下载资产 SHA-256、上游许可证 notice、支持平台、补丁清单和回滚命令：

| 能力 | 锁定版本/source identity | 许可证/治理 |
|---|---|---|
| Build Tools | Visual Studio 2026 Build Tools `18.8.1` build `12021.73`，固定 bootstrapper；MSVC `14.50.35717` LTS、`_MSC_VER=1950`，`v145` x64；Windows SDK `10.0.26100.8876` | Microsoft license；锁定 26100 兼容系列的最新 servicing，只验证受支持安装，不把系统工具复制进仓库 |
| CMake | `4.4.0` Windows x64 ZIP，SHA-256 `156d70eb7625a7b469444df7d0861d2af8d5d0a437fce32c350372b08f5620e8` | BSD-3-Clause；恢复至 `.local/cpp/cmake/4.4.0/`，通过锁定 `VsDevCmd` 与 NMake generator 支持项目局部 Build Tools 根 |
| Jolt Physics | `v5.5.0`，commit `23dadd0e603f1b321142d4c74df07fce85064989` | MIT；只由 `physics/jolt` adapter 链接 |
| Recast/Detour | `v1.6.0`，commit `b4554541b658630816dba41466eb1cefb624519e` | Zlib；只构建 Detour runtime modules，不构建 demo/Recast tool |
| JSON parser | nlohmann/json `v3.12.0`，commit `65ee68451d8eb2b5f3a30b410476ab83deb3289b`，tag source `tar.gz` SHA-256 `4b92eb0c06d10683f7447ce9406cb97cd4b453be18d7279320f7b2f025c10187` | MIT；只在 fixture/config/evidence adapters 私有使用 |

选择 MSVC 14.50 而不是随 Visual Studio Stable 漂移的默认 14.51，是因为 14.50 为三年 LTS 且能被 Visual Studio 2026 显式 pin。Build Tools 同时固定 `en-US` product language，并在 `VsDevCmd` 加载后仅于受控子动作内设置 `VSLANG=1033`/`PreferredUILang=en-US`；这样 MSVC `/showIncludes` 与 diagnostics 使用 ASCII/英文输出，不会被项目 UTF-8 终端错误解码，回调结束后必须恢复调用方环境。Jolt 5.5.0 已声明 Visual Studio 2026 支持；Recast/Detour 1.6.0 是其最新正式 release。JSON parser 是读取现有 JSON corpus 的必要窄依赖，避免自制不完整 parser；它不能渗入 gameplay model。

不引入 Catch2/GoogleTest：测试 executable 使用项目内最小 assertion/runner 并由 CTest 编排，避免仅为测试语法增加第三方依赖。不引入 Asio/KCP，因为本 change 没有网络 I/O。

### 2. 恢复与构建分离，正常 build 必须可离线

唯一入口为 `tools/cpp/cpp.ps1`，至少提供 `restore`、`configure`、`build`、`test`、`verify` 和 `clean-evidence` actions。`restore` 从 `versions.yaml` 读取 URL/source identity/checksum，下载到 ignored `.local/cpp/downloads/`，在解压前校验 SHA-256，在 `.local/cpp/sources/<name>/<version>/` 保留只读 source 与 license。普通 configure/build/test 使用 CMake dependency provider 或明确 `FETCHCONTENT_SOURCE_DIR_*` 指向该缓存，并开启 disconnected mode；缺失或漂移时失败，不在 configure 过程中联网。

已跟踪 `CMakeLists.txt`、`CMakePresets.json`、toolchain/dependency CMake modules 和 `versions.yaml` 是构建事实。Preset 不包含用户目录、密钥、endpoint 或 IDE 状态。系统 MSVC 通过 `vswhere`、固定 component、`VCToolsVersion` 和编译期 `_MSC_FULL_VER` evidence 校验；找不到精确工具链时失败，不退回系统默认编译器。

选择局部 source cache 而不是提交 vendor 源码或使用 vcpkg/conan，是为了保持上游身份、许可证和回滚简单，并与现有 `.local/` 治理一致。若某上游 release archive 无官方 SHA-256，implementation 必须从已批准 tag/commit 获取一次、记录实际 archive SHA-256，并让后续 restore 同时验证完整 commit identity；未经记录不得创建 target。

### 3. Target 图保持最小且表达依赖方向

目标结构为：

```text
simulation/
  CMakeLists.txt
  CMakePresets.json
  cmake/
  src/
    app/
    simulation/
    ecs/
    gameplay/
    physics/
    navigation/
    history/
    fixture/
  tests/
    unit/
    contract/
    integration/
    benchmark/
```

核心 targets：

- `ihomeland_sim_core`：ECS、simulation lifecycle、gameplay、history 与项目 value contracts；不链接 JSON/Jolt/Detour concrete adapter。
- `ihomeland_sim_fixture_adapter`：读取 model/profile corpus、记录化 physics/navigation trace、canonical evidence；私有链接 JSON parser。
- `ihomeland_sim_jolt_adapter`：实现 `PhysicsWorld`，私有链接 Jolt。
- `ihomeland_sim_detour_adapter`：实现 `NavigationWorld`，私有链接 Detour。
- `ihomeland-sim-server`：离线 CLI/loopback harness composition，只接受文件/标准输入 command source 和明确输出目录，不 accept socket。
- 分层 test/benchmark executables，由 CTest label 区分 `unit`、`contract`、`integration`、`determinism`、`sanitizer`、`benchmark`。

不预建 `control/`、`network/` target；B0.4/B0.5 在真实接口成立时再创建。第三方 include directory、handle、allocator、error 或 callback 类型不得出现在 `ihomeland_sim_core` 的 public headers。

### 4. `SimulationInstance` 是唯一 Tick 和世界生命周期 owner

实例必须在启动前一次性接收：

- 完整 `AssignmentStamp` fingerprint、`SimulationInstanceID` 与 mapping generation；
- build/config/model/profile/nav/physics identity；
- 50 ms `SimulationTick`、容量、history、queue 和 budget；
- 初始 actor/content fixture、稳定 seed 与 adapters。

生命周期为 `Created -> Starting -> Running -> Draining -> Stopped`，失败为终态 `Failed`。只有 `Running` 能推进 Tick；只有实例 worker 能写 ECS、physics world、navigation query context、history 与 evidence accumulator。部分启动失败按成功初始化栈逆序回滚；`Drain` 停止接收新 command，完成有限 Tick/结果投影后在 deadline 内停止；deadline 或 hard Tick debt 使实例显式失败，不无限追帧。

本 change 的 loopback 是进程内 command source/response sink，不是 TCP/UDP loopback listener。未来 B0.4 adapter 只能调用同一 lifecycle port，不得复制另一个 Tick owner。

### 5. 最小 ECS 使用 generation-safe sparse-set 与 typed deferred barrier

`EntityID` 由固定宽度 index/generation 组成；free-list 复用 index 时 generation 必须 checked increment，wrap 前使 slot 永久 retired。每个 `SimulationWorld` 拥有独立 registry、容量和 component storages。首期只实现已登记 systems 需要的 `create/destroy/add/remove/get/has` 与有限 typed views；迭代顺序不能直接成为 gameplay 裁决顺序。

结构变化全部写入有界 typed deferred command buffer，并按 `(target_tick, command_kind, source_entity, stable_sequence)` 排序后在唯一 barrier 提交。buffer 超限产生稳定 rejection/evidence 并触发 profile 规定的 overload policy；不得重新分配为无界容器，也不得在 system view 迭代中直接销毁实体。

选择 sparse-set 是因为首期 component set 稳定、实现面积小且可度量。archetype graph、反射、query DSL、自动 serialization、job graph 和 plugin ABI 都没有第二个真实用例，明确不实现。

### 6. Pipeline、数值与随机性完全消费冻结模型

唯一系统顺序为：

```text
DrainInput
-> InputIntent
-> AIIntent
-> AbilityActivation
-> Movement
-> Physics
-> HitDetection
-> Effect
-> Attribute
-> Death
-> Replication
-> CommitDeferredStructuralChanges
```

command 先验证 assignment/mapping generation、actor/session binding、InputTick window、sequence、capacity 和 payload 安全字段，再按冻结 tuple 排序。输入只表达 intent；transform、hit、damage、death、reward 声明稳定拒绝。移动、Attribute、时间和距离使用冻结整数单位与 checked arithmetic；浮点只封装在 physics/navigation adapter 内，回到 core 前量化并按稳定 key 排序。

每个 entity 使用从 instance seed、entity identity 和 stream kind 派生的独立 PRNG stream；一个实体或 system 的随机消费不能扰动其他 stream。任何 hash-map、pointer、线程调度、locale、wall clock 或第三方 iteration order 都不能进入 canonical projection。

### 7. Jolt 与 Detour 只实现项目 value ports，并与记录化 adapter 做 parity

`PhysicsWorld` 只暴露 ground probe、capsule move、shape/ray/overlap 与 projectile sweep 所需的项目整数/量化 value types。Jolt adapter 固定坐标、长度/角度/质量单位、collision layer、shape settings、solver step、编译选项与 hit normalization；Jolt callback 结果先复制、量化，再按 `(fraction, ColliderID, SubshapeID)` 规范排序。Jolt 不拥有 entity、Ability、Damage 或 Tick。

`NavigationWorld` 只暴露版本化 nav asset load、nearest-poly、path query 和有限 steering result。Detour adapter 在实例启动时验证 asset version/digest/coordinate scale；Tick 内不运行 Recast 烘焙，不让 Detour ref 或 status 泄漏到 AI components。相等候选按稳定 polygon/path identity 收敛。

每个冻结 case 默认使用记录化 adapter，以证明纯 gameplay 结果与第三方无关；另有 live smoke/parity corpus 对同一规范 query 比较量化 hit/path 集。live adapter 超出登记容差、顺序不稳定或返回非有限值时 fail closed，并报告 adapter boundary，而不是修改 expected model case。

### 8. History 和 evidence 是有界只读 projection

每 Tick 提交后，history ring 保存最多 16 Tick 的最小查询投影；总分配不得超过 8 MiB target。查询必须绑定 current assignment/mapping generation、当前 Tick、允许回看窗口、query kind 与 actor policy；future Tick 被 clamp，过期/缺帧/旧 generation 稳定拒绝。补偿命中在当前 Tick 进入正常 Effect/Attribute pipeline，不回写历史。

每次 harness run 生成低敏 manifest/report，至少绑定 build/compiler/dependency、model/profile/case/config/nav/physics digest、AssignmentStamp fingerprint、SimulationInstanceID、Tick/input range、seed/stream、canonical state/event/rejection/capacity digest、预算指标和截断原因。不得记录 raw token/ticket/key、账号凭据、聊天、完整玩家资料或未来 settlement payload。Evidence 只用于诊断和资格，不写 MySQL/Redis，也不声明奖励已提交。

### 9. Canonical harness 以已冻结 corpus 为唯一验收输入

Harness 启动前必须先运行现有 battle-model 与 battle-network-profile validators，并校验其 canonical digest。它逐 case 读取 manifest/assumptions/input/recorded query/expected output，通过唯一 runtime 执行，生成规范化 query/state/event/rejection/capacity token 与 SHA-256；不得在 C++ tests 中复制第二份 expected rules。

同一 build/config/case 连续运行至少两次必须得到同 digest，且不得修改 source corpus。额外回归覆盖 manifest drift、unknown field、unsafe payload、stale assignment/mapping/entity、不同 input arrival order、capacity overflow、hard Tick debt、history expiry、partial start rollback 和 shutdown deadline。

### 10. 预算分为 hard correctness gate 与测量 evidence

50 ms Tick 和 8-actor cap 是不可降级 contract；2.5 ms/Tick、64 MiB/instance、8 MiB history、256-item queue 是 target budget。Benchmark 使用 warmup、固定 iteration、固定 affinity/电源条件描述、compiler/build flags 与统计口径，分别测 1、5、8 actors 的 median/p95/max Tick、allocation/high-watermark、history bytes 和 queue watermark。

Correctness、capacity、determinism、sanitizer 或 hard limit 任一失败时 change 不能完成。性能 target 未满足也不能通过 B0.3；不得通过降低 Tick、删减系统、修改 fixtures 或只报告平均值规避。CPU/memory/history 在 Windows x64 reference environment 通过后标记 `implementation-qualified-windows-x64`；这不等于真实 socket/KCP/AEAD、Linux production host 或 B0.6 network qualification 已完成。

### 11. Presets 与验证入口固定证据链

Checked-in presets 至少包含：

- `windows-msvc-debug`：严格 warnings、assertions、unit/contract。
- `windows-msvc-release`：优化后的 determinism/integration/reference benchmark 与 qualification。
- `windows-msvc-asan`：AddressSanitizer 与适用的 runtime checks。
- `windows-msvc-ci`：clean restore/configure/build/CTest/verify 的无交互组合。

所有手写 C++ public/private type、function、method、field 和关键 invariant 注释遵守中文注释规则；编译启用 `/W4 /WX /permissive- /Zc:__cplusplus /Zc:preprocessor /EHsc`，Jolt 自身 warning 不转嫁为项目 warning，但 adapter 仍按项目门禁编译。`cpp.ps1 verify` 聚合依赖、build、CTest、sanitizer、determinism、Release benchmark、battle validators、evidence schema 和缓存/secret 扫描；OpenSpec strict 与跨语言回归属于 change 归档门，由仓库流程独立执行，不能被 C++ wrapper 的局部绿色替代。

## Risks / Trade-offs

- [Jolt/Detour 浮点或平台差异破坏逐位确定性] → core 只比较量化规范结果，固定 adapter 配置与排序；live parity 超出容差 fail closed，跨平台逐位一致性不伪造。
- [C++ scope 同时包含 ECS、玩法、物理和导航，change 较大] → 它们共同构成 B0.1 已冻结的一个无网络权威 owner；tasks 按可独立通过的 source contract、ECS、pipeline、adapter、history、qualification 分段，每段保留测试门，禁止加入 wire/control。
- [MSVC/Visual Studio 安装资产难以完全项目局部化] → 固定 bootstrapper、Build Tools build、`en-US` product language、MSVC LTS component 与 `_MSC_FULL_VER`；只有用户显式运行 `bootstrap` 时允许自动检测和安装，configure/build/test 只验证并调用，不安装或升级系统软件。
- [上游 GitHub source archive 可能重新打包] → 同时锁定 tag、full commit 与首次批准的 archive SHA-256；任一漂移必须显式依赖更新，不自动接受新 archive。
- [自研 ECS 演变为框架工程] → 只实现冻结 systems 的最小 operations/views，新增泛化能力必须有第二个真实用例、benchmark 和独立 OpenSpec。
- [Benchmark 受桌面噪声影响] → correctness 与 hard capacity 独立于性能；performance report记录环境并用固定 warmup/样本/p95，连续基线漂移触发复测而非放宽预算。
- [离线 Windows 资格掩盖未来 Linux 问题] → evidence 明确标记 Windows x64；B0.4 在选择 production process topology/platform 前必须增加对应 toolchain/platform qualification，不能复用标签冒充。

## Migration Plan

1. 先更新 `versions.yaml`、技术版本文档、license/notice 与 `tools/cpp/` restore/verify；在依赖校验通过前不创建 CMake target。
2. 创建最小 CMake/preset/target 图和空行为 smoke executable，确认 exact compiler、offline restore、warnings 与 CTest evidence。
3. 依次实现 ECS/lifecycle、记录化 adapters、固定 pipeline、gameplay systems、history/evidence；每段先加入 fixture/negative tests。
4. 最后接入 Jolt/Detour live adapters 和 parity tests，再执行 sanitizer、1/5/8 actor benchmark 与连续 determinism。
5. 同步 owner docs、roadmap 与主 specs，运行完整 `cpp.ps1 verify`、OpenSpec strict 与 `git diff --check`。
6. 回滚时删除 `simulation/`、`tools/cpp/` 和本 change 的版本/文档增量；`.local/cpp/` 是可删除缓存，不涉及数据库、协议或线上迁移。

## Open Questions

无。Production Linux toolchain、Go/C++ internal transport、battle wire 与进程部署拓扑均明确留给后续 change，不作为本 change 的隐含决定。
