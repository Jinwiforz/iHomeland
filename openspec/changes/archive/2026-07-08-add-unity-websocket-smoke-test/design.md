## Context

`NetworkSystem` 已经能发送二进制 Protobuf envelope，并通过统一请求通道完成心跳、注册、登录、登出和会话恢复。完整房间大厅接入前，开发者仍需要一个不依赖 UI 点击顺序的最小联调入口，快速判断本地服务端和 Unity 生成协议是否匹配。

## Goals / Non-Goals

**Goals:**

- 提供 Unity Editor 菜单入口，验证本地 WebSocket 连接、心跳和账号请求。
- 复用共享 Protobuf 生成代码和 `AppConfig.WebSocketURL`。
- 让测试账号可重复执行，避免每次运行都污染 MySQL 账号表。
- 在 smoke test 结束时尽量登出并关闭 WebSocket。

**Non-Goals:**

- 不接入房间大厅请求；该范围由 `add-unity-room-lobby-flow` 承接。
- 不改变运行时 UI 流程，不把 `Start Game` 改成房间大厅入口。
- 不新增服务端测试账号管理 API 或清库脚本。
- 不手工修改生成的 Protobuf 代码。

## Decisions

### 使用 Unity Editor 菜单而不是运行时隐藏按钮

smoke test 面向开发联调，不属于玩家运行时功能。将入口放在 `iHomeland/Smoke Test/WebSocket Account` 菜单下，可以避免把诊断按钮混入游戏 UI，也不需要改动现有场景和 prefab。

替代方案是在 `HomePage` 放一个临时按钮。该方案会污染第一里程碑 UI，并容易在后续流程中被误认为正式功能。

### 独立 Editor assembly 引用运行时 App assembly

新增 `App.Editor.asmdef`，仅包含 Editor 平台，并引用运行时 `App` assembly。这样可以安全使用 `UnityEditor.MenuItem`，同时复用 `AppConfig` 和生成的 Protobuf 类型，不让 `UnityEditor` 进入运行时代码。

### 使用固定测试账号并对已存在账号回退登录

测试账号使用固定值 `unity_smoke_test`。首次执行会注册账号，后续执行遇到 `ACCOUNT_ALREADY_EXISTS` 时直接登录。这样 smoke test 既覆盖注册路径，也能重复运行而不持续创建账号。

替代方案是每次生成随机账号。该方案可以避免账号冲突，但会在本地 MySQL 中留下大量测试账号，不适合作为常用联调入口。

### 直接使用 ClientWebSocket 构造最小协议请求

Editor 菜单不要求进入 Play Mode，也不依赖 `AppRoot` 生命周期。工具直接使用 `ClientWebSocket` 和 Protobuf 生成类型构造 envelope，验证的重点是本地服务端、二进制 WebSocket 和共享协议生成结果。运行时 `NetworkSystem` 的 UI 集成已由 `add-account-session` 覆盖，后续房间 UI change 再扩展运行时路径。

## Risks / Trade-offs

- [Risk] smoke test 不能完全覆盖 `NetworkSystem` 的生命周期逻辑。→ Mitigation：本 change 只承诺最小联调入口；运行时网络系统仍通过手动 Unity 验证和后续房间 flow 验证。
- [Risk] 固定测试账号密码被改动后登录会失败。→ Mitigation：账号名和密码写在工具常量中，不提供外部可变配置；失败时输出服务端错误码和 detail。
- [Risk] 本地服务端或 MySQL/Redis 未启动时测试失败。→ Mitigation：文档明确前置条件，并保留服务端 `verify-local.bat` 作为基础检查。

## Migration Plan

1. 新增 OpenSpec delta 和任务。
2. 新增 Unity Editor smoke test assembly 与菜单工具。
3. 更新 Unity 接入文档中的本地验收路径。
4. 运行服务端测试与 OpenSpec 校验；Unity 菜单执行需要本地编辑器和服务端联调环境。
