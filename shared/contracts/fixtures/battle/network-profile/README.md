# Battle Network Profile

本目录是 `battle-network-profile-v1` 的唯一 source of truth，由 `battle-network-profile` capability 拥有。它消费 `../model/` 中冻结的 `battle-model-v1`，用于选择网络 cadence、窗口、baseline、MTU、逻辑 lane、KCP 参数和资源预算；它不拥有 gameplay 状态迁移，也不修改 model corpus。

## 版本与文件

- `format_version`：profile 文件结构版本，当前为 `1`。
- `profile_version`：消费者契约版本，当前为 `battle-network-profile-v1`。
- `model-binding.json`：完整绑定 model manifest、assumptions、cases 与 requirements 的 SHA-256。
- `profile.json`：selected candidates、参数分类、MTU/KCP、capacity 和后续补证门。
- `message-inventory.json`：稳定 logical kind 与唯一 raw/KCP lane；不是 production wire registry。
- `fault-matrix.json`：固定 seed、有限网络故障场景与 workload/phase coverage。
- `cases/`：按 cadence、baseline、reliable、MTU、overload 和 negative 分类的 coverage 输入。
- `reports/qualification.json`：由同一 source、tool version 与 seed 重放得到的低敏 canonical evidence。
- `schema.json` 与 `manifest.json`：闭合结构、双向登记、稳定排序和文件摘要。

## 规范化规则

所有 JSON 必须使用 UTF-8 无 BOM、LF、两个空格缩进和单一末尾换行。对象字段顺序以已提交文件为准；需要排序的数组使用 ordinal 升序。文件摘要是原始 canonical bytes 的 lowercase SHA-256。整数必须位于 schema 范围内，时间、大小、频率、角度和距离必须携带登记单位，禁止依赖 locale、wall clock、线程或本机路径。

逻辑 message kind 使用 `battle.<area>.<name>`，只表达 profile 语义。numeric message ID、Protobuf layout、datagram header、production endpoint、listener 和端口等待安全 battle transport change。每个 logical kind 只能有一个 `raw` 或 `kcp` lane；调用方不得动态换 lane或在 UDP 失败时静默转入现有 TLS/TCP/WSS。

## 资格分类

- `profile_qualified`：本目录的确定性 fault matrix 和静态 byte model 已重复证明。
- `target_budget`：后续实现必须满足的明确上限或目标，不是实测实现结果。
- `implementation_required`：需要真实 C++、codec、socket、KCP adapter、AEAD 或平台网络补证；值为 `0` 表示尚无实现测量，不表示无限或已通过。

`profile_status=qualified` 只表示 B0.2 的模型绑定、逻辑网络行为和预算选择完整。它不表示 C++ runtime、安全 UDP/KCP 或 Unity gameplay 已 qualified。

## Workload 与容量

profile 必须覆盖 `solo-owner`、默认 `default-coop`（1 Owner + 4 Visitor）和 `visit-capacity-compatibility`（1 Owner + 32 Visitor），并覆盖 `idle`、`movement-heavy`、`combat-heavy`、`boss-burst`、`disconnect-drain`。默认 5 actors 是 B0.2 hard gate；33 actors 只用于暴露配置兼容性。若 profile 上限更低，后续 battle admission 必须在签发资格前执行显式 capacity gate，当前 Go v1 VisitSession 不在本 change 修改。

## 安全与隐私

本目录只允许低敏合成 identity、相对路径、逻辑 message kind 和预算。禁止 raw credential、ticket、token、AEAD key、nonce、账号或真实玩家资料、IP/endpoint、production message ID、端口、generated wire code 和本机绝对路径。安全 header 与 AEAD 仅以 byte budget 预留，不能携带真实材料。

## Consumer 边界

- B0.3 C++ core 必须消费相同 model binding、cadence、history、capacity 和 target budget，不得使用隐藏默认值。
- B0.5 安全 battle transport 必须把每个 logical kind 一一映射到 numeric message ID，并验证 header/AEAD/MTU 与真实 KCP adapter parity。
- B0.6 network qualification 必须用真实进程和 fault injection 重放同一 profile，补齐所有 `implementation_required` 项。
- Unity gameplay runtime 只能在上述发布门完成后消费已冻结 snapshot、baseline、interpolation 和 correction 参数。

唯一入口为 `tools/battle-network-profile/battle-network-profile.ps1` 的 `validate` 与 `simulate`。两种动作均只读，不启动 listener、Docker、Go server、Unity、C++ runtime，也不安装第三方依赖或重写 corpus。
