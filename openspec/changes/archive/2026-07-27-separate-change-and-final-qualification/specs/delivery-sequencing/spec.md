## MODIFIED Requirements

### Requirement: 安全 battle transport 必须先于网络资格与 Unity runtime 独立交付

项目 MUST 在 B0.4 主 specs 同步归档且 control contract 通过后，独立完成 BattleTicket、
numeric registry、wire、cookie/AEAD/replay、raw/KCP listener、capacity、lifecycle 和
implementation correctness。只有 public credential、真实 production listener、
independent protocol client、定向安全负例、current mapping/assignment fencing 和代表性
clean/loss/reconnect 网络 smoke 在 current model/profile/control identity 上通过，才可
开始 Unity battle runtime。完整 12-scenario、1/5/8 capacity、安全/lifecycle、连续
verify、长时 soak 与 finalize MUST 保留给用户显式冻结的最终资格 candidate，不得成为每个
后续功能 change 的默认进入或完成条件；Unity gameplay vertical slice 通过前仍不得声明
产品 combat 或公网网络 qualified。

#### Scenario: 直接提出 Unity battle runtime

- **WHEN** secure transport 只有设计、内部对象 happy path，公开 BattleTicket 到 production UDP listener 的 input/snapshot 路径或代表性网络 smoke 尚未通过
- **THEN** 评审拒绝 Unity network/gameplay runtime 实现，并要求先完成安全 transport 与 network development-readiness 定向证据

#### Scenario: Development-readiness 通过

- **WHEN** 当前 secure transport 的公开 handshake、raw/KCP、input/snapshot、generation fencing、rebind、安全基本负例和代表性 clean/loss/reconnect smoke 通过
- **THEN** 项目可以开始 Unity gameplay runtime，但不得把未执行的完整 capacity/security/lifecycle/soak 标记为最终资格

## REMOVED Requirements

### Requirement: 完整 B0.6 网络资格必须先于 Unity gameplay runtime

**Reason**: 该要求把工具建立、开发就绪与发布级完整资格绑定为同一门，导致任何后续协议或
实现 change 重复执行历史 clean qualification、完整矩阵、连续 verify 和 soak，且在真实
Unity/gameplay workload 尚未存在时生成会被后续重构立即失效的最终 evidence。

**Migration**: 使用新的 project-validation capability。Unity gameplay runtime 的进入门
改为 current public transport 与代表性网络 development-readiness；原完整 B0.6 fault、
capacity、security、lifecycle、两次 verify、soak 和 finalize 原样保留，由统一工具只在
用户显式冻结最终 candidate 时执行。

## ADDED Requirements

### Requirement: 最终产品资格必须独立于历史 change 编号

项目 MUST 将 B0.3 至未来功能 changes 的 unit、contract、parity、sanitizer、integration
和 system tests维护为当前 capability suites。用户显式冻结里程碑或发布 candidate 时，
最终资格 MUST 构建一次当前产品并运行这些 current mandatory suites；MUST NOT 为同一
candidate 按历史 change 编号重复构建、逐层 finalize 或要求编号最大的 change 单独替代
完整产品验收。

#### Scenario: 长期路线推进到后续 change

- **WHEN** 项目已完成多个后续功能 change 并由用户请求完整最终验收
- **THEN** 统一工具对当前产品 capability manifest 执行一次完整资格链，历史 B0.x reports 只作为迁移与审计记录，不形成必须逐个重签的线性构建链

#### Scenario: 只验收最后一个新增功能

- **WHEN** 最后一个 change 的定向 tests 通过但当前产品 mandatory capability suites 尚未执行
- **THEN** 该 change 可以完成开发验收，但不能据此生成完整产品 qualified 结论
