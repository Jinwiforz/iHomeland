## ADDED Requirements

### Requirement: Battle UDP/KCP 必须共享认证 listener 和安全 session

Game Simulation MUST使用一个由C++ owner管理的UDP listener承载raw、KCP和transport-control，并使用BattleTicket、stateless cookie、AEAD、key epoch/nonce、replay window、endpoint generation、per-IP/session/message/instance rate limit和有界queue保护。KCP MUST只提供已登记message的有限ARQ，不能替代认证、加密、业务expiry或在UDP受阻时回退TCP。Bind endpoint与advertised endpoint MUST显式配置并由ticket绑定；客户端不得硬编码production port。

#### Scenario: Raw 与 KCP 使用不同 socket

- **WHEN**配置或实现为同一SimulationNode分别创建raw UDP与KCP listener
- **THEN**配置/architecture gate失败，ticket不能下发多个隐式endpoint

#### Scenario: UDP 被阻断

- **WHEN**客户端无法建立或维持BattleSession
- **THEN**battle按登记策略暂停、重试ticket或退出，不把同一input/snapshot/event静默改发TLS/TCP或WSS

### Requirement: Battle endpoint rebind 必须重新验证地址但保持会话连续

Active BattleSession MAY在持有current traffic key时请求endpoint rebind；server MUST对新IP/port执行stateless cookie challenge和authenticated confirm，成功后递增endpoint generation并拒绝旧endpoint。Rebind MUST不重置packet sequence、replay window、KCP conversation、key epoch、actor或assignment binding；失败、超时、并发冲突和rate limit MUST保持原endpoint或关闭session，不接受双active endpoint。

#### Scenario: 新 endpoint 只有 cookie 没有 session proof

- **WHEN**remote endpoint能取得cookie但不能生成current session authenticated confirm
- **THEN**server不改变binding、不发送放大响应且不泄漏session或actor是否存在
