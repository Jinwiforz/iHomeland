# 文件结构规范

## 原则

- 目录表达 owner 与依赖方向。
- 只有真实实现出现时才创建目录，不提交空架构层。
- 协议源、registry 与 fixtures/golden 进入 Git；Go/C# generated code、中间产物、缓存和本机配置不进入 Git。
- 跨端契约只在 `shared/` 定义。
- 文档规则不复制到多个 owner 文件。

## 顶层结构（当前与已批准目标）

```text
iHomeland/
  AGENTS.md
  README.md
  buf.yaml
  client/
  docs/
  openspec/
  server/
  simulation/               # C++20 core、stdio control 与安全 UDP/KCP child
  shared/
  tools/
  release.json
  versions.yaml
```

`simulation/` 是 `ihomeland-sim-server` 的唯一 C++ 工程根，不属于现有 Go
`server/` 子目录，也不把第三方源码复制成业务代码。B0.3 已建立真实工程、
exact toolchain bootstrap、离线 adapters、stdio control、tests 与 qualification tools；
B0.5 在同一工程内增加唯一安全 UDP/KCP adapter，不改变 stdio control 的私有边界。

## C++ Game Simulation Server 目标结构

```text
simulation/
  CMakeLists.txt
  CMakePresets.json
  cmake/
  apps/
    sim_server/              # --control-stdio child 与 node-global UDP listener
    replay/                  # 连续确定 replay evidence
    benchmark/               # 1/5/8 actor reference workload
    qualification/           # verify 成功后生成本地资格产物
  include/ihomeland/sim/     # 只暴露项目 value contracts
  src/
    core/                    # Tick、digest 与跨 adapter 规范 identity
    simulation/              # SimulationInstance、输入映射与唯一 worker
    ecs/                     # generation-safe registry/storage/commit barrier
    gameplay/                # 固定 pipeline、movement、ability/effect/AI
    history/                 # 16-Tick 有界 history
    adapters/                # fixture、Jolt、Detour 与 evidence adapters
    control/                 # canonical frame、SimulationNode 与多实例 lifecycle
    transport/               # UDP、crypto、raw/KCP、BattleSession 与资源治理
    qualification/           # reference benchmark implementation
  tests/
  out/                       # ignored build/install tree
  reports/                   # ignored 本地 qualification evidence
```

目录表达依赖方向：`gameplay` 只依赖项目定义的 ECS/physics/navigation ports，
不能在 component 或公开 contract 中暴露 Jolt、Detour 或 JSON 类型。`control/` 只持有
本机 stdio frame 与 node/instance lifecycle；`transport/` 独占 UDP/crypto/raw/KCP，
并且只能向 tick-boundary input queue 写入有界命令，不能直接修改 ECS/physics world。
Recast 离线导航构建工具只有在内容管线
需求成立后才建立独立 target。

## 当前 Go 服务端结构

```text
server/
  README.md
  version.json
  go.mod
  go.sum
  config/
    local.yaml
  cmd/
    server/
      main.go
    qualificationtool/
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
    secret/
    identity/
    session/
    account/
    battleentry/
    battleticket/
    battleticketcontrol/
    personalworld/
    placement/
    simulationcontrol/
      process/
    visitsession/
    worldadmission/
    worldentry/
    transport/
      diagnostic/
      httpapi/
      wscontrol/
      tcpgameplay/
    storage/
      mysql/
        migrations/
      account/
      battleticket/
      session/
      personalworld/
      placement/
      simulationresult/
      visitsession/
      worldadmission/
      redis/
      tlsconfig/
    testclient/
  scripts/
```

### `cmd/server`

只包含进程入口、信号处理、Composition Root 调用和退出码，不包含业务规则。

### `cmd/qualificationtool`

只提供 Q0 独立协议客户端 CLI、临时 TLS、readiness probe 和机器报告入口；不得构造服务端业务对象图、直接操作 Docker/storage 或变成 production 管理 API。基础设施创建、故障 ownership 与精确清理由 `tools/qualification/qualification.ps1` 编排。

### `internal/app`

创建 concrete dependencies、初始化顺序、ready 状态和关闭流程。它是唯一允许了解全部 concrete types 的包。

Lifecycle component 只用于真实持有资源或后台任务的对象；成功启动栈同时是唯一逆序关闭顺序源。所有长生命周期 goroutine 必须向受控 task group 登记，并由 root 或 component context 明确拥有。

`simulation_node` component 在 storage 后、public runtime 前启动精确 C++ child，拥有
process handle、pipe session、health task、node registry 与 result inbox。Production
placement 使用其 `RuntimeController` adapter；unexpected child exit 是 root task failure。
`internal/app` 的进程内 runtime fake 只允许位于 `_test.go`；可执行 graph 不提供
`simulationControl` disabled fallback。

### `internal/simulationcontrol`

拥有 closed control value contracts、canonical JSON/frame codec、serialized session、
SimulationNode/instance registry、capacity selector、`SimulationTarget` 与 result coordinator。
`process/` 只负责 exact artifact 校验、child handle、继承 pipe 和低敏 stderr；它不拥有
placement、MySQL、listener 或 respawn policy。`internal/storage/simulationresult` 借用共享
MySQL pool，保存 immutable receipt 并在 owner transaction 中裁决已登记结果。

### `internal/config`、`secret`、`logging`、`observability` 与 `buildinfo`

- `config`：严格 YAML、白名单环境覆盖和启动前完整校验。
- `secret`：只解析显式 `env:`/绝对 `file:` reference，并提供默认不可展开、可主动清零的 secret value。
- `logging`：`log/slog` handler、level 与敏感字段脱敏。
- `observability`：进程私有 Prometheus registry 和低基数 runtime metrics。
- `buildinfo`：linker 注入且启动后不可变的安全构建身份。

### `internal/transport/diagnostic`

独立标准库 HTTP listener，只提供 health、readiness、version 和 metrics。它不依赖 application service，也不能成为公开业务 API 的临时入口。

### `internal/worldentry`、`internal/transport/httpapi`、`internal/transport/wscontrol` 与 `internal/transport/tcpgameplay`

`worldentry` 是 transport-independent 的窄用例协调器，只编排 own-world bootstrap、invite accept 与 world admission issue；`battleentry`、`battleticket` 与 `battleticketcontrol` 分别拥有 battle admission 编排、ticket policy/value 和 exact child install/revoke control。账号和 Session 用例仍由各自 owner 直接提供。`transport/httpapi` 是公开 HTTP adapter，集中拥有 11 个冻结 operation 的 route metadata、closed-schema codec、稳定错误映射、认证/限流/deadline middleware 及独立 listener 生命周期。只有该 package 可以导入 Gin；它不保存账号、world、visit、battle 或 credential 事实。

`transport/wscontrol` 拥有精确握手、typed PUSH codec、双重有界队列、单 reader/writer、心跳、connection/session/player 索引和 Session 失效关闭。它不依赖业务 storage package，不保存领域事实，不接收客户端 mutation，也不注册 TLS/TCP route。HTTP 与 WSS 共享公开 listener，但两个 adapter 保持独立路由与职责。

`transport/tcpgameplay` 独立拥有 TLS/TCP listener、authentication preface、typed codec/dispatcher、connection registry、双重有界 writer queue 与 typed publisher。它通过窄 application port 调用 PersonalWorld/VisitSession owner，不导入 Gin 或业务 storage adapter，不保存 aggregate snapshot、raw credential 或第二套 session/assignment 事实。

本地明文只允许 bind 与 remote 都是 loopback，production 必须使用 TLS 1.3。公开 ready 表示 HTTP、WSS 与 TLS/TCP graph 均已接线；`publicApi.endpoints.wss`、`publicApi.endpoints.tlsTcp` 都是可与 bind 地址不同的受信部署事实。关闭时全局 readiness 先进入 draining 以拒绝新 HTTP/WSS 握手，TCP 停止 accept，随后并行回收实时连接、停止公开 listener并排空 HTTP in-flight，最后由外层 lifecycle 释放 storage。

### `internal/session`

统一拥有 session id/epoch、opaque token、结构化 connection ticket/nonce、AuthContext、scope policy 和连接失效语义。`SessionStore`、`EndpointProvider` 与 `ConnectionInvalidator` 由该消费包定义；真实 Redis adapter 位于 `internal/storage/session`，现有 HTTP/WSS/TLS-TCP adapters 只把纯 Go ticket 投影转换为 generated Protobuf/OpenAPI model。

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

`types.go` 拥有账号身份和规范化值，`credential.go` 拥有默认脱敏的 password/hash 值，`store.go` 定义 repository、hasher、session、clock 与 ID 消费接口，`service.go` 只编排 register/login。并发 reference repository、fake hasher/session issuer/clock/ID 只允许存在于 `_test.go`；production MySQL repository 与 password hasher 位于 `internal/storage/account`。

Account 不拥有 refresh、logout、ticket、token parsing 或 AuthContext，也不依赖 generated protocol type。包内职责仍清晰时不拆 `domain/application` 子目录；出现真实复杂度后必须通过独立 change 证明拆分价值。

### `internal/visitsession`

VisitSession 独立拥有定向 invite、Visitor membership/capacity、connection-binding 条件、Owner/Visitor grace、绝对 expiry 与 safe-return 结果。它不可变绑定 `account.PlayerID` Owner、`personalworld.PersonalWorldID` 与创建时完整 `placement.AssignmentStamp`；accept、join 和 reconnect 必须由 application 重新读取 current active assignment 与 lease，不能由 payload、旧 endpoint 或 invite 覆盖。

生产 package 只包含纯 Go domain/application、消费侧 `VisitSessionStore`/world/assignment ports、稳定 command fingerprint 与严格 outcome/result 校验。并发 reference store、fake reader/clock/ID 与 admission qualification fixture 只存在于 `_test.go`；package 不启动 timer/goroutine，不拥有 socket、PersonalWorld 持久 mutation 或 placement lifecycle。

VisitSession production Redis adapter 位于 `internal/storage/visitsession`，独占 active/session/command schemas、完整 snapshot/result codec、owner Lua CAS 与 physical TTL；共享 registry 由 `internal/storage` 组合。它不拥有 Redis client、semantic cleanup、admission credential、连接迁移或 safe-return side effect。正式 Composition Root 为 HTTP 与 TLS/TCP 构造同一个 VisitSession service；`internal/app` 中的窄 coordinator 拥有 semantic deadline、connection lifecycle、结果副作用去重、跨通道通知与 safe-return 编排，transport 和 storage adapter 均不反向拥有这些事实。

### `internal/worldadmission`

独立拥有短期 opaque credential、role/purpose/full-assignment binding、稳定 issuance/consume identity、deterministic HMAC derivation、幂等 issuer、单次 verifier 与只读 qualification。它只接收 application 从 AuthContext、PersonalWorld/VisitSession 和 placement 派生的 domain value object；不读取请求 DTO，不拥有 socket、handler、listener、connection registry、Redis client 或业务 aggregate。

Production Redis adapter 位于 `internal/storage/worldadmission`，独占 issue/credential 两类 versioned Hash、digest-only key、owner Lua 与 replay retention。Adapter 借用共享 standalone client/keyspace，不保存 raw credential/derivation key，不关闭资源、不启动 timer/goroutine，也不从 MySQL、日志或 memory 恢复 flush 后的旧资格。正式 Composition Root 已接线 HTTP issuance 与 TLS/TCP consume，transport 只保留只读 Qualification 投影。

### 个人世界阶段目录门禁

每个 owner 对应的 OpenSpec change 进入实现前，不创建 `personalworld`、`worldinstance`、`placement`、`visitsession`、`activity`、`party` 或 `room` 空目录。真实实现出现时按下列职责决定 package，不把名称直接当作必须存在的层级：

| owner | 目录职责 |
|---|---|
| PersonalWorld | world identity、immutable owner、持久 revision 与 lifecycle |
| WorldInstance/Placement | assignment、lease/fencing、运行实例启动/休眠/重建 |
| VisitSession | invite、Visitor membership、role、capacity、grace 与 expiry |
| SimulationInstance | 当前 PersonalWorld 内移动、怪物、Boss、战斗与实时复制；绑定完整 AssignmentStamp |
| ActivityInstance | 拥有独立 lifecycle、admission 与结果边界的副本、战场或剧情位面 |
| Party | 跨场景持续队伍；仅在连续组队需求成立时创建 |
| Room | 可选的活动组建与准备；仅在需要房间列表、房主和 ready/start 语义时创建 |

消费侧 interface 与领域 owner 放在同一 package；MySQL/Redis、transport 和 placement provider adapters 继续归入 infrastructure。地图、任务、探索、家园和奖励出现真实独立规则时建立各自 owner，不集中到 `world/common` 或巨大 PersonalWorld aggregate。

### `internal/transport`

按协议 adapter 分包。Transport 只依赖 application interfaces、protocol codec、session/auth 和 observability，不持有业务事实。

### `internal/storage`

- `mysql`：唯一 `database/sql` pool、嵌入式 migration history/catalog、transaction runner；后续 owner-specific repository adapter 只能在对应业务 change 中加入。
- `redis`：唯一 standalone client、Keyspace/registry metadata、TTL 与 command outcome policy；不提供 generic cache 或预造业务 scripts。
- `tlsconfig`：从已验证 policy 与 secret value 构造 production TLS client identity。
- `account`：借用共享 MySQL pool/transaction policy，实现单表 AccountRepository 与固定 Argon2id hasher；拥有 `accounts` table，不记录 password、PHC、salt 或 tag。
- `session`：借用共享 standalone Redis client 与 Keyspace，以 owner Lua scripts 实现原子 SessionStore；Redis flush 后不恢复旧 session，也不持有 client lifecycle。
- `personalworld`：借用共享 MySQL pool，实现 PersonalWorld identity/lifecycle/revision 与 actor-scoped archive replay；拥有 `personal_worlds`、`personal_world_idempotency` table，不持有 pool 或 lifecycle。
- `placement`：借用共享 MySQL/Redis clients 与 Keyspace，拥有持久 allocation high-watermark、current assignment 与 transition replay；不关闭共享资源、不启动后台任务，也不保存通用 world state。
- `visitsession`：借用共享 standalone Redis client 与 Keyspace，以 owner Lua scripts 原子维护 active index、完整 snapshot 与 command replay；不持有 client lifecycle、semantic cleanup 或 admission credential。
- `worldadmission`：借用共享 standalone Redis client 与 Keyspace，以 owner Lua scripts 原子维护 issuance replay、digest-only credential binding 与 consume tombstone；不持有 raw credential、derivation key、client lifecycle 或 transport。
- repository/cache interfaces 由业务 owner 包定义，避免 infrastructure 反向拥有业务契约。

### `internal/testclient`

Go 协议测试客户端和 scenario runner。它是服务端资格验收的正式消费者，不导入服务端内部业务包，只通过公开网络契约交互。Manifest/runner 双向 completeness、layered evidence catalog 漂移校验、HTTP/WSS/TLS-TCP codec、actor secret 生命周期、fault checkpoint 和低敏 report 都由该 package 拥有；Docker、服务进程与依赖故障仍属于仓库工具 owner。

### `migrations`

Migration 与唯一执行 owner 同包嵌入，例如 `internal/storage/mysql/migrations/`。文件按固定宽度不可变序号排列，每个文件只含一个 statement；已合并 migration 不修改，只新增 forward migration。Storage runtime 最初只创建 `ih_schema_migrations` metadata；当前 catalog 已按 owner change 追加单张 `accounts`、PersonalWorld 与 placement tables，后续业务 schema 仍不得提前创建。

## 共享协议结构

```text
shared/
  proto/
    ihomeland/
      common/v1/
      account/v1/
      session/v1/
      control/v1/
      world/v1/            # PersonalWorld 公开投影与 snapshot
      visit/v1/            # VisitSession 控制、snapshot 与 safe-return
      battle/v1/           # B0.5 battle input/snapshot/control wire source
  contracts/
    http/v1/openapi.yaml
    registry/
      messages.json         # 全部实时消息唯一编号与 owner
      routes.json           # 全部实时消息唯一 channel/QoS/security route
      errors.json           # 稳定错误目录
    fixtures/
      admission/           # issuer/verifier 的抽象语义 corpus，不含 claims
      battle/
        model/             # B0.1 纯模型 schema、manifest、assumptions 与 cases
        network-profile/   # B0.2 cadence、lane、MTU、fault matrix、budget 与 canonical report
        wire/              # B0.5 跨端 canonical wire、crypto/KCP 与 malformed corpus
      http/
      realtime/
      qualification/       # Q0 scenario/evidence manifest、endpoint 示例与 contract freeze digest
```

- `.proto` 是跨端消息源。
- route/error catalog 是协议源的一部分。
- HTTP fixtures、realtime golden packets、negative coverage manifest 与 admission semantic corpus 是兼容性基线，必须版本化并由统一工具重复生成和验证。
- `contracts/fixtures/battle/model/` 是权威 gameplay 模型数据的唯一 owner；B0.2 profile 和后续 C++ harness 只消费其版本化数据，不在网络、C++ 或 Unity 目录复制规则。
- `contracts/fixtures/battle/network-profile/` 是网络 profile 的唯一 owner；它绑定完整 model digest，冻结 logical kind/lane 和 target budget，但不分配 numeric message ID、wire、listener 或端口。
- `contracts/fixtures/battle/wire/` 是 B0.5 wire corpus 的唯一 owner；它冻结 `3000-3007`、header/AAD、crypto/KCP vectors 与 negative cases，资格工具只读消费。
- `descriptor.bin` 与 registry projection 不落盘；validator 使用刚生成的 Go descriptor registry，并在内存构建路由投影。
- 不放服务端 domain model 或 Unity 类型。

`server/internal/contract`、`server/internal/fixtures` 与 `server/internal/protocol` 负责协议验证，不属于进程运行时。`cmd/server` 与 `server/internal/app` 在同一 `server/go.mod` 中提供唯一进程入口和 Composition Root，不建立第二个 module。

`tools/qualification/qualification.ps1` 是唯一可以产生 Q0 结论的入口；临时 binary、TLS、PID、日志和报告只写入被忽略的 `.local/qualification/<run-id>/`。资格客户端、manifest 和入口在 Q0 后长期保留，按公开 capability group 演进，不归入 `client/`，也不替代 Unity 工程。

## 当前 Unity 工程与 Gameplay 目标

Unity 工程已经创建。下面只登记当前资产根；脚本 owner 的当前/目标归属由后续“客户端程序集归属”树唯一维护，避免复制一份会漂移的 `Scripts/` 明细：

```text
client/
  README.md
  version.json
  Architecture/
    owner-registry.json
  Assets/
    App/
      Config/
      Editor/
      Generated/
        Protocol/
          IHomeland.Client.Protocol.Generated.asmdef  # 工具生成，稳定程序集边界
          Sources/                                    # protoc 生成 C#
          Runtime/                                    # 锁定 Google.Protobuf.dll
      Scripts/                    # 见下方唯一程序集归属树
      Tests/
        EditMode/
        PlayMode/
      UI/
        PersonalWorld/
          Prefabs/
      Scenes/
        BootstrapScene.unity
        PersonalWorldScene.unity
  Packages/
  ProjectSettings/
```

### 客户端程序集归属

```text
client/Assets/App/Scripts/
  Foundation/                 # IHomeland.Client.Foundation
  Application/
    Configuration/
    Contracts/
    Ports/
    Session/
    Control/                  # 仅 Application control contracts
    Gameplay/                 # 仅 Application gameplay contracts/models
      Replica/                # 权威 snapshot 的纯 C# 副本
      Prediction/             # input/predicted-state history 与 reconciliation
      Interpolation/          # 远端实体插值采样
    World/
  Infrastructure/
    Http/
    Security/
    Time/
    WebSocket/                # ClientControlChannel 与 WSS components
    Tcp/                      # ClientGameplayChannel 与 TLS/TCP components
    BattleNetwork/            # 后续 secure UDP/KCP concrete adapter
  Presentation/
    Pure/
      Navigation/
      PersonalWorld/
    Hosts/                    # Runtime assembly 的 Unity Hosts
    PersonalWorld/            # Runtime assembly 的 Unity Views/adapters
    Gameplay/                 # Actor、animation、VFX、camera 等 Scene adapter
  Core/
    Bootstrap/
    Composition/
    Configuration/            # Unity 序列化环境资产
    Presentation/             # Runtime 产品 binding 类型边界
    Qualification/
  Scenes/
    PersonalWorld/
      Gameplay/               # scene-bound actor、HUD 与 camera hosts
```

上述 gameplay 子目录同样是目标归属，不要求当前创建。纯 C# replica、history、reconciliation 与 interpolation 属于 App Scope；`BattleNetwork` 只实现已冻结的 secure UDP/KCP port；Actor、Animator、Cinemachine 与 uGUI HUD 属于 Scene Scope，卸载场景时释放，且不得保存权威战斗事实。

`Foundation`、`Application`、`Presentation/Pure` 分别由独立 `noEngineReferences` asmdef 封闭；`Infrastructure` 只能指向 Foundation、Application 与 generated protocol；`Scripts` 根 asmdef 对应 Runtime，是唯一可同时装配 Infrastructure、Presentation 与 Unity 类型的顶层程序集。顶层 `Presentation/Hosts`、`Presentation/PersonalWorld` 与 `Core/Presentation` 都属于该 Runtime 程序集，不是额外程序集。

`Core/Composition` 固定包含 Foundation、Infrastructure、Session、Channel、World、Presentation、Runtime/Qualification 七个子 Composition。它们返回私有封闭 bundle，不形成公共容器。旧 `Core/Lifetime`、Application 内的 concrete channel、跨层 mapper 与过渡 namespace 不得恢复。

`client/Architecture/owner-registry.json` 声明每种状态的唯一 owner、commands、snapshot、module、collaborators 与测试入口；`tools/client-architecture/client-architecture.ps1 -Action verify` 是程序集 DAG、禁止依赖、owner 唯一性、旧路径与 Unity 序列化脚本完整性的 hard gate，并输出 flows、ports、adapters 和提示性复杂度清单。

### `Core`

- Bootstrap：唯一启动入口。
- Composition：对象创建和依赖连接。
- Configuration：只保存 Unity 可序列化的环境资产类型；运行时配置 model/owner 仍归 `Application/Configuration`。
- Presentation：保存必须依赖 Unity 序列化与 Inspector 的产品 binding 类型边界；具体 Host/View 归顶层 `Presentation`。
- Qualification：仅 Editor/Development 的低敏诊断与产品 action 驱动。

Lifetime 已归属 `Foundation/Lifetime`；不得恢复旧 `Core/Lifetime`。

### `Generated`

`Generated/Protocol` 由 `tools/proto/proto.ps1 generate` 独占并在 Unity 编译前整体重建；源码、asmdef、runtime DLL 及 Unity 随后产生的 `.meta` 都保持 Git 忽略。这里不放手写 adapter、fixture 或配置，Scene/Prefab/ScriptableObject 不引用生成脚本。没有独立且已批准的 OpenAPI 客户端生成 change 前，不创建 `Generated/Http`。

### `Application`

纯 C# configuration、session、personal-world、visit-session 状态与命令，不依赖具体 UI、Unity Host、generated protocol 或平台 socket/WebSocket 对象。Application 只定义 channel ports 与强类型 contracts；concrete channel owner 位于 Infrastructure。

当前已落地 `Application/Bootstrap` 的启动配置用例、`Application/Session` 的唯一 session/credential owner、`Application/Control` 与 `Application/Gameplay` 的窄 contracts/ports，以及 `Application/World` 的访问、恢复与 projection feature slice；具体行为由[客户端接入规范](client-integration.md)统一说明。

`Application/World` 只共置紧密协作的不可变 model/mapper、`PersonalWorldService`、`VisitSessionService` 与 `WorldAdmissionCoordinator`；三类 owner 的事实和转换职责仍然分离，该目录不得演变为统一 `WorldManager` 或第二套网络 router。尚未出现独立业务需求的 `Account` 及其他 application 目录不得为了目标树完整而创建空壳。

`Application/Session` 同时放置 secure record 契约与一次性 startup restore owner；`Application/World` 中的 `ClientConnectionRecoveryCoordinator` 只保存 generation、阶段和冻结低敏 descriptor，不复制 Session、World 或 Visit 最终事实。

### `Infrastructure`

HTTP、WSS、TCP、generated protocol 和平台存储 adapters。不得保存第二份业务事实。

当前 `Infrastructure/Http` 放置冻结 operation catalog、JSON codec、共享 transport、result/error projection 与强类型 API；`Infrastructure/WebSocket` 只提供单次连接 adapter、封闭 control route catalog 与 generated Protobuf codec；`Infrastructure/Tcp` 提供 exact stream transport、`IHTP` preface、framing、封闭 gameplay route catalog 与 generated Protobuf codec。`Infrastructure/Security` 独占 Windows DPAPI、ACL、atomic replace、owner-specific path 和 named mutex；非 Windows 明确 Unsupported。以上 adapter 都不保存第二份业务状态。

客户端 C3 manifest/schema、automatic registry 与 diagnostic registry 位于 `shared/contracts/fixtures/client-qualification/`，唯一资格聚合入口及其独立失败回归位于 `tools/client-qualification/`；回归通过 AST 装载入口函数，不复制第二套资格实现。正式运行 build、日志、evidence 与 report 只进入 ignored `.local/client-qualification/<run-id>/`；不构成证据的定向 Development build、启动器与清单只进入 ignored `.local/client-diagnostics/<run-id>/`。真实 Player 服务端进程替换诊断同样位于该工具目录，其低敏日志和信号只进入 ignored `.local/client-recovery-diagnostics/<run-id>/`，不得被正式 `finalize` 读取。具体规则见[客户端 v1 资格验收](client-v1-qualification.md)。

`client/Assets/App/Scripts/Core/Qualification/` 只在 Editor 或 Development Player 中编译低敏资源计数、真实产品 graph soak 与 secure-store owner cleanup 驱动。它不是产品 service locator 或第二套恢复实现；没有显式 qualification mode 时零副作用，Release 必须完全移除该目录内的可执行入口和参数词汇。

### `Presentation`

- `Pure/Navigation`：当前放置纯 C# `ClientUiRegistry`、`ClientUiRouter`、封闭 route id、稳定 layer/input/lifecycle、generation 与不可变 snapshot；不得引用资源路径或 transport。
- `Pure/PersonalWorld`：放置首期 `ClientPersonalWorldExperience`、不可变页面切片、窄语义 actions 与 production route catalog；不得复制 Session/World/Visit 最终事实。
- `Hosts`：放置持久 `ClientUiHostRoot`、UI Toolkit `PanelRenderer` adapter 与 uGUI `Canvas/CanvasGroup/EventSystem` adapter；只接受直接引用，不扫描场景、不加载资源、不保存业务事实。
- `PersonalWorld`：放置依赖 Unity 的 HUD View 与 UI Toolkit 页面 adapter；只消费纯 C# presentation projection。

当前 production registry 只登记 Login、Shell、WorldVisit、WorldHud 与 ConnectionLost，并保持一个逻辑 route 一个 active owner。产品 UXML/USS、WorldHud Prefab 与 `PersonalWorldScene` 由 Unity Editor 创建并通过 Host 直接引用接线；不得把资源 key、credential 或 generated message 塞回 router。

### `Scenes/PersonalWorld`

`Scenes/PersonalWorld` 放置封闭 scene catalog、唯一 transition Host 与轻量 `PersonalWorldSceneContext`。Context 只持有 camera、lighting、scene root 和低敏表现投影，随场景卸载，不得持有 Session、socket 或 world/visit 权威 snapshot。

个人世界客户端已经在服务端 v1 资格验收后落地 pure C# PersonalWorld、VisitSession 与 WorldAdmission application owner，以及 Infrastructure 下的协议 adapters。多个 owner 可以按已批准 change 共置于窄 feature slice，但不能合并状态所有权。World 场景、Actor 和表现仍归 Scene Scope；不得创建持有 socket、token、world snapshot 与 GameObject 的统一 `WorldManager`。

## 文档结构

```text
docs/
  architecture.md
  battle-transport-threat-model.md
  deployment-secure-battle-transport.md
  gameplay-simulation-architecture.md
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
  storage-schema-comment-convention.md
  technology-versions.md
```

战斗模型、安全传输与后续网络资格资产按以下归属演进：

```text
shared/contracts/fixtures/battle/model/  # B0.1 无网络模型 source of truth
tools/battle-model/                      # 只读格式/引用/coverage/digest validator
shared/contracts/fixtures/battle/network-profile/ # B0.2 profile、fault matrix 与低敏 report
tools/battle-network-profile/            # 只读 validator、整数离散事件 simulator 与失败回归
shared/contracts/fixtures/battle/wire/   # B0.5 canonical wire 与 malformed corpus
tools/secure-battle-transport/            # B0.5 verify/finalize 与失败回归
tools/battle-qualification/               # 后续真实进程、故障注入、重连与安全验收
```

`battle-model` validator 只验证纯 gameplay JSON；`battle-network-profile` 在此基础上只重放 logical byte/event、fault、queue 与 deadline，不实现 gameplay evaluator、真实 KCP、socket 或 listener。两个工具都不启动 Docker/Go/Unity/C++、不安装第三方依赖、不写 source corpus，也不依赖 generated code 或本机绝对路径。B0.5 的 `battle/wire/`、numeric registry、listener 与安全 transport 是独立 source/adapter，不反向写入 B0.1/B0.2 corpus。后续资格工具只经公开或受控测试契约驱动真实进程，不导入 C++/Go 内部 gameplay 类型；运行日志、抓包和报告进入 ignored `.local/battle-qualification/<run-id>/`，不得把账号凭据、raw ticket、AEAD key 或玩家资产写入 evidence。

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
  storage/
    storage.ps1
  battle-model/
    battle-model.ps1
    battle-model.tests.ps1
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
