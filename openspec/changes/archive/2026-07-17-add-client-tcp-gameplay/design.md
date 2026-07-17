## Context

服务端 v1 已冻结独立 TLS/TCP gameplay listener、`IHTP` v1 preface、`ReliableEnvelope`、route registry 和 world admission 语义。客户端已有生成协议、HTTPS session owner 与 WSS control，但没有 gameplay socket owner；现有 HTTP capability 又刻意未暴露 `issueWorldAdmission`。本 change 横跨 HTTP credential 交付、TCP I/O、生成协议 codec 与 App Scope 生命周期，且任何重试、无界队列或凭据泄漏都会破坏服务端安全模型。

## Goals / Non-Goals

**Goals:**

- 精确消费冻结的 world admission、preface、frame、envelope 和 route contract。
- 以一个 reader 与一个 serialized writer 拥有 socket I/O，并为 pending、queue、frame 和关闭设置硬上限。
- 让后续 PersonalWorld/VisitSession Services 只能通过强类型 operation catalog 发请求并订阅登记 push。
- 让 credential、session generation 与 channel 生命周期在并发、取消、断线和 App shutdown 下 fail closed。
- 通过不依赖真实 listener 或 Unity Scene 的纯 C# tests 验证 codec 与 owner 状态机。

**Non-Goals:**

- 不实现 `acceptVisitInvite`、PersonalWorld/VisitSession 状态机、重连策略、业务自动重试或场景投影。
- 不自动建立连接，不在 App 初始化时访问网络。
- 不提供任意 message ID、任意 Protobuf payload、底层 socket 或 admission/ticket 字符串给 UI/Scene。
- 不修改服务端协议，不引入 Addressables、资源系统、UI、Prefab 或 Scene 资产。

## Decisions

### 1. HTTP 只增加一个强类型 admission operation

在现有 `IClientHttpApi`、operation catalog 和 codec 上增加 `issueWorldAdmission`，由 `SessionCoordinator` 以当前 access token、session generation、到期时间和单次 take 包装结果。调用方必须提供规范 idempotency key 与封闭 target 类型。选择扩展现有 session owner，而不是创建第二个 credential manager，可确保 logout、refresh、forced invalidation 和 shutdown 同时使未交付 admission 失效。

### 2. Transport adapter 与 gameplay channel 分离

`Infrastructure/Tcp` 负责 connect、TLS/plaintext policy、exact read/write 与 stream 关闭；`Application/Gameplay` 负责连接状态、凭据交付、sequence、pending correlation、push 分派和逆序停止。这样测试可以使用内存 transport 验证协议状态机，同时不把 application 规则下沉到 socket adapter。

生产 endpoint 只允许 TLS 1.3；显式 local/test 的 plaintext 仍必须是 loopback。证书验证使用平台默认信任链与 endpoint host，不提供跳过验证开关。

### 3. 以冻结 descriptor catalog 限制 route

每个可发送 request/command descriptor 固定 request ID、response ID、kind、最大 payload 与 Protobuf parser；每个可接收 push descriptor固定 ID、最大 payload 与 parser。Channel 不公开接受裸 `uint messageId` 的 API。Error envelope 只映射登记错误码和低敏 correlation，不回传原始 payload 或 exception 文本。

### 4. 单 reader、serialized writer 与有界 pending

每个 active connection 只启动一个 receive pump。所有 outbound envelope 先在锁内分配严格递增 sequence、登记唯一 16-byte request/command ID，再进入同时受 item 数与 encoded bytes 约束的 FIFO writer。Writer 是唯一调用 stream write 的任务。Pending 对调用方在收到匹配 response/error、调用取消、deadline、断线或 shutdown 时恰好完成一次。调用取消或 deadline 只结束本地等待；已经发送的 operation 仍保留有界 correlation，直到迟到 response、断线或 shutdown 清理，避免把合法迟到结果误判为未知消息。达到上限时在产生 socket 副作用前拒绝。

### 5. 连接失败不复用 credential

Channel 先从 Session owner 原子取得 admission，再取得匹配 `TLS_TCP/GAMEPLAY` ticket，并验证 endpoint 一致后才连接。任何 connect、TLS、preface 或后续认证结果未知都视为两份 credential 已消耗；本层不猜测服务端提交阶段，也不自动重新签发。`JOIN`/`RECONNECT` 首个 command 重用 admission 的要求由 descriptor 与后续 Service 显式满足，本 change 不自动发送业务 command。

### 6. 稳定 close reason 与安全 push handoff

关闭原因只使用低基数枚举：caller、remote、protocol、timeout、backpressure、session invalidated、application return、shutdown 与 transport failure。2002、2121、2122 push 通过强类型订阅交付；2122 在回调前先使当前 target 停止接受新 mutation。回调异常只结束该订阅投递或连接，不能逃逸 reader pump。

## Risks / Trade-offs

- [Socket/TLS 在 Unity 支持矩阵上存在平台差异] → 本 change 仅承诺当前 Windows/Mono baseline，并以窄 transport interface 隔离；Windows Development build 作为人工验收门。
- [后续 Service 可能需要更丰富的 operation API] → 只通过新增冻结 descriptor 扩展，避免现在创建万能 request bus。
- [一个连接 owner 同时处理协议和并发，代码体积较大] → 拆分 preface/framer、catalog/codec、transport 与 channel；每个类型保持单一所有权并用 contract tests 固定不变量。
- [取消时服务端可能已经提交 command] → 客户端只取消本地等待，不自动重发；由 command ID/idempotency 和后续业务 Service 决定恢复。

## Migration Plan

1. 扩展 HTTP contract、projection 与 admission lease tests。
2. 增加纯 C# TCP codec、catalog 与 transport boundary。
3. 增加 gameplay channel 和 App Scope 生命周期接线，默认不连接。
4. 运行 OpenSpec strict、静态编译与 EditMode tests；再由 Unity 执行 PlayMode 和 Windows Development build。

回滚时可删除 gameplay composition 与新增类型，并恢复 HTTP capability 的八 operation 边界；服务端和已有 WSS 行为不受影响。

## Open Questions

无。业务重连、进入世界状态机与 push 投影留给后续独立 capability。
