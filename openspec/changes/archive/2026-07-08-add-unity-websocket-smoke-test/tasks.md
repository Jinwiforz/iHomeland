## 1. OpenSpec

- [x] 1.1 创建 `add-unity-websocket-smoke-test` proposal、design、tasks 和 client-integration delta spec。
- [x] 1.2 确认本 change 不新增协议、服务端 runtime、MySQL 表或 Redis key。

## 2. Unity 编辑器联调工具

- [x] 2.1 新增 Unity Editor assembly，隔离 `UnityEditor` 引用。
- [x] 2.2 新增 WebSocket smoke test 菜单入口，连接 `AppConfig.WebSocketURL`。
- [x] 2.3 实现 heartbeat 请求/响应校验。
- [x] 2.4 实现固定测试账号注册，账号已存在时回退登录。
- [x] 2.5 实现 smoke test 成功后登出和 WebSocket 关闭。

## 3. 文档与验证

- [x] 3.1 更新 `docs/client-integration.md`，记录 Unity Editor smoke test 入口和前置条件。
- [x] 3.2 运行 `go test ./...` 或项目测试脚本，确认服务端未受影响。
- [x] 3.3 运行 `openspec validate add-unity-websocket-smoke-test --strict`。
- [x] 3.4 记录 Unity Editor 菜单 smoke test 是否已在本地执行；若未执行，说明原因。

## 验证记录

- [x] 运行 `cmd /c server\scripts\test.bat`，通过。
- [x] 运行 `cmd /c openspec validate add-unity-websocket-smoke-test --strict`，通过。
- [x] Unity Editor 菜单 smoke test 已手动触发并通过，Console 输出 `[WebSocketSmokeTest] OK: websocket, heartbeat, account session and logout passed.`。
