## 1. 协议与生成

- [x] 1.1 在 `shared/proto/realtime/v1/envelope.proto` 新增房间已开始状态、`StartRoomRequest`、`StartRoomResponse` 和稳定 message id。
- [x] 1.2 运行 `tools/proto/generate.bat` 同步生成 Go 与 Unity C# 协议代码。
- [x] 1.3 更新协议注册表和协议兼容文档，记录新增 room message id 与 owner。

## 2. 服务端房间闸门

- [x] 2.1 在 `server/internal/room` 模型中实现开始房间状态迁移和不变量校验。
- [x] 2.2 在 `server/internal/room` service 中新增 `StartRoom` 入口并保持 repository 更新和快照返回模式。
- [x] 2.3 在 `server/internal/app` room dispatcher 中接入 `StartRoomRequest`，复用现有 session 身份校验。
- [x] 2.4 补充 room 状态机测试，覆盖房主成功开始、非房主拒绝、未准备成员拒绝、断线成员拒绝和非法状态拒绝。
- [x] 2.5 补充 WebSocket 集成测试，覆盖已登录房主开始成功和开始失败错误响应。

## 3. Unity 接入

- [x] 3.1 在 Unity `NetworkSystem` 新增 `StartRoomAsync` 请求方法。
- [x] 3.2 在 Unity `RoomSystem` 新增开始房间操作，成功后保存最新 `RoomSnapshot`。
- [x] 3.3 在 Unity `RoomPage` 增加房主可用的开始按钮逻辑，非房主不可发起开始请求。
- [x] 3.4 确认开始成功后只进入占位流程或展示已开始状态，不引入正式战斗逻辑。

## 4. 文档与验证

- [x] 4.1 更新 `docs/client-integration.md`、`docs/roadmap.md` 和相关长期规格，说明开始闸门验收路径和第一阶段边界。
- [x] 4.2 运行 `go test ./...` 验证服务端测试。
- [x] 4.3 运行 `openspec validate add-room-start-gate --strict` 验证 change artifacts。
