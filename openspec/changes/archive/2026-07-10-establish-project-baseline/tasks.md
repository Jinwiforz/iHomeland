## 1. 项目入口与协作规则

- [x] 1.1 编写 `README.md` 与 `AGENTS.md`，定义项目目标、客户端进入门、第一里程碑和文档入口
- [x] 1.2 更新 `.gitignore`，覆盖嵌套 Unity、Go、本地 IDE 与缓存目录

## 2. 架构与工程文档

- [x] 2.1 编写 `docs/architecture.md` 与 `server/README.md`，定义服务端分层、状态所有权、存储、生命周期和 v1 完成条件
- [x] 2.2 编写 `docs/network-transport-architecture.md` 与 `docs/protocol-compatibility.md`，定义五种通道、统一会话、路由、安全和新 v1 协议治理
- [x] 2.3 编写 `docs/client-architecture.md`、`docs/client-ui-architecture.md`、`docs/client-integration.md` 与 `client/README.md`，定义客户端进入门、混合运行时和双 UI
- [x] 2.4 编写文件结构、工程标准、代码注释、Git 提交、Redis key 与工作流文档，定义目录、质量、协作、数据和 OpenSpec 规则
- [x] 2.5 编写 `docs/roadmap.md`，细化从协议基线到服务端 v1、客户端 v1 和后续 battle 的 changes、进入条件与完成条件

## 3. 长期规格与校验

- [x] 3.1 将 delivery、server、network 和 client delta specs 同步到 `openspec/specs/`
- [x] 3.2 运行 OpenSpec strict、Markdown、失效/过渡措辞、Git 冲突与分支基线检查
- [x] 3.3 确认工作区不包含未规划运行时代码、Unity 资产、生成协议或本地缓存提交项
