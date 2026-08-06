# Battle Wire Source Corpus

本目录冻结 B0.5 安全战斗传输的源契约，不保存运行时生成物、真实凭据或资格结论。

- `schema.json`：所有 source document 的 closed JSON Schema。
- `manifest.json`：文件集合、内容摘要和必须覆盖的契约域。
- `model-profile-control-binding.json`：B0.3 battle model、B0.2 network profile 与 B0.4 simulation-control 的精确绑定。
- `limits.json`：wire、AEAD、握手、replay、KCP、队列和 actor 的硬上限。
- `wire-layout.json`：除 secure/raw/KCP header 与 acknowledgement 外，还冻结 snapshot 七个 transform scalar、archetype/weapon/max-health content 字段、weapon delta mask、health/lifecycle cross-field invariant、1200-byte partition projection，以及 bits 0–3 phase、bit 4 grounded、bit 31 dead 的 `state_flags` registry；unknown ID、bit 或 presence mismatch 必须拒绝。
- `content_mapping` 精确绑定 production `wire-mapping.json` 的 source digest；旧 digest consumer 必须在 ticket/socket 前 fail closed，不能忽略新增字段加入 current instance。
- `wire-suite.json`：跨语言实现必须消费的正向、边界与 malformed case inventory。
- `secret-policy.json`：fixture、日志、报告和 failure output 的低敏规则。

`tools/secure-battle-transport/secure-battle-transport.ps1 validate-corpus` 只读校验本目录。`test-corpus` 会连续校验两次并比较目录摘要，任何校验器改写、未登记文件、摘要漂移、上游 binding 漂移或 secret 命中都必须失败。

本 corpus 只声明实现契约。B0.5 的 wire golden、实现测量和资格报告必须写入独立派生目录，不能回写这里，也不能据此声称 B0.6 网络故障矩阵已通过。
