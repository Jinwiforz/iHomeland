## MODIFIED Requirements

### Requirement: Battle wire 与 numeric route 必须唯一、闭合且符合 MTU

Battle wire MUST 提供 versioned binary envelope、`battle/v1` Protobuf payload 和唯一 numeric registry。`battle.input.bundle` 至 `battle.resync.response` 的 8 个 logical kind MUST 一一映射到 `3000-3007`、固定 direction 和唯一 raw 或 KCP lane；route MUST 登记 owner、QoS、max encoded/logical size、rate、精确 sender expiry、tick/sequence、idempotency、baseline/recovery 和 assignment/session binding。Numeric registry 的 expiry MUST 与 `battle-network-profile-v2` message inventory 逐项一致，其中 message 3006 与 3007 使用 2250 ms，其他 route 保持各自既有值。Datagram MUST 不超过 1200 bytes 并遵守 48-byte IP/UDP、48-byte secure header、16-byte AEAD tag、16-byte raw 或 24-byte KCP budget；IP fragmentation、通用 Any、未登记 compression、lane fallback 和 payload identity override MUST 禁止。

#### Scenario: Snapshot 通过 KCP 发送

- **WHEN** sender 或 registry 尝试把 full/delta snapshot 编码为 KCP、TLS/TCP 或 WSS route
- **THEN** contract/architecture gate 失败且运行时在 simulation dispatch 前拒绝错误 lane

#### Scenario: Payload 超过 route 或 MTU

- **WHEN** encoded message 超过其 registry max 或完整 datagram 超过 1200 bytes
- **THEN** sender 按登记 split policy 有界拆分或稳定拒绝，不依赖 IP fragmentation、不移除安全字段且不动态换 lane

#### Scenario: Go/C++/C# fixture parity

- **WHEN** 三种实现消费同一 header/protobuf/AAD/crypto/KCP canonical fixture
- **THEN** message ID、bytes、digest、decode result、route expiry 和 negative disposition 一致，unknown field/version 或 registry 漂移使验证失败

#### Scenario: Resync route 仍使用旧 deadline

- **WHEN** numeric registry、wire fixture 或任一语言 projection 把 message 3006 或 3007 登记为 500 ms
- **THEN** profile parity 失败，不生成或启动不一致的 runtime route table

### Requirement: 单 UDP multiplexer 必须有界承载 raw 与 KCP

每个 SimulationNode MUST 只由 C++ 拥有一个 Asio UDP listener；raw、KCP 和 transport-control MUST 共享该 socket 和 authenticated BattleSession。Bind 与 advertised endpoint MUST 分别由 Go 严格配置，production MUST 拒绝 port 0、隐式 Host 推导、地址冲突和自动换端口；本地推荐值为可覆盖的 `58445/udp`，loopback test MAY 使用 `127.0.0.1:0`。KCP adapter MUST 精确使用 profile 登记的 10 ms update、window 64、fast resend 2、RTO 30–200 ms、dead-link 10、1000-byte ceiling和 queue 64；sender application expiry MUST 从 immutable numeric route policy 解析，message 3004/3005 使用 500 ms，message 3006/3007 使用 2250 ms。Receiver reassembly MUST 由 KCP receive window、queue 与 session lifecycle 有界，完整重组后校验 exact route、sequence、Tick 与 generation，不得从首个 segment 推导 sender deadline。

#### Scenario: Production UDP bind 失败

- **WHEN** 配置的 battle UDP address 被占用、advertised endpoint 非法或 listener identity 与 ticket endpoint 不一致
- **THEN** simulation node 启动失败、process 保持 non-ready 并逆序回滚，不随机选择其他 port 或下发不可达 endpoint

#### Scenario: KCP sender message 已过期

- **WHEN** 可靠 message 在 sender queued/inflight 状态超过 registry application expiry
- **THEN** sender 以稳定 inflight expiry 终结，不继续发送、不进入 simulation且不回退 raw/TCP/WSS

#### Scenario: Receiver 首个 segment 先于完整重组到达

- **WHEN** receiver 收到后续 ordered message 的首个 segment，但完整 delivery 等待前序 segment
- **THEN** receiver 不启动 application reassembly timer，继续受 KCP window/queue 与 session lifecycle 硬上限约束

#### Scenario: 一个 instance 尝试创建第二 listener

- **WHEN** runtime、配置或 test 为 raw、KCP 或某个 SimulationInstance 单独 bind socket
- **THEN** architecture gate 失败；所有 lane 必须经 node-global authenticated multiplexer

#### Scenario: KCP caller 尝试覆盖 deadline

- **WHEN** application caller 提供与 numeric route 不一致的 expiry 或绝对 deadline
- **THEN** adapter fail closed 并记录低敏 route policy reason，不发送、截断或延长该消息

### Requirement: B0.5 必须通过跨语言安全 transport 实现资格门

本 capability MUST 提供 versioned schema/manifest、Go/C++/C# fixtures、threat model、dependency lock、真实 child/loopback UDP harness 和唯一 qualification report。Mandatory gates MUST 覆盖 `battle-network-profile-v2` binding、wire/crypto/KCP route-expiry parity、ticket response-loss/consume、forged/replay/amplification/reorder/duplicate/expiry/MTU/backpressure/rebind/rollover/shutdown、own/visit、8/9 actor、assignment/session 失效、child crash/Go restart，以及 B0.3/B0.4、server v1、client v1 和 OpenSpec strict regression。报告 MUST 绑定 source/binary/toolchain/dependency/model/profile/control/wire/registry/config/fixture digest 且保持低敏；只可声明 `secure-transport-qualified-windows-x64`，不得替代 B0.6 网络发布资格。

#### Scenario: 完整 B0.5 verify 通过

- **WHEN** exact source 和锁定工具链依次通过 contract、Go、C++、C#、真实 UDP/KCP、安全、lifecycle、capacity、failure 和 report validation
- **THEN** B0.5 report 声明 Windows x64 secure transport implementation-qualified，并保留 B0.6 fault/capacity/soak 未完成边界

#### Scenario: 500 ms 与 2250 ms route parity

- **WHEN** adapter parity corpus 分别发送一般可靠事件和 resync request/response
- **THEN** 前者在 500 ms 后稳定 sender expired，后者只在 2250 ms 后 sender expired，且两类消息共享同一有界 KCP conversation

#### Scenario: 任一 mandatory gate skipped

- **WHEN** toolchain、Unity parity、真实 socket、KCP、crypto、安全负例、lifecycle 或上游 regression 任一缺失、失败或 skipped
- **THEN** report 保持 not-qualified 且不能把 synthetic fixture 结果替代真实实现证据
