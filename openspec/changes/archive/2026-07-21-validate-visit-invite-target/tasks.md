## 1. Account Player 可用性读取

- [x] 1.1 在 Account owner 定义不泄漏账号详情的 `InvitablePlayerReader` 与 available/unavailable 封闭 outcome。
- [x] 1.2 在 MySQL Account repository 实现按唯一 PlayerID 读取 active 状态，并编写 missing、inactive、损坏状态与依赖失败测试用例。

## 2. VisitSession 首次邀请校验

- [x] 2.1 将 Player reader 作为 VisitSession Service 必需依赖，并在 Owner/active-session 授权与 command replay 决议后执行 self/target 可用性校验。
- [x] 2.2 编写 self、missing、inactive、dependency、零 mutation 与目标状态改变后 replay 的 Service 回归测试用例。

## 3. 正式接线与验收准备

- [x] 3.1 在 Composition Root 复用 Account repository 接入 VisitSession，并更新全部 fixture/fake 与真实存储场景。
- [x] 3.2 构建新的 Windows server binary，记录路径与手动验证步骤；不修改 Unity 或 `.meta` 文件。

- [x] 3.3 将 `resolve_invitable_player` 及 available/unavailable outcome 登记到全局 Metrics 封闭词表，避免成功查询触发 panic，并补充词表回归覆盖。
- [x] 3.4 构建包含 Metrics 词表修复的独立 Windows server binary，供用户重启后手动验收。

## 4. 用户验收

- [x] 4.1 由用户使用真实客户端验证 self、伪造 PlayerID 均不创建邀请或推进 revision，合法 Visitor 仍可正常受邀。
