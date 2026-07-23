## ADDED Requirements

### Requirement: Go 与 C++ 模拟服必须保持独立权威边界
Go Control/Data Plane MUST 继续拥有 Account、Session、PersonalWorld、VisitSession、placement、admission、MySQL/Redis 持久/运行事实与结算；独立 C++ Game Simulation Server MUST 只拥有已分配实例内的实时模拟。两者 MUST 通过版本化、认证且可取消的内部控制契约执行 allocate、start、admit、kick、invalidate、drain、stop、heartbeat/lease 与 result handoff，不得通过共享内存、cgo/FFI、客户端 payload 或重复数据库 owner 隐式共享最终事实。

#### Scenario: Go 分配个人世界模拟实例
- **WHEN** current PersonalWorld assignment、Session epoch、VisitSession membership 与模拟节点容量均通过 Go 权威校验
- **THEN** Go 通过内部契约向 C++ 提交绑定完整 identity/generation 的 allocate/admit command，C++ 只能创建对应实时实例和 player binding，不能改写 Go 的 assignment 或 membership

#### Scenario: 现有个人世界进入首个 gameplay 模拟
- **WHEN** Go 为 Owner 当前 PersonalWorld 的 WorldInstance 启动实时探索、普通怪物或 Boss
- **THEN** placement 通过 `RuntimeController` 的远程 adapter 将完整 AssignmentStamp 绑定到 C++ SimulationInstance，继续复用既有 PersonalWorld、VisitSession 与 admission owner，且不要求预先创建 ActivityInstance

#### Scenario: Session 被 Go 失效
- **WHEN** logout、forced logout 或更高 session epoch 已由 Go 提交
- **THEN** Go 向匹配 C++ connection/instance 传播 invalidation，C++ 停止接纳旧 epoch 输入并有界移除 actor，客户端 payload 不能恢复旧资格

#### Scenario: C++ 产生战斗结果
- **WHEN** 模拟服产生可能影响持久世界、资产、奖励或结算的结果
- **THEN** C++ 只提交带实例 generation、Tick、幂等 identity 和证据摘要的 result handoff，由 Go 的对应 owner 验证并提交最终事实，C++ 不直接写入其数据库表或 Redis owner key

### Requirement: C++ 模拟服必须拥有独立生命周期和可观测状态
Game Simulation Server MUST 使用独立 Composition Root、类型化配置、dependency validation、readiness、drain、结构化日志、低基数 metrics、受监督后台任务与有界逆序关闭。Asio I/O、simulation worker、physics/navigation resource、instance registry 与内部 control adapter MUST 有明确 owner；配置或依赖失败 MUST 在绑定 production UDP endpoint 或接受实例前 fail closed。

#### Scenario: UDP endpoint 绑定失败
- **WHEN** C++ 配置与依赖已验证但 UDP listener 端口冲突
- **THEN** 模拟服保持非 ready、逆序释放已启动组件并返回非零结果，Go 不把该节点分配为可接纳实例

#### Scenario: 模拟服进入 drain
- **WHEN** 部署、致命 worker failure 或受控关闭使节点进入 draining
- **THEN** 节点先拒绝新 instance/admission，再有界结束或迁出已有实例、停止网络接纳并按所有权逆序释放资源
