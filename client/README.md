# iHomeland Unity Client

`client/` 是 Unity PC 客户端入口。服务端 v1 已通过 `qualify-server-v1` 资格门并冻结跨端契约；客户端从 `docs/roadmap.md` 的 `establish-client-runtime` 开始按 change 实现，不能在未立项状态下提前创建 runtime、scene、prefab 或 generated protocol。服务端交付物与接入验收由 `docs/client-integration.md` 定义，运行时与 UI 规则分别由客户端架构文档定义。

服务端的 `server/internal/testclient` 会在 Q0 后长期保留，但它只是公开协议自动化考官，不提供 Scene、UI、输入、表现或产品状态，不替代本目录中的 Unity 客户端。

## 相关文档

- `../docs/client-architecture.md`
- `../docs/client-ui-architecture.md`
- `../docs/client-integration.md`
- `../docs/server-v1-qualification.md`
- `../docs/network-transport-architecture.md`
- `../docs/protocol-compatibility.md`
