## 1. 冻结契约与验证边界

- [x] 1.1 建立 closed `validation.json`，只引用 catalog 已登记的 battle wire、simulation control、C++ acknowledgement、client battle runtime、Unity 与 OpenSpec strict 定向检查；现有 `battle.proto` 未变化，不执行会替换 Unity generated 目录的 `proto-verify`。
- [x] 1.2 在 battle wire source corpus 登记完整 snapshot transform 与 `state_flags` registry，更新 schema、manifest digest、validator、README 和直接绑定身份，保持 message ID、field number、lane、expiry 与 MTU 不变。
- [x] 1.3 增加 registry 的 producer/C++ protocol client/C# consumer source parity gate，拒绝 bit 0 grounded 重解释、未知 bit、缺失 scalar、非法 yaw 与 delta mask/presence 漂移。

## 2. 实现 C++ 权威 Movement/Physics commit

- [x] 2.1 扩展 validated `IngressCommand` 与 `ActorInputResolution` 的 typed move、aim、jump projection，保持 continuous hold、同 Tick last-sample、离散 edge 至多一次和 acknowledgement gap语义。
- [x] 2.2 实现 current PersonalWorld 有界平地 `PhysicsWorld` adapter，固定 world Y=0、capsule/ground query、容量与失败语义，不读取 Unity Scene 或客户端 Transform。
- [x] 2.3 实现每 instance 唯一 `BattleMovementReplicationStore`，以 50 ms fixed Tick复用现有整数 Movement模型，并原子发布全部 actor state与同 Tick acknowledgements。
- [x] 2.4 把 store 接入 `SimulationNode` startup、Tick observer、battle runtime binding、replication snapshot和 teardown，删除动态路径对静态 `initial_states` 的依赖并返回完整 actor集合。

## 3. 闭合 snapshot producer 与 Unity consumer

- [x] 3.1 扩展 `StateProjectionToken` 与 full/delta encoder，显式写 position、规范 yaw、三轴 velocity及phase/grounded/dead flags，并保持 encoded size与partition上限。
- [x] 3.2 增加 C++ movement store、SimulationNode、snapshot encoder与独立协议客户端测试，覆盖move、aim、grounded jump、airborne repeat、同 Tick原子state/ack、Owner/Visitor actor set、unknown flags和完整transform presence。
- [x] 3.3 更新 Unity known flags与authority grounded projection，使 activation/reconciliation只消费bit 4，不从position、Scene collider、phase或prediction推导。
- [x] 3.4 增加纯 C# protocol/replica/runtime tests，覆盖grounded full/delta、unknown bit、缺失transform scalar、local correction和remote interpolation输入。

## 4. 文档与父 change 依赖收口

- [x] 4.1 更新 gameplay simulation、protocol compatibility、client architecture/integration、B0.7 runbook与roadmap，记录平地adapter限制、registry owner、read path、替换边界和非最终资格声明。
- [x] 4.2 在本 change定向验证通过后勾选 `implement-unity-gameplay-runtime` 的6.8，并移除“authority grounded未知/静态snapshot”临时说明；真实Player tasks仍保持独立未完成。

## 5. 定向验证

- [x] 5.1 执行 C++ format/build与movement、physics、projection、simulation node、battle session、protocol client定向tests，并执行battle wire、existing protobuf field消费、client runtime静态与纯C#检查。
- [x] 5.2 使用 `quality.ps1 impact -Change implement-authoritative-battle-movement-projection` 预览后执行 `check-change`，不得调用完整battle matrix、连续verify、soak或finalize。
- [x] 5.3 执行 `openspec validate implement-authoritative-battle-movement-projection --strict`、父change strict验证、`git diff --check`和`.meta` diff审计；Unity Editor/Player验证留到最终用户接管。

## 6. 真实 Player actor 集合回归修复

- [x] 6.1 把 simulation 固定容量 state 与当前 authenticated active session 的公开 actor 集合分离；snapshot 不得把未占用 actor slot 发布为 entity。
- [x] 6.2 增加单 session、第二个 session 加入和 session 离开后的 projection 测试，锁定双方只观察相同的 active actor 集合。
- [x] 6.3 执行本 change 定向验证、strict、`git diff --check` 与 `.meta` diff 审计，不扩大为完整最终资格。

## 7. 真实 Player 预测抖动与跳跃回归修复

- [x] 7.1 更新 proposal、design与battle movement delta spec，冻结25 ms input采样/50 ms movement积分解耦、同SimulationTick fold/recompute和current平地整数落点一致性。
- [x] 7.2 重构 `GameplayPrediction`，按SimulationTick聚合未确认frame、从authority基点重演并复现server 50 ms整数Movement/平地crossing，不修改Scene、Prefab、Input Actions或`.meta`。
- [x] 7.3 增加纯C#/PlayMode回归测试、Editor/Development低敏prediction摘要与客户端owner文档，覆盖同组第二frame不重复位移、组内late jump、ack后transform不回拉、移动中落地精确parity，以及local target更新时临界阻尼速度不被重置。
- [x] 7.4 执行本change impact/check-change；Scene平滑补丁按影响面复跑client owner真实Player、Unity定向EditMode/PlayMode、strict、`git diff --check`与`.meta`完整审计，不扩大为完整最终资格。

## 8. 真实录屏 stale replay、Camera 构图与重进诊断修复

- [x] 8.1 将真实Editor录屏、300/600/900 mm周期rebase与ack gap证据写回proposal、design和movement delta spec，冻结只重演future authority组及第三人称构图边界。
- [x] 8.2 修复`GameplayPrediction`对authority horizon内未确认frame的重复积分，更新已有PersonalWorld Scene rig数值，并增加baseline等待/预测提前量与应用退出清理摘要；不得新增或修改`.meta`。
- [x] 8.3 增加ack frontier停滞但authority推进、Scene camera构图和退出诊断回归测试，并复查录屏帧证明local capsule不再近距占满主视野。
- [x] 8.4 后台执行Unity EditMode/PlayMode、client owner定向检查、OpenSpec strict、`git diff --check`和`.meta`审计，不扩大为完整最终资格。

## 9. 真实 Player successor baseline 与所见即所得修复

- [x] 9.1 修复服务端同instance、mapping与actor的新认证session接管旧UDP predecessor；增加active count不增长、successor full baseline、旧route拒绝与lifecycle close回归测试。
- [x] 9.2 为客户端首个baseline增加5秒deadline，以successor full baseline的既有ack初始化新prediction continuity anchor；恢复期保留最后可信Actor/Camera/HP并显示可执行Basic Latin文案，长帧re-anchor保留已采样Jump/ability edge，增加EditMode/PlayMode回归测试。
- [x] 9.3 修复已消费BattleTicket被credential expiry误杀的问题；增加双actor过期后owner successor接管测试，锁定无关visitor仍current、继续占用slot且successor full baseline包含双方。
- [x] 9.4 重新构建真实Development Player，连续运行210秒后注入transport断线，确认successor generation恢复baseline、Actor、HUD与Input；随后执行本change定向验证、strict、`git diff --check`和`.meta`审计。

## 10. 真实录屏输入时钟漂移与世界参照回归修复

- [x] 10.1 将2026-07-31真实Editor录屏与Development诊断证据写回proposal、design和movement delta spec，冻结未生成InputTick随authority horizon只向前重对齐、离散edge保留及纯表现世界参照边界。
- [x] 10.2 修复`GameplayPrediction`长期运行后InputTick落入已提交authority horizon或超出已观察early window的问题；跨跳号bundle只携带当前连续尾段，确保move/aim/jump不会以过期或TooEarly SimulationTick发送，也不会被本地重演跳过。
- [x] 10.3 增加输入时钟落后/超前、落地Jump、跨跳号bundle与低敏lead诊断回归测试，并为PersonalWorld增加不参与authority/physics的地面网格和固定参照物。
- [x] 10.4 后台执行Unity EditMode/PlayMode、client owner定向检查、OpenSpec strict、`git diff --check`和`.meta`审计；使用真实Player与固定参照复测移动、鼠标和连续落地Jump，不扩大为完整最终资格。

## 11. 真实录屏 render cadence 与 Tab 输入所有权回归修复

- [x] 11.1 分析2026-08-03真实Editor录屏，将“录制期间无大量重复帧、但50 ms local target仍产生速度脉冲”和Tab切换时旧continuous sample被hold的代码证据写回proposal、design与movement delta spec。
- [x] 11.2 为Scene local Actor实现基于已投影velocity的有界render extrapolation，非Cinematic Camera逐帧使用current semantic aim，local move按同yaw转换为world X/Z，不新增资产或手工修改`.meta`。
- [x] 11.3 在UI/focus取代Gameplay输入owner而battle gate active时提交neutral continuous sample，增加外推年龄、相机逐帧yaw、camera-relative move与Tab期间不后台移动的EditMode/PlayMode回归测试及低频帧时间诊断。
- [x] 11.4 执行Unity定向EditMode/PlayMode、client owner `quality.ps1 impact/check-change`、OpenSpec strict、`git diff --check`与`.meta`审计；保持现有手动服务器并交由用户复测长直线、连续转视角、跳跃和Tab往返。

## 12. Editor长帧 observer effect 与停止回摆修复

- [x] 12.1 分析2026-08-03 14:09真实Editor录屏与同场次帧日志，冻结运行中零Console遥测和semantic move驱动local水平render motion边界。
- [x] 12.2 将周期Console诊断替换为定长内存聚合与Scene解绑单次摘要；local Actor水平render motor按current world move逐帧推进，并以prediction sample做有界连续校正。
- [x] 12.3 增加运行中零日志、长帧分桶、连续移动位移和松键不反向的EditMode/PlayMode回归测试，不新增资产或手工修改`.meta`。
- [x] 12.4 执行受影响Unity测试、change定向验证、strict、`git diff --check`与`.meta`审计；恢复体验服务器并交由用户重新录屏验收。
