## 1. 依赖、配置与契约投影

- [x] 1.1 在 `versions.yaml`、`server/go.mod` 与 checksum 中锁定 `github.com/coder/websocket`，记录版本owner并验证依赖、license、`go mod tidy` 与 `go mod verify` 无漂移
- [x] 1.2 为WSS增加严格配置：固定path/subprotocol、Host/Origin allowlist、pre-auth rate、全局/remote/session/player连接上限、queue item/byte预算、write/ping/pong/idle/close deadline，并补齐YAML、环境覆盖白名单和边界测试
- [x] 1.3 扩展contract runtime projection，使codec能按message ID取得WSS route与精确generated payload type，增加500-504、2003、2100-2102完整profile及unknown/wrong-channel/type漂移测试

## 2. WSS codec 与连接资源模型

- [x] 2.1 新建 `internal/transport/wscontrol`，实现只接受9个已登记PUSH的typed payload校验、deterministic Protobuf编码、`ReliableEnvelope`构造和route/global完整frame预算检查
- [x] 2.2 为codec增加全部9类push、sequence/timestamp、wrong kind/channel/direction/type、oversize、确定性重编码与fuzz测试，复用现有realtime golden/negative基线而不创建同义DTO
- [x] 2.3 实现不可变encoded message与同时限制item/bytes的非阻塞发送队列，覆盖入队顺序、预算边界、关闭竞态、重复关闭和内存释放测试
- [x] 2.4 实现每连接单reader/单serialized writer、binary-only、禁压缩、write deadline、ping/pong/idle检测和稳定close映射，覆盖peer close、意外application frame、timeout与slow consumer

## 3. Connection registry 与受信投递

- [x] 3.1 实现CSPRNG ConnectionID、connection/session/player索引和线性化注册/移除，确保entry只保存AuthContext摘要、连接句柄、队列与生命周期状态
- [x] 3.2 实现全局、remote identity、SessionID和PlayerID连接预算以及有界idle rate state，在upgrade前执行reserve/commit/release并覆盖连接风暴与失败回滚
- [x] 3.3 实现按ConnectionID/SessionID/PlayerID的窄typed publisher与投递结果聚合，确保publisher不直接写socket、不引入全局event bus且wrong target/message fail closed
- [x] 3.4 增加registry并发注册、并发投递、断开清理、session多连接、queue overflow和race detector测试，验证不存在僵尸反向索引或goroutine泄漏

## 4. 握手认证与会话失效

- [x] 4.1 实现唯一 `GET /v1/control` handler，严格校验readiness、TLS/local模式、Host、可选Origin、Upgrade、`ihomeland.control.v1`、Ticket Authorization语法与query/cookie拒绝
- [x] 4.2 解析32位小写hex nonce并调用现有Session service以 `ChannelWSS` 和受信advertised endpoint原子消费ticket，稳定映射missing/expired/replay/epoch/dependency结果且不记录凭据
- [x] 4.3 覆盖并发ticket消费、错误channel/endpoint/scope、旧epoch、consume后upgrade失败、错误subprotocol/Origin/Host和diagnostic path隔离测试
- [x] 4.4 让WSS registry实现production `ConnectionInvalidator`，按安全reason best-effort发送session-invalidated/forced-logout push后无条件关闭全部旧epoch连接，并覆盖重试幂等、满队列和新epoch连接保护

## 5. Composition Root 与生命周期接线

- [x] 5.1 在public runtime中按codec/registry → Session service → WSS handler →顶层public mux顺序构图，使mux仅分流 `/v1/control`并把其余请求委托既有10-operation HTTP router；用真实registry替换 `NoActiveRealtimeConnections`，且不创建第二listener或第二SessionStore
- [x] 5.2 为WSS registry分配稳定 `websocket_control` TaskOwner，由registry拥有并等待每连接reader/writer；调整public component使draining后按拒绝upgrade → 停止publisher/并行关闭并等待WSS → HTTP Shutdown → Redis/MySQL的顺序释放
- [x] 5.3 覆盖构图失败、listener bind/serve失败、部分WSS启动失败、active HTTP+WSS shutdown、deadline强制关闭、任务异常和重复Stop的process/lifecycle测试
- [x] 5.4 更新本地示例配置和advertised WSS endpoint，使直接开发连接与显式ingress/port mapping语义清晰，并验证production TLS 1.3及loopback明文例外

## 6. 可观测与安全边界

- [x] 6.1 扩展metrics observer，覆盖handshake、active/rejected、push/bytes、queue、heartbeat、slow consumer、close与invalidation，限制label为稳定低基数字段
- [x] 6.2 增加结构化日志与panic/dependency failure恢复测试，断言Authorization、ticket/digest、IP、Origin、payload、principal、invite/admission和backend文本不会进入响应、close、日志或metrics
- [x] 6.3 扩展architecture/structure tests，禁止WSS package依赖业务storage/client、保存领域事实、注册TLS/TCP route、接收client mutation或引入memory fallback/global registry

## 7. 真实集成与并发验收

- [x] 7.1 扩展统一storage integration harness，以临时TLS、真实public router、HTTPS ticket签发和production Redis SessionStore验证一次性consume、重放、logout invalidation与活动连接shutdown；另以真实WebSocket wire test验证全部9类push
- [x] 7.2 分层覆盖Redis unavailable/flush/corrupt、ticket replay/expiry/epoch/endpoint、Host/Origin/subprotocol、oversize与consume后断线：存储/Session contract验证权威绑定，handler/codec验证upgrade前与frame边界，真实harness验证组合路径
- [x] 7.3 增加slow consumer、heartbeat/idle timeout、并发连接风暴、session多连接失效、并行graceful shutdown与资源清理场景，并在受影响包和storage integration上执行race detector
- [x] 7.4 运行统一protocol verify，证明message/route registries、generated Go、realtime golden/negative fixtures和既有HTTP contract未发生未声明漂移

## 8. 文档与完成门

- [x] 8.1 更新 `docs/architecture.md`、`docs/network-transport-architecture.md`、`docs/network-port-allocation.md`、`docs/protocol-compatibility.md`、`docs/client-integration.md`、`docs/file-structure.md`、`docs/roadmap.md` 与 `server/README.md`，记录WSS握手、owner、配置、关闭和验收边界
- [x] 8.2 复核所有新增Go/PowerShell/Protobuf注释与配置说明符合项目中文注释规范，删除重复、过期、逐行复述或泄密内容
- [x] 8.3 执行formatter、全量Go tests、受影响包与storage race、`go vet ./...`、`go mod verify`、protocol verify、真实storage/WSS integration、`git diff --check` 与 `openspec validate --all --strict`
- [x] 8.4 确认本change未实现TLS/TCP、world admission consume、world/visit mutation、safe-return、Unity、跨节点pub/sub或虚假业务producer，并将全部任务完成证据同步到归档前评审
