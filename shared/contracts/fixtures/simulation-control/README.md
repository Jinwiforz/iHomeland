# Simulation Control 契约

本目录是 B0.4 Go↔C++ 私有进程控制契约的唯一 source of truth。它只描述由 Go
父进程通过继承 stdin/stdout handles 驱动的低频 lifecycle frame，不是客户端协议、
battle wire、公开 listener 或服务发现契约。

## Owner 与边界

- Owner：`go-simulation-control`
- Consumer：Go `server/internal/simulationcontrol` 与 C++ `ihomeland_sim_control_adapter`
- Transport：4-byte big-endian payload length + UTF-8 canonical JSON
- Hard frame limit：65536 bytes
- Pending request hard limit：256
- Result outbox hard limit：256 entries/SimulationNode
- Actor qualification：最多 8 actors/instance

Frame 顶层只允许 `schemaVersion`、`sessionNonce`、`sequence`、`requestId`、`kind`
和 `payload`。所有可能超过 JSON 精确整数范围的值使用无前导零的十进制字符串。
`sessionNonce` 是每次 child 启动唯一的 256-bit 小写十六进制值，不是网络 credential。

## 消息

`message-inventory.json` 登记全部允许 kind、方向、大小、deadline class 与 replay
identity。`cases/` 提供正向 lifecycle/result 和负向 mutation 描述；
`canonical-golden.json` 固定跨语言 frame bytes。`runtime/` 保存
`control-baseline-v1` 的 config、Detour navigation 与 Jolt physics source；
manifest 中各文件的 SHA-256 就是 start binding 使用的 identity，禁止以重复字符或
无 source 的魔法 digest 替代。

本目录禁止出现：

- gRPC、HTTP、TCP/UDP listener 或 production port；
- battle numeric message ID、raw/KCP lane、ticket、cookie、AEAD 或 replay window；
- Account credential、raw token、玩家个人资料、奖励或资产 payload；
- Unity、生成协议代码、本机绝对路径或 secret。

工具只能读取并验证 source，不能自动重写 digest、golden 或 expected outcome。
