# iHomeland 工程标准

## 基本原则

- 文档、OpenSpec 和协作说明默认使用中文。
- Go 代码遵循官方风格，优先简单、清晰、可测试。
- 代码质量必须达到可长期维护标准：正确、精确、优雅、可测试、可观测。
- 一个 OpenSpec change 只解决一个明确问题。
- 设计决策放 `design.md`，系统行为放 `specs/`，实现步骤放 `tasks.md`，长期路线放 `docs/roadmap.md`。
- 第一阶段不为“看起来完整”而提前引入复杂抽象。

## 推荐目录结构

```text
iHomeland/
  AGENTS.md
  README.md
  client/
  docs/
    architecture.md
    engineering-standards.md
    workflow.md
    roadmap.md
    file-structure.md
    protocol-compatibility.md
    redis-keys.md
  openspec/
    config.yaml
    specs/
    changes/
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
  shared/
    proto/
  tools/
```

## Go 命名规则

- 包名使用小写短名，不使用下划线、中划线或复数形式。
- 文件名使用小写蛇形，例如 `room_state.go`。
- 导出标识符使用 PascalCase，例如 `RoomService`。
- 非导出标识符使用 camelCase，例如 `roomStore`。
- 常见缩写保持一致：`ID`、`URL`、`HTTP`、`RPC`、`TCP`、`JSON`、`SQL`。
- 错误变量使用 `Err` 前缀，例如 `ErrRoomClosed`。
- 接口只在存在替换实现、边界抽象或测试隔离需求时创建。

## 代码质量规则

- 正确性优先于速度，清晰性优先于炫技。
- 每个函数应只有一个主要职责，避免把解析、校验、状态修改、持久化和响应组装混在一起。
- 每个包都应有明确职责；如果包名无法清楚表达职责，先调整设计再实现。
- 业务不变量必须集中表达，例如房间状态迁移只能通过状态机完成。
- 错误必须显式处理，不得静默吞掉；需要忽略错误时必须说明原因。
- 魔法数字、魔法字符串必须抽成具名常量，除非其含义在上下文中绝对明显。
- 并发代码必须说明所有权、生命周期和关闭策略。
- 对外接口应尽量小，参数和返回值应表达业务语义，不用无意义的 `map[string]any` 代替明确结构。
- 不要提前引入复杂抽象；当重复出现稳定模式或确有测试/替换边界时再抽象。
- 代码提交前必须能通过相关格式化、静态检查和测试；如果暂时不能运行测试，必须说明原因。

## 注释规则

- 注释使用中文，保留英文技术名词、协议字段、错误码和命令。
- 注释用于解释意图、约束、边界、不变量和权衡，不用于复述代码。
- 导出的 Go 标识符必须有 Go doc 注释，并以标识符名称开头。
- 以下场景必须写注释：
  - 房间状态机和状态迁移规则
  - Protobuf 协议兼容和字段废弃规则
  - 重连恢复和成员身份去重逻辑
  - 分布式锁、幂等写入和重试逻辑
  - 权限校验和安全边界
  - Redis key 的 TTL、恢复来源和清理触发条件
  - MySQL 事务边界和一致性策略
- 以下注释应避免：
  - 只复述代码的注释，例如“遍历列表”“设置字段”“返回错误”
  - 与代码不一致的过期注释
  - 没有上下文的 `TODO`
- `TODO` 必须写成可追踪形式，说明原因和后续动作，例如：

```go
// TODO(room): 当前只支持单进程内存房间；接入 Redis room index 后需要补充分布式恢复策略。
```

## 精确性规则

- 名称必须表达业务含义，例如 `roomID`、`hostPlayerID`、`reconnectDeadline` 优于 `id`、`owner`、`deadline`。
- 错误应区分类型，例如协议错误、权限错误、状态迁移错误、存储错误。
- 日志字段必须稳定，同一含义不能在不同位置使用多个字段名。
- 时间单位必须写入变量名或类型，例如 `heartbeatTimeout`、`timestampMs`。
- 状态枚举必须覆盖未知值或非法值处理。

## 配置规则

- 配置从环境变量、本地配置文件或 secret mount 读取。
- 禁止在代码中硬编码 Redis、MySQL、端口、账号或密钥。
- `.env.example` 只能包含示例值，不得包含真实密钥。
- 配置加载完成后必须校验必要字段。

## 日志规则

- 第一阶段默认使用 `slog`，通过项目级 logger 包封装。
- 业务代码不得直接散落临时打印。
- 日志字段保持稳定，推荐字段包括：
  - `component`
  - `operation`
  - `request_id`
  - `connection_id`
  - `player_id`
  - `room_id`
  - `error`

## 错误处理规则

- 内部错误可保留诊断细节，但不得直接泄漏给客户端。
- 客户端错误响应必须结构化。
- 跨包可判断错误使用哨兵错误或自定义错误类型。
- 网络错误、协议错误、权限错误、状态迁移错误应区分。

## 测试规则

- 房间状态机必须使用表驱动测试。
- 协议版本拒绝、envelope 解码、心跳超时必须有测试。
- 修复 bug 时优先增加能复现问题的测试。
- 不依赖真实网络的业务逻辑，不应通过启动 WebSocket 才能测试。

## OpenSpec 任务规则

- `tasks.md` 只写可以执行和验收的动作。
- 避免写“决定某某方案”；决策应写在 `design.md`。
- 避免一个 task 覆盖多个模块。
- 一个 task 最好能在几分钟到几十分钟内完成。
- 如果 task 会生成大量文件，说明它可能应拆成新的 change。
