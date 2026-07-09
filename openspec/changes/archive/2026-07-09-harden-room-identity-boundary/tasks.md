## 1. 服务端身份边界

- [x] 1.1 在 `roomDispatcher` 中为所有房间大厅请求校验 connection session 已绑定玩家身份。
- [x] 1.2 在 `roomDispatcher` 中校验 payload `player_id` 与 session `PlayerID` 一致，不一致时返回结构化身份错误。
- [x] 1.3 确认 room service 仍只接收业务玩家身份，不直接依赖 gateway session。

## 2. 测试覆盖

- [x] 2.1 调整现有房间 WebSocket 流程测试，使正常路径先建立账号会话再发送房间请求。
- [x] 2.2 添加未登录 connection 发送房间请求被 `UNAUTHENTICATED` 拒绝的测试。
- [x] 2.3 添加已登录 connection 伪造其他 `player_id` 被拒绝且不创建房间的测试。

## 3. 清理与验证

- [x] 3.1 清理本 change 触及的无上下文 `TODO` 欠账。
- [x] 3.2 运行 `go test ./...` 验证服务端测试。
- [x] 3.3 运行 `openspec validate harden-room-identity-boundary --strict` 验证 change artifacts。
