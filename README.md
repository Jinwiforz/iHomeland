# iHomeland

iHomeland 是一个面向 PC 的在线游戏项目。当前运行基线由 Go Control/Data Plane 与 Unity 客户端组成；已批准的 gameplay 目标架构将增加独立 C++ Game Simulation Server。核心玩法采用个人持久世界：玩家默认进入自己的 PersonalWorld，并可通过 VisitSession 邀请其他玩家临时访问。

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
  -> C++ 权威 Gameplay：simulation model -> network profile -> simulation core -> 可玩战斗竖切
```

Go 服务端 v1 与 Unity 客户端 v1 均已通过资格验收；这些门禁仍是后续变更必须保持的回归基线。Gameplay 的严格顺序与每个 change 的进入/完成条件只由 `docs/roadmap.md` 管理。

## 第一业务里程碑

第一阶段已经交付并完成资格验收：可观测 Go 服务端、账号与统一会话、PersonalWorld、WorldInstance、VisitSession、MySQL/Redis、HTTPS/WSS/TLS-TCP、Go 协议测试客户端，以及 Unity own-world/visit-world 客户端。ActivityInstance、Room、Party、匹配、正式战斗、Game Simulation Server、UDP/KCP、观战、回放和完整经济系统不属于该已冻结里程碑。

## 顶层目录（当前与已批准目标）

```text
client/      已落地的 Unity 客户端版本入口
docs/        架构、协议、工程标准、路线和流程
openspec/    长期规格与变更 artifacts
server/      已落地的 Go Control/Data Plane 版本入口
simulation/  已落地的无网络 C++20 Game Simulation Core、adapters 与资格工具
shared/      跨端协议与契约
tools/       生成、测试、构建和运维工具
versions.yaml 协议、工具链、语言、基础设施与 Unity 技术版本目录
```

## 必读文档

- `AGENTS.md`：协作与质量硬规则
- `docs/architecture.md`：总体架构与状态所有权
- `docs/gameplay-simulation-architecture.md`：权威状态同步、帧同步技术、ECS/GAS-like 与模拟边界
- `docs/roadmap.md`：严格 change 路线与进入条件
- `docs/network-transport-architecture.md`：五种通道与统一会话
- `docs/network-port-allocation.md`：默认端口、环境覆盖与冲突处理
- `docs/protocol-compatibility.md`：新 v1 协议治理
- `docs/client-architecture.md`：Unity 混合运行时
- `docs/client-ui-architecture.md`：UI Toolkit/uGUI 规则
- `docs/client-integration.md`：服务端交付包与 Unity 接入验收
- `docs/server-v1-qualification.md`：Q0 唯一入口、冻结摘要与长期 Go 资格客户端边界
- `docs/client-v1-qualification.md`：C3 唯一入口、冻结摘要与 Unity 资格结论
- `docs/file-structure.md`：目标目录与文件职责
- `docs/engineering-standards.md`：工程与测试标准
- `docs/code-comment-convention.md`：Go、C++、C#/Unity 与协议注释规范
- `docs/workflow.md`：OpenSpec 与交付流程
- `docs/git-commit-convention.md`：Git 提交消息与提交粒度
- `docs/redis-keys.md`：Redis owner、TTL 和恢复规则
- `docs/technology-versions.md`：集中版本目录与升级规则
- `openspec/specs/`：长期行为契约

## C++ 本地环境与换机恢复

新 Windows 电脑只需拉取仓库后运行：

```powershell
& .\tools\cpp\cpp.ps1 bootstrap
& .\tools\cpp\cpp.ps1 verify
```

`bootstrap` 会按 `versions.yaml` 自动检测并恢复 `.local/cpp/` 下的精确 CMake、
Jolt、Detour、JSON 与项目指定 Build Tools；缺少 MSVC/Windows SDK 时使用已锁定且
验签的 Microsoft installer 安装。`.local/`、`simulation/out/` 和
`simulation/reports/` 都是可重建的本机缓存/证据，不随 Git 复制；换电脑时由入口
重新检测和下载。Build Tools 的英文 UI resource 也由 `bootstrap` 检测，构建入口
据此统一 MSVC 日志编码，用户无需修改 Windows Terminal 或系统区域设置。详细边界见
`docs/technology-versions.md`。
