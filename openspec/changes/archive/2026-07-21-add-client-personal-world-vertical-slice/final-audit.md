# 最终跨 Change 审计

## 审计范围

本次审计在归档前覆盖当时全部五个 active change：

- `add-client-personal-world-vertical-slice`
- `recover-idle-gameplay-connection`
- `recover-stale-visit-session-on-open`
- `retire-stale-visit-invites`
- `validate-visit-invite-target`

审计对象包括 change artifacts、对应长期 specs、客户端与服务端实现、协议源与 registry、测试、owner 文档、注释和仓库卫生。审计不归档 change，也不改变已冻结的业务边界。

## 结论

五个 change 的实现与测试均有对应 requirement/scenario，状态所有权仍由 Session、PersonalWorld、VisitSession、WorldAdmission、transport 与 UI router 的既有 owner 分别承担。未发现第二套 session/world/visit 状态机、service locator、全局 event bus、客户端权威事实副本、跨通道双入口、固定延时恢复、Update/tick 状态修正或隐式 mutation 重试。

大体量类型主要集中在 `ClientPersonalWorldExperience`、`ClientGameplayChannel`、`ClientUiRouter`、`VisitSessionService` 与 `WorldAdmissionCoordinator`。它们分别保持单一产品编排、单通道连接 owner、唯一路由事务 owner、VisitSession 投影 owner 和 target flow owner；当前没有稳定且独立的第二职责证据。为避免只按行数拆出空 manager、万能 interface 或新的事实 owner，本轮不做装饰性拆分。后续只有出现可独立验收、独立生命周期或可复用策略时再通过单独 change 提取。

## 已修复问题

1. 五个 change 的 delta specs 先前尚未完整同步至长期 `openspec/specs/`。现已新增 `client-personal-world-vertical-slice` capability，并将 heartbeat、显式恢复、stale Open、邀请退役和目标 Player 可用性要求智能合并到各 owner spec；所有 delta Requirement/Scenario 标题均可在长期 spec 中解析。
2. `docs/client-ui-architecture.md`、`docs/file-structure.md` 与 `docs/roadmap.md` 仍包含“产品页面/资产/双客户端验收未交付”和旧 `Presentation/Models`、`Scenes/Contexts` 目录描述，现已更新为当前实现事实。
3. `server/.local/` 属于本机可重建产物但未被仓库规则覆盖，现已显式加入 `.gitignore`，未删除本机目录。
4. Gameplay 菜单与 UI Cancel 回调曾直接丢弃异步导航任务，其中关闭 route 的异常可能成为未观察任务。现统一通过 Experience 内部观察边界消费正常取消，并把异常映射为低敏失败，不改变权威状态或导航提交语义。

## 注释与安全审计

- 变更和新增 C# 类型/成员的 XML documentation 扫描无缺口；`async void` 仅保留 Unity callback 或事件 adapter，并在边界内观察异常。
- Go 新增导出合同、业务类型和字段均保留 owner/契约注释；`go vet` 与 `gofmt` 通过。
- Proto 新增 message、enum value 与 field 均有中文源注释，generated code 未手改、未跟踪。
- 未发现无上下文 `TODO`、`FIXME`、`HACK`、空 catch、credential/payload 日志、任意资源路径、`Resources`/Addressables、场景全局扫描或本地 PlayerID 代替服务端授权。
- Heartbeat 只使用登记的 TLS/TCP GAMEPLAY request/response、同一 pending/writer 和 generation close path；WSS ping/pong、TCP keepalive 与未来 UDP/KCP 心跳仍保持各自 transport owner。

## 验证结果

- 静态 C# Runtime build：通过，0 error；2 条 `CS9057` 来自 .NET 8 编译器低于 Unity 6 analyzer 编译器版本，不是项目源码告警。
- `tools/go/go.ps1 vet ./...`：通过。
- changed Go `gofmt -l`：无输出。
- `tools/proto/proto.ps1 verify`：9/9 通过，包含格式/lint、兼容性、确定性生成、registry/OpenAPI/fixtures 和服务端全量 Go tests。
- `openspec validate --all --strict`：34/34 通过。
- 五个 change 归档后再次执行 `openspec validate --all --strict`：长期 specs 29/29 通过，active change 列表为空。
- delta/main spec heading parity：全部同步。
- `git diff --check`：通过。
- Unity EditMode/PlayMode、真实双 EXE、服务端中断/恢复、邀请退役、目标 ID 验证和显示输入矩阵沿用本 change 已记录且由操作者确认通过的证据；本轮按要求未重新运行 Unity，也未生成或修改 `.meta`。

## 剩余非阻断风险

- “超过服务端 30 分钟 idle safety 上限的真实静默连接”没有再次人工等待完整窗口。协议/服务端/客户端自动化已覆盖 heartbeat route、deadline、pending-target 拒绝、generation close 与 read deadline 刷新；发布前若需要网络设备级信心，可在 C3 `qualify-client-v1` 中加入一次真实 31 分钟 soak，而不在本 change 引入缩短生产时限或测试专用分支。
- 同一账号多客户端并存属于当前服务端允许的多 Session 语义，不在本轮扩展为 single-session policy；每个客户端只服从自身 session generation，不能把另一 Session 的 UI 当作本地权威事实。

## 归档结果

五个 change 均已完成任务、同步长期规格并通过 strict 验证。四个派生 change 已按依赖边界先行归档，随后归档 `add-client-personal-world-vertical-slice`；归档日期为 2026-07-21，归档后 active change 列表为空。路线图、客户端接入、总体架构、协议治理与 Redis value schema 字典已同步记录派生修复和最终行为。
