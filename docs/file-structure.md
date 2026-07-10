# 文件结构规范

## 原则

- 目录表达 owner 与依赖方向。
- 只有真实实现出现时才创建目录，不提交空架构层。
- 协议源、registry 与 fixtures/golden 进入 Git；Go/C# generated code、中间产物、缓存和本机配置不进入 Git。
- 跨端契约只在 `shared/` 定义。
- 文档规则不复制到多个 owner 文件。

## 顶层结构

```text
iHomeland/
  AGENTS.md
  README.md
  buf.yaml
  client/
  docs/
  openspec/
  server/
  shared/
  tools/
  release.json
  versions.yaml
```

## 服务端目标结构

```text
server/
  README.md
  version.json
  go.mod
  go.sum
  compose.yaml
  config/
    local.yaml
  cmd/
    server/
      main.go
  internal/
    contract/
    fixtures/
    generated/proto/       # Buf 重建并由 Git 忽略
    protocol/
    app/
    buildinfo/
    config/
    logging/
    observability/
    session/
    account/
    personalworld/
    worldinstance/
    visit/
    transport/
      diagnostic/
      http/
      websocket/
      tcp/
    storage/
      mysql/
      redis/
    testclient/
  migrations/
  scripts/
```

### `cmd/server`

只包含进程入口、信号处理、Composition Root 调用和退出码，不包含业务规则。

### `internal/app`

创建 concrete dependencies、初始化顺序、ready 状态和关闭流程。它是唯一允许了解全部 concrete types 的包。

Lifecycle component 只用于真实持有资源或后台任务的对象；成功启动栈同时是唯一逆序关闭顺序源。所有长生命周期 goroutine 必须向受控 task group 登记，并由 root 或 component context 明确拥有。

### `internal/config`、`logging`、`observability` 与 `buildinfo`

- `config`：严格 YAML、白名单环境覆盖和启动前完整校验。
- `logging`：`log/slog` handler、level 与敏感字段脱敏。
- `observability`：进程私有 Prometheus registry 和低基数 runtime metrics。
- `buildinfo`：linker 注入且启动后不可变的安全构建身份。

### `internal/transport/diagnostic`

独立标准库 HTTP listener，只提供 health、readiness、version 和 metrics。它不依赖 application service，也不能成为公开业务 API 的临时入口。

### `internal/session`

统一拥有 session id/epoch、opaque token、结构化 connection ticket/nonce、AuthContext、scope policy 和连接失效语义。`SessionStore`、`EndpointProvider` 与 `ConnectionInvalidator` 由该消费包定义，真实 Redis 和 transport adapter 在后续目录实现；协议 adapter 负责将纯 Go ticket 投影转换为 generated Protobuf/OpenAPI model。

生产 package 不包含 memory store、listener、数据库 client 或 generated protocol type。并发参考 store、fake clock 与确定性 generator 只允许出现在 `_test.go`，防止 Composition Root 在真实 adapter 完成前提供看似可用但不可恢复的 session 服务。

### `internal/account`

Account 采用单 package 扁平结构：

```text
account/
  doc.go
  types.go
  credential.go
  store.go
  service.go
  errors.go
  *_test.go
```

`types.go` 拥有账号身份和规范化值，`credential.go` 拥有默认脱敏的 password/hash 值，`store.go` 定义 repository、hasher、session、clock 与 ID 消费接口，`service.go` 只编排 register/login。并发 reference repository、fake hasher/session issuer/clock/ID 只允许存在于 `_test.go`；production MySQL 与 password adapter 归入后续 infrastructure change。

Account 不拥有 refresh、logout、ticket、token parsing 或 AuthContext，也不依赖 generated protocol type。包内职责仍清晰时不拆 `domain/application` 子目录；出现真实复杂度后必须通过独立 change 证明拆分价值。

### 个人世界阶段目录门禁

每个 owner 对应的 OpenSpec change 进入实现前，不创建 `personalworld`、`worldinstance`、`placement`、`visit`、`activity`、`party` 或 `room` 空目录。真实实现出现时按下列职责决定 package，不把名称直接当作必须存在的层级：

| 未来 owner | 目录职责 |
|---|---|
| PersonalWorld | world identity、immutable owner、持久 revision 与 lifecycle |
| WorldInstance/Placement | assignment、lease/fencing、运行实例启动/休眠/重建 |
| VisitSession | invite、Visitor membership、role、capacity、grace 与 expiry |
| ActivityInstance | 副本、Boss、剧情位面或战斗的运行生命周期与 admission |
| Party | 跨场景持续队伍；仅在连续组队需求成立时创建 |
| Room | 可选的活动组建与准备；仅在需要房间列表、房主和 ready/start 语义时创建 |

消费侧 interface 与领域 owner 放在同一 package；MySQL/Redis、transport 和 placement provider adapters 继续归入 infrastructure。地图、任务、探索、家园和奖励出现真实独立规则时建立各自 owner，不集中到 `world/common` 或巨大 PersonalWorld aggregate。

### `internal/transport`

按协议 adapter 分包。Transport 只依赖 application interfaces、protocol codec、session/auth 和 observability，不持有业务事实。

### `internal/storage`

- `mysql`：connection、transaction、repository adapters。
- `redis`：cache adapters、key builders、TTL 和 scripts。
- repository/cache interfaces 由业务 owner 包定义，避免 infrastructure 反向拥有业务契约。

### `internal/testclient`

Go 协议测试客户端和 scenario runner。它是服务端资格验收的正式消费者，不导入服务端内部业务包，只通过公开网络契约交互。

### `migrations`

新 v1 空库 migration baseline。文件按不可变序号排列，已合并 migration 不修改，只新增后续 migration。

## 共享协议结构

```text
shared/
  proto/
    ihomeland/
      common/v1/
      account/v1/
      session/v1/
      control/v1/
      world/v1/            # 对应业务协议 change 落地时创建
      visit/v1/            # 对应业务协议 change 落地时创建
  contracts/
    http/v1/openapi.yaml
    registry/
    fixtures/
```

- `.proto` 是跨端消息源。
- route/error catalog 是协议源的一部分。
- HTTP fixtures、realtime golden packets 与 negative coverage manifest 是兼容性基线，必须版本化并由统一工具重复生成和验证。
- `descriptor.bin` 与 registry projection 不落盘；validator 使用刚生成的 Go descriptor registry，并在内存构建路由投影。
- 不放服务端 domain model 或 Unity 类型。

`server/internal/contract`、`server/internal/fixtures` 与 `server/internal/protocol` 负责协议验证，不属于进程运行时。`cmd/server` 与 `server/internal/app` 在同一 `server/go.mod` 中提供唯一进程入口和 Composition Root，不建立第二个 module。

## Unity 客户端目标结构

Unity 工程在服务端 v1 冻结后创建：

```text
client/
  README.md
  version.json
  Assets/
    App/
      Generated/
        Protocol/
        Http/
      Scripts/
        Core/
          Bootstrap/
          Composition/
          Lifetime/
          Configuration/
        Application/
          Session/
          Account/
          PersonalWorld/
          VisitSession/
          WorldAdmission/
        Infrastructure/
          Http/
          WebSocket/
          Tcp/
          Protocol/
        Presentation/
          Navigation/
          Models/
          Hosts/
            UIToolkit/
            UGUI/
        Scenes/
          Contexts/
        Editor/
      UI/
        UIToolkit/
          UXML/
          USS/
        UGUI/
          Prefabs/
        Theme/
      Scenes/
      Config/
  Packages/
  ProjectSettings/
```

### `Core`

- Bootstrap：唯一启动入口。
- Composition：对象创建和依赖连接。
- Lifetime：App Scope 状态、tick、回滚和关闭。
- Configuration：环境、endpoint、build 配置。

### `Application`

纯 C# session/account/personal-world/visit-session 状态与命令，不依赖具体 UI 或 transport component。

### `Infrastructure`

HTTP、WSS、TCP、generated protocol 和平台存储 adapters。不得保存第二份业务事实。

### `Presentation`

- Navigation：screen id、layer、input 和 lifecycle。
- Models：复杂共享展示投影，按需创建。
- Hosts：UIDocument、Canvas、EventSystem 和 Unity lifecycle adapters。

### `Scenes/Contexts`

只在正式场景需要应用接口时创建。SceneContext 持有 camera/world/scene UI 引用，随场景卸载。

个人世界客户端只能在服务端 v1 资格验收后增加 pure C# PersonalWorld、VisitSession 与 WorldAdmission application 目录，以及 Infrastructure 下的协议 adapters。World 场景、Actor 和表现仍归 Scene Scope；不得创建持有 socket、token、world snapshot 与 GameObject 的统一 `WorldManager`。

## 文档结构

```text
docs/
  architecture.md
  roadmap.md
  network-transport-architecture.md
  network-port-allocation.md
  protocol-compatibility.md
  client-architecture.md
  client-ui-architecture.md
  client-integration.md
  file-structure.md
  engineering-standards.md
  code-comment-convention.md
  git-commit-convention.md
  workflow.md
  redis-keys.md
  technology-versions.md
```

## OpenSpec 结构

```text
openspec/
  config.yaml
  specs/
    <capability>/spec.md
  changes/
    <change-name>/
      .openspec.yaml
      proposal.md
      design.md
      tasks.md
      specs/<capability>/spec.md
    archive/
```

## 工具结构

```text
tools/
  go/
    go.ps1
  proto/
    proto.ps1
    buf.gen.go.yaml
    buf.gen.csharp.yaml
```

工具目录只在对应工具可运行时创建，不保存本机二进制缓存。

`tools/` 只保存进入 Git 的脚本、模板与自动化源码；`.local/` 保存不进入 Git、可按版本目录重建的本机工具与缓存。协议命令见 `server/README.md`，版本、下载校验与缓存规则只由 `docs/technology-versions.md` 定义。

## 禁止归属

- handler 中的个人世界、访客会话或活动状态机
- Room、transport、SceneContext 或连接 registry 中的 PersonalWorld/VisitSession 最终事实
- Visitor 客户端持有或继承 WorldOwnerID
- 没有真实 owner change 的个人世界空目录与通用 `WorldManager`
- `shared/` 中的业务实现
- ScriptableObject 中的在线 session/world/visit state
- generated 目录中的手写文件
- Scene、Prefab 或 ScriptableObject 对已忽略 Generated code 的序列化引用
- `docs/` 中可执行但无人维护的临时脚本
- 没有 owner 的 `Manager`、`Utils`、`Common` 大杂烩目录
