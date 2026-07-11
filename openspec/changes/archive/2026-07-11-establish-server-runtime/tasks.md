## 1. 运行基础与配置

- [x] 1.1 核验并锁定本 change 新增的正式 Go dependencies，在 `versions.yaml`/`go.mod` 各自职责内更新版本声明，保持项目 Go wrapper、generated code 与 contract tests 可重复运行
- [x] 1.2 创建 `server/config/local.yaml` 与 `internal/config` 类型化 schema，支持显式 `--config`、安全默认值和白名单 `IHOMELAND_` 环境覆盖
- [x] 1.3 实现配置严格解码与完整校验，拒绝未知字段、非法 duration/level/address/range 和冲突配置，并证明失败发生在 listener、文件写入或 goroutine 之前
- [x] 1.4 增加配置优先级、未知字段、非法覆盖、敏感值不回显和启动配置快照的 table-driven tests
- [x] 1.5 创建不可变 build info、system clock 与 `crypto/rand` ID generator，并提供只用于测试的 deterministic fake

## 2. 日志、Metrics 与状态

- [x] 2.1 基于 `log/slog` 实现 JSON/text logger 构建与启动期 level 配置，统一稳定字段并增加配置、凭据和 header/body 不泄漏测试
- [x] 2.2 引入锁定版本的 Prometheus Go client，建立只包含低基数 lifecycle、startup、shutdown、diagnostic request 与 task failure 指标的 registry
- [x] 2.3 实现并发安全的 `starting -> ready -> draining -> stopped` 单向 readiness 状态机，拒绝非法回退
- [x] 2.4 为 readiness 并发读取、状态迁移、非法迁移和 race 行为增加单元测试

## 3. 生命周期与后台任务

- [x] 3.1 在 Composition Root 消费侧定义只面向真实资源的 lifecycle component 契约和稳定组件命名规则
- [x] 3.2 实现顺序启动与 started stack，确保 Start 成功后才登记组件，并在中间失败时只逆序回滚已成功组件
- [x] 3.3 实现共享总 deadline 的逆序关闭、Stop 错误聚合和继续释放语义，记录超时或失败组件但不重复 Stop
- [x] 3.4 实现受控 task group，登记 root/component owner context、稳定任务名、异常 error 传播、panic recovery、stack 内部诊断与有界 Wait
- [x] 3.5 使用 fake components/tasks 覆盖完整启动、中间失败、回滚顺序、Stop 聚合、任务异常、panic、忽略取消、并发 shutdown 和 race tests

## 4. 诊断 Listener

- [x] 4.1 使用独立标准库 `net/http` server 实现诊断 listener，并配置 header/read/write/idle timeout、最大 header 与 graceful shutdown
- [x] 4.2 实现 `/healthz`、`/readyz`、`/version` 与 `/metrics`，保证 method/path、status、content type、响应大小和敏感字段边界稳定
- [x] 4.3 确保诊断 listener 默认绑定 loopback、未知或业务路径不进入 application service，并在非 loopback 配置中输出明确安全诊断
- [x] 4.4 使用进程内 HTTP server 与真实动态端口覆盖 starting/ready/draining/stopped、未知路径、错误 method/body、timeout、metrics 低基数和关闭后端口释放

## 5. Composition Root 与进程入口

- [x] 5.1 创建 `internal/app` 唯一 Composition Root，按 config -> logging/metrics/build info -> readiness -> diagnostic -> required components 的顺序构造和启动
- [x] 5.2 实现运行时致命错误与第一次 OS signal 触发 draining、按 component ownership 取消任务、逆序关闭和诊断 listener 最后停止的统一流程
- [x] 5.3 创建薄 `cmd/server`，只处理 bootstrap flag、平台 signal/cause、第二次终止请求、稳定退出结果映射和唯一 `os.Exit` 边界，并以表驱动测试固定退出码
- [x] 5.4 增加真实 binary process tests，使用临时配置与动态端口验证 invalid config、ready 探针、正常 signal 零退出、关闭超时、日志和端口释放
- [x] 5.5 验证当前 runtime 不创建 MySQL、Redis、账号/session/业务领域、公开 HTTPS、WSS/TCP/UDP/KCP 或 Unity 依赖，不存在第二个 Composition Root、service locator 或 package-global mutable service

## 6. 文档与完成门禁

- [x] 6.1 更新 `server/README.md`、`docs/file-structure.md`、`docs/architecture.md`、`docs/engineering-standards.md` 与 `docs/roadmap.md`，记录配置、诊断端口、启动/停止命令、退出码和 S2 消费入口
- [x] 6.2 从 clean generated 状态执行协议生成与验证，再运行 Go format、vet、unit/process/race tests、依赖与 secret 扫描、`git diff --check`
- [x] 6.3 同步 `server-architecture` 主 spec，确认全部 tasks、注释规则、文档和实际进程行为一致并通过 OpenSpec strict
