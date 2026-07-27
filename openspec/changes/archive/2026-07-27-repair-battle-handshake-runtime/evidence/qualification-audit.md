# B0.5 握手与生产运行时修复资格审计

## 结论与边界

本轮重新取得 `secure-transport-qualified-windows-x64`，替代
`establish-secure-battle-transport` 的旧实现证据。结论只覆盖 Windows x64、本地受控
loopback、B0.5 安全传输实现与现有 server/client 自动回归；不声明公网、NAT 包络、
B0.6 fault/capacity/soak 或 Unity battle runtime 已取得发布资格。

## 被修复的生产链路

1. BattleTicket proof key 只从 raw ticket secret、raw ticket ID 和
   `ihomeland/battle-ticket/proof-key/v2` 派生；旧 domain、错误 ID/secret 与响应丢失
   重放均 fail closed。
2. `SimulationNode` 是唯一 `BattleUdpListener`、handshake、active session 与
   simulation projection owner。raw、KCP、handshake 和 transport-control 均由同一
   production socket 收发，不存在资格专用 listener 或 adapter bypass。
3. 首次认证成功通过 move-only、析构清零的 bootstrap 把 secret 与 binding 所有权转交
   session；exact `ClientAuth` replay 只重放 byte-identical accept，不能二次消费。
4. runtime worker 即使没有新 ingress 也按 10 ms cadence 推进 KCP ACK、首包与重传；
   snapshot 只读取 simulation owner 提供的 committed actor projection。
5. Go parent 启动真实 `ihomeland-sim-server` child；独立
   `ihomeland-battle-protocol-client` 只经固定 `IHBQ` stdin/stdout contract 接收一次性
   ticket，并通过真实 UDP socket 执行 raw/KCP/rebind/rekey/close。

## 真实故障与容量证据

真实三进程测试覆盖 Owner、Visitor、8 个 active actor 与第 9 actor 拒绝，并在生产
socket 上验证 input、probe、full/delta snapshot、KCP resync、exact replay、tag tamper、
过期、1200-byte MTU、burst/backpressure、endpoint rebind、key rollover 和 authenticated
close。每个 replay/tamper/oversize 负例之后必须继续接收合法 snapshot，避免把 listener
或 session 已死亡误判为安全拒绝。

同一套 process owner 回归还覆盖 response loss、ticket/session/assignment invalidation、
child crash/reap、Go parent 重启恢复语义、drain、shutdown deadline 与剩余 session
资源清理。资格客户端 stdout、report 与 overlay 均不保存 raw ticket、traffic/proof key、
endpoint、PlayerID、payload 或账号凭据。

## 最终门禁

| 门禁 | 结论 | SHA-256 |
| --- | --- | --- |
| C++ Release build identity | clean build、45/45 CTest | `cc2ebf0a97393a555b151ca7e825dea49c6d506b202a048317cf01d61cc315c6` |
| C++ ASan build identity | clean build、46/46 CTest | `d860998736ae06209ae3c12139de636ef4e7410212ae0324afb184811b1e5086` |
| C++ qualification gate receipt | CI/ASan 同源 | `3dd0ccec9fb4e3cf039c5092793de4055354ab00ce43e17b576348ad6dc55d52` |
| C++ qualification report | qualified | `2b8901596a49a1f9a279f2f4b222b85d039ea31d6fa3e273ac72403f9d829634` |
| 独立 protocol client manifest | closed contract | `cb10e4107f41d6281b053f588533748512c04cfc320cf1e4bac24fab0f5f54ac` |
| server v1 report | 26/26 mandatory、11/11 gates、cleanup pass | `249c3432c17527ec9eb8b6488c8930ce355c2e365bd04936b01ae7360272718d` |
| B0.5 wire corpus | 连续 verify digest 一致 | `c96050cebd1008f13c4e6546ce98d4998bdde1e1191b4d926938b5a33d49f25b` |
| B0.5 implementation overlay | implementation qualified、B0.6 false | `798c8f1220e4e26ea4b02918ba0ca29df77c98e00ded4a0c4e0f01fbd000559d` |

Go 门禁覆盖完整 unit、显式非零 fuzz、MSYS2 `-race`、真实 child/socket 与 secret
regression。Unity `6000.5.2f1` 自动回归在最终 downstream binding 上重新执行全部
279 个 EditMode、18 个 PlayMode、Development/Release build、release scan 与双 Player
smoke；缺失资格账号时不会把未执行的五分钟 soak 或人工双 Player 矩阵伪造成 current
qualified report。B0.4 的历史资格结论不重签，当前 control corpus、真实 child 与
isolated regression 独立通过。

## 回滚

回滚单位是完整 repair：停止公开 BattleTicket 签发，revoke 已安装 ticket/session，
drain simulation，按 deadline 关闭 UDP/control 与 child，再恢复上一份可验证源码。
不得回滚到旧 proof domain、第二 listener、qualification-only socket、无 ingress 才不
推进 KCP 的运行时，或复用本审计明确替代的旧 B0.5 report。
