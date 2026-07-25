## ADDED Requirements

### Requirement: Battle contract 必须由统一 source 生成并拥有独立 numeric range

协议source MUST新增`battle/v1` Protobuf package与`battle: 3000-3199` owner range。初始8个logical kind MUST按profile一一登记为`3000-3007`，并在message/route registry中固定owner、direction、UDP或KCP allowed channel、battle auth scope、QoS、max size、rate、idempotency、expiry、tick/sequence、baseline/recovery和assignment/session binding。Go/C#/C++ generated code MUST由统一入口重建且保持忽略；schema、registry、fixture和lock/checksum MUST提交。Unknown message、错误lane/direction、duplicate ID、logical kind漏映射或同一kind跨channel双写 MUST使validator失败。

#### Scenario: Logical kind 未分配 numeric ID

- **WHEN**network profile inventory包含8个kind但battle registry缺少、合并或额外拆分任一映射
- **THEN**contract validation失败且UDP dispatcher不能启动

#### Scenario: Snapshot 同时登记 UDP 与 KCP

- **WHEN**route registry为full/delta snapshot登记多个allowed channel或KCP
- **THEN**validator拒绝，不生成可运行route table

#### Scenario: Payload 声明权威身份或结果

- **WHEN**battle input schema新增可覆盖PlayerID/role/actor/assignment或声明最终hit/damage/death/reward的字段
- **THEN**schema security gate失败且不得通过generated adapter进入simulation

### Requirement: BattleTicket HTTP contract 必须 closed 且独立于既有凭据

OpenAPI MUST新增认证、幂等的BattleTicket issuance operation，request只允许target selector与expected client wire compatibility，response只返回ticket ID/secret、advertised UDP endpoint、wire suite、expiry和client-safe binding。Contract MUST明确BattleTicket不能用于WSS/TLS-TCP、ConnectionTicket不能用于UDP/KCP、WorldAdmission不能替代battle actor admission。Fixtures MUST覆盖own/visit成功、8/9 actor、stale target、wrong epoch、response-loss replay、unknown field和credential redaction。

#### Scenario: Client 提交 endpoint 或 actor slot

- **WHEN**BattleTicket request包含host、port、scope、PlayerID、role、actor slot、assignment或SimulationInstanceID
- **THEN**closed decoder在application前拒绝，不用payload影响endpoint、身份、容量或target解析
