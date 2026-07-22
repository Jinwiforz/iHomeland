## Why

C2 个人世界产品竖切及其四个权威一致性修复已经归档，但当前客户端仍缺少三类发布级闭环：进程退出后只能重新输入账号密码，WSS 与 TLS/TCP 故障只能部分恢复或依赖玩家手工重试，现有 EditMode、PlayMode、Development build 与双客户端记录也尚未收敛为可重复、可审计且 fail-closed 的客户端 v1 资格门。因此第一业务里程碑虽然可以人工演示，仍不能证明 clean install、会话恢复、通道隔离、Release Player 和持续运行在同一冻结契约上长期可靠。

## What Changes

- 新增客户端 v1 资格 manifest、证据目录、单一验证入口和低敏报告，统一覆盖 clean checkout/generation、静态与 Unity 分层测试、跨端 fixtures、Development/Release build、双 Player、服务端/通道故障、生命周期竞态、受控 soak 和清理。
- 为 Windows Player 增加唯一安全会话存储边界：只持久化可轮换 refresh lineage 及必要的低敏 schema/environment binding，使用当前 Windows 用户范围的 OS 保护；access token、ticket、admission、password 和业务投影仍禁止落盘。
- 启动时在任何产品 route 或 world 网络动作前显式尝试恢复持久 session；成功轮换后原子提交新 lineage，失效、损坏、环境不匹配或提交结果未知时 fail closed 并稳定回到 Login，不循环刷新、不使用旧 token。
- 在 App Scope 增加唯一通道恢复协调者。WSS 与 gameplay 分别报告代际化健康状态；WSS 的既有有限重连不重建健康 gameplay，恢复后通过权威 snapshot 补齐断线窗口；gameplay 中断立即关闭旧 target mutation/Scene，并在统一预算内使用新 ticket/admission 恢复 OwnWorld 或以 `RECONNECT` 恢复仍有效的 Visitor membership，失败后才进入可手工重试状态。
- 将恢复 UI 明确区分 `RestoringSession`、`RecoveringControl`、`RecoveringWorld` 与稳定 `ConnectionLost`，所有 automatic/manual recovery 共享同一 intent owner、deadline、generation gate 与 terminal snapshot，不使用 tick、固定延时猜测或并行 socket 修正状态。
- 更新交付顺序，使 C3 只有在全部 mandatory 资格证据、低敏报告、Release Player 与双客户端故障矩阵完成后才可声明第一业务里程碑客户端 qualified；资格门不引入 ActivityInstance、Room、Party、战斗、UDP/KCP 或内容资源系统。

## Capabilities

### New Capabilities

- `client-v1-qualification`: 定义客户端 v1 的版本化资格矩阵、证据分层、单一入口、Development/Release Player、真实双客户端/故障/soak 验收、低敏报告与长期回归门。

### Modified Capabilities

- `client-http-bootstrap`: 增加 Windows 安全 refresh lineage 存储、原子轮换、启动恢复、删除与损坏/提交未知时 fail-closed 的会话契约。
- `client-personal-world-services`: 增加 WSS 与 gameplay 独立健康投影、唯一恢复协调者、OwnWorld/Visitor 权威重建和断线窗口后的完整 snapshot 收敛。
- `client-personal-world-vertical-slice`: 增加启动会话恢复、自动/手工通道恢复的稳定产品状态、Visitor `RECONNECT` 恢复和资格失败下的 UI 行为。
- `delivery-sequencing`: 增加 C3 完整资格门及第一业务里程碑客户端 qualified 的解锁条件。

## Impact

- 主要影响 `client/Assets/App/Scripts/Application`、`Infrastructure`、`Presentation` 与 `Core/Composition`：新增安全存储 port/Windows adapter、恢复协调者和只读恢复 View State；不让 MonoBehaviour、页面或 Scene 成为 token、session 或连接 owner。
- 新增客户端资格 manifest、Editor/PowerShell 单一入口和 `docs/client-v1-qualification.md`；ignored 运行目录只保存临时构建、日志和机器报告，Git 只提交稳定 manifest、测试源、配置与资格摘要。
- Unity 测试与 Windows Player 构建仍由锁定 Editor 执行；资格入口必须生成而不提交 C# protocol、拒绝本机缓存/密钥/绝对路径，并精确清理自己启动的 Player、server 与 storage run。
- Windows 安全存储采用平台 adapter，测试使用受控 fake；其他平台在没有单独 capability 前 fail closed 为“不支持持久恢复”，不得回退到 PlayerPrefs、明文文件、Unity asset 或自制可逆加密。
- 资格运行时间高于普通开发测试，因此提供分层开发入口；只有完整 mandatory 入口可产生 qualified 结论，单次手工联调或一组绿色 Unity tests 不能替代发布资格。
- 公开 schema 只做两项向后兼容扩展：World admission 返回 Visitor 首帧使用的权威 `visitRevision`，`SafeReturnDirective` 携带产生该指令的 VisitSession revision；不新增 token operation、message ID、table 或 Redis key。回滚安全存储与恢复协调者后，客户端回到显式登录/手工恢复基线，但 C3 qualified 状态同时失效。
