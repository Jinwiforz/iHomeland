## 1. 协议源与生成绑定

- [x] 1.1 为 full/delta snapshot Protobuf message 增加具有显式 presence 的 `last_processed_input_tick`，并补充符合规范的中文字段注释。
- [x] 1.2 更新 battle wire schema、registry 与 manifest metadata，不改变 1200-byte datagram 或 route payload 预算。
- [x] 1.3 通过仓库统一生成入口重新生成 Go、C++ 与 C# 协议绑定。
- [x] 1.4 增加生成物与漂移检查，证明生成绑定和 descriptor 与协议源一致。

## 2. 模拟输入确认投影

- [x] 2.1 将 actor `InputTimeline` 的连续终结前沿公开为 mapping generation 范围内的不可变 projection。
- [x] 2.2 后继 mapping generation 建立时将 projection 重置为零，并拒绝旧 generation 推进当前前沿。
- [x] 2.3 增加 simulation 测试，覆盖 gap 阻塞、gap 稳定终结、重复/过期输入与 generation replacement。
- [x] 2.4 证明 network/replication consumer 读取冻结 projection，而非可变 simulation state。

## 3. 快照发布与 partition 约束

- [x] 3.1 使用已认证 actor 的冻结确认 projection 填充 full/delta snapshot。
- [x] 3.2 同一逻辑 snapshot 的全部 partition 必须冻结完全一致的确认值。
- [x] 3.3 在 observer 发布前拒绝字段 presence 缺失、partition 确认值漂移和陈旧 mapping generation。
- [x] 3.4 增加 MTU/预算测试，覆盖零值、大 varint 与新增 partition 的边界。

## 4. 独立协议客户端与资格 observer

- [x] 4.1 在独立 C++ 协议客户端中解码并校验 full/delta snapshot 的确认字段 presence。
- [x] 4.2 仅在已认证 partition 重组、sequence、baseline 和 generation 全部校验通过后发布确认值。
- [x] 4.3 扩展 Go supervisor/workload observer，维护 actor 与 mapping generation 范围内的确认状态。
- [x] 4.4 关联 input window 与确认前沿推进，不从 `ServerTick` 或日志文本推测。
- [x] 4.5 增加 own/visit、reconnect、loss/reorder/duplicate 与 backpressure correlation 测试。

## 5. 跨语言 fixture 与证据

- [x] 5.1 增加 full/delta canonical fixture，覆盖显式零值、常规值、大 varint 与多 partition 确认值。
- [x] 5.2 增加字段 presence 缺失、partition 漂移与陈旧 generation 的 negative fixture。
- [x] 5.3 验证 Go/C++/C# 的 bytes、presence、decode result、negative disposition 与 digest parity。
- [x] 5.4 通过各自权威工具更新 fixture/registry manifest 及受校验 digest。

## 6. 集中验证

- [x] 6.1 运行定向 simulation、transport、protocol-client 与 Go qualification 测试。
- [x] 6.2 通过统一 `check-change` 运行 acknowledgement 影响面的 C++/Go 定向测试、必要 sanitizer 与静态门禁，不执行 clean 全量资格。
- [x] 6.3 验证 battle wire corpus、跨语言生成与当前 producer/consumer contract，不重新生成 B0.5 最终资格报告。
- [x] 6.4 使用显式 `diagnose` 验证 clean input→ack、loss/reorder/gap 与 reconnect generation reset 代表性场景，不执行完整 B0.6 matrix、soak 或 finalize。
- [x] 6.5 运行本 change OpenSpec strict 并核对受影响架构与协议文档；server/client 完整产品资格留给用户显式最终验收。
