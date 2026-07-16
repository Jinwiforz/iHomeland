## ADDED Requirements

### Requirement: 服务端 v1 契约必须由 Q0 确定性冻结
`qualify-server-v1` MUST 将当前 versioned Protobuf schema、HTTP OpenAPI、message/error/route registry、qualification scenario/evidence manifests、contract fixtures/golden和endpoint manifest示例定义为排序后的冻结输入集，并从已提交原始bytes计算稳定aggregate digest。Digest record、generated code、descriptor、运行报告与本机配置 MUST NOT参与输入。Contract test MUST在clean checkout重新计算相同digest并拒绝未评审漂移；digest只证明交付集完整性，Git、OpenSpec和各owner source仍是唯一语义事实。

#### Scenario: Clean checkout 重算冻结摘要
- **WHEN** 相同提交从无generated code和本机缓存的checkout执行contract freeze验证
- **THEN** 输入文件集合、逐文件bytes与aggregate digest和资格基线一致，不需要持久化descriptor或runtime projection

#### Scenario: Registry 或 fixture 静默变化
- **WHEN** schema、OpenAPI、message/error/route registry、qualification manifest或既有fixture/golden任一内容改变但未更新资格基线并重跑Q0
- **THEN** contract gate因digest漂移失败，C0不能继续使用旧qualified声明

#### Scenario: Q0 后需要改变基础契约
- **WHEN** 已冻结v1的field、operation、message/error ID、route、credential、endpoint或恢复语义需要不兼容修改
- **THEN** 项目必须先创建独立OpenSpec，采用新版本或明确兼容窗口、更新fixtures与迁移说明并重新执行服务端资格，不能直接覆盖原冻结含义
