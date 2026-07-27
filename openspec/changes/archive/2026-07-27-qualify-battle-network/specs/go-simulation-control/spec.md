## ADDED Requirements

### Requirement: Qualification metrics 必须经私有只读 control contract 获取

Simulation control MUST 增加 versioned `battle_qualification_snapshot_request/receipt` frame，只允许受监督 Go parent 在显式 qualification mode、匹配 bootstrap nonce/sequence、current node incarnation 和 current run identity 时读取。Receipt MUST 只包含有界低敏累计与 high-watermark，包括 node/instance/session count、raw/KCP bytes/packets、drop/reject/expiry/retransmit、ingress/egress/KCP queue、Tick duration/debt、rebind/rekey 和稳定 close reason；它 MUST NOT 返回 credential、ticket/proof/traffic key、cookie secret、remote endpoint、PlayerID、payload、完整 binding 或可修改 runtime 的 command。Snapshot 读取 MUST 不暂停 simulation、不重置计数、不延长 ticket/session/assignment 生命周期，且 production mode MUST 稳定拒绝该 request。

#### Scenario: Qualification parent 读取 current snapshot

- **WHEN** 显式 qualification mode 的 Go parent 以 current run/node/sequence 请求低敏 metrics
- **THEN** C++ 返回绑定 exact node/instance/generation 和 monotonic sample sequence 的只读 snapshot，资格工具可与 fault gateway、client 和 OS process evidence 交叉核对

#### Scenario: Production runtime 请求 qualification snapshot

- **WHEN** 非 qualification 配置、错误 run identity、旧 node incarnation 或外部进程尝试请求 snapshot
- **THEN** control session 稳定拒绝且不泄漏 runtime counters，不开放 listener、文件轮询或第二管理面

#### Scenario: Snapshot 导致状态变化

- **WHEN** 连续读取前后没有 gameplay input 或生命周期事件
- **THEN** actor/session/queue/ticket/assignment 与 simulation state 保持不变，只有 sample sequence 可以前进
