## Why

S0 已冻结跨端契约与无 listener codec，但仓库还没有可启动的服务端进程、统一生命周期或诊断入口。后续身份、领域、存储和网络 changes 需要先共享一个能在初始化失败时回滚、在关闭时有界退出并能被自动化观察的运行基础。

## What Changes

- 扩展现有 Go module，创建唯一 `cmd/server` 进程入口与 Composition Root。
- 建立类型化配置、启动期完整校验、结构化日志、clock/ID/metrics 边界和稳定 build metadata。
- 建立组件生命周期协议，按依赖顺序启动、在部分失败时逆序回滚，并按 deadline 逆序关闭。
- 提供独立诊断 HTTP listener，暴露 `/healthz`、`/readyz`、`/version` 与 metrics，不承载公开业务 API。
- 建立 readiness 状态机、受控后台任务、OS signal、退出码和稳定关闭原因。
- 增加不依赖 MySQL、Redis 或业务 listener 的 unit、process、failure injection、并发与 shutdown tests。
- 不实现账号、session、业务领域、repository、公开 HTTPS、WSS、TLS/TCP、UDP/KCP 或 Unity runtime。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `server-architecture`：细化服务端进程配置、Composition Root、组件生命周期、诊断 listener、readiness、后台任务和有界关闭的可验收行为。

## Impact

- 扩展 `server/go.mod`，新增 `server/cmd/server` 与 `server/internal` 下的运行时、配置、诊断和可观测基础包。
- 新增服务端本地配置示例、启动命令和 process test 入口，并同步服务端、目录、工程与路线文档。
- 后续 S2-S6 必须把真实组件注册到本 change 建立的生命周期和诊断边界，不得创建第二个 Composition Root 或平行进程框架。
