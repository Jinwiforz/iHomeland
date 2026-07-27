## ADDED Requirements

### Requirement: 默认 change 验证必须只覆盖声明的影响面

每个实现 change MUST 提供机器可读且 closed-schema 的 validation plan，引用中央 catalog
登记的 check ID，并逐项说明当前行为或下游 contract 为什么受影响。默认
`check-change` MUST 只执行 plan 中的 incremental 或显式 targeted checks，MUST NOT
调用历史阶段完整资格、完整产品矩阵、连续 verify、长时 soak 或 finalize。共享协议、
registry、session identity、generated binding 或 owner contract 变化 MUST 显式包含所有
登记 consumer 的 contract/parity check；工具不得以 dirty worktree 路径猜测替代 plan。

#### Scenario: Input acknowledgement change 执行定向验证

- **WHEN** change 只修改 InputTimeline projection、snapshot acknowledgement 和对应跨语言 consumer
- **THEN** validation plan 执行 timeline/generation、presence/partition、parity、MTU、相关 loss/reconnect smoke 与受影响 sanitizer，不执行第 9 actor、完整安全矩阵、连续 verify 或 soak

#### Scenario: Change plan 引用最终资格动作

- **WHEN** `check-change` 读取的 validation plan 包含 final-only check
- **THEN** 工具在执行前稳定拒绝并报告非法 check ID，不把用户的 change 验证请求解释为完整资格授权

### Requirement: 影响面与执行原因必须可预览和审计

项目 MUST 提供唯一公共质量入口的 `impact` 动作，在不构建、不启动 listener、不修改
evidence 的情况下验证 change plan、中央 catalog、check class 和 owner，并输出稳定排序的
check ID、成本分类与中文原因。Plan MUST NOT 嵌入任意 shell 文本、绝对路径或未登记工具；
unknown、duplicate、循环依赖或缺失 reason MUST fail closed。

#### Scenario: 使用者预览 change 验证

- **WHEN** 使用者对一个有效 OpenSpec change 执行 `impact`
- **THEN** 工具只显示将执行的既有 owner checks、分类和原因，不运行测试、不创建资格 run 且不要求使用者理解底层脚本调用顺序

#### Scenario: Plan 尝试注入任意命令

- **WHEN** validation plan 包含中央 catalog 未登记的 command、脚本路径或 check ID
- **THEN** schema/catalog gate 拒绝该 plan，统一入口不得执行其中内容

### Requirement: 完整最终资格必须由显式冻结 candidate 触发

完整产品资格 MUST 只由统一入口的 `qualify` 动作执行，并 MUST 要求使用者显式传入等于
current `HEAD` 的 commit、clean worktree 和完整 final qualification manifest。该动作
MUST 构建一次 current product candidate，运行当前 mandatory capability suites、完整
fault/capacity/security/lifecycle matrix、连续两次 verify、mandatory soak、cleanup 与
finalize；它 MUST NOT 按 B0.3、B0.4 至未来历史 change 编号为同一 candidate 重复构建
产品。任何 source、contract、config、tool 或 harness 变化 MUST 终止当前 candidate，
不得复用旧最终结论。

#### Scenario: 用户显式启动最终资格

- **WHEN** 用户以 clean current commit 调用 `qualify` 且所有 final manifest 输入完整
- **THEN** 工具可以执行完整最终资格并生成一个绑定当前产品 capability suites 的结论，不要求用户手工串联底层工具

#### Scenario: 代理尝试自动升级

- **WHEN** 用户只要求实现、修复、诊断或 change 检查，而没有显式调用最终资格
- **THEN** 工具和代理不得执行完整矩阵、连续 verify、soak 或 finalize，即使受影响代码属于协议、安全、C++ 或网络边界

#### Scenario: Dirty worktree 请求最终资格

- **WHEN** `qualify` 的工作区存在 tracked 或 untracked source 变化，或 candidate 不等于 current `HEAD`
- **THEN** 入口在 clean build、storage 和 listener 启动前拒绝，不对变化中的开发工作区生成最终证据

### Requirement: 统一入口必须复用 owner 和同源开发产物

公共质量入口 MUST 只编排现有 Go、C++、Proto、storage、server/client 和 battle
qualification owner，不得复制 validator、test oracle、fault gateway、protocol client 或
finalize 规则。开发诊断 MAY 按 source/dependency/tool identity 复用 ignored binary cache
和现有增量 build tree，但每个 run MUST 新建 credential、endpoint、process、evidence 和
cleanup owner；最终资格 MUST NOT 消费诊断 cache、diagnostic evidence 或旧 cleanup 结论。

#### Scenario: 相同 source 重复诊断

- **WHEN** 使用者在未修改 source/tool identity 时重复执行同一或相邻诊断场景
- **THEN** 工具复用已验证 binaries 或增量 build tree并建立新的隔离 run evidence，不重复 clean build、不复制 credential或复用资格结论

#### Scenario: 缓存 identity 漂移

- **WHEN** source、dependency、toolchain 或缓存 receipt 与当前输入不一致
- **THEN** 开发入口丢弃该缓存并增量重建，最终资格仍按 clean candidate 生成独立产物
