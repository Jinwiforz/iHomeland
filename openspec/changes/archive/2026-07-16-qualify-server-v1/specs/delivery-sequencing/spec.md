## MODIFIED Requirements

### Requirement: 服务端 v1 必须具备独立消费者验收
服务端 MUST 交付只依赖公开网络和已提交跨端契约的 Go 协议测试客户端、versioned qualification manifest、contract fixtures、golden packets 与可重复单一资格入口，使账号、会话、HTTP/WSS/TCP、own-world 与 visit-world 流程不依赖 Unity 或服务端内部业务包即可验证。Q0 MUST 同时聚合 contract、unit/integration、fuzz/race、真实storage、独立进程故障、资源背压、shutdown、cleanup与文档一致性证据，生成低敏机器报告，并提交冻结摘要和资格文档；任一 mandatory gate 未通过时 MUST 保持客户端运行时进入门关闭。

#### Scenario: 验证个人世界与访客联机
- **WHEN** Go 客户端完成登录、进入自己的 PersonalWorld、邀请 Visitor、断线重连并结束 VisitSession
- **THEN** 服务端维持单调 revision、唯一 writable WorldInstance、不可转移 Owner、幂等 membership 和明确 safe-return 结果

#### Scenario: 资格客户端导入服务端实现
- **WHEN** Go test client直接导入Composition Root、domain/application、storage、protocol codec或transport adapter来构造请求、预期或恢复状态
- **THEN** architecture gate失败，该结果不能作为独立消费者验收证据

#### Scenario: 部分质量门通过
- **WHEN** happy path或storage integration已通过，但任一mandatory fuzz/race、故障恢复、资源、shutdown、cleanup、contract freeze或report gate缺失/失败
- **THEN** `qualify-server-v1`不得归档，Unity C0仍只能维护文档和契约评审

#### Scenario: 服务端 v1 资格完成
- **WHEN** 独立client与全部mandatory分层矩阵在clean contract基线上重复通过，资格报告、主specs和owner docs已同步并归档
- **THEN** 当前v1 schema、registry、fixtures/golden与endpoint交付集成为C0输入，Unity runtime change可以开始但不得反向修改已冻结服务端语义
