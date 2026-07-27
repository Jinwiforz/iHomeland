## MODIFIED Requirements

### Requirement: 资格证据必须低敏、完整、可审计且清理干净

项目的统一质量入口 MUST 将 battle qualification 的 `validate`、定向 `diagnose`、完整
`verify`、长时 `soak` 与 `finalize` 作为内部 owner actions 暴露。Change-level
`impact`、`check-change` 与 `diagnose` MUST NOT 产生资格结论；只有用户对 clean frozen
candidate 显式调用最终 `qualify`，才可连续消费同一 identity 下两次完整 verify、
mandatory soak、安全矩阵、当前 mandatory capability regression 和 OpenSpec strict
evidence，生成 `battle-network-qualified-windows-x64-controlled` 报告。报告 MUST 绑定
全部 source/binary/config/environment/fault/workload digest，逐场景记录 measured value、
unit、budget、disposition、run evidence digest、cleanup 和 scope；missing、failed、
skipped、stale、unsupported、unclassified 或 cleanup failure 任一存在时 MUST
not-qualified。Tracked corpus 与报告不得包含 raw ticket、proof/traffic key、cookie
secret、完整 credential、玩家资料、payload dump 或本机绝对路径。

Qualification tooling change MAY 用 validators、failure regression、scope/security tests
和代表性真实场景证明工具可用，而不为变化中的开发工作区生成 qualified report；这不得把
未运行的完整矩阵标记为通过，也不得降低未来最终资格的 mandatory coverage。

#### Scenario: 连续运行结论不一致

- **WHEN** 显式最终资格的两次完整 verify identity 相同但任一 mandatory scenario disposition 不同、测量超出可重复容差或 cleanup 不一致
- **THEN** finalize 拒绝生成 qualified report，并要求保留两个低敏 run digest 供定位

#### Scenario: 全部门禁通过

- **WHEN** 用户显式冻结的 candidate 通过两次 verify、mandatory soak、安全负例、当前 mandatory regression、strict validation 与 cleanup
- **THEN** 报告可声明 `battle-network-qualified-windows-x64-controlled`，但不声明 Linux、公网运营商、未包含的 Unity runtime 或产品内容已 qualified

#### Scenario: Qualification tooling 已完成但未执行最终资格

- **WHEN** corpus、gateway、独立协议客户端、runner、failure regression、代表性真实场景和统一显式入口均通过，而用户尚未请求完整最终资格
- **THEN** tooling change 可以完成且报告保持未生成或 not-qualified，完整矩阵、连续 verify 与 soak 继续作为可随时执行的最终动作
