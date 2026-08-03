## MODIFIED Requirements

### Requirement: Battle wire 与 numeric route 必须唯一、闭合且符合 MTU

Battle wire MUST 提供 versioned binary envelope、`battle/v1` Protobuf payload 和唯一 numeric registry。`battle.input.bundle` 至 `battle.resync.response` 的 8 个 logical kind MUST 一一映射到 `3000-3007`、固定 direction 和唯一 raw 或 KCP lane；route MUST 登记 owner、QoS、max encoded/logical size、rate、精确 sender expiry、tick/sequence、idempotency、baseline/recovery 和 assignment/session binding。Numeric registry 的 expiry MUST 与 `battle-network-profile-v2` message inventory 逐项一致，其中 message 3006 与 3007 使用 2250 ms，其他 route 保持各自既有值。Datagram MUST 不超过 1200 bytes 并遵守 48-byte IP/UDP、48-byte secure header、16-byte AEAD tag、16-byte raw 或 24-byte KCP budget；IP fragmentation、通用 Any、未登记 compression、lane fallback 和 payload identity override MUST 禁止。

`BattleEntityState` 与 `BattleEntityDelta` 的transform MUST 显式提供position X/Y/Z、yaw和velocity X/Y/Z，yaw范围固定为`[-180000, 180000)`。`state_flags` registry MUST 保留bits 0–3作为phase token、登记bit 4为authority grounded、保留bit 31为dead，其余bit MUST 为零；delta `state_mask`与字段presence MUST 精确一致。Producer和全部consumer MUST 对缺失scalar、非法yaw、未知flag或mask/presence漂移fail closed，不得默认为零、掩码或重解释已有bit。以上字段仍使用现有field number、raw snapshot lane、payload ceiling和partition policy。

#### Scenario: Snapshot 通过 KCP 发送

- **WHEN** sender 或 registry 尝试把 full/delta snapshot 编码为 KCP、TLS/TCP 或 WSS route
- **THEN** contract/architecture gate 失败且运行时在 simulation dispatch 前拒绝错误 lane

#### Scenario: Payload 超过 route 或 MTU

- **WHEN** encoded message 超过其 registry max 或完整 datagram 超过 1200 bytes
- **THEN** sender 按登记 split policy 有界拆分或稳定拒绝，不依赖 IP fragmentation、不移除安全字段且不动态换 lane

#### Scenario: Go/C++/C# fixture parity

- **WHEN** 三种实现消费同一 header/protobuf/AAD/crypto/KCP canonical fixture
- **THEN** message ID、bytes、digest、decode result、route expiry、snapshot transform/state flags registry和negative disposition一致，unknown field/version或registry漂移使验证失败

#### Scenario: Resync route 仍使用旧 deadline

- **WHEN** numeric registry、wire fixture 或任一语言 projection 把 message 3006 或 3007 登记为 500 ms
- **THEN** profile parity 失败，不生成或启动不一致的 runtime route table

#### Scenario: Grounded bit 与 phase bit 冲突

- **WHEN** producer或consumer把bit 0重解释为grounded，或把bit 4解释为phase/dead以外状态
- **THEN** wire registry parity失败且snapshot不得发布，已有低四位phase语义保持不变

## ADDED Requirements

### Requirement: 同 actor 的认证 successor 必须有界接管 predecessor

当新BattleSession已通过一次性ticket认证且绑定到与既有active session相同的SimulationInstance、mapping generation和ActorID时，runtime MUST 在发送新session首个full baseline前终结并移除predecessor。Predecessor MUST 以低敏lifecycle原因记账，旧secure route MUST 立即不可路由；其他actor、instance或mapping的session MUST NOT 被替换。Successor的首个公开projection MUST 只包含唯一actor identity，不得因重复actor永久等待baseline。

客户端从`ServerAccept`进入`AwaitingBaseline`后 MUST 使用有界deadline等待首个完整full snapshot；deadline到期 MUST 发布稳定可恢复失败并关闭current generation，MUST NOT 无限保持loading状态。

BattleTicket绝对expiry MUST 只终结尚未消费的`Installed`凭据并阻止其建立新BattleSession。一旦ticket已由成功握手原子转换为`Consumed`，该credential deadline MUST NOT 把active actor改为`Expired`、释放其actor slot或使其session authority失效；active actor只可由显式revoke、同actor successor或session、assignment、target、instance、node lifecycle终结。

#### Scenario: Client进程消失后同角色重新进入

- **WHEN** predecessor未发送close但同一角色持新ticket完成认证
- **THEN** runtime原子退休predecessor，active session数量不增加，successor收到完整baseline且旧route的数据包被拒绝

#### Scenario: 首个baseline未到达

- **WHEN** client已认证但在登记deadline内没有提交首个完整full snapshot
- **THEN** current generation以可恢复timeout终结并进入既有single-flight recovery，UI不得永久显示同步中

#### Scenario: 快速target替换收到旧handshake响应

- **WHEN** connected UDP endpoint复用使结构匹配的旧`Retry`或`ServerAccept`先于current handshake响应到达
- **THEN** client只可接受由current transcript认证的响应；旧候选必须原位清零并在原绝对deadline内继续等待，不能建立session、延长deadline或立即升级为终态Security

#### Scenario: 另一个actor在ticket expiry后仍保持在线

- **WHEN** Visitor已用短期ticket完成握手并保持active，原ticket deadline过去后Owner安装并认证同actor successor
- **THEN** Visitor仍为`Consumed`并继续占用原slot，Visitor secure route保持可用，Owner successor的首个full baseline仍包含Owner与Visitor且不得只剩当前本地actor
