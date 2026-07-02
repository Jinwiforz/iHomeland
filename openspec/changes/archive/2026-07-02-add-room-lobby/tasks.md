## 1. 协议消息

- [x] 1.1 在 `shared/proto/realtime/v1/envelope.proto` 中新增房间大厅 message id、房间枚举、请求、响应和 `RoomSnapshot` 消息
- [x] 1.2 运行协议生成入口，更新 Go Protobuf 生成代码
- [x] 1.3 在 `server/internal/protocol` 中注册 room message id、owner 和 payload 类型
- [x] 1.4 添加协议注册测试，覆盖 room message id、owner、payload 类型和请求响应关联

## 2. 房间领域模型

- [x] 2.1 创建 `server/internal/room` 包，定义 Room、Member、Seat、Team、RoomState、MemberConnectionState 和业务错误
- [x] 2.2 实现房间快照结构和从内部模型生成快照的转换函数
- [x] 2.3 实现房间 ID、成员 ID 和座位/阵营的基础校验与确定性分配规则
- [x] 2.4 添加模型和快照转换测试

## 3. 状态机与业务规则

- [x] 3.1 实现创建房间状态迁移，校验创建者、容量和初始房主不变量
- [x] 3.2 实现加入房间状态迁移，校验开放状态、容量、重复成员和座位分配
- [x] 3.3 实现准备/取消准备状态迁移，校验成员身份和连接状态
- [x] 3.4 实现退出房间状态迁移，覆盖普通成员退出、房主退出、自动房主转移和最后成员解散
- [x] 3.5 实现显式房主转移状态迁移，校验房主权限和目标成员状态
- [x] 3.6 实现断线保留和重连恢复状态迁移，校验重连截止时间和重复成员去重
- [x] 3.7 添加表驱动状态机测试，覆盖权限、容量、唯一房主、关闭房间和非法迁移

## 4. Service 与 Repository

- [x] 4.1 定义 room service 请求/响应结构和对 gateway dispatcher 可调用的接口
- [x] 4.2 实现单进程内存 repository，并用 mutex 保护房间读写
- [x] 4.3 实现 room service 的 CreateRoom、JoinRoom、SetReady、LeaveRoom、TransferHost、DisconnectMember 和 ReconnectMember 方法
- [x] 4.4 添加 service/repository 测试，覆盖重复请求、并发敏感不变量和错误映射

## 5. Gateway 分发接入

- [x] 5.1 实现 room 协议消息到 room service 方法的 dispatcher
- [x] 5.2 在 `server/internal/app` 中组装 room service、内存 repository 和 gateway dispatcher
- [x] 5.3 确认 gateway 不直接依赖房间内部状态机或 repository 实现
- [x] 5.4 添加 WebSocket 集成测试，覆盖创建房间、加入房间、准备、退出和房主转移请求响应

## 6. 重连与连接清理

- [x] 6.1 在 gateway 连接关闭路径中通知 room service 成员断线，保持传输层和房间业务边界清晰
- [x] 6.2 实现重连恢复协议请求和响应处理
- [x] 6.3 添加断线保留、重连成功和重连过期测试

## 7. 文档与验证

- [x] 7.1 更新 `docs/protocol-compatibility.md`，记录新增房间消息、message id、owner 和兼容影响
- [x] 7.2 更新 `server/README.md`，说明房间大厅当前能力和测试入口
- [x] 7.3 运行 `server/scripts/test.bat` 或等价 Go 测试命令，确认服务端测试通过
