## Context

S0 已提供最小 Go module、版本锁定工具、契约 validator、fixtures 和 listener-independent codec。当前没有 `cmd/server`、进程配置、Composition Root、诊断 listener、后台任务 owner 或关闭协议；如果 S2-S6 各自建立这些能力，将产生多个入口、不同退出语义和无法统一回滚的资源生命周期。

本 change 只建立单进程平台。它必须能在没有 MySQL、Redis、账号、个人世界和业务网络 listener 的情况下由 unit/process tests 独立验收，并为后续 concrete components 提供窄而稳定的接线边界。

## Goals / Non-Goals

**Goals:**

- 创建可直接启动的 `cmd/server` 与唯一 Composition Root。
- 在任何网络或后台副作用前完成类型化配置验证。
- 固定 lifecycle component、受控任务、readiness、signal、退出码和总关闭 deadline。
- 提供与业务面隔离的健康、就绪、版本和 metrics 诊断入口。
- 使用结构化、可测试且不泄漏敏感值的日志与可观测边界。
- 用 fake component、fake clock 和真实子进程覆盖启动、回滚、并发与关闭行为。

**Non-Goals:**

- 不实现账号、session、ticket、安全凭据、个人世界/访客会话领域或 repository。
- 不连接 MySQL、Redis 或外部 observability backend。
- 不实现公开 HTTPS API、WSS、TLS/TCP、UDP/KCP 或 Go test client。
- 不创建通用 service locator、全局 mutable registry、热重载或插件系统。
- 不创建 Unity 工程或客户端代码。

## Decisions

### 1. `cmd/server` 保持薄入口，Composition Root 返回稳定运行结果

`cmd/server/main.go` 只解析 bootstrap 级 `--config`、建立 signal context、调用 `app.Run` 并在唯一位置执行 `os.Exit`。`internal/app` 拥有 concrete dependency graph、启动顺序、readiness 与关闭编排；其他 package 不允许导入 `cmd/server` 或保存全局 service。

`app.Run` 返回可分类结果，由 main 映射退出码：正常 signal 且干净关闭为零，配置/初始化/运行时致命错误/关闭超时为非零。选择返回结果而不是在深层调用 `log.Fatal` 或 `os.Exit`，使生命周期可以在进程内单测，也保证 defer 与回滚有效。

替代方案是让每个 adapter 自行监听 signal 和关闭资源；该方案无法定义全局顺序与总 deadline，因此拒绝。

### 2. 配置集中在 `server/config/`，采用显式文件与白名单环境覆盖

仓库只提交不含秘密的 `server/config/local.yaml`。进程通过 `--config` 选择文件，使用现有 YAML parser 严格解码到类型化结构，并拒绝未知字段。部署差异通过显式 `IHOMELAND_` 白名单覆盖，优先级固定为内建安全默认值、配置文件、环境覆盖；不自动读取 `.env`，不支持任意反射式 key 映射。

配置加载返回启动后不再修改的值快照。地址、duration、日志级别、关闭预算和诊断 timeout 在创建 logger/listener/task 前一次性完成交叉校验；错误只报告键与原因，不回显值。本 change 不实现热重载，避免后续组件误以为所有配置都能原子变化。

端口遵守 `docs/network-port-allocation.md`：仓库值只是可覆盖的推荐默认值，环境配置持有实际绑定地址。端口冲突必须使 listener 启动失败并进入统一回滚，不允许自动递增或随机回退；process tests 则使用操作系统动态分配端口，避免不同开发机和 CI worker 争用固定值。

替代方案是只使用环境变量，但本地可发现性与结构化校验较差；只使用 YAML 又不适合容器部署覆盖，因此采用受控组合。

### 3. 生命周期接口只覆盖真实资源，启动栈是唯一关闭顺序源

在 Composition Root 消费侧定义窄接口：稳定组件名、`Start(context.Context) error` 与 `Stop(context.Context) error`。纯值对象、logger、clock 和无资源 service 不注册 component，避免产生空 manager 或仪式化 interface。

Runtime 顺序启动组件，并仅在 Start 成功后压入 started stack。失败回滚和正常关闭都只遍历该栈的逆序；失败组件不会 Stop，Stop 错误会聚合但不会阻断剩余释放。所有 Stop 共享一个总 shutdown context，组件不得各自重新获得完整预算。

替代方案是为每个阶段维护手写启动/关闭列表；两份列表容易漂移，无法证明部分失败路径，因此拒绝。

### 4. 受控任务组拥有所有长生命周期 goroutine

后台任务必须向 Composition Root 的 task group 登记，具有稳定名称、明确 owner context、错误通道和 Wait。Root task 使用进程 context；component task 使用该组件的 child context，并由组件 Stop 取消和等待，因此全局关闭不会在逆序 Stop 之前粗暴取消诊断任务。任务在未取消时返回 error 或 panic 均视为运行时致命失败：记录组件名，readiness 切到 draining，并触发统一关闭。panic 在任务边界恢复并转换为带 stack 的内部诊断，但 stack 只进入受控服务端日志。

任务组统一观察异常并在 shutdown deadline 内确认所有已登记任务退出；超时报告仍未完成的任务名称并返回非零结果。短生命周期的请求 goroutine 由对应 listener owner 管理，不重复注册到全局组。

替代方案是允许 package 直接 `go func()`；这种任务无法审计所有权、取消与退出，因此禁止。

### 5. Readiness 使用单向状态机，诊断 listener 最先启动、最后关闭

状态固定为 `starting -> ready -> draining -> stopped`，不允许从 draining 回到 ready。配置验证和纯内存依赖构建完成后，先启动诊断 listener；全部必需组件成功后才切到 ready。关闭触发后先进入 draining，再逆序停止组件并由各 owner 取消自身任务；诊断 listener 因最先进入 started stack 而最后关闭，使编排系统能够观察整个排空窗口。

- `/healthz`：进程事件循环仍可服务时返回 200，不把外部依赖健康误当作 liveness。
- `/readyz`：仅 ready 返回 200，其他状态返回 503 和稳定状态名。
- `/version`：返回有界、非敏感、不可变 build metadata。
- `/metrics`：使用 Prometheus/OpenMetrics 兼容文本，禁止 player/session/world/visit 等无界标签。

诊断面使用独立标准库 `net/http` server，而不是提前引入公开业务 Gin router。配置包含 header/read/write/idle timeout；默认只绑定 loopback，部署改为非 loopback 时必须由网络策略限制访问。

### 6. 可观测基础优先使用标准库并保持 facade 窄小

结构化日志基于 `log/slog`，生产输出 JSON，本地可选择 text；字段名遵守工程标准。package 接收 `*slog.Logger` 或其派生 logger，不再包装一套重复日志 API。日志级别可配置，但敏感值过滤是调用边界责任，配置错误和 HTTP handler 不输出原始 body/header。

正常结构化运行日志写入 `stdout`，使终端、IDE 和容器采集器不会把 INFO 误呈现为错误；参数解析、bootstrap 失败、强制退出和最终非零结果写入 `stderr`。严重性仍由结构化 `level` 字段表达，不能仅依赖输出流判断日志等级。进程不直接管理日志文件和轮转，持久化由本地重定向或部署日志采集器负责。

Metrics 使用成熟 Prometheus Go client，并在实现时按 `versions.yaml`/`go.mod` 规则锁定兼容正式版。只预注册进程生命周期、启动结果、shutdown、HTTP 诊断请求和 task failure 等低基数指标。Clock 与 ID generator 只在真实消费边界定义窄接口，production 实现使用系统时间和 `crypto/rand`，测试使用 deterministic fake。

替代方案是自研 metrics 格式或日志 facade；前者增加兼容成本，后者只复制 `slog`，因此拒绝。

### 7. Process tests 使用动态端口和真实信号验证外部行为

单元测试通过 fake components 覆盖顺序、回滚、Stop 聚合、任务失败、panic 与 deadline。诊断 handler 通过进程内 HTTP server 与动态端口验证状态和响应边界。Process tests 构建真实 binary，使用 OS 分配端口和临时配置启动，轮询 ready 后发送受支持信号，验证退出码、日志、探针和端口释放；不得依赖固定 sleep、真实数据库或公网。

Windows 与 Linux signal 能力不同，process helper 统一抽象“请求受控关闭”，平台专用文件只处理信号差异，不改变 app lifecycle 语义。Race test 覆盖 readiness、任务错误与并发 shutdown。

## Risks / Trade-offs

- [诊断 listener 在业务组件前可访问] -> readiness 在全部组件完成前保持 503，listener 只暴露有界诊断路径并默认绑定 loopback。
- [通用 lifecycle interface 被滥用] -> 只允许真实资源/后台生命周期注册，纯 service 和值对象保持直接构造。
- [任务 panic recovery 掩盖缺陷] -> recovery 只用于记录并触发非零关闭，不允许进程继续以 ready 状态运行。
- [单一总 shutdown deadline 使后置组件预算变少] -> 这是保证进程退出上限的有意取舍；日志和 metrics 暴露耗时组件，后续按数据调整顺序与预算。
- [本地 YAML 与环境覆盖产生双源] -> 配置结构只有一个 Go owner，覆盖键显式登记并由 table-driven tests 验证优先级和非法值。
- [Prometheus dependency 扩大基础层] -> 只在 observability adapter 使用，application/domain 不依赖其类型。
- [平台 signal 行为不同] -> app 层只消费 context/cause，平台差异限制在 cmd/process test adapter。

## Migration Plan

1. 扩展版本与依赖声明，建立配置、build info、logging、metrics、clock/ID 和 readiness 基础类型。
2. 实现 lifecycle runner 与受控 task group，先用 fake components 完成失败注入和 race tests。
3. 创建诊断 listener 与 handlers，验证 timeout、状态、低基数 metrics 和安全响应。
4. 创建 `cmd/server`、signal/exit mapping 与 process tests，确认本地可启动、可探测、可关闭。
5. 更新服务端、目录、运行命令与路线文档，执行 clean checkout、Go test/vet/race、OpenSpec strict 和仓库卫生验证。

当前没有生产部署或其他 runtime 消费者，失败时可以整体回退本 change。后续 change 一旦注册真实组件，不得建立平行 Composition Root；需要改变 lifecycle contract 时必须通过独立 OpenSpec change 演进。

## Open Questions

无。
