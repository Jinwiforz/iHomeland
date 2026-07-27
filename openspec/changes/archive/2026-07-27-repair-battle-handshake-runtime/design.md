## Context

B0.5 已分别实现 BattleTicket、stateless cookie、authenticated handshake、
secure datagram、KCP、rebind/rekey、multiplexer 和 UDP listener，但 composition
root 只给 listener 安装空 callback，listener 也没有同 socket send API。
`BattleAuthenticatedHandshake::Result` 没有安全转移 session seed，导致成功
ServerAccept 后无法构造唯一 secure session。

另外，Go 以 `HKDF(ticketSecret, bindingFingerprint, proofKeyDomain)` 派生 proof
key，而 HTTPS response 不公开完整 binding fingerprint。独立客户端不能按公开
材料生成 ClientAuth，qualification harness 若从 private control 取得 proof 则不再
是黑盒。

本 change 同时影响 Go derivation、C++ secret ownership、listener concurrency、
session lifecycle、canonical vectors 和资格报告。它不改变 handshake/secure header
字节布局、密码算法、MTU、numeric routes 或 KCP 参数。

## Goals / Non-Goals

**Goals:**

- 让客户端仅凭 HTTPS ticket ID/secret 计算 exact transcript proof。
- 在唯一 production UDP socket 上完成 handshake、secure raw/KCP/control 收发。
- 让 session seed 以 move-only、可清零 ownership 从 handshake 转移到唯一 session。
- 保持 cookie-first、ticket one-time consume、exact accept replay、replay/nonce、
  rebind/rekey、queue/capacity 和 lifecycle fail-closed。
- 用真实 `ihomeland-sim-server` child、private control 和 UDP socket 重做 B0.5
  qualification。

**Non-Goals:**

- 不改变 X25519、HKDF-SHA-256、ChaCha20-Poly1305、48-byte secure header、
  1200-byte MTU、raw/KCP route 或 gameplay model。
- 不实现 Unity runtime、fault gateway、B0.6 matrix/soak 或公网资格。
- 不新增 listener、远程管理端口、persistent battle state 或 Redis secret。
- 不在日志、report、fixture source 或 crash evidence 保存 ticket/proof/traffic key。

## Decisions

### 1. proof key 使用 ticket ID 作为公开 salt

proof key 固定为：

`HKDF-SHA-256(ikm=ticketSecretRaw32, salt=ticketIdRaw16, info="ihomeland/battle-ticket/proof-key/v2")`

Go 在签发时生成相同 proof 并只经 private control 安装到 C++；独立客户端从 HTTPS
返回的 canonical ticket ID/secret 本地派生。ticket secret 仍由 server-owned root
key、issuance ID 和完整 binding fingerprint 确定性生成，所以改变任何 binding
事实仍会改变 secret、ticket ID 与 installed proof。选择 ticket ID 而不是公开完整
binding，是为了保持 HTTP projection 低敏并消除客户端重建 private authority facts
的需求。

这是 derivation identity 的 breaking change。旧 installed ticket、旧 canonical
vector 和旧 B0.5 report 全部失效，不提供跨版本兼容；ticket 本身短期且一次性，
部署必须先 drain 旧 node。

### 2. ServerAccept 是客户端取得 binding fingerprint 的唯一位置

ClientAuth 只证明持有 ticket secret 派生的 proof。服务端原子消费 exact installed
binding 后，把完整 binding fingerprint 放在 AEAD 保护的 ServerAccept parameters
中。客户端在认证解密后锁定该 fingerprint，并用其 discriminator 初始化 secure
identity；Go supervisor 不传 proof key 或 binding。

客户端不能预先判断服务端 binding 是否符合业务期望，但 ticket secret 已由 HTTPS
authenticated request 和完整 server-side binding 派生；ServerAccept 的 AEAD key
又绑定 X25519 shared secret、proof key 和完整 transcript。这比向客户端暴露 private
binding fields 更小、更稳。

### 3. listener 拥有唯一 socket，runtime 拥有 protocol state

`BattleUdpListener` 增加 `Send(datagram, remote)`，所有 send 都序列化到 listener
自己的 Asio executor；调用方不能取得 socket。receive worker 只复制一个受
`MaximumDatagramBytes` 限制的 owned datagram 并提交给 node-global
`BattleTransportRuntime`，不在 callback 栈执行昂贵 crypto 或 simulation work。

`BattleTransportRuntime` 是每个 SimulationNode 唯一的协议 owner：

- pre-auth 路径只持有 cookie gate、handshake owner 与固定/有界 replay；
- active session map 以 session digest 路由，容量不超过 node actor hard cap；
- 每个 session 独占 secure channel、endpoint rebind、KCP adapter、resource
  governor、BattleSessionContext 和 queue；
- send 只能回到同一 listener；
- instance/session/assignment revoke 通过显式 command 删除并清零 session。

不把该逻辑塞进 listener，是为了保持 socket lifecycle 与业务/crypto 状态分离；
不复用 control server callback，是为了让 stdin control 和 UDP data plane 保持不同
owner。

### 4. handshake result 使用 move-only secret transfer

首次 accepted result 携带 `SessionBootstrap`：session seed、binding fingerprint、
session ID、generation、slot、role、endpoint generation 与 key epoch。其 copy
操作删除，析构和 moved-from 状态清零。exact ClientAuth replay 只返回缓存的相同
ServerAccept，不再次转移 seed、不创建第二 session。

runtime 只有在 listener send 已排队且 bootstrap 完整时发布 active session；任何
构造或排队失败都撤销 consumed session、清零 seed 并产生稳定 low-sensitive close
reason。因为 ticket consume 已不可逆，客户端只能重新 admission。

### 5. 以单 writer strand 和 bounded queues 处理并发

listener receive 可以并发到达，但 runtime 的 session lookup/transition 在单一
executor 上线性化。simulation worker 仍是 gameplay state 唯一 writer。每个
datagram、send queue、handshake replay 与 active session 都有 manifest hard cap；
超过上限按既有 resource policy 丢弃或关闭，不动态扩容。

### 6. 资格迁移采用 drain-and-replace

先更新 Go/C++/C# derivation 与 canonical vector，再构建新 child。Go 停止签发旧
ticket、drain 旧 child，启动绑定新 wire/derivation identity 的 child 后恢复签发。
回滚只能整体回滚 Go 与 C++ 并继续使用旧 report；新旧 proof 版本不得在同一 node
并存。完成真实 child/socket security suite 后生成新 B0.5 report，B0.6 upstream
binding 随后更新。

## Risks / Trade-offs

- [ticket ID 是公开 salt，攻击者可离线尝试 proof] → IKM 是 256-bit 随机 ticket
  secret，公开 salt 不降低穷举成本；HMAC proof 仍不发送 secret。
- [listener send 与 Stop 并发造成迟到 callback] → strand 线性化 send/close，
  generation token 拒绝 stop 后 completion，cleanup 等待 bounded outstanding。
- [terminal response 的冗余副本命中已关闭 Windows UDP 远端并触发
  `WSAECONNRESET`] → listener 在首次 receive 前强制关闭 `SIO_UDP_CONNRESET`；
  该 socket policy 配置失败即启动失败，真实 loopback 回归证明后续合法 ingress
  不受旧远端 ICMP 影响。
- [consume 后 session 构造失败导致 ticket 不可用] → fail closed、显式 closed
  receipt 与重新 admission；禁止恢复 consumed proof。
- [跨 lane 到达顺序不同] → secure replay、raw replaceable sequence 与 KCP ordered
  state分别维护，不用 wall-clock arrival 推断 gameplay 顺序。
- [改动使旧 B0.5 report 失效] → 这是纠正错误资格结论的必要代价，manifest 和
  report digest 强制阻止旧证据复用。
