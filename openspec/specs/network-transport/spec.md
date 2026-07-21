# Network Transport 规格

## Purpose

定义 HTTPS、WSS、TLS/TCP、裸 UDP 与 KCP 的职责、唯一消息路由、统一账号会话、安全边界、故障语义、可观测指标和按业务里程碑启用的验收行为。

## Requirements

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

### Requirement: TLS/TCP gameplay 必须使用独立 heartbeat 证明静默连接存活

系统 MUST 仅在 `TLS_TCP/GAMEPLAY` 通道登记 common owner 的 `GAMEPLAY_HEARTBEAT_REQUEST` 与 `GAMEPLAY_HEARTBEAT_RESPONSE`。heartbeat MUST 使用可靠有序 request/response correlation、空业务 payload、独立低成本 rate policy 与有界 response deadline；它 MUST NOT 读取或修改 PersonalWorld、VisitSession、PlayerState、revision、membership 或 settlement。WSS ping/pong、OS TCP keepalive 与业务 snapshot request MUST NOT 代替该 heartbeat。

#### Scenario: Active 玩家长期没有业务操作

- **WHEN** 已认证 active gameplay connection 连续多个 heartbeat interval 没有业务 request、command 或 push
- **THEN** 客户端仍按唯一 gameplay route 发送最多一个 pending heartbeat，服务端返回精确 correlation response，连接不会仅因缺少业务消息触发 idle timeout

#### Scenario: Heartbeat response 超过 deadline

- **WHEN** heartbeat request 已写入连接但在冻结 deadline 内没有收到匹配 response 或 error
- **THEN** 客户端以稳定 heartbeat/transport failure 关闭 current generation，完成全部 pending，并进入受控断线恢复状态而不自动重发 credential 或业务 mutation

#### Scenario: 在错误通道发送 heartbeat

- **WHEN** peer 在 WSS、HTTP 或非 GAMEPLAY auth scope 提交 heartbeat message ID
- **THEN** route registry 在 application service 前拒绝该消息，且系统不提供跨通道兼容入口
