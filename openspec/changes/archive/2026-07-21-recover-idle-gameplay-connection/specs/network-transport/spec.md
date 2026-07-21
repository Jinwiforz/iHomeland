## ADDED Requirements

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
