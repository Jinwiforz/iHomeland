# 文件结构规划

## 当前顶层目录

```text
client/      客户端工程或客户端版本信息
docs/        项目文档、架构、路线图、规范
openspec/    需求、设计、规格和变更任务
server/      Go 服务端代码
shared/      跨端共享协议和生成配置
tools/       开发、生成、构建和运维辅助工具
```

## 服务端目标结构

```text
server/
  cmd/
    server/
      main.go
  internal/
    config/
    logger/
    ops/
    gateway/
    room/
    storage/
    protocol/
```

### `cmd/server`

应用入口，只做启动编排：

- 加载配置
- 初始化 logger
- 初始化依赖
- 注册 HTTP 和 WebSocket
- 启动和优雅关闭服务

### `internal/config`

配置结构、默认值、加载和校验。

### `internal/logger`

项目级日志适配层。业务代码只依赖该包，不直接依赖 `slog` 或 `zap`。

### `internal/ops`

运维接口：

- `/healthz`
- `/readyz`
- `/version`
- 后续 metrics hook

### `internal/gateway`

实时网关：

- WebSocket/TCP 连接
- Protobuf envelope 编解码
- session 管理
- 心跳和超时
- 消息分发

### `internal/room`

房间业务：

- 房间模型
- 状态机
- 权限
- 成员和座位
- 重连资格
- 房间事件

### `internal/storage`

存储适配：

- MySQL repository
- Redis cache/session/index
- 事务边界
- 幂等处理

### `internal/protocol`

协议注册、消息 ID 映射和生成代码适配。`.proto` 源文件优先放在 `shared/proto/`。

## 文档结构

```text
docs/
  architecture.md              总体架构
  engineering-standards.md     工程标准
  workflow.md                  项目流程规范
  roadmap.md                   路线图和 change 拆分
  file-structure.md            文件结构规划
  protocol-compatibility.md    协议兼容规则
  redis-keys.md                Redis key 规则
```

## OpenSpec 结构

```text
openspec/
  config.yaml
  specs/
    protocol/
    gateway/
    room/
    storage/
  changes/
```

`openspec/specs/` 是长期行为契约。`openspec/changes/` 是一次次拟议变更，完成后归档并同步到主 specs。
