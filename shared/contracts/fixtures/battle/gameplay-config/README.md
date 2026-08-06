# Gameplay Configuration Governance

本目录是 PersonalWorld combat gameplay configuration 的跨运行时治理契约。目录 owner
为 `gameplay-configuration-governance`；B0.8 的 Go selector、C++ authority loader 与 Unity
presentation mapper 必须消费同一 format，不得从任一运行时实现反向生成另一份事实。

## 身份与规范格式

- format identity 固定为 `gameplay-config-format-v1`。
- JSON 必须使用 UTF-8 无 BOM、LF、单一末尾换行和 closed schema；manifest 登记原始文件
  SHA-256，因此空白、字段顺序或换行变化也会改变 source identity。
- manifest 以规范相对路径双向登记全部 schema、registry 与 package source；路径按 ordinal
  排序，禁止绝对路径、父目录逃逸和未登记 JSON。
- package identity 使用 lowercase SHA-256。输入为 UTF-8 bytes：
  `ihomeland-gameplay-config-v1\n`，随后按 `identity_files` 路径 ordinal 顺序依次追加
  `path + NUL + document_kind + NUL + file_sha256 + LF`。
- `ConfigIdentity` 使用上述 package identity；`NavigationIdentity` 与 `PhysicsIdentity` 由
  `bindings.json` 独立提供。三者必须互不相同，不能使用重复字符或无 source 的占位摘要。
- package 必须精确绑定 `battle-model-v1`、`battle-network-profile-v2` 与 `battle-wire-v1`
  的 current manifest SHA-256。任一 source 漂移都会关闭后续 consumer 进入门。

## Owner 与消费边界

- `authority.json` 只描述 C++ 权威 actor、weapon、Ability、projectile、Effect、AI、Boss
  phase、encounter、collision/navigation policy 与 cue semantic key。
- `presentation.json` 只把 actor/weapon/Ability/projectile/Effect/cue semantic ID 映射为显示、
  Animator、VFX、Audio、HUD、camera 或 prediction 逻辑资源键。它不能重复伤害、Cost、
  Cooldown、AI、命中、spawn、死亡或权威 lifetime。
- `wire-mapping.json` 是 actor、weapon、Ability 与 projectile semantic ID 到 `uint32` wire ID
  的唯一治理映射；条目按 numeric ID 严格递增，已退役 ID 不得复用。
- `bindings.json` 只保存 map、navigation 与 physics 的逻辑 identity 和 digest，不包含二进制
  map/navmesh/physics material，也不把 Unity GUID/path 暴露给服务端。
- Go 是 production package 选择 owner；C++ 在 SimulationInstance 启动前独立重验 source；
  Unity 只消费 presentation mapping。v1 不支持 hot reload、partial merge 或 active instance
  内版本切换，配置更新与回滚必须通过完整 package 和更高 assignment generation replacement。

## Reference package

`packages/governance-reference-v1/` 使用保留的 `fixture/` semantic namespace，并标记为
`governance-only`。它只证明 schema、引用、单位、范围、coverage 与 presentation parity 可
表达，不能被 Go Composition Root、C++ child 或 Unity Player 当作 production content。
B0.8 的 production package 位于
`shared/contracts/gameplay/battle/packages/personal-world-combat-v1/`，地图、navigation 与
physics source 位于 `simulation/content/personal-world-combat-v1/`。production package 不得
使用 `fixture/` namespace，并必须独立绑定 governance、model、network profile 与 wire source。

## 安全与验证

唯一验证入口是 `tools/gameplay-config/gameplay-config.ps1 validate`；production package 使用
`-ProductionRoot shared/contracts/gameplay/battle/packages/personal-world-combat-v1` 显式加入
验证。工具只读取 JSON，不得启动 Go/C++/Unity、listener、Docker、CMake、网络或 gameplay
evaluator，也不得重写 manifest/digest。诊断只包含稳定 reason、document kind、semantic ID
和 corpus-relative path；禁止 credential、PlayerID、个人资料、奖励/资产事实、本机绝对路径、
Unity Library 路径、完整 package dump 与 secret。
