## 1. 契约投影与严格配置

- [x] 1.1 定义并集中验证authentication preface的magic/version、4-byte big-endian framing、ticket/admission长度和安全ASCII边界，新增deterministic golden/negative fixtures且不分配业务message ID
- [x] 1.2 扩展contract runtime projection，按2000-2002与2103-2122解析精确TLS_TCP route、generated payload、kind、direction、correlation、rate和deadline，并增加unknown/wrong-channel/type漂移测试
- [x] 1.3 为gameplay listener增加bind/advertised endpoint、production TLS/local loopback、handshake/frame/read batch/queue/connection/rate/keepalive/idle/close/shutdown预算，补齐YAML、环境变量白名单和边界测试
- [x] 1.4 验证gameplay端口不与public/diagnostic冲突，advertised endpoint与Session/WorldAdmission签发值精确一致，配置错误在任何listener/storage副作用前失败

## 2. Framing、preface 与 typed codec

- [x] 2.1 新建 `internal/transport/tcpgameplay`，复用无listener的共享4-byte big-endian framer，覆盖零长、1 MiB上限、半包、粘包、多frame batch、截断和不按声明长度无界分配
- [x] 2.2 实现authentication preface encode/decode与有界reader，严格校验magic/version、credential lengths/grammar、handshake deadline和总buffer预算，且错误格式化永不暴露credential
- [x] 2.3 实现registry驱动的C2S envelope decoder，校验version/sequence/timestamp、REQUEST/COMMAND correlation、route/global size和精确generated payload type
- [x] 2.4 实现deterministic S2C response/error/push encoder与length-prefix writer，确保message/kind/type/correlation和完整frame预算一致
- [x] 2.5 增加framer/preface/codec table、golden、negative与fuzz测试，覆盖partial/batched frame、错误channel/direction/kind/type、unknown enum/ID、oversize和确定性重编码

## 3. Connection registry 与资源模型

- [x] 3.1 实现CSPRNG ConnectionID、global/remote/session/player/target reservation和有界remote rate state，确保认证各阶段commit/release线性化且失败无资源泄漏
- [x] 3.2 实现connection/session/player/world/visit索引与pending/active/returning/closing状态迁移，entry只保存AuthContext/Qualification摘要、连接句柄、queue和route rate state
- [x] 3.3 实现item/encoded-byte双重有界发送队列与关闭内存释放，覆盖enqueue/close并发、重复关闭、slow consumer和未写出消息结果
- [x] 3.4 实现单reader同步dispatch、serialized writer、有限read batch、read/write/idle deadline、OS TCP keepalive和稳定close reason，不创建未登记application heartbeat
- [x] 3.5 增加registry/queue/connection并发、storm、slow consumer、peer close、idle、index cleanup与race测试，证明goroutine/timer/buffer均有硬上限

## 4. Ticket 与 WorldAdmission 握手

- [x] 4.1 在preface handler中以受信TLS_TCP advertised endpoint原子消费ConnectionTicket，只接受精确GAMEPLAY scope并构造不可伪造AuthContext
- [x] 4.2 以ConnectionID派生稳定consume identity调用production WorldAdmission verifier，绑定current full AssignmentStamp并实现ticket已提交后失败不补偿语义
- [x] 4.3 实现OWN_WORLD直接active与JOIN/RECONNECT pending状态，限制pending deadline内只允许匹配首个command且禁止snapshot/其他mutation
- [x] 4.4 对Join/Reconnect payload使用同一consume identity恢复Qualification，逐字段比较preface binding并在application成功后恰好一次转为active
- [x] 4.5 覆盖ticket/admission missing、expiry、replay、epoch、endpoint/channel、stale assignment、dependency unavailable、response-loss精确重试、credential切换和并发消费测试

## 5. Dispatcher 与 application ports

- [x] 5.1 定义transport消费侧窄ports与集中handler catalog，将AuthContext、Qualification、ConnectionBindingID和typed payload映射到现有PersonalWorld/VisitSession application API，不依赖Gin/socket或storage adapter
- [x] 5.2 接入WorldSnapshot、VisitSnapshot的REQUEST处理，验证active target与request ID并生成匹配correlation的2001/2120 response或稳定ErrorDetail
- [x] 5.3 接入VisitOpen/CreateInvite/RevokeInvite/Join/Leave/Kick/Reconnect/Close COMMAND处理，保持command ID、expected revision、actor/role、target和store幂等边界
- [x] 5.4 实现per-route rate/deadline和持续滥用关闭，command按唯一reader顺序串行，重复/冲突不由transport缓存或伪造application结果
- [x] 5.5 增加每个C2S route的success/error/authorization/idempotency/rate/timeout测试，覆盖payload身份覆盖、pending状态、系统lifecycle伪造和application panic/dependency failure

## 6. Response、Push 与跨通道失效

- [x] 6.1 实现按ConnectionID/PlayerID/PersonalWorldID/VisitSessionID的typed publisher，只允许2002、2121、2122且publisher不直接写socket或扩大payload target
- [x] 6.2 实现response/push共享serialized writer与单调S2C sequence，覆盖并发response/push、correlation、queue overflow、wrong target/channel/type和oversize
- [x] 6.3 为VISIT_SAFE_RETURN_PUSH校验目标Visitor与current gameplay binding，投递后阻止旧target新mutation并进入有界returning/closing，不与WSS close notice互相替代
- [x] 6.4 实现WSS/TCP组合ConnectionInvalidator，在一个共享deadline内并行尝试全部旧epoch连接、聚合稳定结果且任一失败不跳过另一通道
- [x] 6.5 增加publisher、safe-return、session多连接/多通道失效、queue failure、新epoch保护、幂等重试与race测试

## 7. Composition Root 与生命周期接线

- [x] 7.1 按config/catalog/registry/dispatcher/listener顺序在任何bind前构造TCP graph，复用唯一SessionStore、WorldAdmission verifier、clock/ID、TLS loader和共享MySQL/Redis runtime
- [x] 7.2 为gameplay accept loop与registry分配稳定TaskOwner，区分单连接错误与系统性listener/task失败，并使后者撤销readiness和触发非零受控关闭
- [x] 7.3 接入独立TLS 1.3 listener与local/test loopback明文例外，验证bind/remote地址、TLS handshake deadline、TCP keepalive和advertised endpoint
- [x] 7.4 调整draining顺序：停止TCP accept和HTTP/WSS握手、停止publishers、共享deadline并行关闭/等待TCP与WSS、关闭HTTP in-flight、最后释放Redis/MySQL/diagnostic
- [x] 7.5 覆盖graph构造、TLS/listener bind/serve、部分启动回滚、active/pending连接shutdown、deadline强制关闭、任务异常和重复Stop的process/lifecycle测试

## 8. 可观测、安全与真实集成

- [x] 8.1 扩展metrics observer覆盖TLS/handshake、active/rejected、frame/message bytes、dispatch、in-flight、rate、queue、keepalive/idle、slow consumer、push、close、invalidation和shutdown，固定低基数label词表
- [x] 8.2 增加结构化日志、错误与panic recovery测试，断言ticket/admission/digest、raw IP、payload、principal、full AssignmentStamp、invite和backend文本不会进入响应、日志或metrics
- [x] 8.3 扩展统一storage integration harness，以临时TLS、真实HTTPS签发、production Session/WorldAdmission Redis adapter和真实TCP wire覆盖OWN_WORLD、JOIN、RECONNECT、ticket重放、logout失效与shutdown；stale、wrong endpoint、framing和backpressure由对应分层测试覆盖
- [x] 8.4 增加connection storm、partial/batched frame、backpressure、session invalidation、process shutdown与资源清理分层测试，并在TCP、session、worldadmission、app和storage受影响包执行race detector
- [x] 8.5 扩展architecture/structure tests，禁止tcpgameplay依赖Gin、业务storage实现、全局event bus、memory fallback、第二listener owner、WSS route或未登记heartbeat/message

## 9. 文档与完成门

- [x] 9.1 更新architecture、network transport、port allocation、protocol compatibility、client integration、file structure、roadmap与server README，记录preface、配置、owner、关闭和验收边界
- [x] 9.2 复核全部新增Go/PowerShell/contract注释和配置说明符合中文注释规范，重点解释单位、资源预算、credential生命周期、并发线性化和失败不补偿
- [x] 9.3 执行formatter、全量Go tests、受影响包/storage race、go vet、go mod verify、protocol verify、真实storage/TCP integration、framer/preface fuzz、git diff --check与OpenSpec all strict
- [x] 9.4 确认本change未新增message ID、generic world action、业务事实缓存、未登记heartbeat、完整producer/orchestration、Go资格客户端、Unity、UDP/KCP或第二套service/store graph，并记录归档前验收证据

## 实现验收记录

- `go test -count=1 ./...`、`go vet ./...`、`go mod verify` 通过。
- `go test -race -count=1` 覆盖 `tcpgameplay`、`wscontrol`、`session`、`worldadmission`、`app` 与全部 storage package，结果通过。
- `FuzzDecodePreface` 与 `FuzzCodecDecode` 各运行 10 秒通过；framing 的半包、粘包、batch 与 oversize 由 table/wire tests 覆盖。
- `tools/proto/proto.ps1 verify`、`tools/storage/storage.ps1 -Action verify -TimeoutSeconds 600`、`git diff --check` 与 `openspec validate --all --strict` 通过；真实 harness 覆盖 WSS shutdown close frame，以及 TCP `OWN_WORLD`、`JOIN`、`RECONNECT` 和 logout 失效。
- 协议 registry 未新增 message ID；实现仅消费既有 2000-2002、2103-2122 TLS/TCP routes。未引入 generic world action、application heartbeat、业务事实缓存、第二套 service/store graph、Go 资格客户端、Unity 或 UDP/KCP；完整 producer 与 semantic cleanup/orchestration 仍留在后续竖切 change。
