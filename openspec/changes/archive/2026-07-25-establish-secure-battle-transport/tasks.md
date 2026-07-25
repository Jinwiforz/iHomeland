## 1. 冻结安全与依赖基线

- [x] 1.1 建立 B0.5 threat model，登记攻击者能力、信任边界、凭据/密钥生命周期、抗放大、replay/rebind、资源耗尽与稳定失败矩阵，并以场景交叉检查本 change specs。
- [x] 1.2 评审并在 `versions.yaml` 锁定 standalone Asio、KCP、C++ crypto provider 与 C++ Protobuf lite 的精确 version/source identity/SHA-256/license/adapter owner/rollback；扩展 `tools/cpp/cpp.ps1` 完成校验恢复和离线重复构建。
- [x] 1.3 建立 `shared/contracts/fixtures/battle/wire/` 的 schema、manifest、model/profile/control binding、wire suite、limits 与低敏 secret policy，验证所有 source corpus 连续运行不被工具改写。

## 2. Battle 协议、registry 与跨语言 fixtures

- [x] 2.1 新增 `battle/v1` Protobuf source，定义 input/probe/full-delta snapshot/reliable event/entity lifecycle/resync payload，并为 message、enum 与每个 field 添加中文契约注释。
- [x] 2.2 在 message/route registry 新增 `battle: 3000-3199` owner range及`3000-3007`唯一映射，完整登记direction、raw/KCP lane、QoS、size/rate/expiry、tick/sequence、idempotency、baseline/recovery与binding policy。
- [x] 2.3 扩展统一生成入口生成并验证Go/C#/C++ battle code，保持generated artifacts忽略，增加dependency lock/checksum与禁止手工修改/第三方类型扩散的architecture gate。
- [x] 2.4 冻结48-byte secure header、16-byte raw header、KCP/reassembled route envelope、endian/width/MTU与malformed corpus，生成Go/C++/C#双向encode/decode canonical golden。
- [x] 2.5 在OpenAPI、HTTP operation catalog和fixtures新增认证幂等`issueBattleTicket`，覆盖closed request、client-safe response、own/visit、8/9 actor、stale target、response-loss与credential redaction。

## 3. Go BattleTicket 与 target admission

- [x] 3.1 在Go中实现BattleTicket value contracts、binding fingerprint、deterministic secret/proof derivation、expiry/idempotency policy和稳定错误，确保不依赖Gin、generated DTO、Redis或C++ handle。
- [x] 3.2 实现Redis BattleTicket issuance/replay adapter、versioned key/value schema与owner Lua，保持digest/handle-only、短TTL、原子幂等/冲突和Redis flush后不恢复旧资格，并补充中文字典与集成测试。
- [x] 3.3 实现PersonalWorld/VisitSession role、current SimulationTarget、trusted endpoint和installed+active 8-actor capacity的application orchestration；并发第9个actor、assignment replacement与commit-unknown均fail closed。
- [x] 3.4 实现`issueBattleTicket` handler/codec/middleware接线，只执行decode/validate/authorize/call/encode；验证body/deadline/rate/idempotency、Host/endpoint隔离和draining gate。

## 4. Go/C++ control 扩展

- [x] 4.1 扩展simulation-control schema/inventory/golden，增加listener status、ticket install/status/revoke、BattleSession revoke/closed frames，并保持64 KiB、nonce/sequence、closed schema、request replay与secret字段规则。
- [x] 4.2 在Go control session实现lifecycle/health/revoke高优先级lane与ticket低优先级有界queue，验证突发install不能饿死health、drain、stop或shutdown。
- [x] 4.3 在C++ SimulationNode实现`Installed -> Consumed -> Revoked/Expired` ticket registry、exact binding/idempotency、proof key安全内存与instance级installed+active actor slot hard cap。
- [x] 4.4 实现跨Redis与child install的有界补偿：只有issuance/install都成功才返回credential，response-loss查询并重放，cancel/timeout/stale target执行exact revoke且不撤销successor。

## 5. 握手、密码与 replay

- [x] 5.1 在项目`CryptoProvider` adapter后实现并验证CSPRNG、X25519、HKDF-SHA-256/HMAC-SHA-256、ChaCha20-Poly1305和constant-time compare，使用RFC与跨语言vectors验证provider parity。
- [x] 5.2 实现轮换cookie key、128-bit stateless cookie与ClientHello/Retry处理；在cookie验证前禁止ticket lookup/consume、X25519和session/KCP/queue allocation，并以测试证明response bytes不超过request。
- [x] 5.3 实现ClientAuth transcript proof、ticket原子consume、ephemeral X25519 key schedule与AEAD ServerAccept，覆盖exact retry replay、字段漂移、ticket race、expiry和wrong endpoint/node/instance。
- [x] 5.4 实现方向隔离key、32-bit epoch + 64-bit sequence nonce、256-packet sliding replay window和AAD验证，覆盖duplicate/too-old/future-jump/tamper/wrong direction/sequence exhaustion。
- [x] 5.5 实现10分钟或`2^20` packet触发的authenticated key rollover、previous epoch 3秒overlap与deadline/wrap关闭，并验证任一epoch不发生nonce reuse。

## 6. UDP listener、raw/KCP 与资源治理

- [x] 6.1 在C++ node composition实现唯一Asio UDP listener、fixed-size receive path、malformed fast reject、bind/advertised identity和真实loopback harness；禁止per-instance/raw/KCP第二listener。
- [x] 6.2 实现authenticated multiplexer与raw lane codec/dispatcher，按registry校验direction、size、rate、session/endpoint/target、tick/sequence/expiry和split policy。
- [x] 6.3 实现KCP adapter并精确锁定10 ms update、window 64、fast resend 2、RTO 30–200 ms、dead-link 10、1000-byte ceiling、queue 64与500 ms expiry；完成segment/golden/parity和过期终结测试。
- [x] 6.4 实现per-IP/ticket/session/message/instance token bucket、pre-auth无动态session状态、node/session 256-item hard budget、KCP 64-message queue和route-specific drop/close/backpressure metrics。
- [x] 6.5 实现authenticated endpoint rebind cookie/confirm、endpoint generation与单active endpoint，确保packet sequence/replay/KCP/key/actor/assignment在rebind后连续。

## 7. Simulation ingress、replication 与失效

- [x] 7.1 建立不可变BattleSessionContext与C++ BattleActorBinding，把PlayerID/role/assignment只从Go install投影到exact instance，禁止UDP payload覆盖并验证8/9 actor。
- [x] 7.2 将合法input bundle经tick/sequence/expiry/command allowlist送入SimulationInstance有界inbox，拒绝final transform/hit/damage/effect/death/reward/history state并保持worker唯一写。
- [x] 7.3 从simulation只读projection/event queue实现full/delta snapshot、ability event、entity lifecycle和resync；严格遵守唯一lane、baseline/recovery、expiry与可替换snapshot策略。
- [x] 7.4 组合Session epoch、membership/Owner grace、assignment/target、instance/node/listener/protocol failure的本地与Go revoke，验证任一侧先观察到失效都立即停止新gameplay且旧资格不可复活。

## 8. Composition Root、配置、端口与可观测

- [x] 8.1 扩展严格配置schema，分离UDP bind与advertised endpoint、cookie/rekey/rate/queue/deadline上限；production拒绝port 0/自动递增/隐式Host，local示例使用可覆盖`58445/udp`，test仅用`127.0.0.1:0`。
- [x] 8.2 将battle listener、ticket application/store、HTTP route和session invalidator接入唯一Go Composition Root，保持storage → child/control → UDP ready → HTTP issuance → process ready的启动顺序和部分失败逆序回滚。
- [x] 8.3 实现停止issuance/public input → revoke ticket/session → bounded KCP/egress drain → simulation result/fence/instance/node → UDP/control → storage的关闭顺序，并覆盖deadline、child crash与Go restart。
- [x] 8.4 增加低基数metrics和低敏日志，覆盖accepted/rejected handshake、cookie、auth/replay/rate/route、bytes、RTT/jitter/loss/retransmit、queue/backpressure/rebind/rekey/close reason；secret/credential/full binding不得输出。
- [x] 8.5 更新本地Docker/部署示例、listener冲突检查和network port文档，确保production endpoint只由配置/ticket下发且不把UDP暴露给diagnostic或既有TLS/TCP owner。

## 9. Contract、集成与安全验证

- [x] 9.1 完成Go value/policy/store/handler/control unit、fuzz、race和真实Redis tests，覆盖idempotency conflict、response-loss、flush/corruption、target replacement、8/9 actor和session invalidation。
- [x] 9.2 完成C++ wire/crypto/ticket/listener/raw/KCP/session/rebind/rekey unit、contract、integration、ASan与architecture tests，覆盖所有secret、capacity、queue和生命周期失败路径。
- [x] 9.3 使用真实Go parent、exact qualified C++ child和loopback UDP protocol client执行own-world与visit-world端到端ticket/install/handshake/input/snapshot/event/resync/close流程。
- [x] 9.4 执行伪造、重放、反射/放大、乱序、重复、过期、MTU、malformed flood、backpressure、rebind hijack、key rollover、assignment replacement、child crash/Go restart与shutdown fault matrix。
- [x] 9.5 扩展独立failure-regression验证schema/registry/profile/control/wire/dependency/report任一漂移、删减或旧evidence复用都会fail closed且不会改写source corpus。

## 10. B0.5 资格与文档收口

- [x] 10.1 建立`tools/secure-battle-transport/`唯一verify/finalize入口，聚合dependency restore、Go/C++/C# parity、ASan/race/fuzz、真实Redis/child/socket、安全负例与scope/secret/cache gates。
- [x] 10.2 生成只读profile implementation overlay，记录wire encoded-size、KCP parity、socket CPU/memory/queue measurements及exact source/binary/dependency/config identity，不改写B0.2 corpus或声称B0.6 fault qualification。
- [x] 10.3 重跑B0.3 full verify、B0.4 control qualification、server v1、client v1 mandatory qualification与连续B0.5 verify，要求当前源码下全部mandatory gate通过且连续低敏report digest一致。
- [x] 10.4 更新`docs/architecture.md`、`docs/gameplay-simulation-architecture.md`、`docs/network-transport-architecture.md`、`docs/network-port-allocation.md`、`docs/protocol-compatibility.md`、`docs/file-structure.md`、`docs/technology-versions.md`和`docs/roadmap.md`，明确owner、端口、安全、资格结论与B0.6后置门。
- [x] 10.5 执行格式化、静态/架构/注释/生成物/secret/cache检查与`openspec validate establish-secure-battle-transport --strict`；记录归档前审计，逐项核对spec scenarios、evidence digest、回滚和未完成范围。
