## Why

iHomeland 需要建立可长期维护的在线游戏架构，避免在服务端、协议和客户端同时变化时形成循环依赖。交付以冻结契约为边界：先由独立测试客户端完成服务端 v1 资格验收，再按该契约实现 Unity 客户端。

## What Changes

- 建立独立的服务端、协议、数据和 Unity 目标基线，所有实现只服从本 change 产出的规格与路线。
- 建立严格的客户端进入门：协议、会话、PersonalWorld、WorldInstance、VisitSession、存储、HTTPS、WSS、TLS/TCP 和自动化验收完成后，才能开始 Unity 实现。
- 定义 Go 服务端的 Composition Root、application/domain/infrastructure 分层、状态所有权、存储边界、可观测和生命周期规则。
- 定义 HTTPS、WSS、TLS/TCP、裸 UDP 和 KCP 的唯一职责、统一会话、消息路由、安全和故障语义。
- 定义 Unity 混合架构：Composition Root、App Scope、Scene Scope、纯 C# Service、Unity Host、UI Toolkit/uGUI 和场景职责。
- 建立项目文档体系、目录规划、工程标准、协议治理、Redis key 规则、路线图和 OpenSpec 工作流。
- 第一阶段只交付账号、会话、个人持久世界与访客联机；Room、Party、ActivityInstance 和 UDP/KCP 必须等待各自产品模型与进入条件。

## Capabilities

### New Capabilities

- `delivery-sequencing`：规定项目基线、契约依赖顺序、服务端 v1 完成门槛和客户端进入条件。
- `server-architecture`：规定 Go 服务端组合、分层、领域、存储、生命周期、测试和可观测边界。
- `network-transport`：规定 HTTP、WebSocket、TCP、UDP、KCP 的职责、路由、会话、安全和故障行为。
- `client-runtime`：规定 Unity Composition Root、App/Scene Scope、双 UI、状态所有权和客户端接入顺序。

### Modified Capabilities

无。

## Impact

- 仓库：建立只包含架构、规格、文档与版本占位的项目基线。
- 服务端：未来以 Go 单进程逻辑分层起步，使用 Gin、Protobuf、MySQL、Redis 和按需 transport adapter。
- 客户端：未来在服务端 v1 冻结后创建 Unity 工程与运行时代码，采用 UI Toolkit/uGUI 混合呈现。
- 协议：采用 v1 schema、message registry、错误目录和 contract fixtures。
- 数据：采用 MySQL migration baseline 和 Redis namespace。
- 本 change 只创建架构、规格和文档，不实现服务端、客户端、协议生成物、场景或 prefab。
