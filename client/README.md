# iHomeland Unity Client

`client/` 是 Unity PC 客户端入口。服务端 v1 已通过 `qualify-server-v1` 资格门并冻结跨端契约；客户端从 `docs/roadmap.md` 的 `establish-client-runtime` 开始按 change 实现，不能在未立项状态下提前创建 runtime、scene、prefab 或 generated protocol。服务端交付物与接入验收由 `docs/client-integration.md` 定义，运行时与 UI 规则分别由客户端架构文档定义。

服务端的 `server/internal/testclient` 会在 Q0 后长期保留，但它只是公开协议自动化考官，不提供 Scene、UI、输入、表现或产品状态，不替代本目录中的 Unity 客户端。

## 相关文档

- `../docs/client-architecture.md`
- `../docs/client-ui-architecture.md`
- `../docs/client-integration.md`
- `../docs/server-v1-qualification.md`
- `../docs/client-v1-qualification.md`
- `../docs/network-transport-architecture.md`
- `../docs/protocol-compatibility.md`

C3资格由仓库根 `tools/client-qualification/client-qualification.ps1` 唯一编排。缺陷反馈可使用其`diagnose -Scenario`动作定向运行登记fixture并只构建Development Player；该动作固定输出非证据清单，不能替代完整资格。Editor/Development中的 `Assets/App/Modules/AppShell/Runtime/Qualification/` 只提供低敏计数、受控transport故障和run内secure-store cleanup；普通启动零副作用，Release不得包含这些入口。
