## Context

仓库当前按 B0.3、B0.4、B0.5、B0.6 历史阶段串联资格 receipt。后续 change 即使只修改
handshake、KCP expiry 或 snapshot acknowledgement，也会在自身 tasks、上游 binding 和
B0.6 finalize 中重复要求 clean C++ CI/ASan、Go race、server/client 全资格、完整 fault/
capacity/security/lifecycle 矩阵、连续 verify 与 soak。

现有底层工具已经分别拥有 C++、Go、Proto、storage、server/client qualification 和
battle qualification 能力，问题不是缺少测试，而是公开入口过多、默认动作过重、影响面
缺少机器声明，且最终资格被用作日常定点调试。当前 battle qualification 每个 run 还会
重新生成同源 binaries 并复制到独立目录，进一步放大时间和磁盘成本。

该变更必须保留 fail-closed 的最终资格、安全预算、故障参数和低敏 evidence，同时让个人
开发者只需理解一个公共工具，并确保代理不能以“更稳妥”为由自动升级到完整资格。

## Goals / Non-Goals

**Goals:**

- 将 change 定向验证与完整产品最终资格建立不同的命令、证据和触发边界。
- 让每个 change 用机器可读计划声明受影响 capability、现有 check owner 和必要 smoke。
- 提供一个公共质量入口，执行前打印选择原因，默认只运行增量且有界的 checks。
- 保留完整 B0.6 fault/capacity/security/lifecycle、两次 verify、soak 和 finalize，供用户
  在任意冻结 candidate 上显式调用。
- 复用现有脚本和 build tree；只收敛编排、缓存同源 run binaries、删除重复包装和任务。
- 将历史 B0.x qualification 转换为当前 capability suites，最终候选只构建一次，不按
  历史 change 编号重复生产 binaries。

**Non-Goals:**

- 不修改 production gameplay、BattleSession、协议、密码、KCP 参数、listener 或存储。
- 不降低最终资格的 12 个 fault、1/5/8 actor、第 9 actor、安全、生命周期、两次 verify、
  soak、cleanup 或报告严格性。
- 不以路径启发式静默猜测全部影响面；共享协议或 owner 变化必须由显式计划登记。
- 不在本 change 执行最终完整资格或生成 qualified report。
- 不重新实现现有 Go/C++/PowerShell validators、fault gateway 或 protocol client。

## Decisions

### 1. 使用单一公共入口，既有工具保持内部 owner

新增 `tools/quality/quality.ps1`，只公开：

- `impact`：读取 change validation plan，打印 checks、原因和是否昂贵，不执行。
- `check-change`：执行 plan 中的增量 checks，禁止调用最终资格动作。
- `diagnose`：显式运行一个 battle 场景并生成非资格 evidence。
- `qualify`：对用户显式提供的冻结 candidate 运行当前完整最终资格。

现有 `tools/cpp`、`tools/proto`、`tools/simulation-control`、
`tools/secure-battle-transport`、`tools/battle-qualification` 等仍是各自实现 owner，
统一入口只编排，不复制逻辑。文档和代理默认只引用统一入口；底层入口保留给工具维护者和
定向故障定位。

选择包装而不是合并脚本，是为了最小化变更和回归风险，同时消除使用者必须记忆十余个命令
的负担。

### 2. 每个 change 提交闭合 validation plan

活跃 change 根目录新增 `validation.json`，只引用中央 catalog 登记的 check ID，并为每个
check 说明受影响行为。计划禁止嵌入任意 shell 文本，避免 change 自己绕过 owner 或悄悄
加入完整资格。

中央 catalog 将 check 分为：

- `incremental`：format、validator、unit、contract、parity、增量 build/test；
- `targeted-expensive`：仅在受影响并发、sanitizer 或真实 socket 边界时显式选择；
- `final-only`：完整历史回归、两次 verify、完整矩阵、soak、finalize。

`check-change` 发现 `final-only` ID 必须拒绝，而不是执行。共享 `.proto`、registry、
session identity 或 generated binding 变化由 plan 显式引用跨语言 consumer check；工具
输出选择理由供评审。

选择显式 plan 而不是只分析 dirty worktree，是因为当前多个 change 共享同一工作区，Git
路径差异无法可靠归属到某个 change。未来独立分支可以用 `impact` 辅助建议，但声明仍是
验收事实源。

### 3. 最终资格只接受显式冻结 candidate

`qualify` 必须要求：

- 用户传入 current `HEAD` commit；
- worktree 无 tracked/untracked source 变化；
- final qualification manifest 完整；
- 当前平台和依赖满足既有资格要求。

它先构建一次 exact candidate 和上游 receipts，再执行当前产品 mandatory suites。完整
B0.6 仍运行两次独立 verify、mandatory soak 和 finalize；但不得在每次 B0.6 run 内重做
未变化的历史 clean build，也不得把 B0.3、B0.4 等历史 change 当作需要分别交付的产品。

`qualify` 是唯一允许调用 final-only checks 的动作。代理不得根据 source 类型、失败次数或
“更安全”推断用户已经授权完整资格。

### 4. B0.6 工具交付与实际 qualified 结论解耦

现有 `qualify-battle-network` change 保留 qualification corpus、gateway、独立客户端、
metrics、runner、完整矩阵和 finalize 判定，但完成条件改为：

- validators、failure regression 和 scope/security tests 通过；
- 代表性的 clean、loss/baseline、reconnect/rebind 场景证明公共路径可用；
- 完整最终动作可以由统一工具显式启动；
- 不要求当前开发工作区立即产生 qualified report。

完整 12-scenario、1/5/8、安全、生命周期、两次 verify、soak 和最终报告仍是最终
qualification manifest 的 mandatory 内容，不因工具 change 归档而被标记为已通过。

选择解耦而不是新增 `qualify-battle-release` change，是因为完整资格是可重复的操作，不是
一次性功能实现。工具和长期 spec 应持续存在，用户可以在任意里程碑显式调用。

### 5. 衍生 change 只保留与行为直接相关的验收

- handshake runtime repair 保留 proof、真实 listener/socket、安全负例、capacity owner 和
  shutdown/restart 定向证据。
- resync expiry 保留 profile/registry parity、KCP sender deadline、baseline-gap 和
  retransmit 定向场景。
- input acknowledgement 保留 timeline/generation、snapshot presence/partition、
  cross-language parity、MTU、loss/reorder/gap、reconnect 和多 actor 隔离。

它们不再分别拥有完整 B0.3/B0.5/B0.6、server/client 全资格、两次 verify、soak 或最终
report。最终工具仍会在冻结产品上覆盖这些能力。

### 6. 同源诊断 binaries 使用内容寻址缓存

`battle-qualification diagnose` 继续建立隔离 run/evidence/credential owner，但 Go
binaries 按 source identity 写入 ignored build cache，并在 run 目录使用同卷 hard link；
缓存缺失或 receipt 漂移时才增量重建。C++ 继续使用现有 preset build tree，不执行 clean。

最终 `qualify` 仍使用 clean build 和严格 receipts。诊断缓存不得进入最终 evidence，也
不得复用 credential、endpoint、PID、run index 或 cleanup 结论。

## Risks / Trade-offs

- [定向 plan 漏掉下游 consumer] → 共享 owner 变更必须引用中央 contract check，plan 由
  schema/closed check ID 校验，统一入口始终打印原因。
- [开发期未执行完整矩阵导致晚期问题集中] → 保留代表性端到端 smoke、定向 sanitizer 和
  显式 `diagnose`；用户可随时运行 `qualify`，但工具不得自动触发。
- [统一入口成为新的巨大脚本] → 只负责参数、plan/catalog 校验和既有 owner 调用，不实现
  build、test、gateway 或 report 逻辑。
- [缓存 binary 与 source 不一致] → cache key 绑定 source/dependency/tool identity并保存
  digest receipt；任何缺失或漂移都重建，最终资格完全不消费诊断缓存。
- [移除子 change 全资格任务被误解为降低最终标准] → final-only catalog 和长期
  battle-network-qualification spec 保留原矩阵及 fail-closed 语义。
- [已有 dirty worktree 无法立刻执行最终资格] → 这是预期边界；开发检查可继续，只有用户
  建立 clean frozen candidate 后才允许最终资格。

## Migration Plan

1. 建立 project-validation spec、统一 catalog/schema 和公共入口的只读 `impact`。
2. 为三个活跃衍生 change 增加 validation plan，并实现 `check-change` 的最小既有工具编排。
3. 增加显式 `diagnose` 和 `qualify`；`qualify` 只编排既有完整链并拒绝 dirty/non-current
   candidate，本 change 不实际运行该动作。
4. 为 battle diagnose 增加同源 binary 缓存与 hard-link run materialization，不改变
   runtime/evidence owner。
5. 调整活跃 change artifacts、长期 delivery sequencing、workflow、roadmap 和 runbook。
6. 运行 catalog/plan/failure tests、各活跃 change 的定向 checks 和 OpenSpec strict。

回滚时删除统一入口、catalog 和 validation plans，恢复原文档与 tasks；既有底层资格脚本、
corpus、run evidence 和 production code均保持不变，不需要数据迁移。

## Open Questions

无。完整资格的具体执行时点由用户或未来发布流程显式决定，不由工具或代理推断。
