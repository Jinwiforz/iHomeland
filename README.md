# iHomeland

iHomeland 是一个面向 PC 的在线游戏项目，使用 Go 服务端与 Unity 客户端。核心玩法采用个人持久世界：玩家默认进入自己的 PersonalWorld，并可通过 VisitSession 邀请其他玩家临时访问。

## 交付原则

项目按可验证的契约依赖顺序交付：

```text
基础协议治理、Server Runtime、Session、Account
  -> PersonalWorld Domain 与 WorldInstance Placement
  -> MySQL / Redis Runtime 与领域 Adapters
  -> VisitSession Domain
  -> PersonalWorld / VisitSession Protocol
  -> HTTPS / WSS / TLS-TCP 与 Go 客户端资格验收
  -> Unity Composition Root 与 Network Services
  -> Own-world / Visit-world 双 UI vertical slice
```

服务端必须先通过 Go 协议测试客户端完成资格验收并冻结跨端契约，达到该门槛后才开始 Unity 运行时代码。严格顺序与每个 change 的完成条件见 `docs/roadmap.md`。

## 第一业务里程碑

第一阶段交付可观测 Go 服务端、账号与统一会话、PersonalWorld、WorldInstance、VisitSession、MySQL/Redis、HTTPS/WSS/TLS-TCP、Go 协议测试客户端，以及契约冻结后的 Unity own-world/visit-world 客户端。ActivityInstance、Room、Party、匹配、正式战斗、battle server、UDP/KCP、观战、回放和完整经济系统不在第一阶段范围内。

## 顶层目录

```text
client/      Unity 客户端版本入口；服务端 v1 冻结后创建工程
docs/        架构、协议、工程标准、路线和流程
openspec/    长期规格与变更 artifacts
server/      Go 服务端版本入口；实现从服务端阶段开始
shared/      跨端协议与契约
tools/       生成、测试、构建和运维工具
versions.yaml 协议、工具链、语言、基础设施与 Unity 技术版本目录
```

## 必读文档

- `AGENTS.md`：协作与质量硬规则
- `docs/architecture.md`：总体架构与状态所有权
- `docs/roadmap.md`：严格 change 路线与进入条件
- `docs/network-transport-architecture.md`：五种通道与统一会话
- `docs/protocol-compatibility.md`：新 v1 协议治理
- `docs/client-architecture.md`：Unity 混合运行时
- `docs/client-ui-architecture.md`：UI Toolkit/uGUI 规则
- `docs/client-integration.md`：服务端交付包与 Unity 接入验收
- `docs/file-structure.md`：目标目录与文件职责
- `docs/engineering-standards.md`：工程与测试标准
- `docs/code-comment-convention.md`：Go、C#/Unity 与协议注释规范
- `docs/workflow.md`：OpenSpec 与交付流程
- `docs/git-commit-convention.md`：Git 提交消息与提交粒度
- `docs/redis-keys.md`：Redis owner、TTL 和恢复规则
- `docs/technology-versions.md`：集中版本目录与升级规则
- `openspec/specs/`：长期行为契约
