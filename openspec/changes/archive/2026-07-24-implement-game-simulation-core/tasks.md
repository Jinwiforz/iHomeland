## 1. 锁定 C++ 工具链与依赖治理

- [x] 1.1 在 `versions.yaml` 登记 Visual Studio 2026 Build Tools 18.8.1、MSVC 14.50 LTS、Windows SDK 10.0.26100.8876、CMake 4.4.0、Jolt Physics 5.5.0、Recast/Detour 1.6.0 与 nlohmann/json 3.12.0 的精确版本、官方来源、full source identity/SHA-256、许可证、支持平台和唯一 owner
- [x] 1.2 更新 `docs/technology-versions.md`，记录各依赖的允许 API surface、编译选项、传递依赖、notice、补丁清单、升级/回滚步骤和 ignored 离线缓存位置
- [x] 1.3 实现 `tools/cpp/cpp.ps1 restore`，按 `versions.yaml` 下载到 `.local/cpp/`、在解压前校验 SHA-256/source identity、保留 license，并为 checksum、archive traversal、部分下载与依赖漂移增加隔离失败回归
- [x] 1.4 实现 exact Visual Studio/MSVC/Windows SDK 发现与 `_MSC_VER`/`_MSC_FULL_VER` evidence，验证找不到锁定 toolset 时不回退到系统默认 compiler
- [x] 1.5 扩展 `.gitignore` 与缓存/secret 扫描，证明第三方 source、build tree、benchmark report、本机 preset 和工具下载不会进入 Git

## 2. 建立可重复的 CMake 工程

- [x] 2.1 创建具有真实 smoke 行为的 `simulation/`、根 `CMakeLists.txt`、dependency/toolchain modules 与 `ihomeland_sim_core`，不创建空 `control`/`network` 层
- [x] 2.2 创建 `windows-msvc-debug`、`windows-msvc-release`、`windows-msvc-asan` 与 `windows-msvc-ci` Presets，固定 C++20、MSVC toolset、runtime、warnings-as-errors、输出路径和 disconnected dependency mode
- [x] 2.3 建立 `ihomeland_sim_fixture_adapter`、`ihomeland_sim_jolt_adapter`、`ihomeland_sim_detour_adapter`、`ihomeland-sim-server` 与分层 CTest target 图，验证 core public headers 不暴露 JSON/Jolt/Detour 类型
- [x] 2.4 完成 `tools/cpp/cpp.ps1 configure/build/test/verify/clean-evidence` actions，并用清缓存与禁网构建测试证明正常 configure/build/test 不访问外部网络
- [x] 2.5 为 exact compiler/dependency/build flags 生成低敏 build manifest，并验证相同 source/config 重建得到稳定 target identity

## 3. 消费冻结 model/profile source of truth

- [x] 3.1 实现严格 fixture/config reader，在构造实例前调用现有 battle model/profile validators并校验 manifest、assumptions、case 与 canonical report digest
- [x] 3.2 将 50 ms SimulationTick、40 Hz InputTick mapping、16-Tick history、8-actor cap、queue 256 和 CPU/memory/history target 解析为强类型 checked configuration，拒绝未知字段、单位、隐藏默认值和溢出
- [x] 3.3 实现规范 JSON/token serialization 与项目内 SHA-256 module，使用标准 vectors、locale/换行/键顺序和跨重复运行测试固定 digest
- [x] 3.4 为 manifest drift、未登记/缺失 case、unsafe field、非法单位、budget overflow、旧 report 和 source corpus 写入尝试增加 negative tests
- [x] 3.5 建立 file/stdin command source、recorded physics/navigation source 与 result/evidence sink 的离线 harness contracts，确认所有边界有容量、错误类型和关闭语义

## 4. 实现最小 generation-safe ECS

- [x] 4.1 实现固定宽度 `EntityID`、slot generation/free-list/retirement、per-world registry 与 stale handle rejection，并覆盖复用、wrap、容量和 reset tests
- [x] 4.2 实现已冻结 gameplay 所需的类型安全 component storages 与最小 views，验证 iteration invalidation、容量、内存 ownership 和跨 world handle 拒绝
- [x] 4.3 实现 typed deferred structural command buffer、稳定排序 tuple 与唯一 commit barrier，覆盖迭代中 create/destroy/add/remove、冲突和 duplicate tests
- [x] 4.4 实现 structural/component capacity tokens 与 overload rejection，证明超限不无界分配、不产生部分 mutation且结果可确定重放
- [x] 4.5 增加 ECS unit、fuzz-like randomized sequence、ASan 和连续 reset tests，验证生命周期无泄漏、use-after-free 或 stale mutation

## 5. 实现 SimulationInstance 生命周期与输入边界

- [x] 5.1 实现不可变 `AssignmentStamp`/`SimulationInstanceID`/mapping generation/build-config identity value objects 与完整 fingerprint validation
- [x] 5.2 实现 `Created -> Starting -> Running -> Draining -> Stopped/Failed` 状态机、成功初始化栈和逆序 rollback，覆盖每个启动阶段故障注入
- [x] 5.3 实现唯一 simulation worker、50 ms fixed Tick、显式 clock 与 bounded inbox，证明异步 command source 不能在 Tick 中途写 ECS/adapter/history
- [x] 5.4 实现 assignment/mapping/actor binding、InputTick window、sequence、expiry、duplicate 与 payload 安全验证，并输出稳定 rejection reason
- [x] 5.5 实现 command canonical sort、continuous hold/gap 语义、hard Tick debt 与 bounded drain/stop deadline，覆盖 arrival reorder、queue saturation 和停止竞态

## 6. 实现固定权威 gameplay pipeline

- [x] 6.1 建立唯一系统注册与 `DrainInput -> InputIntent -> AIIntent -> AbilityActivation -> Movement -> Physics -> HitDetection -> Effect -> Attribute -> Death -> Replication -> CommitDeferredStructuralChanges` 顺序门，拒绝缺失、重复或重排 system
- [x] 6.2 实现整数单位移动、加速/减速、重力、jump edge、ground/slope/step policy 和 checked mapping，并通过相关 model cases
- [x] 6.3 实现武器切换、Ability grant/activation、cost、cooldown、phase、GameplayTag 与稳定拒绝，不建立第二套 entity 生命周期
- [x] 6.4 实现剑 sweep activation-target 去重、扇子 deferred projectile lifecycle、hit ordering 与 authoritative cue source，并覆盖创建 Tick 不提前命中
- [x] 6.5 实现 GameplayEffect stack/refresh/expiry/immunity、signed 64-bit scaled Attribute、toward-zero rounding、damage 与唯一 Death cause
- [x] 6.6 实现普通怪物/Boss 的固定 AI state、target/threat/distance/ActorID tie-break、Boss phase 下一 Tick 生效和 entity-local PRNG streams
- [x] 6.7 实现 Replication/canonical state-event-rejection-capacity projection，证明 projection 只读且不反向修改 gameplay components
- [x] 6.8 逐个跑通 10 个冻结 model cases及其覆盖矩阵，不复制第二份 expected rules，也不为通过测试修改 source corpus

## 7. 接入 Jolt Physics 与 Detour navigation adapters

- [x] 7.1 定义只含项目 value types 的 `PhysicsWorld` 与 recorded adapter，实现 ground/capsule/shape/ray/overlap/projectile queries 和规范 hit sorting
- [x] 7.2 实现 Jolt adapter 的固定坐标/单位/layer/shape/solver/allocator 配置、量化与非有限值拒绝，并验证第三方类型不越过 adapter
- [x] 7.3 为 recorded/Jolt adapter 建立 live smoke/parity corpus，覆盖 callback reorder、equal fraction/ColliderID/SubshapeID tie-break 和量化容差失败
- [x] 7.4 定义只含项目 value types 的 `NavigationWorld` 与 recorded adapter，实现版本化 asset load、nearest-poly、path query 和稳定 path identity
- [x] 7.5 实现 Detour runtime adapter 的 nav digest/scale/map validation、有限 query budget 和规范排序，不创建 Recast 烘焙 target或 Tick 内动态烘焙
- [x] 7.6 为 recorded/Detour adapter 建立 live smoke/parity corpus，覆盖 asset drift、status mapping、相等候选和超容差失败

## 8. 实现有界 history、延迟补偿与 evidence

- [x] 8.1 实现 16-Tick history ring、最小 projection、generation-safe slot identity 和 8 MiB hard accounting，覆盖淘汰、缺帧、reset 与 assignment 切换
- [x] 8.2 实现绑定 actor/query kind/current Tick 的只读历史查询、future clamp、expiry/stale rejection 与当前 Tick 结果提交，证明查询不回写历史
- [x] 8.3 实现低敏 replay evidence manifest/report，绑定 build/dependency/model/profile/config/nav/physics/assignment/instance/Tick/input/seed 与 canonical digest
- [x] 8.4 实现 evidence size/retention/truncation、secret/个人数据拒绝和稳定 schema validation，证明 evidence 不写 MySQL/Redis且不表达 settlement
- [x] 8.5 增加 replay 工具路径，以相同 case/build/config/seed 连续运行并验证 digest、截断原因和 source corpus 只读性

## 9. 完成实现资格与预算补证

- [x] 9.1 聚合 unit、contract、integration、negative、Jolt/Detour parity、determinism 与 ASan CTest labels，确保任一 missing/skipped/failed gate 使整体 not-qualified
- [x] 9.2 实现固定 warmup/sample/statistics 的 1/5/8 actor benchmark，报告 median/p95/max Tick、allocation/memory、history bytes 与 queue high-watermark
- [x] 9.3 在登记 Windows x64 reference environment 验证 50 ms hard Tick、8-actor capacity、2.5 ms/Tick、64 MiB instance、8 MiB history 与 256 queue，并保留未通过时不可降级的报告
- [x] 9.4 增加不同 input arrival order、stale assignment/mapping/entity、capacity/queue overflow、hard Tick debt、partial start、drain deadline 和连续多实例 reset 的综合测试
- [x] 9.5 生成唯一 B0.3 qualification manifest/report 和 `implementation-qualified-windows-x64` 结论，明确 Linux、Go control、socket/KCP/AEAD、network 与 Unity 仍未 qualified
- [x] 9.6 增加 architecture/scope gate，扫描并拒绝 socket/listener/port、Asio/KCP、battle ticket、numeric battle message、Go repository、Unity type 和未批准 dependency

## 10. 文档、规格同步与归档门

- [x] 10.1 更新 `docs/gameplay-simulation-architecture.md`、`docs/file-structure.md`、`docs/engineering-standards.md` 与 `README.md`，记录真实 target、owner、lifecycle、adapter、命令和 evidence 边界
- [x] 10.2 更新 `docs/roadmap.md` 的 B0.3 completion evidence 与 B0.4 entry condition，保持 B0.5-B0.8 和 production UDP/Unity 门关闭
- [x] 10.3 按 `docs/code-comment-convention.md` 为所有手写 C++ 类型、成员、字段与关键不变量补齐中文文档注释，并运行声明覆盖/禁用 TODO/敏感字段检查
- [x] 10.4 连续两次运行 `tools/cpp/cpp.ps1 verify`，确认 exact dependency、clean offline build、全量 tests、sanitizer、determinism、benchmark、battle validators 和 report digest 无漂移
- [x] 10.5 同步 `game-simulation-core` 与 `delivery-sequencing` delta 到主 specs，运行 OpenSpec strict、`git diff --check`、缓存/secret 扫描和现有 Go/Unity 相关回归
- [x] 10.6 在全部 tasks 勾选、qualification report qualified、主 specs 同步且 strict 通过后归档 change，并保留开始提交作为完整回滚点

## 11. 归档前最终审计

- [x] 11.1 修正资格证据链，使固定 Release build 生成 benchmark/report，报告绑定包含 source digest 的 build identity，并严格验证 preset、ASan、编译选项与依赖 identity
- [x] 11.2 移除未消费的 DetourCrowd/DetourTileCache 构建与链接，消除 inbox/history Tick 热路径的临时分配，并让 benchmark 使用真实有界 queue 与显式内存 accounting
- [x] 11.3 加固 qualification label gate，修正文档中的 B0.3 状态、构建缓存路径、Release/ASan 证据口径和 C++ Doxygen 自动化能力描述
- [x] 11.4 运行相关 Debug/Release/ASan CTest、连续资格摘要、OpenSpec strict、`git diff --check`、缓存/secret、Go 与 Unity 工具回归，确认审计修正后仍满足归档门
- [x] 11.5 固定受控 MSVC 子进程的英文 UI locale，消除本地化 `/showIncludes` 前缀与 UTF-8 终端之间的乱码，并增加环境恢复回归与文档说明
