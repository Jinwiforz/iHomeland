# 代码注释规范

本文档是 iHomeland 服务端、客户端、跨端协议和测试代码注释规则的唯一 owner 文档。注释是代码契约的一部分；错误、过期或与实现冲突的注释按代码缺陷处理。

## 目标

- 让阅读者不必逆向实现即可理解设计意图、调用约束和失败边界。
- 让 IDE、Go doc、C# XML documentation 和协议文档能够提取稳定说明。
- 明确状态所有权、生命周期、并发、安全、兼容和单位等无法仅靠类型表达的信息。
- 保持较高注释覆盖率，同时避免逐行翻译代码产生噪声。

## 核心原则

### 先建立定位，再解释原因

声明注释的第一句需要说明该符号在系统中的职责，以满足语言文档工具和代码导航；后续内容重点解释：

- 为什么需要该边界或约束。
- 调用者必须满足什么前置条件。
- 哪些状态会被读取或修改，最终事实由谁持有。
- 生命周期、线程、取消、幂等和顺序要求是什么。
- 失败如何表达，调用者应如何处理。
- 哪些安全、兼容、性能或数据取舍不能被随意改变。

禁止只把声明翻译成中文。例如“`Enter` 用于进入世界”信息不足；应说明资格为什么来自 AuthContext/admission、何时允许进入、陈旧资格如何拒绝以及重复调用如何处理。

### 详细程度与风险匹配

以下边界必须提供更完整注释：

- 领域状态机和业务不变量。
- 身份、权限、凭据、ticket 和 session。
- 并发访问、锁、队列、后台任务和关闭顺序。
- 网络 framing、重试、幂等、消息路由和兼容策略。
- MySQL transaction、Redis TTL/恢复和缓存一致性。
- Unity 生命周期、主线程、事件订阅、异步取消和 Scene/App Scope。
- 非显然性能优化、资源池、缓冲区和内存所有权。

简单数据容器仍需声明级说明，但不需要为显然事实堆叠空泛段落。

### 注释与实现同步

- 修改契约、状态、错误、线程、生命周期或单位时必须同步修改注释。
- 删除实现时同时删除失效注释，不保留“曾经如何”的历史叙述。
- 设计历史和替代方案写入 OpenSpec `design.md`；代码注释只保留理解当前实现所必需的取舍。
- 无法确认注释仍然正确时必须先查证，不得复制旧注释蒙混通过。

## 覆盖范围

所有手写声明按下表执行：

| 声明 | 要求 |
|---|---|
| Go package | 必须有 package comment，说明职责、边界和禁止依赖 |
| Go 导出类型、函数、方法、变量、常量 | 必须有符合 Go doc 的注释 |
| Go 非导出业务类型、函数、方法 | 必须注释职责以及存在原因 |
| Go struct 字段、interface 方法 | 必须说明语义、所有权、约束、单位或失败行为 |
| C# class、struct、interface、record、enum、delegate | 必须使用 XML documentation |
| C# 构造函数、方法、属性、事件、字段 | 无论访问级别都必须有声明级注释 |
| C# enum 成员、业务常量 | 必须说明业务语义；数值来源或兼容约束不明显时必须解释 |
| Protobuf message、enum、service、RPC、field | 必须在 `.proto` 源文件中注释 |
| 测试函数与复杂 fixture | 必须说明保护的行为、回归原因或特殊构造意图 |
| 生成代码 | 禁止手工补注释；在 schema、模板或生成器源头维护 |

局部变量不使用声明级文档注释。名称已经完整表达含义的 `ctx`、`err`、循环索引、短生命周期临时值不逐个注释；存在以下情况时必须在声明前或复杂代码块前增加短注释：

- 单位、坐标系、时区或数值范围不明显。
- 值是快照、缓存、借用引用还是最终事实不明显。
- 变量必须满足跨语句不变量。
- 变量与锁、线程、取消或资源释放顺序相关。
- 采用看似多余的中间值是为了安全、兼容、性能或避免竞态。

逐个注释显然局部变量会降低信息密度，因此不作为覆盖率目标；清晰命名始终优先。

## 通用内容要求

声明级注释按适用性覆盖以下信息，不要求机械堆满所有项目：

1. **职责**：该符号属于哪个边界，解决什么问题。
2. **原因**：为什么由这里负责，为什么存在该限制或取舍。
3. **输入**：有效范围、单位、是否可空、身份来源和前置条件。
4. **输出**：结果语义、所有权、有效期和调用者责任。
5. **副作用**：状态迁移、I/O、日志、事件、缓存和持久化影响。
6. **失败**：错误类型、异常、重试性、超时和部分成功行为。
7. **并发**：线程安全、锁要求、可重入性和调用顺序。
8. **生命周期**：创建者、停止、释放、取消和重复调用行为。
9. **安全与兼容**：权限、不可信输入、协议版本和迁移限制。

不要为不适用的内容写“无”“没有”或模板占位。

## 语言与风格

- 注释默认使用中文，代码标识符、协议字段、错误码、命令和外部 API 保留英文。
- 使用完整、直接、可验证的句子，避免“显然”“简单地”“临时先这样”等模糊措辞。
- 术语必须与 owner 文档、协议和代码一致，同一概念不得交替使用多个名称。
- 注释靠近被解释的声明或代码块，不把关键约束放在远处文件头。
- 注释行应保持易读；长段落按语义拆分，不用超长单行承载多个约束。
- 不在注释中写作者、日期、提交记录或需求聊天记录，历史由 Git 和 OpenSpec 管理。

## Go 注释规则

### Package comment

每个 Go package 必须有 package comment。复杂 package 使用 `doc.go`；简单 package 可以放在主要源文件的 `package` 声明之前。

```go
// Package personalworld 实现个人持久世界的身份与 revision 规则。
//
// 该包不依赖 transport 或 storage，使世界 owner 与 mutation 不变量可以在无网络、
// 无数据库环境下独立测试。连接身份和 admission 必须由 application 层解析后传入，
// payload 中的玩家标识不能作为授权依据。
package personalworld
```

package comment 必须以 `Package <name>` 开头，并说明：

- package 的稳定职责。
- 允许和禁止的依赖方向。
- 对调用者重要的不变量或安全边界。

### 声明注释

- 导出标识符的注释必须以标识符原名开头，例如 `PersonalWorld`、`Apply`、`ErrNotOwner`。
- 使用连续的 `//` Go doc；除许可证或工具要求外，不使用块注释 `/* */` 编写 API 文档。
- 非导出业务声明也使用位于声明正上方的 `//` 注释，但不强制以名称开头。
- 注释必须紧邻声明，中间不能插入无关空行。
- 同组 `const` 或 `var` 只有在共享同一语义时才能使用组注释；成员语义不同则逐项注释。

```go
// PersonalWorld 持有个人世界身份与粗粒度持久生命周期。
//
// Aggregate 使用值语义，不持有 socket、storage transaction 或 mutable cache。调用方不能
// 原地修改 snapshot；持久并发由 repository 使用 expected revision 原子决议。
type PersonalWorld struct {
	// snapshot 保存 ID、immutable owner、lifecycle、revision 与 created time 的完整持久投影。
	snapshot Snapshot
}

// ArchiveWorld 以 Owner 身份、expected revision 和 idempotency identity 归档世界。
//
// command 中的 actor 必须来自 AuthContext，而不是客户端 payload。Service 先完成 Owner
// authorization，再由 repository 原子决议 replay、revision conflict 与 lifecycle；ctx 取消
// 不能证明 transaction 未提交，调用方必须依据 CommitPhase 选择恢复动作。
func (service *Service) ArchiveWorld(ctx context.Context, command ArchiveCommand) (PersonalWorld, error) {
	// ...
}
```

### 函数与方法

函数和方法注释按适用性说明：

- 参数来源、有效范围、单位和是否会被保留。
- 返回值所有权以及是否允许调用者修改。
- 明确可能返回的稳定错误及其重试语义。
- 是否执行 I/O、持久化、缓存、发布事件或状态迁移。
- 是否线程安全、是否要求持锁、是否允许并发调用。
- `context.Context` 的取消、deadline 和部分完成语义。

接口方法既要在 interface 中写契约，实现方法也要写实现特有的失败、性能或资源行为；实现与接口完全一致且没有额外约束时可以保持简短，但不能省略。

### 字段、变量与常量

- struct 字段说明业务语义、所有权、可变性、零值行为和单位。
- 时间值必须说明时区或使用 UTC 的原因；duration 必须说明单位或直接使用 `time.Duration`。
- ID 必须说明所属实体以及是否来自可信身份上下文。
- slice、map、buffer 和 channel 必须说明谁可修改、容量/背压以及是否允许返回后继续持有。
- 魔法阈值必须提升为命名常量，并解释来源、风险或协议限制。
- package 变量必须解释为什么需要共享状态；可由依赖注入替代时不得创建。

### 并发与资源

锁、goroutine、channel、timer、连接和事务附近的注释必须解释设计约束：

- 哪个 owner 创建与关闭。
- 哪些字段受哪个锁保护。
- 是否允许在持锁期间调用外部代码。
- channel 满时采用阻塞、丢弃、合并还是断开策略。
- goroutine 如何收到取消信号并在 deadline 内退出。
- 资源转移后原持有者是否还能访问。

不要写“加锁保证线程安全”这类空泛注释；必须指出被保护的不变量和锁顺序。

### Go 文件组织

Go 没有 `#region` 语言结构，禁止加入 `// region`、`//region` 或编辑器折叠标记模拟区域。使用以下方式组织：

- 一个文件围绕一个稳定职责命名，例如 `world_state.go`、`world_command.go`。
- 声明按类型、构造、公开行为、内部 helper 的阅读顺序排列。
- 只有在一组声明共享重要约束时使用简短章节注释。
- 文件需要大量章节才能阅读时，应按职责拆分文件或类型。

## C# 与 Unity 注释规则

### XML documentation

所有手写类型和类型成员无论访问级别都使用 `///` XML documentation：

- class、struct、record、interface、enum、delegate。
- constructor、method、operator、property、indexer、event。
- const、static field、instance field 和 `[SerializeField]` field。
- enum 的每个业务成员。

常用标签：

| 标签 | 使用要求 |
|---|---|
| `<summary>` | 必须；简明定位职责与关键意图 |
| `<remarks>` | 存在生命周期、线程、状态、不变量或取舍时使用 |
| `<param>` | 每个参数必须说明来源、约束、单位或所有权 |
| `<typeparam>` | 每个泛型参数必须说明角色和约束原因 |
| `<returns>` | 非 `void` 成员必须说明结果语义和所有权 |
| `<exception>` | 可预期抛出的异常必须说明触发条件 |
| `<value>` | 属性值存在非显然范围、单位或缓存语义时使用 |
| `<see>` / `<seealso>` | 指向直接相关契约，避免复制另一份说明 |

`<inheritdoc />` 只在继承契约完全适用且实现没有额外线程、生命周期、异常或性能约束时使用。存在差异时必须写本实现的 `<summary>` 或 `<remarks>`，不能用继承注释隐藏差异。

```csharp
/// <summary>
/// 协调个人世界进入与界面无关的应用用例。
/// </summary>
/// <remarks>
/// 该服务属于 App Scope，避免场景切换时丢失世界身份和 admission。所有状态通知都在
/// Unity 主线程派发，transport 回调必须先通过主线程调度器进入。
/// </remarks>
internal sealed class PersonalWorldService
{
    /// <summary>
    /// 请求进入当前玩家的个人世界，并在权威快照返回后更新本地只读状态。
    /// </summary>
    /// <param name="cancellationToken">
    /// 在页面销毁或应用关闭时取消等待；取消不代表服务端命令未被接收。
    /// </param>
    /// <returns>服务端确认后的个人世界快照。</returns>
    /// <exception cref="WorldAdmissionException">
    /// 当前 session 无有效进入资格时抛出，调用方应刷新 admission 而不是自动重试。
    /// </exception>
    public async Task<PersonalWorldSnapshot> EnterAsync(CancellationToken cancellationToken)
    {
        // ...
    }
}
```

### 字段与局部变量

- 字段注释说明 owner、初始化时机、可空性、变更来源和销毁责任。
- `[SerializeField]` 字段同时使用 XML documentation；当 Inspector 使用者需要理解输入约束时增加简洁 `[Tooltip]`。
- `[Tooltip]` 面向编辑器使用者，不能替代代码契约注释。
- 缓存字段必须说明失效条件；事件字段必须说明订阅与退订 owner。
- 局部变量不使用 `///`；仅在命名无法表达单位、不变量、快照语义或线程原因时使用 `//`。

### Unity 生命周期

即使 `Awake`、`OnEnable`、`Start`、`Update`、`OnDisable`、`OnDestroy` 名称固定，也必须使用 XML documentation 说明该组件在对应阶段为什么执行这些动作：

- 依赖在哪个阶段已经可用。
- 事件在哪订阅、在哪对称退订。
- 是否创建协程、Task、native resource 或 cancellation source。
- 是否要求 Unity 主线程。
- 重复启用、场景卸载和应用关闭时如何保持幂等。
- 与 App Scope、Scene Scope 或 screen lifecycle 的关系。

每帧方法必须额外说明为什么必须逐帧执行；能由事件、timer 或集中 tick 替代时不得保留空泛 `Update`。

### Async 与事件

async 方法和事件处理器的注释必须说明：

- continuation 是否回到 Unity 主线程。
- cancellation 表示停止等待还是取消远端事实。
- 页面销毁后如何阻止回写。
- 异常由谁观察，是否允许重试。
- 事件是否可能重复、乱序或在对象停用后到达。

`async void` 仅允许 Unity callback 或 event handler，并必须说明异常汇聚位置。

## C# `#region` 规则

`#region` 只用于提高较长类型的导航效率，不用于隐藏职责过多的问题。

允许按类型实际职责从以下稳定名称中选择并排序：`Constants`、`Serialized Fields`、`State`、`Construction`、`Unity Lifecycle`、`Public API`、`Event Handlers`、`Internal Logic`、`Cleanup`。

硬规则：

- 只有至少两个相关成员形成稳定职责组时才创建 region。
- 不为单个成员创建 region。
- 不嵌套 region，避免折叠后隐藏控制流和依赖。
- 一个类型通常不超过五个 region；超过时优先拆分类或 partial 的明确职责。
- region 名称使用稳定英文职责，不使用“Other”“Misc”“Temporary”等名称。
- region 不能把一个公开 API 的相关重载、契约或生命周期对称方法分散到难以追踪的位置。
- 小型类型不添加 region；自然的成员顺序比折叠标记更清晰。

示例：

```csharp
#region Unity Lifecycle

/// <summary>
/// 在组件启用期间订阅导航事件，使停用页面不会继续接收输入。
/// </summary>
private void OnEnable()
{
    // ...
}

/// <summary>
/// 对称解除导航订阅，避免场景卸载后回调已销毁对象。
/// </summary>
private void OnDisable()
{
    // ...
}

#endregion
```

## Protobuf 与跨端契约

- 每个 message 说明业务角色、发送方向和允许通道。
- 每个 field 说明语义、单位、范围、是否可空、默认值和未知值行为。
- ID 字段说明身份来源；客户端可填写的 ID 不得暗示其具有授权效力。
- enum 的零值必须有明确未知/未指定语义，每个成员说明兼容含义。
- service/RPC 说明认证 scope、幂等、deadline、重试和错误模型。
- deprecated 字段说明替代字段和保留窗口，不删除已发布 field number。
- 生成后的 Go/C# 文件禁止手工修改注释，所有修订回到 `.proto` 或生成模板。

## 测试代码

- 测试函数注释说明保护的行为或回归来源，不复述测试函数名。
- table-driven case 名称必须描述输入条件和预期结果。
- fixture、fake clock、故障注入和特殊顺序必须解释为什么需要该构造。
- 不给普通 Arrange/Act/Assert 每行加说明；只有阶段边界复杂时才使用章节注释。
- 测试中的 sleep、随机数、端口、超时和并发数量必须说明稳定性依据，或改用可控机制。
- 被跳过的测试必须说明 owner、原因、恢复条件和跟踪项。

## TODO 与临时约束

TODO 必须使用以下格式：

```text
TODO(owner): 原因；满足什么条件后处理；具体后续动作或跟踪项。
```

规则：

- owner 必须是团队、模块或可持续维护的身份。
- 必须说明为什么当前不能完成，而不是只写待办事项。
- 必须有可判断的触发条件和明确动作。
- 安全、数据一致性和协议兼容问题不能只用 TODO 延后。
- 临时代码到达约定触发条件后必须删除或转为正式设计。

## 禁止写法

- 逐行翻译代码，例如“将 count 加一”“返回 result”。
- 用注释掩盖含糊命名、超长函数或职责过多的类型。
- 保留大段被注释掉的代码，历史应由 Git 保存。
- 记录作者、日期、提交 hash 变化史或聊天上下文。
- 写无法验证的保证，例如“这里永远不会失败”。
- 使用分隔符横幅、ASCII 装饰或大量空 region 制造结构感。
- 在生成代码中手工补注释。
- 注释与实现冲突后只改代码不改注释。

## 评审清单

代码评审至少检查：

1. 所有手写声明是否达到本规范的覆盖要求。
2. 注释是否解释设计意图、契约和风险，而非复述语法。
3. 参数、返回值、错误、单位和所有权是否与实现一致。
4. 状态机、身份、安全、并发、生命周期和关闭语义是否足够明确。
5. Go doc 是否以导出标识符开头，package comment 是否以 `Package <name>` 开头。
6. C# XML 标签是否完整，`<inheritdoc />` 是否确实适用。
7. `#region` 是否提升导航效率而没有掩盖类型过大。
8. TODO 是否有 owner、原因、触发条件和后续动作。
9. 生成代码是否只从源 schema 或模板获得注释。
10. 修改行为时是否同步更新相关注释和 owner 文档。

## 自动化质量门

服务端代码阶段启用能够检查 exported comment、package comment 和无效 directive 的 Go lint 规则；客户端代码阶段启用 XML documentation 与 C# analyzer 检查。自动化只负责发现缺失和格式问题，注释是否准确、是否解释“为什么”仍必须由评审确认。
