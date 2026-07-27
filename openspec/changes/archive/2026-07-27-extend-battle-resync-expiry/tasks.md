## 1. Profile v2 契约

- [x] 1.1 将 network profile corpus 升级为 `battle-network-profile-v2`，保持 model、fault、MTU、lane 与 KCP transport 参数不变
- [x] 1.2 将 lane 级 application expiry 改为 route-owned sender expiry 上限，把 resync request/response 冻结为 2250 ms，其他 logical kind 保持原值
- [x] 1.3 明确 receiver reassembly 由 KCP window/queue 与 session lifecycle 有界，禁止复制或推导 sender deadline
- [x] 1.4 重建并验证 profile manifest、model binding 与 canonical qualification report

## 2. Wire 与版本绑定迁移

- [x] 2.1 将 numeric route 3006/3007 expiry 更新为 2250 ms，并保持 3000-3005 的 route policy 不变
- [x] 2.2 更新 battle wire fixture、registry projection 和跨语言 parity 基线，拒绝 profile v1/v2 或 expiry 漂移
- [x] 2.3 迁移 current profile consumer binding、schema 与 manifest并稳定拒绝旧 v1 digest；不为本 change 重建历史 B0.3/B0.5 最终资格报告
- [x] 2.4 更新 B0.6 upstream binding、schema 与 manifest，使资格工具只接受完整 profile v2 identity

## 3. Route policy 驱动的 KCP runtime

- [x] 3.1 在 C++ production KCP adapter 中引入 immutable route sender expiry policy，移除 lane 级 500 ms 默认值
- [x] 3.2 使 KCP queued/inflight sender 状态执行同一绝对 route deadline，并提供稳定 inflight expiry 原因
- [x] 3.3 移除 production adapter 与独立协议客户端的 receiver application reassembly timer，保留 KCP/session 资源边界和完整消息校验
- [x] 3.4 保持 resync generation、2/s rate、单 KCP conversation、queue limit 和 shutdown ownership不变

## 4. 分层测试与反例

- [x] 4.1 增加 profile/registry contract tests，证明一般可靠消息为 500 ms、resync 为 2250 ms
- [x] 4.2 增加 C++ adapter unit/integration tests，覆盖 route mismatch、sender deadline、ordered reassembly 与 caller override
- [x] 4.3 证明 500/1000 ms 在同一 `baseline-gap` 输入下以 sender inflight expiry 失败，资格工具不能用 receiver timer 或定时 resync掩盖
- [x] 4.4 证明 committed Tick 驱动的 10 Hz snapshot、每 10 个发布周期 full baseline 与 2250 ms resync 在同 seed、同 fault pattern 下恢复合法 baseline，且不放宽 2 秒 recovery 与 raw snapshot freshness

## 5. 资格回归与交付

- [x] 5.1 通过统一 `check-change` 执行 profile/registry、Go 与 C++ resync 定向测试，覆盖 sender route expiry、持续 ingress 不饿死 periodic update 与无 catch-up burst
- [x] 5.2 验证 current B0.3/B0.5 consumers 拒绝 profile v1 并接受 v2 binding，不执行历史完整 qualification 或重签最终 report
- [x] 5.3 使用显式 `diagnose` 验证 baseline-gap、KCP retransmit 与相邻 loss 代表性场景，不执行 capacity/security/lifecycle 全矩阵或 soak
- [x] 5.4 更新 battle 网络、协议兼容、资格和路线图文档，明确 profile v2、sender route expiry、receiver reassembly 与未解锁边界
- [x] 5.5 同步 delta specs 到主 specs，执行受影响 change 与全仓 OpenSpec strict validation
