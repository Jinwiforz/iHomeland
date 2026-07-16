# 服务端 v1 资格验收

## 目的与边界

`qualify-server-v1` 是 Unity 运行时代码开始前的 Q0 发布门。它证明一个不了解服务端内部类型的消费者，仅凭已提交的 HTTPS、WSS、TLS/TCP 与 Protobuf/OpenAPI 契约，可以完成 account/session、own-world、visit-world、安全拒绝、依赖恢复和资源关闭流程。

资格客户端位于 `server/internal/testclient`，是长期保留的服务端外部回归工具，不是 Unity 的替代品，也不是产品 SDK。它不拥有 Scene、UI、输入、表现或客户端产品状态机；正式玩家体验始终由 Unity 客户端实现。

本资格边界不包含 ActivityInstance、Room、Party、匹配、战斗、UDP/KCP、观战、回放、完整经济系统、Unity 工程或跨节点 placement。

## 冻结基线

- qualification version：`server-v1`
- manifest schema：`1`
- report schema：`1`
- protocol version：`1`
- contract aggregate algorithm：`sha256-path-content-v1`
- contract aggregate digest：`9d659c133e167654661c699629af53a83ce4f5583809c7313bbdabbb1d6107fb`

冻结输入由 `shared/contracts/fixtures/qualification/freeze.json` 所登记算法确定性计算。输入包括已提交 schema、OpenAPI、route/error registry、qualification manifest、layered evidence manifest 与兼容 fixtures/golden；generated code、descriptor、运行报告和 digest record 自身不参与摘要。任何输入增删或内容漂移都必须更新对应契约、兼容证据和本摘要，不能只改 digest。

## 唯一完整入口

在仓库根目录执行：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass `
  -File .\tools\qualification\qualification.ps1 `
  -Action verify `
  -TimeoutSeconds 1200 `
  -FuzzTimeSeconds 3
```

`-Action contract` 只验证协议、fixture、freeze、manifest/runner completeness 和工具契约；`-Action blackbox` 用于缩短本地排障路径。两者都会生成 `qualified=false` 的报告，不能解锁 C0。只有 `-Action verify` 可以产生资格结论。

入口在 `.local/qualification/<run-id>/` 创建 ignored 临时目录，构建精确 server/client binary，生成临时 TLS，借用 `tools/storage/storage.ps1` 创建带 owner label 的隔离 MySQL/Redis，并使用动态 loopback 端口。故障和删除只允许作用于当前 run-id/PID；失败、超时或中断后仍进入独立 cleanup budget。该目录中的 binary、证书、日志和报告都不是版本化交付物。

## Mandatory 证据

| 门禁 | 证明内容 | 证据层级 |
| --- | --- | --- |
| contract | Proto format/lint/breaking、deterministic generation、registry、fixtures、manifest 与 freeze | contract |
| unit | 全 Go module 非缓存单元/集成图 | package |
| fuzz | account/session/contracts/world/visit/storage/WSS/TCP/testclient 的显式非零 target 清单 | fuzz |
| race | account/session/placement/VisitSession/app/WSS/TCP/testclient 并发 owner | race |
| storage | 真实 MySQL/Redis migration、codec、重启、flush、corruption 与 recovery | storage integration |
| layered-evidence | commit-unknown、lease/response-loss、slow consumer、半帧、并发 writer、registry/rate 上限、shutdown deadline 与低基数 metrics 的稳定命名测试 | package/process |
| build | 按本轮 source 构建精确 server 与 qualification binaries | toolchain |
| environment | 临时 TLS、隔离 storage、动态 loopback listeners 与 readiness | process/storage owner |
| black-box | 独立 `cmd/server` 上的全部 manifest 场景、真实 server/Redis/MySQL fault checkpoint、diagnostic gauge 收敛与 secret 扫描 | public wire + owned fault |
| governance | Q0 主规格与全仓 OpenSpec strict、归档任务完成度、diff 格式和 generated projection 边界 | repository |
| cleanup | 精确 PID 与 storage run ownership 清理 | process/storage owner |

黑盒场景覆盖 register/login/refresh/logout、WSS session invalidation、重复/并发 own-world bootstrap、一次性 ticket/admission、TCP snapshot、invite/accept/join、leave/kick/close、多 Visitor、Owner/Visitor 断线恢复、grace expiry、权限和 correlation 负向矩阵。恢复场景真实执行 server process replacement、Redis flush 和 MySQL restart，并验证旧连接、session、VisitSession、assignment 与 credential fail closed。

26 个 mandatory scenario 按 `execution` 分为 2 个 contract、16 个独立 black-box 与 8 个 layered evidence。无法由公开网络稳定制造的 commit-unknown 精细线性化点、response-loss 和确定性慢 reader/writer 由 manifest 明确标为分层证据；`shared/contracts/fixtures/qualification/evidence-manifest.json` 是 layered scenario 到 package/test identity 的唯一映射源，资格入口按目录真实执行，Go contract test 同时拒绝目录、manifest 与测试声明漂移。分层证明不得伪称为 wire fault。

公开 diagnostic 的前后收敛断言只覆盖 WSS/TCP active connection 与 TCP in-flight gauges；queue/runtime/deadline 和低基数 label vocabulary 由同轮分层 owner tests 验证。Server logs 扫描本轮已知 secret，client/report 额外拒绝本机路径、IP/port、identity 字段和 backend 文本；文档不把这一边界扩写为任意日志内容的通用 DLP 保证。

## 报告与判定

每次运行在 ignored run directory 写入 schema-versioned `report.json`。报告只包含 run ID、版本、contract digest、稳定 gate/scenario ID、execution、outcome、毫秒耗时、首个稳定失败类别和 cleanup 结果，不包含 credential、payload、IP/port、PID、本机路径、Player/Session/World/Visit identity 或 backend 异常文本。机器报告用于本轮诊断与验收，不提交 Git；仓库长期提交的是冻结摘要、资格文档和可重复生成报告的入口。

判定规则：

- `qualified=true` 只允许由完整 `verify` 在全部 mandatory gate/scenario 为 `pass` 且 cleanup 为 `pass` 时写入。
- mandatory `skipped` 与 `fail` 等价地阻止资格成功。
- 主门禁失败与 cleanup failure 同时保留，cleanup 失败不能覆盖最初失败。
- 两次 clean `verify` 的 scenario 集合、稳定 outcome 与 contract digest 必须一致；run ID、端口和耗时允许不同。
- 单独通过 storage、black-box、race 或人工联调都不构成 Q0。

## 长期演进

公开 HTTP operation、实时 message/channel、credential、错误或恢复语义发生变化时，对应 OpenSpec 必须在同一 capability group 更新 qualification manifest、runner、fixtures 与冻结摘要，并继续运行全部仍受支持的旧 mandatory 回归。纯内部 package、算法或 storage adapter 重构只要公开行为不变，不得迫使 testclient 复制内部结构。

未来 Activity、battle 等能力使用独立 qualification group，由发布门聚合；不得把全部游戏流程堆进单个巨型 scenario。旧版本场景只有在版本正式退役、兼容窗口结束并保留编号/历史后才能退出 active matrix。

## C0 结论

本 change 已连续两次执行完整 `verify`：两轮均为 11/11 gates、26/26 mandatory scenarios、`qualified=true`，execution 分布均为 contract=2、black_box=16、layered=8，contract digest 均为 `9d659c133e167654661c699629af53a83ce4f5583809c7313bbdabbb1d6107fb` 且 cleanup=`pass`；两轮墙钟耗时分别为 306527ms 与 303530ms。`go vet`、`go mod verify`、change strict 与全仓 strict 同时通过。因此当前实现证据已满足 Q0 服务端 v1 资格边界。

`qualify-server-v1` 已完成主 specs 同步并于 2026-07-16 归档，C0 的流程进入条件已满足，可以提出 `establish-client-runtime`。后续客户端 change 只能依赖本文冻结的 v1 边界，不能把 Q0 自动扩解释为 Activity、battle 或其他非目标能力已完成。
