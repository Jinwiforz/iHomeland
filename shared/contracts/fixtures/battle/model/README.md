# Battle Simulation Model Fixtures

本目录是首个 PersonalWorld 服务器权威 gameplay 纯模型 corpus 的唯一 owner。它冻结无网络 simulation harness 可消费的输入、记录化 adapter 结果和预期输出，不定义 Protobuf、message ID、UDP/KCP lane、socket framing、C++ 类型或 Unity 类型。

## 版本

- `format_version`：fixture 容器格式，当前固定为 `"1"`。
- `model_version`：玩法状态迁移语义，当前固定为 `"battle-model-v1"`。
- `config_version`：case 使用的示例配置版本，当前固定为 `"battle-config-v1"`。
- 任一字段、枚举、canonical 语法或状态迁移变化都必须通过 OpenSpec；不兼容变化提升对应版本，不得静默改写既有 case。

`schema.json` 是每个 case 的闭合结构源，`manifest.json` 是 case 与 requirement coverage 的登记源，`assumptions.json` 是 B0.2 网络 profile 的 workload 输入。`cases/` 中没有被 manifest 登记的文件和 manifest 中不存在的路径均视为漂移。

## Identity 与排序

所有 ID 使用 lowercase kebab-case ASCII，并由所属 owner 生成或配置：

- `actor-*`：SimulationInstance 内稳定 actor identity；不能替代 PlayerID。
- `entity-*`：非 actor entity identity，例如 projectile。
- `activation-*`、`effect-*`、`query-*`、`event-*`：当前 model generation 内的稳定语义 identity。
- `assignment_fingerprint`：仅用于关联 current AssignmentStamp 的低敏 SHA-256 示例，不包含 node、fence、lease、Session 或 credential。

数组必须按 schema 与 validator 规定的稳定 key 排序。Command 的处理顺序为 `target_tick`、`actor_id`、`input_tick`、`sequence`、`kind`；`arrival_order` 只记录网络到达扰动，不参与裁决。Physics hit 按 `fraction_millionths`、`collider_id`、`subshape_id` 排序。状态、事件和拒绝使用显式 `order` 或稳定 identity，不能依赖 JSON object、hash map、pointer 或 callback 的迭代顺序。

## Tick 与单位

- `S0` 是实例初始化完成后的状态；`SimulationTick T` 从 `S(T-1)` 产生完整 `S(T)`。
- `server_tick` 只表示已经完成的后状态。
- `input_tick` 是当前 mapping/session generation 内从 1 开始的采样索引。
- 时间步使用 `ns`，距离使用 `mm`，速度使用 `mm_per_s`，角度使用 `millirad`。
- 物理命中比例使用 `[0, 1_000_000]` 的 `millionths`。
- gameplay Attribute 使用 `milli_attribute` 的 signed 64-bit scaled integer；乘法回到 scale 时 toward zero。
- Tick、sequence、generation 与容量均使用非负十进制整数；需要正值的字段由 schema 明确限制。

Mapping 使用 checked integer arithmetic：

```text
mapped_tick =
  base_simulation_tick
  + floor((input_tick - base_input_tick) * input_step_ns / simulation_step_ns)
```

精确 cadence、提前/迟到窗口、history 长度和 queue/bandwidth 预算是 B0.2 的 `profile_output`，本目录只提供待测参数与 workload。

## Canonical 输出与摘要

每个 case 的 `expected.canonical_output` 使用 UTF-8、无 BOM、LF 语义的单行 ASCII token stream。Token 顺序固定为：

```text
queries|states|events|rejections|capacity
```

各段由 case 明确列出的 expected collection 生成；空段使用 `-`，token 内只使用登记 ID、十进制整数、`=`、`,`、`:`、`;` 和 `/`。`expected.canonical_sha256` 是该单行文本 UTF-8 bytes 的 lowercase SHA-256。它是后续 C++ harness 比较 query/state/event/rejection/capacity 结果的稳定摘要，不是安全签名，也不证明结算或持久化完成。

`manifest.json` 另外记录每个 case 文件原始 bytes 的 `file_sha256`，用于发现 schema 合法但未同步登记的 corpus 修改。所有 JSON 必须为 UTF-8 无 BOM、LF、文件末尾单个换行。

## 安全与隐私

Fixtures 只能使用虚构 gameplay identity 和低敏 assignment fingerprint。以下内容禁止出现：

- username、email、PlayerID、SessionID/epoch、真实 PersonalWorldID 或 VisitSessionID；
- password、token、ticket、credential、nonce、AEAD key 或其他 secret；
- message ID、channel/lane、endpoint、IP、端口、packet、frame 或 wire layout；
- Jolt、Detour、Asio、KCP、UnityEngine 或其他第三方运行时对象/type dump；
- 账号、资产、奖励、聊天和个人数据。

Validator 对闭合 schema、禁止字段/文本和路径逃逸 fail closed。Fixtures 不授予 admission，不覆盖连接 actor，也不成为 Go settlement owner。

## 消费边界

### B0.2 Network Profile

`define-battle-network-profile` 读取 `assumptions.json` 和 case 的 command/state/query/event 维度，测量并填充 tick cadence、窗口、最大 actor/profile、history、snapshot、queue、CPU、memory 和 bandwidth。B0.2 可以新增 profile evidence，但不得改写既有模型状态迁移来迎合网络参数。

### B0.3 C++ Simulation Core

无网络 C++ harness 读取 manifest/cases，注入记录化 physics/navigation/random trace，执行唯一 simulation model，并比较 expected query/state/event/rejection/canonical digest。Harness 不启动 production listener，不导入 Go domain 或 Unity 类型；Jolt/Detour adapter 的 live 差异必须定位在 port boundary。

### Validator

`tools/battle-model/battle-model.ps1 -Action validate` 只验证格式、引用、排序、coverage 和摘要。它不计算 movement、damage、AI 或 history 结果，不得演变为第二套 gameplay engine。
