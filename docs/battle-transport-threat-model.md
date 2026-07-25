# Battle Transport 威胁模型

## 文档职责

本文档定义 B0.5 `establish-secure-battle-transport` 的攻击者能力、信任边界、资产、凭据与密钥生命周期、抗放大、重放、endpoint rebinding、资源耗尽和稳定失败策略。实现、fixtures、资格工具和评审必须同时满足本文档与 OpenSpec `secure-battle-transport` requirements；发生冲突时以更严格且不扩大授权的规则为准。

本文档只覆盖 BattleTicket、UDP/KCP wire 与它们连接 Go/C++ simulation runtime 的边界。账号登录、HTTPS/WSS/TLS-TCP、WorldAdmission、持久奖励与 Unity runtime 分别由既有或后续 capability 管理。

## 安全目标

- 只有有效 Account Session、current PersonalWorld/VisitSession role、current `SimulationTarget` 和可用 battle actor slot 的主体能够建立 BattleSession。
- 网络观察者不能从 UDP datagram 恢复 ticket secret、traffic key、账号凭据或 gameplay 明文，也不能静默修改 header、lane、Tick、sequence 或 payload。
- 一个 endpoint、ticket、session generation、key epoch 或 packet sequence 的合法数据不能在其他绑定或生命周期中复用。
- 未验证地址不能诱导服务端发送比请求更大的响应、执行昂贵 key agreement 或分配 session/KCP/actor queue。
- 客户端输入只能表达已登记 intent；PlayerID、role、actor、最终 transform、hit、damage、death、reward 和结算不能由 payload 提升为权威事实。
- assignment、session epoch、membership、node、instance 或 listener 失效后，旧 credential、key、endpoint 和 replay state 均不能恢复写资格。
- malformed、伪造和超限流量必须以固定内存、固定计算上限和低基数诊断处理，不能拖垮 simulation lifecycle、health、drain 或 shutdown。

## 非安全承诺

- B0.5 不证明公网 latency、jitter、loss、reorder、NAT 和运营商网络包络；这些属于 B0.6。
- Windows x64 B0.5 资格不自动适用于 Linux、移动平台、Unity IL2CPP 或其他 crypto provider。
- BattleSession 不保存持久奖励、资产或结算事实；成功发送或接收 UDP packet 不表示任何 MySQL transaction 已提交。
- KCP 只提供有限 ARQ，不提供身份、机密性、完整性、抗重放、限流或业务授权。
- 流量分析、DDoS upstream 清洗和主机完全失陷不由应用层协议单独解决；实现仍必须限制本进程放大与资源占用。

## 资产与敏感度

| 资产 | Owner | 存储位置 | 敏感度与约束 |
| --- | --- | --- | --- |
| Account SessionID/epoch 与 PlayerID | Go Session owner | Redis/Go AuthContext | 身份事实；UDP payload 不得覆盖 |
| BattleTicket secret | Go BattleTicket issuer 与客户端 | HTTPS response、调用方短期内存 | 高敏、一次性、不得进入 UDP/日志/Redis/fixture |
| BattleTicket proof key | Go issuer 与 exact C++ child | 继承 control pipe、C++ 短期安全内存 | 高敏认证能力；不得持久化或输出 |
| Cookie keys | C++ listener | 当前 child process 内存 | 高敏；轮换且只接受 current/previous epoch |
| X25519 ephemeral private keys | 客户端与 C++ listener | 单次握手内存 | 高敏；accept 后清零，不写 evidence |
| Traffic keys/rekey secret | BattleSession owner | C++ session memory、客户端内存 | 高敏；方向与 epoch 隔离，关闭时清零 |
| BattleSessionID/generation | C++ BattleSession owner | C++ bounded registry | 低敏关联值；本身不授予资格 |
| AssignmentStamp/SimulationTarget binding | Go placement/control owner | Go/C++ runtime | 安全绑定；日志只能输出摘要 |
| Packet sequence/replay bitmap | C++ BattleSession owner | C++ bounded session state | 防重放事实；rebind 不得重置 |
| Gameplay input/snapshot/event | C++ simulation/replication owner | 有界 queue 与短期 history | 业务敏感；不进入通用日志或资格报告 |
| Qualification report | B0.5 qualification owner | ignored run 目录与低敏 digest | 只能包含 identity/digest/计数/稳定 reason |

## 攻击者模型

### 网络攻击者

攻击者可以：

- 观察、丢弃、延迟、复制、乱序、截断、拼接和篡改任意 UDP datagram；
- 伪造 source IP/port（受实际网络路径限制）并向 listener 发送任意大小和速率的数据；
- 捕获旧 ticket ID、cookie、ciphertext、endpoint 和低敏 session摘要；
- 在 NAT 变化或客户端重连期间竞速旧、新 endpoint；
- 诱导 KCP 重传、ordered blocking、queue pressure 和 application expiry；
- 并发尝试多个 ticket、相同 ticket、相同 PlayerID 或第 9 个 actor。

攻击者不能被假定可以：

- 解密 production HTTPS 或直接读取客户端 BattleTicket secret；
- 读取 Go/C++ process memory、继承 control handles 或 server secret；
- 绕过 Account Session、PersonalWorld/VisitSession 和 placement owner 的进程内调用边界。

若这些假设不成立，必须按主机或 TLS compromise 处理，不得声称 UDP 协议仍保持完整授权。

### 恶意或被攻陷的合法客户端

合法客户端拥有自己的 ticket secret、traffic key 和 actor binding，并可以构造任何通过 AEAD 的 payload。服务端仍必须拒绝：

- 其他 PlayerID、role、actor、world、visit、assignment 或 instance 声明；
- final transform、hit target、damage、effect、death、reward、asset 或 settlement；
- future/expired/stale InputTick、sequence gap abuse、重复 command 和超限 bundle；
- 错误 direction、lane、message ID、payload size、rate、baseline 或 recovery；
- 未经 challenge 的 endpoint rebind、旧 generation、旧 key epoch 和 replay packet。

AEAD 只证明 packet 来自持有当前 key 的主体，不证明业务命令合法。

### 依赖与内部故障

必须按敌对输入等价处理：

- Redis timeout、flush、corruption、commit-unknown 与 response loss；
- Go/C++ control frame 丢失、重复、错序、unknown field、stdout 污染和 deadline；
- child crash、listener bind/runtime failure、Go restart 和 shutdown timeout；
- crypto/KCP/provider 版本或配置漂移；
- assignment replacement、lease expiry、membership/Owner grace 与 Session epoch 竞态；
- queue 满、Tick debt、KCP dead-link 和 partial drain。

任何故障都不能创建 memory fallback、默认允许、自动新端口、旧 credential 恢复或跨 transport fallback。

## 信任边界

```text
Internet / client process
  | HTTPS TLS 1.3
  v
Go Public HTTP Adapter
  | AuthContext + target selector
  v
BattleTicket Application
  | Session / world-visit role / SimulationTarget / capacity
  +---- Redis BattleTicket issuance record
  |
  | inherited private stdio control (secret-bearing)
  v
C++ SimulationNode
  | installed ticket / proof key
  +---- UDP listener: untrusted datagrams
  |       -> cookie
  |       -> handshake / AEAD / replay / route
  |       -> BattleSessionContext
  v
SimulationInstance bounded inbox / replication projection
```

边界规则：

- HTTP handler、UDP codec、control codec 和 storage adapter 都不是业务授权 owner。
- Go 不能把客户端 target 字段直接写入 control binding；C++ 不能从 UDP 恢复 PlayerID 或 assignment。
- Redis 不保存 raw ticket secret、proof key、traffic key、cookie key、完整 packet 或 gameplay state。
- C++ 不访问 MySQL/Redis，不签发 Account Session，不修改 PersonalWorld/VisitSession 持久事实。
- Simulation worker 是 ECS、physics、navigation、history 和 authoritative gameplay 的唯一 writer。

## 凭据与密钥生命周期

### BattleTicket

```text
Derived
  -> Redis issuance committed
  -> Installed in exact child
  -> Returned by HTTPS
  -> Consumed by one exact ClientAuth
  -> Expired / Revoked
```

- ticket ID 可出现在 UDP；ticket secret不得出现。
- 相同 idempotency identity 与完整相同 binding 可以重放首次 credential；任一语义字段漂移必须冲突。
- child install 先于 HTTPS success；部分成功必须以 status/revoke 收敛。
- installed + active actor slot总数不得超过8。
- Session epoch、target revision、assignment、node、instance或role失效立即撤销。

### Handshake keys

- Cookie key由C++ CSPRNG生成并轮换，只用于HMAC cookie；不得复用为ticket、traffic或rekey key。
- Ticket proof key由BattleTicket secret和完整binding经HKDF派生，只用于transcript proof和traffic secret输入。
- 客户端与服务端各生成单次X25519 ephemeral private key；shared secret、proof key和transcript hash共同进入HKDF。
- Client-to-server、server-to-client和rekey使用不同label派生的独立key。
- 握手临时secret在ServerAccept形成后清零；失败路径同样清零。

### Traffic keys

- 同一方向、key epoch下packet sequence严格单调，96-bit nonce不得复用。
- 10分钟或`2^20` packet先到者触发rollover；previous receive epoch最多保留3秒。
- rebind不重新派生相同epoch key、不重置sequence或replay bitmap。
- close、revoke、deadline或protocol violation后清零key，并拒绝该session generation后续packet。

## 抗放大与预认证预算

| 阶段 | 允许操作 | 禁止操作 | 响应预算 |
| --- | --- | --- | --- |
| Malformed/undersized | 固定头检查、per-IP token bucket | lookup、X25519、allocation、日志payload | 默认静默丢弃 |
| ClientHello 无cookie | 固定解析、stateless cookie HMAC | ticket lookup/consume、X25519、session/KCP/actor queue | Retry bytes不得超过request |
| ClientAuth cookie无效 | cookie verify、低基数计数 | ticket lookup、X25519、详细错误 | 静默丢弃或不放大的固定错误 |
| ClientAuth cookie有效 | installed ticket lookup、proof verify、一次X25519 | 无界重试、跨node lookup | 验证地址后允许有界ServerAccept |
| Authenticated session | AEAD/replay/route/rate | 未登记消息、无界queue | 受per-session/route预算约束 |

实现必须使用固定上限buffer读取至多1201 bytes，以区分合法1200-byte datagram与超限输入；不能依据未认证length字段分配内存。

## Replay、乱序与 endpoint rebind

- 每个receive key epoch使用256-packet sliding bitmap；只有AEAD成功后才能提交窗口。
- duplicate、too-old和future-jump超限在application dispatch前终结。
- KCP内部sequence不能替代安全packet replay，application idempotency也不能替代两者。
- 新endpoint必须同时证明current traffic key和绑定新IP/port的cookie；成功后endpoint generation原子递增。
- rebind期间最多一个current endpoint；旧endpoint、旧cookie、旧generation或重复confirm不能恢复。
- previous key overlap与rebind是正交状态，均不能延长对方deadline。

## 资源耗尽边界

| 资源 | Hard limit / owner | 满载行为 |
| --- | --- | --- |
| UDP datagram | 1200 bytes / listener | 超限fast reject |
| Pre-auth state | 无per-client session allocation / listener | token bucket后丢弃 |
| Installed + active actor | 8 / SimulationInstance | 第9个battle admission拒绝 |
| Node/session ingress/egress | profile 256 items / multiplexer | route-specific drop或关闭 |
| KCP queue | 64 messages / BattleSession | expired终结或hard backpressure关闭 |
| Replay window | 256 packets/epoch/direction | too-old/duplicate拒绝 |
| Handshake replay | 有界短TTL / listener | exact replay返回同一accept，否则拒绝 |
| Control ticket queue | 配置hard cap / Go control | install拒绝，不阻塞health/lifecycle |
| Rate limiter entries | 配置hard cap + idle eviction / listener | 新identity fail closed |

任何capacity错误都不得通过扩容、丢弃安全字段、改变lane、自动切换TCP/WSS或修改VisitSession容量解决。

## 稳定失败矩阵

| 条件 | Stable reason | 状态影响 |
| --- | --- | --- |
| Unknown/invalid wire version | `wire-version` | 丢弃，不创建状态 |
| Datagram 超过1200 bytes | `datagram-size` | 丢弃 |
| Cookie缺失/无效/过期 | `cookie-required` / `cookie-invalid` | 不查询ticket |
| Ticket proof错误 | `ticket-proof` | ticket保持未消费 |
| Ticket过期、已消费或revoke | `ticket-expired` / `ticket-replay` / `ticket-revoked` | 不创建session |
| Target/assignment/node/instance漂移 | `stale-target` | revoke ticket/session |
| Actor capacity达到8 | `battle-capacity` | 只拒绝battle，不改VisitSession |
| AEAD失败 | `auth-tag` | 丢弃，不推进replay |
| Packet重复/过旧/future jump | `packet-replay` / `packet-window` | 不dispatch |
| 错误lane/direction/message | `route-policy` | 拒绝或按policy关闭 |
| InputTick/expiry非法 | `input-window` / `message-expired` | 不进入simulation |
| Queue达到hard limit | `backpressure` | 丢弃可替换项或关闭session |
| Rebind proof/generation错误 | `rebind-proof` / `rebind-stale` | 保持current endpoint |
| Rollover超时/epoch耗尽 | `key-rollover` / `key-exhausted` | 关闭并要求新ticket |
| Session epoch失效 | `session-invalidated` | revoke lineage |
| Listener/control/child failure | `transport-unavailable` / `node-unhealthy` | 撤销readiness与全部target |
| Shutdown deadline | `shutdown-deadline` | fail closed继续逆序释放 |

公开响应和日志只能使用稳定reason类别，不返回proof、key、cookie、ticket存在性、完整binding或provider内部错误。

## Requirement 与测试追踪

| 威胁主题 | OpenSpec requirement | Mandatory evidence |
| --- | --- | --- |
| Ticket授权与一次消费 | BattleTicket必须绑定current target | Go policy/store、真实Redis、child install/consume race |
| 反射与放大 | 握手必须先验证地址 | response/request byte ratio、无allocation/无X25519 instrumentation |
| 机密性与篡改 | 每个datagram必须加密认证 | RFC vectors、cross-language AEAD、tamper corpus |
| Replay/nonce | datagram nonce与replay discipline | duplicate/old/future/wrap/rollover tests |
| 错误channel/MTU | Battle wire唯一闭合 | registry parity、1200-byte boundary、no-fragment architecture gate |
| KCP滥用 | 单UDP multiplexer有界承载raw/KCP | exact parameter parity、expiry、queue/retransmit tests |
| 恶意合法客户端 | BattleSessionContext唯一绑定actor | identity/authority negative corpus、simulation inbox tests |
| NAT与endpoint劫持 | Rebind保持generation与replay连续 | cookie/proof/race/old-endpoint tests |
| 生命周期复活 | Session、target、process失效撤销 | epoch/assignment/child/restart/shutdown fault tests |
| 旧evidence误用 | B0.5独立资格门 | digest-bound report、failure regression、连续报告一致 |

## 评审与变更规则

- 改变算法、握手round、cookie字段、nonce构造、replay窗口、key rollover、endpoint rebind、numeric route、MTU、安全header或credential语义必须更新OpenSpec、fixtures、threat model和资格baseline。
- 只调整环境实际端口不改变本威胁模型；新增listener、拆分socket或改变advertised endpoint owner必须走OpenSpec。
- 新增gameplay message必须先登记owner、direction、唯一lane、size/rate/expiry、tick/idempotency和安全字段，再生成跨语言fixtures。
- 任何资格报告若未绑定当前threat-model、wire、dependency、model/profile/control和source digest，只能作为诊断，不能解锁B0.6。
