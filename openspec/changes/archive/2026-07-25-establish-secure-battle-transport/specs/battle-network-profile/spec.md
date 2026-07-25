## ADDED Requirements

### Requirement: B0.5 implementation evidence 必须作为冻结 profile 的只读 overlay

Secure battle transport MUST读取并验证`battle-network-profile-v1`完整manifest与digest，使用真实wire、socket和KCP adapter输出独立versioned implementation report，补证`wire-encoded-size-parity`与`kcp-adapter-parity`等B0.5负责的`implementation_required`指标。实现报告 MUST绑定exact profile/source/binary/dependency/config identity并逐项记录measured value、unit、workload、method和disposition；它 MUST NOT改写B0.2 source corpus、降低MTU/安全开销、改变lane/KCP参数或把B0.6完整fault matrix标为已通过。

#### Scenario: Wire encoded size 超出 profile

- **WHEN**真实secure/raw/KCP envelope使任一登记message超过1200-byte datagram、lane logical payload或1000-byte KCP ceiling
- **THEN**B0.5 implementation report标记失败并定位message/header，不修改profile、不删除安全字段且不提高MTU绕过

#### Scenario: KCP adapter 与冻结参数一致

- **WHEN**exact KCP dependency和adapter对canonical corpus产生与profile一致的segment、deadline、queue与retransmit disposition
- **THEN**独立report可将该adapter identity标记implementation-qualified，但B0.6网络qualification仍保持未完成

#### Scenario: Profile corpus 被实现工具改写

- **WHEN**B0.5 verify前后任一B0.2 manifest/profile/inventory/case/report bytes或digest变化
- **THEN**资格失败且旧implementation report失效，工具不得自动接受新digest
