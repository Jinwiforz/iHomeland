## Context

iHomeland 是由 Go 在线游戏服务端与 Unity PC 客户端组成的项目。第一里程碑是个人持久世界与邀请式访客联机，长期通信技术包括 HTTPS、WSS、TLS/TCP、裸 UDP 和 KCP。为避免服务端与客户端同时试错造成协议反复变化，项目先通过独立测试客户端稳定并验证服务端契约，再由 Unity 按冻结契约接入。

## Goals / Non-Goals

**Goals:**

- 建立从协议到服务端验收、再到客户端实现的严格依赖顺序。
- 让业务领域、存储和 transport adapter 具有清晰所有权。
- 让每种通信技术只承担符合其交付语义的职责。
- 让服务端在没有 Unity 客户端时也能通过自动化完成 v1 验收。
- 让 Unity 使用 Composition Root、App Scope、Scene Scope 和双 UI 的混合架构。
- 为未来 battle、UDP/KCP 和服务拆分设置客观进入条件。

**Non-Goals:**

- 本 change 不实现服务端、客户端、协议生成物、数据库、场景或 prefab。
- 第一里程碑不实现正式战斗、匹配、观战、回放或完整经济系统。
- 不提前创建 UDP/KCP listener、battle server 或微服务占位。
- 不规定所有未来玩法、战斗 tick rate 或部署容量。
- 不引入第三方依赖注入容器作为架构前提。

## Decisions

### 1. 以服务端资格验收作为客户端进入门

实现顺序固定为：基础协议治理、服务端运行基础、会话安全、账号、PersonalWorld、WorldInstance placement、存储、VisitSession、world/visit 协议、HTTPS/WSS/TLS-TCP、服务端资格验收、Unity 运行时、Unity 通道、业务 UI。

Unity 代码只能在 `qualify-server-v1` 完成后开始。替代方案是双端同步开发，但它会让 schema、错误语义、session 和重连行为在两个代码库中同时震荡，因此拒绝。

### 2. 服务端以单进程逻辑分层起步

第一阶段由一个 Go 二进制组装 HTTP、WSS、TCP、application service、domain、MySQL 和 Redis adapter。逻辑边界清晰但不提前拆进程；只有独立扩缩容、故障隔离、区域部署或团队 ownership 出现后才评估 gRPC 和服务拆分。

目标依赖方向：

```text
cmd/server
  -> Composition Root
      -> Transport Adapters
          -> Application Services
              -> Domain
              -> Repository Interfaces
                  -> MySQL / Redis Adapters
```

transport 只能 decode、validate、authorize、调用 application service 和 encode，不能持有业务状态机。

### 3. 协议先于 listener 冻结

新 v1 先定义 schema、message id、错误码、route registry、session/ticket、framing、大小限制和 contract fixtures，再实现 listener。每个实时消息必须登记 owner、direction、allowed channel、auth scope、QoS、max size、rate limit 和 idempotency。

协议冻结后按正常版本治理演进，不依赖实现类或 socket API。

### 4. 按交付语义划分五种通道

| 通道 | 职责 | 排除 |
|---|---|---|
| HTTPS | 版本、配置、账号、token、ticket、endpoint manifest | 世界实时状态、战斗同步 |
| WSS | 维护、强制下线、排队、endpoint 更新等带外控制 | 普通 gameplay command、资产变更 |
| TLS/TCP | 个人世界、访客会话、聊天、任务等权威可靠业务 | 高频连续输入和可丢快照 |
| 裸 UDP | 探测、晚到无价值且可被新数据覆盖的时序数据 | 账号、资产、奖励、结算 |
| KCP | 丢失不可接受且晚到仍有意义的低延迟战斗数据 | 普通大厅与账号业务 |

同一 message id 只有一个合法业务通道，不做 WSS/TCP 双写。裸 UDP 与 KCP 默认共享一个受控 UDP session。

### 5. 多通道共享唯一会话事实

HTTPS 签发 access token 和短期一次性 connection ticket。WSS、TCP、UDP/KCP 绑定同一 `session_id`、`session_epoch` 与授权 scope。payload 中的玩家标识不能覆盖连接身份；epoch 失效必须使全部旧连接和 ticket 失效。

### 6. 服务端测试客户端代替早期 Unity

服务端每个 transport vertical slice 都交付 Go 协议测试客户端、golden packet 和 contract fixture。`qualify-server-v1` 必须覆盖 unit、integration、contract、race、恢复、并发、背压、慢消费者、连接风暴和 graceful shutdown。

独立协议测试客户端使早期服务端交付仍有真实消费者反馈，Unity 接入时也无需定义第二套基础契约。

### 7. MySQL 与 Redis 职责分离

MySQL 保存账号、Player、PersonalWorld 和需要恢复的持久事实；Redis 保存 session、presence、WorldInstance assignment/lease、VisitSession、admission 和 rate limit 等可恢复运行态。业务只依赖 repository/cache interface，不能直接依赖具体 client。

每个表和 key 必须有 owner、生命周期、恢复来源和失败策略。Redis 清空不得造成持久事实丢失。

### 8. Unity 采用混合运行时架构

客户端使用：

```text
BootstrapScene
  AppBootstrap
    -> AppComposition
        -> App Scope
            AppRoot / Services / Unity Hosts
        -> Scene Flow
            Scene Scope / SceneContext
```

不依赖 Unity 生命周期的账号、PersonalWorld、VisitSession、网络状态和业务规则使用纯 C# Service；Coroutine、主线程、UI、Audio 和场景引用由薄 Unity Host 承担。Scene/Prefab 管内容表现，ScriptableObject 管配置和共享定义，不保存在线业务最终事实。

### 9. UI Toolkit 与 uGUI 按页面能力选型

UI Toolkit 优先用于列表、表单、筛选和样式复用明显的屏幕页面；uGUI 优先用于世界空间、场景绑定、材质动画和战斗 HUD。一个逻辑 screen 只有一个 active owner，不为展示技术栈而强制混搭。

简单 view 可以读取业务 Service 的只读状态；只有复杂共享投影才引入 Presenter/View State。

### 10. battle 技术在玩法模型后进入

UDP/KCP 必须等待 battle simulation model、tick rate、人数、权威边界、延迟和带宽预算明确。先实现 transport 占位会产生无法验收的安全与拥塞参数，因此拒绝。

## Risks / Trade-offs

- [客户端反馈出现较晚] -> 服务端以协议测试客户端和 versioned fixtures 提供持续消费者验证。
- [服务端阶段范围过大] -> 每个职责拆成独立 OpenSpec change，并以第一阶段完成门限制范围。
- [五种通道增加运维成本] -> 按里程碑启用，未达到语义与验收条件的通道不创建。
- [单进程可能限制未来扩容] -> 保持逻辑接口和状态 owner，指标证明需要后再物理拆分。
- [过度抽象降低交付速度] -> 只为真实替换、测试和边界建立接口，不创建空 manager/adapter。
- [双 UI 增加维护成本] -> 页面级唯一 owner、共享设计语义和混合场景验收。

## Delivery Plan

1. 归档本架构 change，冻结长期规则。
2. 按 `docs/roadmap.md` 从 `establish-server-contracts` 开始逐个创建实现 change。
3. 完成 `qualify-server-v1` 前不创建 Unity 运行时代码。
4. 服务端 v1 通过后，从 `establish-client-runtime` 开始客户端实现。
5. 第一阶段双端 v1 完成后再进入 battle model 与 UDP/KCP。

## Open Questions

- 第一阶段账号凭据采用自有账号密码还是外部身份提供商，需要在 account change 中确定。
- WSS 与 HTTPS 是否同端口托管，需要结合部署入口和证书管理决定。
- TCP payload 是否统一使用 Protobuf envelope，需在协议基线 change 中结合压缩与最大帧预算确认。
- PersonalWorld 持久 revision、WorldInstance lease/fencing 与 VisitSession 恢复边界，需要在各 owner change 中确定。
