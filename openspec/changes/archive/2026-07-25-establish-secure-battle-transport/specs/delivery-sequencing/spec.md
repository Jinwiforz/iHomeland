## ADDED Requirements

### Requirement: 安全 battle transport 必须先于网络资格与 Unity runtime 独立交付

项目 MUST在B0.4主specs同步归档且连续control qualification通过后，独立完成BattleTicket、numeric registry、wire、cookie/AEAD/replay、raw/KCP listener、capacity、lifecycle和implementation qualification。只有`secure-transport-qualified-windows-x64`报告绑定当前model/profile/control/source digest且全部mandatory gate通过，才可开始`qualify-battle-network`；B0.6通过前 MUST NOT实现Unity battle runtime，B0.7通过前 MUST NOT交付产品combat slice。

#### Scenario: 直接提出 Unity battle runtime

- **WHEN**secure transport仅完成设计或loopback happy path，尚无完整B0.5 qualification report
- **THEN**评审拒绝Unity network/gameplay runtime实现，并要求先完成B0.5及B0.6

#### Scenario: B0.5 report 通过

- **WHEN**BattleTicket、安全wire、真实UDP/KCP、失效与全部既有regression在同一identity下通过
- **THEN**项目只解锁`qualify-battle-network`，不据此声明公网网络或Unity gameplay可发布
