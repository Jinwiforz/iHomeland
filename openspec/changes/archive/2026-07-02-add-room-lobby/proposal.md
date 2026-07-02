## Why

第一里程碑已经具备服务端基础接口、Protobuf envelope 和 WebSocket 网关，现在需要实现第一个可玩的业务闭环：自定义房间大厅。该能力让客户端可以通过实时协议创建房间、加入房间、准备、退出、转移房主，并在断线保留期内恢复房间身份。

## What Changes

- 新增房间领域模型，管理房间 ID、房主、成员、座位、阵营、准备状态、连接状态和生命周期状态。
- 新增显式房间状态机，集中校验创建、加入、准备、取消准备、退出、房主转移、解散、断线保留和重连恢复的不变量。
- 新增进程内 room service 接口与内存 repository，用于第一阶段验证房间大厅逻辑。
- 新增房间大厅 Protobuf 消息和 message id，包括创建、加入、准备/取消准备、退出、转移房主、重连恢复、房间快照和错误响应关联。
- 将 WebSocket gateway 的业务分发接入 room service，但网关不得直接修改房间状态。
- 补充房间状态机、权限、重复成员、断线重连和协议请求响应测试。
- 本 change 不实现匹配系统、完整持久化、Redis/MySQL 房间存储、TCP 传输、观战、回放或高频战斗模拟。

## Capabilities

### New Capabilities

- 无。

### Modified Capabilities

- `room`: 细化自定义房间大厅的模型、状态机、成员身份、准备、退出、房主转移、断线保留和重连恢复行为。
- `protocol`: 新增第一阶段房间大厅实时消息、message id owner 和请求响应关联规则。

## Impact

- 影响 `shared/proto/realtime/v1/`、协议生成输出、`server/internal/protocol` 消息注册和协议测试。
- 影响 `server/internal/room`，新增房间模型、状态机、服务接口、内存 repository 和测试。
- 影响 `server/internal/gateway` 或 `server/internal/app` 的业务 dispatcher 组装，使房间消息通过接口进入 room service。
- 不新增 MySQL schema，不新增 Redis key，不引入 gRPC 服务边界。
