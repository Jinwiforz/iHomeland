# B0.5 资格审计

## 范围

本审计只声明安全战斗传输 implementation qualification，不声明 B0.6 弱网、容量或生产
放量资格。B0.2 model/profile 与 B0.3/B0.4 corpus 均保持只读。

## 端到端链路

真实链路由以下同一组 production adapter 组成：

1. Go Composition Root 在 MySQL/Redis 后启动 exact qualified C++ child 与 stdio control。
2. own-world/visit-world application tests 解析 current assignment、8-slot capacity 与
   trusted advertised endpoint，经真实 Redis Lua issuance 和 child install/status/revoke。
3. C++ loopback UDP harness 执行 ClientHello/Retry/ClientAuth/ServerAccept、raw input、
   KCP snapshot/event/resync 与 authenticated close；C++ control stdio test 验证 exact child
   ticket/session lifecycle。
4. process owner integration tests 验证 child crash、Go restart、deadline 与逆序 shutdown，
   不依赖第二 listener 或 fake network owner。

资格入口必须同时通过这些 package/test executable；任何单层通过均不能代替完整 gate。

## 安全与故障矩阵

| 风险 | 证据 owner | 稳定裁决 |
| --- | --- | --- |
| forged/tamper/wrong direction | crypto、handshake、secure datagram tests | reject/close |
| replay/duplicate/too-old/future jump | secure datagram、replay window tests | drop/close |
| reflection/amplification | cookie 与 UDP loopback tests | pre-cookie response 不超过 request |
| expiry/MTU/malformed flood | protocol、listener、resource tests | fast reject、无 session allocation |
| backpressure/KCP dead link | KCP、resource、session tests | replace/drop/terminal close |
| rebind hijack | endpoint rebind tests | cookie confirm 失败且 generation 不变 |
| key rollover/wrap | secure datagram tests | overlap 内接受、deadline/wrap 关闭 |
| assignment replacement/8→9 | Go battleentry 与 C++ ticket/session tests | stale/capacity reject |
| child crash/Go restart | process owner 与 Redis tests | 旧资格不恢复 |
| shutdown fault/deadline | lifecycle、control stdio、SimulationNode tests | fail closed、逆序回收 |

## Evidence identity 与回滚

`tools/secure-battle-transport/secure-battle-transport.ps1 finalize` 连续执行完整 verify，
要求 source tree digest 相同，再生成 `.local/evidence/secure-battle-transport/` 下只读、
低敏、确定性的 implementation overlay。report 不保存 secret、credential、endpoint、
ticket、session 或完整 binding。

本轮归档前证据如下，所有 SHA-256 均由当前工作区的最终报告或只读资格产物计算：

| 门禁 | 结论 | 证据 SHA-256 |
| --- | --- | --- |
| B0.3 C++ CI build identity | qualified | `0864e3f42aaa3029600fa31ddae3673fb62b932fa6252022ab9675a62b575424` |
| B0.3 C++ ASan build identity | qualified | `085b444c82535e3e7d141e41211a2fe468a30ec3af5a803d16edb6c870f3eb13` |
| B0.3 qualification receipt | qualified | `32b5d61f2e3d3f700187462540028a05d76a478898684d219e5e27cf038a9b19` |
| B0.4 report 1/2 | stable、qualified | `5f7ea6969423fe064fcd3470461ddea535e986b04b216eef69490dd91c6924e6` |
| server v1 report | 26/26 scenarios、11/11 gates、cleanup pass | `82aca9bf663777e1ce68f47a23c9c7a70e80595150cba82832a6c6c23c53819a` |
| client v1 report | 20/20 mandatory、cleanup pass | `35a5bd6c67d8cdb098c1e7763ad4733a0600846042f726e297fdc639ba00e0fd` |
| B0.5 wire corpus | 连续 verify digest 一致 | `6553ed814be6722e3e5c7a18fb8a10967bb02f955881d5ba1cdd179be66b4cba` |
| B0.5 implementation overlay | implementation qualified、B0.6 false | `dd6bfb0c2c959ed77ec1af98df41b1aae785d3b1e409da741eee990b2a519da4` |

client v1 证据由 Unity `6000.5.2f1` 重新编译并执行 279 个 EditMode tests、
PlayMode、Development/Release build、双 Player smoke、五分钟真实恢复 soak 与三组真实
Owner/Visitor operator 场景。未手工新增或修改 Unity `.meta`。

回滚单位是整个 `establish-secure-battle-transport` change：停止公开
`issueBattleTicket`，撤销已安装 ticket/session，drain simulation，关闭 UDP/control，
移除 battle route/config；不得回滚为明文 UDP、固定 production 端口或旧 evidence。
