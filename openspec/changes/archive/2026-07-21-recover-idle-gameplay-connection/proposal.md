## Why

当前 gameplay TCP 在 30 分钟没有业务帧后由服务端按 `idle_timeout` 关闭；客户端虽然能发现断线，但显式重连可能长期停留在 `EnteringOwnWorld`，且底层异常被折叠后缺少可定位的诊断信息。个人世界是长驻产品场景，合法静默连接必须由明确的保活协议维持，任何恢复事务也必须在有限 deadline 内提交成功或进入稳定失败态。

## What Changes

- 为 TLS/TCP gameplay 增加独立、低成本、带 request/response correlation 的 heartbeat 路由；它只证明当前连接存活，不读取或修改 PersonalWorld/VisitSession 业务状态。
- 客户端 gameplay channel 在 active generation 内以单 owner 运行 heartbeat，在响应超时、transport 失败或 generation 撤销时走统一 terminal close；静默玩家不再因缺少业务 command 触发服务端 idle timeout。
- 为 own-world 显式恢复事务增加统一 deadline 和稳定失败收敛，确保 UI 不会无限停留在 `EnteringOwnWorld`。
- 保留安全、低基数的公开失败分类，同时在 Development/内部诊断中记录被折叠异常的 component、operation、generation、stage 和异常类型，不记录 credential、payload 或 endpoint。
- 补充服务端真实 TCP 与客户端纯 C# 生命周期测试，覆盖静默保活、heartbeat 丢失、断线重连、重复点击和 shutdown 竞态。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `network-transport`: 明确 TLS/TCP gameplay heartbeat 的唯一通道、delivery、deadline、rate limit 与故障语义。
- `server-tcp-gameplay`: 接受并响应已认证 active connection 的 heartbeat，以成功业务或 heartbeat I/O 共同刷新 idle deadline。
- `client-tcp-gameplay`: 为 active generation 增加单 owner heartbeat、终止原因诊断和有界关闭行为。
- `client-personal-world-services`: 显式连接恢复必须在统一 deadline 内进入成功或稳定失败状态，并使 UI 与权威 flow snapshot 一致。

## Impact

- 协议源与 registry 新增 common owner 的 heartbeat request/response message ID；不改变既有业务 message 语义，不提供兼容降级或跨通道双写。
- 服务端影响 `contract`、TLS/TCP codec/dispatcher、连接 deadline、metrics 和协议测试客户端。
- 客户端影响 gameplay channel、world admission/experience 恢复事务、Development 诊断与纯 C# 测试。
- 不改变 MySQL、Redis、VisitSession mutation、Scene、Prefab、ScriptableObject 或 UI 布局；不生成或修改 Unity `.meta`。
