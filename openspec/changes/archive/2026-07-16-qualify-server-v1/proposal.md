## Why

PersonalWorld 服务端竖切已经通过同进程 integration harness，但尚缺少只依赖公开 HTTPS/WSS/TLS-TCP 契约的独立 Go 消费者、可重复故障/资源矩阵和可审计资格报告，因此还不能冻结 v1 契约或开放 Unity C0 进入门。Q0 现在需要把既有分层测试收敛为一条可重复、默认清理且 fail-closed 的服务端发布门，而不是继续增加业务功能。

## What Changes

- 在 `server/internal/testclient` 交付独立 Go 协议测试客户端和 scenario runner，只使用公开网络、generated Protobuf 与已提交 contract artifacts，不导入服务端 application/domain/storage/transport 实现。
- 增加版本化资格场景 manifest，覆盖账号/session、own-world、visit-world、断线恢复、safe-return、重放/陈旧资格、Redis/MySQL/process 故障、背压、slow consumer、connection storm 与 graceful shutdown。
- 增加单一资格入口，创建隔离 MySQL/Redis、临时 TLS、动态 loopback endpoints 和真实 `cmd/server` 子进程，执行 contract/unit/integration/fuzz/race/black-box 矩阵并在成功、失败、超时或中断后按 run ownership 清理。
- 冻结 v1 schema、OpenAPI、message/error/route registry、fixtures/golden 与 endpoint manifest 示例的摘要，输出不含凭据和本机路径的资格报告及 Unity 接入交付清单。
- 将 Go 资格客户端确立为长期外部消费者：后续只随新增/变更的公开协议能力扩展分组，永久保留既有回归，不因服务端内部重构而复制或同步业务实现。
- 明确只有资格 manifest 全覆盖、质量门全部通过、无未接线 owner/adapters、机器报告已生成且冻结摘要与资格文档已提交后，`qualify-server-v1` 才完成并允许提出 C0 change。
- 不新增业务 operation、Protobuf 字段、message/error ID、listener、MySQL table、Redis key 或 Unity 运行时代码。

## Capabilities

### New Capabilities

- `server-v1-qualification`: 定义独立协议消费者、资格场景矩阵、故障/资源验收、契约冻结、证据报告和可重复单入口的服务端 v1 发布门。

### Modified Capabilities

- `delivery-sequencing`: 将 Q0 从原则性“Go 客户端验收”收紧为可机器验证的完整矩阵、资格报告与 C0 解锁条件。
- `server-contracts`: 增加 Q0 对 schema、registry、fixtures/golden 和 endpoint manifest 的确定性冻结与摘要漂移拒绝行为。

## Impact

- 新增 `server/internal/testclient`，并以 architecture test 禁止其导入服务端内部业务、storage、transport 或 Composition Root。
- 新增 `shared/contracts/fixtures/qualification/` 场景 manifest、`tools/qualification/` 单一入口和 `docs/server-v1-qualification.md` 资格证据；同步 roadmap、file structure、server/client integration 说明。
- 复用 `tools/go/go.ps1`、`tools/proto/proto.ps1` 与 `tools/storage/storage.ps1` 的锁定工具链和 run ownership；必要的故障动作必须通过 owner 校验入口执行，不允许资格脚本按模糊名称删除 Docker 资源。
- 资格运行会启动真实 Docker storage 和独立服务端进程，耗时高于普通 unit test，但必须有全局 deadline、阶段预算、结构化结果和无 orphan 清理保证。
- 不改变公开协议与持久化 schema；回滚只需移除资格客户端、manifest、入口和报告，既有服务端运行行为保持不变。
