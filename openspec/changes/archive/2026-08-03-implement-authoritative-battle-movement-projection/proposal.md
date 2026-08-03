## Why

当前 C++ `SimulationNode` 已接收并确认 Battle input，但 Tick observer 只推进 `InputTimeline`，对外 snapshot 仍发布启动时的静态 `initial_states`；同时 producer 没有写出 `yaw`、`velocity`，也没有登记 authority `grounded` bit。继续推进真实 Player movement/jump 验收会把 Unity 本地预测误当作服务器事实，因此必须先闭合 input、Movement/Physics commit、snapshot 与 Unity reconciliation 的同一条权威链路。

## What Changes

- 在 C++ runtime 中建立每个 actor 的有界 Movement/Physics state owner，使 current mapping generation 的 move、aim 与 jump intent 在唯一 simulation worker 上按固定 Tick 提交。
- 将 position、yaw、三轴 velocity 与 grounded 从同一次 committed state 投影到 full/delta snapshot，并保持 `ServerTick`、`LastProcessedInputTick` 与 entity state 一致。
- 为 `BattleEntityState.state_flags` 建立结构化 registry：保留低四位 phase 与 bit 31 dead，新增 bit 4 grounded；producer 和 consumer 必须拒绝未知 bit，不重解释已有 bit。
- 让 Unity replica/reconciliation 只从已登记 grounded bit 读取权威接地事实，并增加 C++ producer、协议客户端和纯 C# consumer 的跨端定向测试。
- 将 Unity 的 25 ms input采样与50 ms authority simulation积分解耦：同一SimulationTick内只按服务端fold规则积分一次，并锁定平地落点的整数舍入，使ack后的重演不再周期性拉回本地角色与相机。
- Reconciliation 只重演晚于最新authority ServerTick的future input组；已经落在authority Tick或更早、但因连续ack gap仍留在history中的frame只保留发送/确认所有权，不得从current authority state重复积分。
- 让Scene local Actor在render timeline使用保留跨帧速度的临界阻尼追踪prediction target，Camera继续只跟随该平滑Transform，避免20 Hz目标更新产生可见速度脉冲。
- 根据真实Editor录屏修正current PersonalWorld第三人称构图：FollowProxy仍跟随平滑Actor，但Exploration等rig使用角色上半身高度、可读跟随距离和低延迟阻尼，避免2米近距中心构图把胶囊体放大到几乎占满画面并放大细小修订。
- Editor/Development诊断区分authority/prediction提前量、target rebase与baseline等待态；Unity应用退出阶段的尽力清理聚合改为低敏warning摘要，非退出路径的清理失败仍保持error。
- 同一instance、mapping与actor的新认证BattleSession必须在首个baseline前接管旧UDP会话，避免客户端重进后因重复actor永久等待；旧route立即失效并记为lifecycle终结。
- BattleTicket绝对expiry只限制尚未消费的握手凭据；成功握手后的active actor不得在其他玩家安装successor ticket时因原凭据TTL被误标记Expired，必须继续占用原slot并保持session current，直到显式撤销、同actor接管或authority/instance终结。
- Successor full baseline携带的既有`LastProcessedInputTick`必须初始化新prediction generation的authority acknowledgement anchor；客户端不得把同actor连续timeline的合法非零ack误判为future ack，也不得继承旧预测history。
- 客户端首个baseline等待必须有界。短时恢复保留最后可信角色画面和生命值并明确标识正在重连/last known；终态失败显示玩家可执行文案，不暴露`Protocol`等内部分类，也不得无限显示同步占位。
- Render长帧触发prediction安全re-anchor时，已采样的Jump/ability离散边沿必须保留到新timeline发送一次，避免卡顿帧造成按键无响应。
- 长时间运行期间，客户端尚未生成的InputTick必须随最新authority horizon只向前重对齐，并在本地时钟短时快于simulation时停在已观察early window边缘，避免继续发送已经落入authority过去或尚不被server接受的move、aim、jump；前向重对齐与等待均不得丢弃已采样离散edge。
- PersonalWorld必须提供稳定、可读的世界空间地面网格和固定参照物，以便玩家区分角色修订与相机运动；这些对象只属于Scene表现，不参与服务器Physics、grounded或输入裁决。
- 2026-08-03真实Editor录屏表明，拉回已消失但local Actor与Camera仍在20 Hz prediction target之间呈现速度脉冲。Scene表现必须利用已投影velocity在有界时间内逐render frame推进local target，Camera必须逐帧消费同一semantic Aim，不能等待50 ms prediction坐标跳变后再转动。
- Gameplay输入owner被Tab菜单或窗口失焦取代时，客户端必须立即向prediction提交零move且无edge的连续样本，不得让切换前的WASD持续在后台hold；回到Gameplay后重新读取current Player map。键盘/手柄的local move必须按current semantic aim yaw转换为world move，使W始终对应当前画面前方。
- 2026-08-03第二段真实Editor录屏与同场次日志证明，基础帧平均仅约4–8 ms，但运行中每两秒Console诊断造成160–248 ms长帧，最近窗口达到392 ms；运行时遥测必须改为定长内存聚合并只在Scene解绑后一次输出，不能让可观测性反向污染手感。Local水平render motion必须由current semantic move逐帧推进，并以prediction sample做有界连续校正；松键时不得继续按旧velocity外推后再反向收敛。
- 不扩大到 hit、damage、ability、reward、正式地图碰撞内容、Room/Party、网络端口或 transport profile；不把客户端 Transform、Scene collider 或本地预测写回服务器。

## Capabilities

### New Capabilities

- `battle-movement-replication`: 定义 current battle input 经 C++ Movement/Physics committed state、snapshot projection 到 Unity reconciliation 的端到端权威移动链路。

### Modified Capabilities

- `battle-simulation-model`: 明确 live `SimulationInstance` 必须把 input resolution 接入唯一 Movement/Physics commit，并发布同一 Tick 的动态只读 projection。
- `secure-battle-transport`: 冻结 snapshot transform 字段 presence/range 与 `state_flags` bit registry，要求 producer/consumer 对未知值 fail closed。

## Impact

- C++：`InputTimeline` resolution、runtime movement state owner、`SimulationNode` Tick observer/replication binding、snapshot encoder 与对应 simulation/transport tests。
- 共享协议契约：现有 `battle.proto` 字段不改号；更新 battle wire source corpus、manifest digest、validator 与跨端 fixtures。
- Unity：`ClientBattleEntityState` known flags、protocol adapter、runtime reconciliation、输入时钟前向重对齐、local Actor临界阻尼表现、PersonalWorld Scene-owned Cinemachine rig参数/纯表现世界参照与EditMode/PlayMode tests；不修改 Input Actions，任何必要`.meta`只允许由Unity导入生成。
- 验证：使用 closed `validation.json` 运行 battle model/wire、C++ targeted、client battle runtime 与 OpenSpec strict checks；现有 `battle.proto` 不变，因此不运行会替换 Unity generated 目录的完整 `proto-verify`，也不触发完整资格、连续 verify、soak 或 finalize。
