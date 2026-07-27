# Battle Protocol Client Contract

本目录冻结 B0.6 独立 C++ 协议客户端与 Go supervisor 之间的 inherited
stdin/stdout 契约。它不是网络 wire corpus，也不引入 listener、端口或 gameplay
协议。

- `contract.json`：frame、kind、sequence/deadline、credential ownership 与输出低敏边界。
- `schema.json`：source contract 与 manifest 的 closed JSON Schema。
- `manifest.json`：文件集合与 byte-exact SHA-256。

stdin/stdout 使用 `u32-be length || body`。body 固定以 28-byte `IHBQ` header
开始，最大 64 KiB。credential 只允许出现在单次 `session-start-request` payload，
由 child 读取后立即清零；stdout、stderr、退出码与低敏 receipt 均不得回显
credential、ticket proof、完整 binding、wire payload、PlayerID、IP 或本机路径。

`session-start-v1` 是固定 76-byte field table：`clientSlot(1)`、
`addressFamily(1)`、`port(2)`、16-byte numeric address、`ticketId(16)`、
`ticketSecret(32)` 与 `ticketExpiresAtUnixMs(8)`。`proofKey` 只能由客户端按
`HKDF-SHA-256(ikm=ticketSecretRaw32, salt=ticketIdRaw16,
info="ihomeland/battle-ticket/proof-key/v2")` 在进程内生成，不能由 Go parent 或
private control 旁路注入；完整 binding 只允许在认证解密 `ServerAccept` 后取得，
不得进入 child stdin。
所有整数使用 network big-endian；IPv4 使用 IPv4-mapped 16-byte 表达。

`session-event-v1` 有两个互斥的闭合变体：成功为
`clientSlot(1) || established(1)`；失败为
`clientSlot(1) || failed(2) || failureCode(1)`。`failureCode=1..10`
只登记 invalid request、deadline、socket、handshake setup、Retry exchange/validation、
ServerAccept exchange/validation、transport setup 与 unknown 阶段，不得携带 endpoint、
socket 错误文本、credential 或 payload。失败请求的 slot 不可信时固定返回零；成功
slot 仍只能是 1..8。Supervisor 收到失败变体后必须终止 child，
未知 state、长度或失败码一律按协议漂移 fail closed。

`network-transition-v1` 支持 `rebind`、`rekey`、`close` 与
`old-epoch-probe`。对应的 8-byte event 使用 `committed` 与 `failureCode`
形成互斥终态：成功时 `committed=1`、`failureCode=0`；失败时
`committed=0`、`failureCode=1..35` 且 `generation=0`。失败码按 operation
和 request/receive/authentication/application/interleave/validate 阶段封闭登记，
只允许输出低敏分类，不得传播 socket、endpoint 或 payload 文本。

`poll-receipt-v1` 固定为 144 bytes。成功与失败 receipt 都原子返回累计 application
状态；失败额外使用 `failureCode` 与 `kcpCloseReason`。末尾九个 `u64` 只记录低敏
KCP 分层状态：secure KCP datagram、primitive input、ACK/PUSH command、output、
ACK reconciliation，以及当前 queued/inflight/waiting 数。它们用于区分 secure
delivery、peer ACK 与 client reconciliation，禁止包含 packet sequence、conv、
endpoint 或 payload。`lastProcessedInputTick` 与 `baselineGapCount` 仍只能来自完整
通过 partition、generation、sequence 与 baseline 校验的客户端状态，不得从
`ServerTick`、日志或 supervisor 本地计时推导。

`workload-command-v1` 的 `deliveryMutation=0..12` 是完整闭合枚举，覆盖正常发送、
exact replay、tag/AAD/ciphertext 修改、oversize/malformed、future/too-old
sequence、wrong direction/lane、rebind hijack 与 KCP expiry。除
`unmodified` 和仅适用于 resync 的 `kcp-expired` 外，负例不得用于普通 workload
推进，也不得改变权威 gameplay state。
