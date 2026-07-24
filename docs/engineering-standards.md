# iHomeland 工程标准

## 基本原则

- 正确性优先，清晰性优先于炫技。
- 所有权、依赖、生命周期和失败行为必须显式。
- 抽象只为替换、测试或稳定边界服务。
- 文档、规格、代码和实际运行行为必须一致。
- 先写可验收行为，再实现代码。

## Go 规则

- 包名小写短名，不使用下划线、中划线或无意义复数。
- 文件名小写蛇形，例如 `world_state.go`。
- 导出标识符 PascalCase，非导出标识符 camelCase。
- 缩写保持 `ID`、`URL`、`HTTP`、`RPC`、`TCP`、`UDP`、`JSON`、`SQL`。
- 错误变量使用 `Err` 前缀。
- `context.Context` 是函数首参并命名 `ctx`，不得存入 struct。
- 接口由消费者或边界 owner 定义，只在有替换/测试需求时创建。
- 禁止 package global 保存业务 service、连接或 mutable state。

## C# 与 Unity 规则

- 业务 Service 优先使用普通 C# 类型。
- MonoBehaviour 只用于 Unity callbacks、GameObject、Inspector 和 Host。
- 公共类型 PascalCase，私有字段 `_camelCase`。
- async 方法使用 `Async` 后缀并显式处理取消与异常。
- UnityEngine.Object 只能在主线程访问。
- 不使用静态 singleton 传播全部服务；Composition Root 显式注入窄接口。
- Scene/Prefab/ScriptableObject 不保存在线业务最终事实。

## C++ Game Simulation Core 规则

- 语言基线为 C++20，唯一入口是 `tools/cpp/cpp.ps1` 与 tracked CMake Presets；
  不接受系统默认 compiler、浮动依赖或手工 IDE project 作为构建事实。
- `SimulationInstance` 唯一拥有 worker、Tick、inbox 与 startup/drain/stop 生命周期；
  producer 只能提交 intent，不能在 Tick 中途写 ECS、adapter 或 history。
- structural mutation 只在唯一 commit barrier 执行；component、query、history 和
  evidence 均有 hard capacity，超限必须原子拒绝。
- gameplay 只依赖项目 value ports；Jolt、Detour 与 JSON types 只能出现在 concrete
  adapter translation units 或 adapter tests。
- C++ 手写声明、成员、字段和关键不变量使用中文 `///` 文档注释，参数、返回值、
  异常、ownership、单位与 lifetime 语法遵守 `docs/code-comment-convention.md`。
- `windows-msvc-debug` 用于开发，`windows-msvc-ci` 用于 release-style clean gate，
  `windows-msvc-asan` 用于项目与 Jolt 使用一致 sanitizer ABI 的内存安全门。

## 分层规则

- Transport 不实现业务状态机。
- Application 表达用例和事务。
- Domain 表达规则、不变量与状态迁移。
- Infrastructure 实现外部依赖。
- Composition Root 连接 concrete types。
- UI 只显示状态并提交 command。

依赖不得反向：domain 不引用 transport/storage/UI；服务端不得引用 Unity；客户端 view 不引用 socket。

## 生命周期与并发

- 每个长生命周期对象必须说明 owner、创建、停止和释放。
- 初始化部分失败必须回滚已成功项。
- 关闭顺序默认与成功初始化顺序相反。
- 后台任务必须有 context/cancellation 和 bounded shutdown。
- 长生命周期 goroutine 必须向 Composition Root task group 登记稳定 owner/name；禁止无法等待、无法取消或错误无人消费的裸 goroutine。
- Component task 由 component context 拥有并在 Stop 中取消，不能在 draining 开始时无序取消全部任务。
- channel/queue 必须有容量与背压策略。
- 锁必须保护明确不变量，不用全局大锁掩盖 ownership 不清。
- 并发 registry、world mutation、lease 和 visit membership 必须运行 race tests。

## 错误处理

- 错误必须显式返回或处理，不静默吞掉。
- 跨边界错误使用稳定类型或 code。
- 内部错误保留诊断 cause，外部错误不泄漏实现细节。
- retryable、timeout、conflict、permission 和 validation 必须区分。
- 需要忽略错误时必须注释原因和安全后果。

## 配置与密钥

- 全部技术版本以根目录 `versions.yaml` 为治理源，升级规则见 `docs/technology-versions.md`。
- 禁止浮动 `latest`、未锁定 generator 或与版本目录不一致的生态配置。
- Go 命令通过 `tools/go/go.ps1` 使用校验后的项目 SDK，不得依赖系统 Go，也不得用 `go env -w` 写入项目专用配置。
- 配置来自文件、环境变量或 secret mount。
- 默认配置不包含真实密钥。
- 启动后立即校验必要字段和范围。
- 未知配置字段、非法环境覆盖和多 YAML document 必须在任何 listener、文件写入或 goroutine 创建前失败。
- 配置错误只报告键和约束，不回显环境值、secret 或完整配置。
- 地址、端口、TTL、超时和大小不得散落硬编码；端口默认值、环境覆盖、映射与冲突处理遵守 `docs/network-port-allocation.md`。
- 推荐端口只是可覆盖默认值。固定 listener 绑定失败必须显式失败并保留地址与原因，不得静默递增或随机改用其他端口。
- 进程内自动化测试打开真实 listener 时使用操作系统动态分配端口。跨进程测试优先回传实际绑定地址；只能先分配后重绑时必须限制在测试代码中并执行有界重试，不得占用仓库默认端口。
- 生产 TLS 私钥、数据库密码和 token signing key 不进入 Git。
- 本机配置使用 `.env.local` 或等价忽略文件。

## 日志与可观测

日志字段保持稳定：

- `component`
- `operation`
- `request_id`
- `command_id`
- `connection_id`
- `session_id`（安全摘要）
- `player_id`
- `personal_world_id` / `visit_session_id`（低敏关联值）
- `message_id`
- `error`

级别：

- Debug：开发诊断，不承载审计。
- Info：正常生命周期和重要业务结果。
- Warning：可恢复异常、拒绝和降级。
- Error：不可恢复错误、不变量破坏和依赖失败。

不得记录密码、完整 token/ticket、密钥或包含凭据的 payload。

正常结构化运行日志写入 `stdout`；参数解析、bootstrap 失败和无法继续运行的进程级诊断写入 `stderr`。日志严重级别仍以结构化 `level` 字段为准，输出流只表达命令行消费边界。服务进程不直接创建或轮转本地日志文件；本地留档由 IDE/终端重定向负责，部署环境由日志采集器持久化。

Metrics label 必须来自稳定有限集合；禁止把 request URL、错误文本、player/session/world/visit ID 或其他无界值作为 label。健康、就绪、版本和 metrics 使用独立诊断 listener，不得混入公开业务 handler。

## 数据规则

- MySQL migration 必须可按顺序初始化空数据库。
- 已合并 migration 不修改。
- 表、字段、索引和事务边界有 owner 与用途。
- MySQL 表和列必须提供中文短注释；时间、时长、容量与速率必须注明真实时区、精度或单位，完整规则见 `docs/storage-schema-comment-convention.md`。
- Redis key 必须记录 owner、TTL、value、恢复来源和清理触发。
- Redis 没有原生 COMMENT 时，已实现 value schema 必须在 `docs/redis-keys.md` 维护英文字段名、类型/编码、中文短注释与必填规则。
- Redis 不保存唯一持久事实。
- schema/key 变化必须更新文档和 integration tests。
- 个人世界交互必须分别指定 PlayerState、PersonalWorldState、VisitSessionState 或 SimulationInstanceState owner；未来具有独立生命周期的活动才使用 ActivityInstanceState。Visitor 奖励与 Owner 世界 mutation 不得通过跨存储顺序双写伪装原子提交。
- WorldInstance assignment、lease、presence、invite 和 admission 是可恢复或可失效运行态，不得进入 PlayerID/PersonalWorldID 主键或覆盖持久世界 owner。

## 协议规则

- 禁止手工修改 generated code。
- Go/C# generated code、descriptor 和可推导 projection 进入精确忽略目录，由统一入口在编译前重建。
- `go.sum`、Unity 正常资产 `.meta`、registry 与 fixtures/golden 属于可复现或兼容性基线，不能因其由工具写入而忽略。
- Unity Scene、Prefab 与 ScriptableObject 不得序列化引用已忽略的 generated scripts，避免本机 `.meta` GUID 成为共享事实。
- 每个 message id 和错误码有 owner。
- 每个实时消息登记唯一 allowed channel。
- 字段必须明确单位、范围、是否可空和未知值行为。
- 帧与消息必须有大小上限。
- request/command/push 语义分离。
- schema 变化必须同步 fixtures、route registry 和双端生成验证。

## 注释规则

代码注释的唯一 owner 文档是 `docs/code-comment-convention.md`。

- 所有手写 package/type/member 级声明按语言规则提供可提取的文档注释。
- Go 使用 Go doc；C# 使用 XML documentation；Protobuf 在 `.proto` 源文件维护注释。
- 注释简明定位职责，并重点解释原因、不变量、所有权、失败、并发、安全和生命周期。
- 显然局部变量依赖清晰命名；存在单位、范围、快照、线程、资源或安全语义时必须补充原因。
- 状态机、幂等、重连、权限、锁、缓存恢复和关闭竞态必须说明设计意图。
- C# `#region` 受控使用，Go 不使用伪 region。
- `TODO` 必须包含 owner、原因、触发条件和后续动作。

## 测试标准

服务端：

- domain table-driven unit tests
- application tests with fakes
- repository/cache/codec contract tests
- MySQL/Redis/HTTP/WSS/TCP integration tests
- race tests
- Go protocol client scenarios
- recovery/load/shutdown qualification

客户端：

- pure C# EditMode tests
- lifecycle/network PlayMode tests
- Go/C# golden packet parity
- dual UI focus/layer tests
- multi-client manual acceptance
- Windows Development/Release build

跨端模型 fixtures：

- schema、manifest、case inventory、引用、稳定排序、单位、coverage 与 canonical digest 验证
- 隔离临时副本上的 unknown field、duplicate ID、漂移、安全字段和缺失 coverage 失败回归
- validator 连续运行输出一致且不修改 corpus；纯模型门不得启动网络、Docker 或 gameplay evaluator

C++ simulation：

- unit、contract、integration、negative、determinism、Jolt parity、Detour parity、
  replay、comprehensive、benchmark、architecture 与 ASan labels 缺一不可；
- reference benchmark 固定 1/5/8 actors、warmup/sample/statistics，并报告
  median/p95/max Tick、Tick 热路径 allocation、显式 instance/history bytes 与真实
  bounded queue high-watermark；
- qualification 只能在 clean Release CI 和独立 ASan 全套成功后由 Release binary 生成，
  并绑定含 source digest 的 build identity 与同源 CI/ASan gate receipt；
  Debug/ASan benchmark smoke 不能替代 Release reference measurement；
  qualification 必须声明
  `implementation-qualified-windows-x64`，不得据此声称 Linux、Go control、网络或
  Unity runtime 已获资格。

Bug 修复优先增加可复现测试。无法运行测试时必须说明原因与剩余风险。

## 质量门

每个 change 完成前至少通过：

- formatter/linter
- relevant unit/integration tests
- OpenSpec strict
- `git diff --check`
- 声明级注释覆盖与内容符合 `docs/code-comment-convention.md`
- 生成物一致性检查（适用时）
- 无密钥、缓存和本机文件
- 文档与实现一致性评审
