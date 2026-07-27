# 实现阻塞审计

本文只记录 apply 阶段从当前 source 直接验证出的前置缺口。它不放宽 B0.6
requirement，不把 unsupported 标记成 pass，也不授权 qualification harness 导入
production 内部实现绕过公开 wire。

## BQ-BLOCKER-001：公开 BattleTicket 无法派生 ClientAuth proof

`server/internal/battleticket/derivation.go` 使用完整 binding fingerprint 作为
HKDF-SHA-256 salt 派生 `proofKey`。该 fingerprint 包含 private control 才持有的
session、assignment、node、instance、mapping、model/profile/config 等完整事实。
但 `server/internal/transport/httpapi/codec.go` 的 `BattleTicketResponse` 只返回
ticket ID、ticket secret、endpoint、wire suite、role、target kind/revision 和
expiry，没有返回可供客户端重建或验证的 binding material。

因此独立客户端仅凭 HTTPS response 无法生成 C++ installed ticket registry 要求的
exact proof key。Go parent 直接经 stdin 注入 `proofKey` 或完整 binding 会违反本
change 的黑盒边界和 task 3.2；当前 `session-start-v1` 已收紧为只交付 ticket ID、
ticket secret、numeric gateway endpoint 与 expiry。

解除条件：先用独立 OpenSpec change 冻结客户端可计算且服务端可验证的 proof
derivation/binding confirmation 契约，并完成 Go/C++/C# canonical parity、迁移与
B0.5 重新资格。该变更不能在 B0.6 内隐式改写。

受阻任务：3.3 的真实 HTTPS credential 路径、3.8 live parity、5.3、6.1–6.6、
7.2、9.3。

## BQ-BLOCKER-002：production UDP listener 尚未组合 handshake/session/send

`simulation/src/control/control_server.cpp` 启动 `BattleUdpListener` 时安装的是
不产生响应的空 callback。`simulation/include/ihomeland/sim/transport/udp_listener.hpp`
只暴露 receive lifecycle，没有向同一 socket/remote 回送 Retry、ServerAccept 或
secure datagram 的 API。与此同时，
`simulation/include/ihomeland/sim/transport/authenticated_handshake.hpp::Result`
只返回低敏 routing projection，没有把成功 handshake 的 session seed 安全转移给
`BattleSecureChannel` owner。

因此当前真实 child 不能在唯一 production listener 上完成 ClientHello/Retry/
ClientAuth/ServerAccept，也不能建立后续 raw/KCP session。loopback unit harness
不能替代 mandatory real process/socket evidence。

解除条件：独立 OpenSpec change 补齐同一 listener 的 bounded send owner、
handshake→session secret ownership transfer、authenticated multiplexer composition、
rollback/cleanup 与真实 socket 回归，然后重新生成 B0.5 qualification report。

受阻任务：4.4、4.6 的真实 rebind 部分、5.3、5.7、6.1–6.6、7.2、9.3。

## BQ-BLOCKER-003：snapshot wire 缺少 LastProcessedInputTick

`shared/proto/ihomeland/battle/v1/battle.proto` 的 `BattleFullSnapshot` 与
`BattleDeltaSnapshot` 只有 ServerTick、SnapshotSequence、BaselineID、partition
和 entity state。C++ `InputTimeline` 内部虽然维护
`last_processed_input_tick`，但冻结 snapshot wire 没有投影该确认 frontier。

客户端不能从 ServerTick、snapshot sequence 或接收时刻推断该值，否则会把网络
到达顺序伪装成 simulation 已处理事实。

解除条件：独立 OpenSpec change 决定是否把确认 frontier 加入 full/delta snapshot
或建立其他唯一公开确认 route，并同步 registry、size budget、canonical golden、
Go/C++/C# parity 与 B0.2/B0.5 回归。

受阻任务：3.5、3.8、5.5 的 LastProcessedInputTick 断言部分、6.1–6.2、6.6–6.7、
9.3。
