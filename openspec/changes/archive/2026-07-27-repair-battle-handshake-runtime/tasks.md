## 1. 冻结可公开计算的 proof derivation

- [x] 1.1 更新 secure battle transport delta contract、wire crypto vector、canonical golden、secret policy 与 manifest，冻结 `proof-key/v2`、raw ticket ID salt 和旧版本拒绝。
- [x] 1.2 修改 Go BattleTicket deriver，使 proof key 只从 raw ticket secret、raw ticket ID 和 versioned domain 派生，同时保持 ticket secret 对完整 binding 的确定性绑定。
- [x] 1.3 修改独立 C++ protocol client 从 stdin ticket ID/secret 本地派生 proof，并仅在认证解密 ServerAccept 后锁定 binding fingerprint。
- [x] 1.4 更新 C# fixture parity helper，不新增 Unity runtime 依赖。
- [x] 1.5 增加 Go/C++/C# positive、wrong ticket ID/secret/domain、response-loss 与 secret cleanup tests。

## 2. 建立同一 UDP socket 的收发 owner

- [x] 2.1 为 `BattleUdpListener` 增加 bounded serialized send API、owned datagram、remote validation、queue hard limit 与低敏 send counters。
- [x] 2.2 实现 receive/send/stop 的单 executor 生命周期，保证 Stop 后无迟到 callback、重复关闭安全且不暴露 socket。
- [x] 2.3 增加 bind conflict、oversize、invalid remote、send pressure、concurrent stop 与同 socket round-trip tests。

## 3. 安全转移 handshake session bootstrap

- [x] 3.1 为首次 accepted handshake 定义 move-only、析构清零的 `SessionBootstrap`，包含 seed、binding、session/generation/slot/role/epoch/endpoint generation。
- [x] 3.2 修改 `BattleAuthenticatedHandshake`，首次成功只转移一次 bootstrap，exact ClientAuth replay 只返回 byte-identical accept。
- [x] 3.3 增加 consume 后构造失败、move/析构清零、exact replay、field drift、expiry 与并发 consume tests。

## 4. 组合 node-global authenticated runtime

- [x] 4.1 实现 `BattleTransportRuntime`，以唯一 listener、cookie/handshake owner 和最大 actor 容量管理 pre-auth 与 active session。
- [x] 4.2 将 secure raw/KCP/control、resource governor、rebind/rekey 与 `BattleSessionContext` 组合为每 session 单 owner，并保持 simulation worker 唯一写者。
- [x] 4.3 实现 ClientHello/ClientAuth/secure datagram closed dispatch 与同 listener output，不允许第二入口、跨 lane fallback 或 payload identity override。
- [x] 4.4 接入 ticket/session/assignment/instance/node revoke、drain、listener failure 与 startup rollback，所有 secret/queue 在 deadline 内清理。
- [x] 4.5 在 simulation-control composition root 替换空 UDP callback，并增加 architecture/import/link gate 防止 qualification bypass。

## 5. 真实进程与安全回归

- [x] 5.1 增加真实 `ihomeland-sim-server` child + private control + 单 loopback UDP listener integration harness，完成公开 BattleTicket ClientHello/Retry/ClientAuth/ServerAccept。
- [x] 5.2 在真实 socket 上验证 raw input、snapshot、KCP、replay、tamper、expiry、MTU、backpressure、rebind、rekey 与 close。
- [x] 5.3 验证 own/visit、response-loss、8/9 actor、session/assignment invalidation、child crash、Go restart 和 shutdown cleanup。
- [x] 5.4 执行 Go unit/race/fuzz、C++ unit/contract/integration/Release/ASan、C# parity、server/client v1 与 B0.3/B0.4 regression。
- [x] 5.5 重新生成并验证 `secure-transport-qualified-windows-x64` report，更新所有 downstream digest binding，禁止复用旧 report。
- [x] 5.6 更新架构、网络、协议、端口、工程结构、runbook 与 roadmap，并执行 `openspec validate repair-battle-handshake-runtime --strict`。
