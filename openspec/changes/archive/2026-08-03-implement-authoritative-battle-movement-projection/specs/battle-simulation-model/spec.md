## MODIFIED Requirements

### Requirement: 模拟管线与 mutation owner 必须固定

每个 world MUST 只有一个 simulation worker 写入 ECS、physics binding 和 gameplay state，并 MUST 依次执行 `DrainInput -> InputIntent -> AIIntent -> AbilityActivation -> Movement -> Physics -> HitDetection -> Effect -> Attribute -> Death -> Replication -> CommitDeferredStructuralChanges`。网络、control、physics/navigation callback 和 evidence writer MUST 只提交不可变有界输入或结果，不得直接修改世界。Transform/velocity/grounded MUST 只由 Physics 提交，Attribute 当前值 MUST 只由 Attribute 提交，alive/dead 与 death cause MUST 只由 Death 提交；entity/component 结构变化 MUST 在末尾 barrier 按稳定顺序提交，新结构最早从下一 Tick 参与迭代。Live `SimulationInstance` MUST 将 `InputTimeline` 的 typed resolution接入上述 Movement/Physics stages，并在 Replication stage把同一 committed Tick的position、orientation、velocity、grounded与输入确认原子冻结为只读projection；启动时state不得在收到合法输入后继续冒充current snapshot。

#### Scenario: Physics callback 在 Tick 中途返回
- **WHEN** 异步或 adapter callback 在 Attribute stage 返回新的碰撞结果
- **THEN** callback 不能修改当前 Transform 或 hit state；结果只能进入登记的后续 Tick 边界或使原有同步 query 按失败 policy 终结

#### Scenario: Ability 创建 projectile
- **WHEN** 扇子 activation 在 AbilityActivation stage 成功并请求创建 projectile
- **THEN** create 进入 typed deferred buffer，在末尾 barrier 获得稳定 EntityID，projectile 最早于下一 Tick 执行 Movement/Physics/HitDetection

#### Scenario: 同 Tick 多次伤害致死
- **WHEN** 多个已验证 DamageIntent 在同一 Tick 指向同一低生命 actor
- **THEN** Attribute 按稳定 key 应用到首次归零，Death 记录唯一 cause；后续 intent 不得替换 death owner 或让 actor 在同 Tick 再次激活能力

#### Scenario: Live runtime 收到移动输入
- **WHEN** `SimulationNode` current mapping generation的合法move或jump input在目标Tick被`InputTimeline`终结
- **THEN** 唯一worker执行Movement与Physics后发布动态committed projection，network thread不能继续读取启动时静态state或半完成Tick

#### Scenario: 仍在 late window 的输入刚错过目标 Tick
- **WHEN** `CommandIngress` 接纳一个仍在冻结 late window内、但其目标 Tick已提交的move、aim或jump command
- **THEN** `InputTimeline`在首个尚未提交的current Tick消费该ready command并推进对应确认；不得只确认而静默丢失，也不得接纳超出late window的过期输入

### Requirement: 移动与跳跃必须由权威 kinematic capsule 模型裁决

玩家、普通怪物与 Boss 的移动 MUST 使用配置驱动且带单位的 position、orientation、velocity、grounded、movement mode 与 capsule 定义。平面移动输入 MUST clamp 到单位长度，并以固定 Tick 的 acceleration、deceleration、maximum speed、air control、gravity、slope、step 与 skin policy 推进。Jump MUST 只消费一次合法 `JumpPressed`，且 actor 必须 alive、可移动并在 Movement stage 开始时 grounded；客户端声明的 Transform、velocity 或 grounded MUST NOT 参与裁决。Live runtime的aim yaw MUST 只来自已验证量化intent并在Physics commit边界规范化，不能由Unity Camera、Scene Transform或payload actor identity覆盖。

物理访问 MUST 只通过项目 `PhysicsWorld` value contract 提供的 ground probe、capsule move、shape/ray/overlap 与 projectile sweep；结果 MUST 规范排序且不得暴露 Jolt 类型或 callback 顺序。纯模型 fixtures MUST 能以记录化 physics result 替代真实物理库。尚未绑定正式地图碰撞内容的current PersonalWorld runtime MAY 使用由server `physics_identity`约束的固定平地adapter，但该adapter MUST 保持有界query、相同Physics mutation owner和明确替换边界，且不得读取Unity Scene。

#### Scenario: 玩家在地面跳跃
- **WHEN** alive 且允许移动的 grounded actor 在当前 Tick 提交唯一 JumpPressed
- **THEN** Movement 应用配置 jump impulse，Physics 根据记录化 capsule query 提交权威 velocity/grounded/position，并忽略客户端预测 Transform

#### Scenario: 空中重复跳跃
- **WHEN** actor 在 Movement stage 开始时不 grounded 且提交 JumpPressed
- **THEN** 首期模型以稳定 invalid-state 终结该边沿，不产生二段跳、额外 impulse 或延后到落地后补执行

#### Scenario: 两个物理命中具有相同 fraction
- **WHEN** physics adapter 对同一 query 返回相同规范 fraction 的多个 collider
- **THEN** model 按 ColliderID、SubshapeID 稳定排序并产生与 callback/容器顺序无关的移动或命中结果

#### Scenario: Current PersonalWorld 使用平地 adapter
- **WHEN** live instance尚未绑定正式地图碰撞资产且其已验证physics identity选择current平地adapter
- **THEN** server只在world Y=0与登记capsule/ground policy内裁决移动和落地，结果仍由Physics stage提交且不得外推为正式地图collision资格
