## ADDED Requirements

### Requirement: 通道必须按交付语义分工
系统 MUST 根据可靠性、时效性、顺序和安全需求选择 HTTPS、WSS、TLS/TCP、裸 UDP 或 KCP，不得仅为了使用技术而分配业务。

#### Scenario: 新增消息
- **WHEN** 项目设计新的客户端与服务端消息
- **THEN** 设计先声明交付语义，再登记唯一 allowed channel

### Requirement: HTTPS 必须承载启动与账号面
HTTPS MUST 承载版本、配置、账号、token、connection ticket 和 endpoint manifest，不得承载个人世界实时状态或高频战斗同步。

#### Scenario: 客户端登录
- **WHEN** 客户端提交账号凭据
- **THEN** 它通过 HTTPS 获取账号 session 或结构化错误，不依赖实时连接

### Requirement: WSS 必须承载带外控制面
WSS MUST 承载维护、强制下线、排队和 endpoint 更新等控制通知，不得作为普通权威 gameplay command 的入口。

#### Scenario: Gameplay 命令从 WSS 到达
- **WHEN** 客户端通过 WSS 提交只允许 TLS/TCP 的个人世界 command
- **THEN** 服务端拒绝消息且不得修改业务状态

### Requirement: TLS/TCP 必须承载权威可靠业务
TLS/TCP MUST 承载个人世界、访客会话、聊天和其他晚到仍有意义的可靠业务，并使用长度 framing、大小限制、deadline 和 backpressure。

#### Scenario: TCP 发生拆包或粘包
- **WHEN** 单次 socket read 包含半帧或多个连续帧
- **THEN** framing 层正确重组消息且不把 read 边界当作消息边界

### Requirement: 裸 UDP 必须只承载可丢弃时序数据
裸 UDP MUST 只承载晚到不如不到、可被新数据覆盖的探测、连续输入或状态快照，不得承载账号、资产、奖励或结算。

#### Scenario: 旧快照晚到
- **WHEN** 客户端已经处理更高 sequence 的状态快照
- **THEN** 客户端丢弃晚到旧快照并继续使用最新状态

### Requirement: KCP 必须只承载晚到仍有意义的可靠战斗数据
KCP MUST 只承载丢失不可接受且重传后仍有价值的战斗数据；应用层仍必须校验 tick、序列、过期和合法性。

#### Scenario: 可靠帧命令重传到达
- **WHEN** KCP 交付一个仍在允许 tick 窗口内的缺失帧命令
- **THEN** 服务端去重并按战斗规则接纳，超出窗口则拒绝执行

### Requirement: 实时消息必须具有唯一路由
每个实时 message id MUST 登记 owner、direction、allowed channel、auth scope、QoS、max size、rate limit 和 idempotency；服务端必须拒绝错误通道消息。

#### Scenario: 同一消息尝试双通道提交
- **WHEN** 某业务消息已登记为 TLS/TCP
- **THEN** WSS 和其他通道不得接受同一 message id 的业务执行

### Requirement: 多通道必须共享统一会话
WSS、TCP、UDP 和 KCP MUST 绑定同一 session id 与 session epoch，payload 身份不得覆盖连接身份。

#### Scenario: session epoch 失效
- **WHEN** 玩家登出、被强制下线或 session epoch 更新
- **THEN** 所有绑定旧 epoch 的连接与 ticket 都失去授权

### Requirement: UDP 与 KCP 必须同时交付安全能力
UDP/KCP 首次启用时 MUST 同时提供短期 ticket、cookie challenge、AEAD、重放保护、速率限制、抗放大和 endpoint 校验。

#### Scenario: 重放数据包
- **WHEN** 服务端收到重放窗口内已处理的 sequence
- **THEN** 服务端丢弃数据且不重复执行业务语义
