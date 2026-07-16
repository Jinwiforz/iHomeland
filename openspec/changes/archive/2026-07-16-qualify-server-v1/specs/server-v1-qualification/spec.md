## ADDED Requirements

### Requirement: Go 资格客户端必须是独立公开契约消费者
服务端 MUST 交付位于 `server/internal/testclient` 的 Go 协议客户端。该客户端 MUST 只通过真实 HTTPS、WSS 与 TLS/TCP 与独立 `cmd/server` 进程交互，只依赖标准库、锁定的网络/Protobuf runtime 和 generated Protobuf；MUST NOT 导入服务端 Composition Root、application/domain、storage、protocol codec 或 transport adapter。客户端 MUST 从公开 bootstrap/auth response 取得 advertised endpoints，并自行实现 ticket/admission 生命周期、`IHTP` preface、frame、envelope sequence/correlation、binary WSS 与 TLS 1.3 验证，不得读取 Redis/MySQL 或进程内事实补全结果。

#### Scenario: Architecture test 检查独立消费者
- **WHEN** testclient 新增对 `internal/app`、业务 owner、storage、protocol 或 transport package 的 import，或 scenario 直接构造服务端 service/fake store
- **THEN** architecture test 失败且资格运行不得开始黑盒场景

#### Scenario: 客户端解析 advertised endpoint
- **WHEN** 服务端 bind 地址与公开 WSS/TLS-TCP advertised endpoint 使用不同动态端口映射
- **THEN** client 只连接公开 response 下发的 endpoint，ticket、admission与preface继续绑定同一受信地址，不从 Host header、本机配置或 PlayerID 猜测目标

### Requirement: 资格 manifest 与 runner 必须一一对应且版本化
仓库 MUST 在 `shared/contracts/fixtures/qualification` 维护 schema-versioned Q0 manifest。每个场景 MUST 具有唯一稳定 ID、封闭 group、mandatory 标记、前置 phase、总预算、预期公开 outcome 与证据类别；MUST NOT 保存 raw credential、运行时 identity、Docker 名称、本机路径或内部异常。Go runner MUST 以显式 registry 为每个场景提供唯一实现，contract test MUST 双向拒绝 manifest 漏实现、隐藏 runner、重复 ID、未知枚举和无界预算。

#### Scenario: Manifest 新增 mandatory 场景但未实现
- **WHEN** qualification manifest 增加一个 mandatory scenario ID，而 Go registry没有同名runner
- **THEN** contract gate 失败并且 report不能把该场景记为skipped或qualified

#### Scenario: Runner 注册未声明场景
- **WHEN** Go registry包含manifest未登记的业务或故障场景
- **THEN** completeness test失败，要求先评审其前置、预算、预期和资格影响

### Requirement: 黑盒功能矩阵必须覆盖完整 own-world 与 visit-world
Mandatory functional scenarios MUST 使用至少两个独立 actor，通过公开协议覆盖 register/login/refresh、WSS control、own-world bootstrap/admission/TCP snapshot、重复 bootstrap、定向 invite、HTTPS accept、JOIN、snapshot response/push、LEAVE、KICK、CLOSE、Owner/Visitor disconnect、grace 内 RECONNECT 和 safe-return。断言 MUST 验证 session epoch、一次性 credential、完整 assignment lineage 的公开投影、单调 revision、唯一 target、Owner 不可转移和 response/push correlation；MUST NOT 依赖跨连接全局发送顺序或客户端自造领域事实。

#### Scenario: 完成 own-world 并重复 bootstrap
- **WHEN** 同一已认证 actor 重复 bootstrap、分别签发一次性 ticket/admission并请求world snapshot
- **THEN** 两次bootstrap收敛到同一current assignment lineage且只存在一个可写WorldInstance；已消费credential重放被拒绝

#### Scenario: 双客户端完成访问生命周期
- **WHEN** Owner创建invite，目标Visitor accept/join后依次执行disconnect/reconnect、leave，并在独立场景中由Owner kick或close
- **THEN** 每次成功mutation只推进权威revision，当前binding收到匹配snapshot/safe-return，旧binding与其他Player不能取得目标资格

#### Scenario: Owner grace 到期
- **WHEN** Owner异常断开且未在公开absolute grace deadline前恢复
- **THEN** VisitSession terminal关闭，Visitor收到明确control/gameplay收敛和safe-return后停止旧target mutation，不能本地继承Owner

### Requirement: 资格矩阵必须验证陈旧资格与依赖恢复边界
Mandatory recovery/security scenarios MUST 覆盖 ticket/admission replay、错误 purpose/channel/endpoint、stale assignment/instance/lease、stale session epoch、process restart、Redis flush、Redis restart、MySQL restart、duplicate bootstrap/instance 与服务端不可用窗口。故障注入 MUST 只作用于当前 run-id 明确拥有的资源。Redis 丢失运行态后 MUST fail closed且不得从MySQL、payload或client memory恢复旧session/membership/assignment；MySQL持久事实在dependency恢复后 MUST 按现有owner contract保留；未知commit结果的精细线性化证据 MAY 由mandatory storage/domain tests提供，但report MUST标明证据层级。

#### Scenario: 进程重启使旧 assignment 失效
- **WHEN** 保留MySQL/Redis并重启独立server process，新进程bootstrap replacement上一进程assignment
- **THEN** generation/fence单调推进，旧ticket/admission/connection无法恢复资格，新credential只能进入successor

#### Scenario: Redis flush 后使用旧连接
- **WHEN** 当前run的Redis被受控flush且旧WSS/TCP客户端继续使用原session、membership或target
- **THEN** 服务端拒绝或关闭旧连接，不伪造safe-return/assignment事实；客户端必须重新认证并按公开流程建立新运行态

#### Scenario: MySQL restart 后读取持久世界
- **WHEN** MySQL在当前run内受控重启并恢复healthy，服务端经历有界dependency failure
- **THEN** 已提交Account与PersonalWorld持久事实仍可由重新认证/bootstrap解析，运行态credential不会因MySQL恢复而自动复活

### Requirement: 资源、背压和关闭资格必须验证有界收敛
Mandatory resource scenarios MUST 以manifest固定的有限profile覆盖slow WSS/TCP consumer、半帧/慢帧、并发response/push、connection storm、queue/backpressure、rate limit、graceful shutdown和超时强制回收。Manifest MUST 以 `execution` 明确区分 public wire、contract 与本轮实际执行的 layered owner evidence；分层证据目录 MUST 将每个 layered scenario 映射到稳定 package/test identity。Profile MUST规定尝试、字节、并发与时间上限并低于服务端硬上限；资格只断言结果守恒、稳定拒绝/关闭、其他连接隔离、sequence/frame完整，以及公开 WSS/TCP active/in-flight gauge 和 owner test 覆盖的 queue/runtime/deadline 收敛，MUST NOT把分层证明伪称为 wire 场景或使用依赖CPU型号的吞吐与延迟排名。

#### Scenario: Slow consumer 填满发送预算
- **WHEN** 一个matching客户端停止读取且业务产生超过其queue预算的replacement push
- **THEN** 该连接以稳定slow-consumer/backpressure结果关闭，已提交领域事实不回滚，其他actor仍能完成请求且所有queue最终释放

#### Scenario: 有界 connection storm
- **WHEN** runner按固定并发同时建立manifest规定数量的合法和拒绝连接
- **THEN** 成功、拒绝与超时数量等于总尝试数，不超过registry/handshake/task预算，storm结束后readiness与资源gauge回到允许基线

#### Scenario: Graceful shutdown 存在活跃连接
- **WHEN** 服务端ready时仍有HTTP请求、WSS和TCP连接并收到受控停止
- **THEN** 新入口先被拒绝，已接收工作在共享deadline内完成或取消，连接/runtime/task/storage按owner顺序回收且进程以稳定结果退出

### Requirement: 单一资格入口必须拥有完整生命周期与低敏报告
项目 MUST 提供 `tools/qualification/qualification.ps1` 作为唯一可产生Q0结论的入口。入口 MUST 使用锁定Go/Proto工具和storage run ownership，创建动态loopback endpoints、临时TLS、独立server process及全局/阶段deadline；成功、测试失败、子进程异常、timeout或用户中断都 MUST 尝试终止精确PID并通过storage owner清理精确run。机器报告 MUST记录schema/manifest版本、阶段和场景稳定outcome、耗时、contract digest及cleanup结果，但 MUST NOT记录credential、secret、payload、IP/port、PID、本机绝对路径、Player/Session/World/Visit identity或backend文本。

#### Scenario: 黑盒场景失败后清理
- **WHEN** 任一mandatory scenario失败或资格入口超过全局deadline
- **THEN** report保留首个稳定失败阶段，入口在独立cleanup budget内回收server和当前run资源并返回非零；cleanup失败与主失败同时报告且资格状态保持false

#### Scenario: 两次 clean qualification
- **WHEN** 同一提交在满足依赖的clean环境连续执行完整资格入口
- **THEN** 两次mandatory scenario集合、稳定outcome和contract digest一致，临时run ID、端口、耗时差异不进入冻结证据

### Requirement: 服务端 v1 只能由完整证据集标记 qualified
Q0 MUST 聚合并通过 protocol/contract clean generation、Go unit/integration、显式fuzz、race、真实storage、独立black-box functional/recovery、显式layered resource/shutdown evidence、OpenSpec strict、owner/document consistency和cleanup gates。仓库 MUST 提交 `docs/server-v1-qualification.md`，记录manifest版本、冻结contract digest、执行命令、mandatory gate汇总、证据层级、非目标与C0结论。任一mandatory gate失败、缺失、skipped、报告digest漂移、存在未接线production adapter/无owner message/table/key/listener或工作区包含未确认contract漂移时 MUST NOT声明qualified或解锁Unity runtime。

#### Scenario: 只有 storage verify 通过
- **WHEN** 真实MySQL/Redis integration通过但独立client、fuzz/race、资源或恢复matrix尚未全部通过
- **THEN** Q0保持未完成，资格文档不得声明server v1冻结或允许C0

#### Scenario: 全部 mandatory gate 通过
- **WHEN** 单一资格入口在clean contract集合上完成全部mandatory阶段、report与cleanup，主specs/docs同步且strict验证通过
- **THEN** `qualify-server-v1`可以归档，资格文档声明当前v1交付集qualified，后续C0 proposal可以引用该冻结基线

### Requirement: 资格客户端必须随公开 capability 长期演进
Q0 归档后 Go qualification client、manifest、runner与单一入口 MUST 作为长期服务端外部回归保留。新增或改变公开 operation、message、channel、credential、错误或恢复语义的 change MUST 在对应 capability group 同步增加或更新资格场景，并继续执行全部仍受支持版本的既有 mandatory 回归；只改变服务端内部 package、算法或storage adapter且公开行为不变时 MUST NOT复制内部实现或迫使client跟随。未来Activity、battle或其他独立能力 MUST使用独立qualification group并由发布门聚合，MUST NOT把资格客户端扩张为持有UI、Scene、表现或服务端业务状态机的产品客户端替代品。

#### Scenario: 新增公开 Activity capability
- **WHEN** 后续change登记新的Activity operation/message与客户端可见恢复语义
- **THEN** 项目新增独立activity qualification group并保留account/session/personal-world/visit全部旧回归，完整发布门聚合各mandatory group

#### Scenario: 服务端只重构内部 package
- **WHEN** domain/storage/transport内部结构调整但schema、registry、公开错误、时序与恢复行为未变化
- **THEN** 既有资格客户端无需导入或同步内部类型，原场景不修改即可继续证明兼容

#### Scenario: 资格客户端试图替代 Unity
- **WHEN** implementation向testclient加入Scene、UI、表现、输入、客户端产品状态机或复制服务端规则
- **THEN** 评审必须移除这些职责，仅保留公开协议消费和自动化断言；正式游戏体验仍由Unity client拥有
