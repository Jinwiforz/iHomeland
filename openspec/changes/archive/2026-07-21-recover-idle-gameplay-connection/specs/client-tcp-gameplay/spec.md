## ADDED Requirements

### Requirement: Client gameplay channel 必须为每个 active generation 运行单一 heartbeat owner

客户端 MUST 在 OWN_WORLD connection 建立 active 状态后，或 JOIN/RECONNECT 首个 command 成功把 pending 提升为 active 后，启动且仅启动一个 heartbeat owner。heartbeat MUST 通过现有 typed operation、pending correlation 与 serialized writer 发送；任一时刻 MUST 至多存在一个 heartbeat pending。close、session invalidation、safe-return、generation replacement 与 App shutdown MUST 撤销并有界等待 heartbeat owner，旧 generation 的 delay、response 或 callback MUST NOT 修改新 generation。

#### Scenario: Own-world connection 没有业务操作

- **WHEN** active own-world generation 在一个 heartbeat interval 内没有其他可证明存活的 gameplay response
- **THEN** heartbeat owner 通过同一 writer 发送登记 request，并在匹配 response 后继续保持 current generation active

#### Scenario: Heartbeat 与显式 close 竞态

- **WHEN** heartbeat 正在 delay、排队、写入或等待 response 时调用 `CloseAsync`
- **THEN** channel 线性化撤销 generation、恰好完成 heartbeat pending、等待全部 I/O owner，并返回 Ready；不得遗留 socket、task、queue item 或 terminal callback

#### Scenario: Heartbeat 发现网络黑洞

- **WHEN** socket 没有立即报错但 heartbeat response 超过 operation deadline
- **THEN** channel 关闭 current generation、发布一次 unexpected disconnect，并使调用方能够立即建立新 generation，而不等待服务端 30 分钟 idle safety deadline

### Requirement: Client gameplay 失败诊断必须保留安全 cause

客户端 MUST 为 connect、preface write、reader、writer、heartbeat、close 与 pending timeout 产生稳定诊断事件。Development sink MUST 记录 component、operation、generation、stage、稳定 failure/close kind 与异常类型；MUST NOT 记录异常 message、endpoint、credential、payload、session/player/world/visit identity。诊断投递失败 MUST NOT 阻塞或改变 channel 状态机。

#### Scenario: Reader 因 socket exception 结束

- **WHEN** active reader 捕获底层 socket/IO 异常
- **THEN** channel 仍向产品层暴露稳定 Transport close reason，同时 Development 日志包含 reader stage 与安全异常类型，可区分 remote EOF、protocol、timeout 和本地 transport failure

#### Scenario: 诊断 sink 不可用

- **WHEN** sink 拒绝、抛错或主线程队列已关闭
- **THEN** connection generation 仍按原 terminal path 完成，诊断不得占用 terminal lifecycle 保留槽或成为重连、shutdown 的依赖
