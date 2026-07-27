# Battle 网络资格运行手册

## 资格边界

`tools/quality/quality.ps1` 是项目唯一公开质量入口。普通开发 change 使用 `impact` 和
`check-change`；真实网络排障使用显式 `diagnose`；只有使用者明确要求并传入 clean
current HEAD 时才使用 `qualify`。底层
`tools/battle-qualification/battle-qualification.ps1` 是内部 owner，负责在 Windows x64
`controlled-local-fault-gateway` 环境中驱动真实 Go Composition Root、C++
SimulationNode、单 production UDP listener、独立 C++ 协议客户端和 opaque fault
gateway。

B0.6 tooling、failure regression 与代表性 development-readiness 可以在没有最终报告时
完成。只有完整 `qualify` 才能生成
`battle-network-qualified-windows-x64-controlled`；`diagnose`、定向测试、单次
`verify` 或手工观察均不能产生该结论。该标签也不代表公网运营商、Linux、
Unity/IL2CPP 或 production 放量已通过。

## 本地前置条件

- PowerShell 7 用于 B0.6 入口；仓库脚本会按需调用 Windows PowerShell 兼容入口。
- `tools/go/go.ps1`、`tools/cpp/cpp.ps1` 与 `tools/proto/proto.ps1` 是唯一受支持的
  Go、C++ 和协议工具入口；禁止使用系统同名工具替代锁定版本。
- C++ 依赖、CMake、MSVC Build Tools 与 Windows SDK 缺失时，先运行
  `tools/cpp/cpp.ps1 bootstrap`。恢复完成后的构建阶段按离线策略执行。
- MySQL/Redis 由 `tools/storage/storage.ps1` 的 Docker harness 创建；Docker 不可用时
  资格必须 fail closed，不能换成未登记的本机数据库。
- Unity 不参与 B0.6，已安装 Unity 也不能替代独立协议客户端或提前实现 B0.7 runtime。

## 日常开发：只验证当前影响面

先预览 change 会运行什么。该动作不构建、不启动 listener，也不创建 evidence：

```powershell
& .\tools\quality\quality.ps1 impact -Change <change-name>
```

确认后执行该 change 的 closed validation plan：

```powershell
& .\tools\quality\quality.ps1 check-change -Change <change-name>
```

`validation.json` 只能引用中央 catalog 登记的 owner checks。普通 plan 不允许包含完整
矩阵、连续 verify、soak 或 finalize；unknown check、任意命令文本、重复项与缺失原因会在
执行前失败。共享协议、registry、generated binding 或 identity 变化仍必须登记全部直接
consumer 的 contract/parity check。

已知真实网络风险可单独诊断：

```powershell
& .\tools\quality\quality.ps1 diagnose `
  -Scenario real-baseline-gap `
  -TimeoutSeconds 7200
```

相同 source/dependency/tool identity 的重复诊断可以复用 ignored Go binary cache 和
增量 C++ build，但每次都会新建 credential、endpoint、process、evidence 与 cleanup
owner。诊断 cache、RunId 和结果永远不能进入最终资格。

## 显式最终资格

当功能已经达到使用者希望冻结的里程碑，先提交所有预期 source、contract、配置和工具
变化，确认 worktree clean，再显式传入当前 commit：

```powershell
$candidate = git rev-parse HEAD
& .\tools\quality\quality.ps1 qualify `
  -Candidate $candidate `
  -TimeoutSeconds 14400
```

统一入口会在启动 build、storage 或 listener 前拒绝 dirty worktree、非 current HEAD 或
缺失 candidate。通过后，它自动调用既有 owners：验证 current profile，构建一次当前
C++ 候选，验证 simulation control 与 secure transport，运行完整 fault/capacity/
security/lifecycle capability suites、连续两次 battle `verify`、mandatory 30 分钟 soak
和 `finalize`。使用者不需要手工管理 B0.3～B0.6 顺序或 RunId。

完整资格执行后不得因时间压力减少 actor、故障、安全、生命周期、soak 或 cleanup
coverage；任一 mandatory 项 missing、failed、skipped、stale、unsupported 或
unclassified 都保持 not-qualified。任何后续 source、contract、配置、tool 或 harness
变化都会使该候选结论失效，下一次只需对新的当前产品候选再次运行统一 `qualify`，不按
历史 change 编号逐层重建。

## 指标与证据口径

- Gateway 只记录方向、长度、公开 packet kind、规则 disposition 与单调时间，不解密或
  改写 payload。
- NAT rotation 先创建 pending successor，但客户端 uplink 的公开 `Control` kind 不能
  提交 mapping。只有 connected successor socket 实际观察到 C++ backend 返回的
  `Control` 响应后，Gateway 才原子提升 successor，并从该时刻开始计算 predecessor
  的 3 秒迟到窗口；pending、active 与 predecessor 始终由同一 event loop owner 修改。
- 独立协议客户端用 8-byte `network-transition-event-v1` 返回互斥成功或失败终态。
  `failureCode=1..35` 按 rebind/rekey/close/old-epoch operation 及
  request/receive/authentication/application/interleave/validate 阶段封闭分类，
  编排器拒绝跨 operation 的失败码，日志不得包含 endpoint、payload 或 socket 文本。
- `session-event-v1` 成功保持 2-byte receipt；握手失败使用 3-byte 闭合变体，
  `failureCode=1..10` 只报告 request/deadline/socket/handshake/Retry/ServerAccept/
  transport 阶段。Supervisor 不再把 child 的低敏失败降级为无上下文 EOF，也不得输出
  底层 WinSock 错误或 endpoint。
- `delivery-age` 固定为成功 socket write 的 `DeliveredAt - ReceivedAt`。Intentional
  loss、burst loss 和客户端 snapshot receipt gap 不计入单 packet delivery age。
- Snapshot cadence 由 committed SimulationTick 驱动：20 Hz simulation 下每 2 Tick
  至多发布一次，每 10 个正常发布周期建立 full baseline；持续 ingress 不得饿死
  periodic update，调度延迟不得产生 catch-up burst。
- Baseline recovery 从合法 resync 到 successor full baseline 计算，最大值仍为
  2,000,000 µs；2250 ms resync sender expiry 不放宽该预算或 raw snapshot freshness。
- 合法 resync 的首次 raw full 后，服务端在固定 30 committed Tick 窗口内按既有
  2/s 上限补充 full；重复请求不延长窗口，调度延迟不追赶补发。
- 一般 reliable event/lifecycle sender expiry 为 500 ms，resync request/response 为
  2250 ms；receiver 不复制 sender deadline。
- Gateway、client、C++ control snapshot 与 OS process sampler 的窗口必须交叉守恒。
  Missing sample、未知 disposition、identity 漂移或预算超限均为 not-qualified。
- Measurement 结束后，Gateway 先停止产生新的 fault decision，再由 event-loop barrier
  等待全部已调度 packet copy 获得 delivered/dropped/expired 终局；禁止用固定 sleep、
  提前丢弃或清空队列伪造收敛。
- Authenticated close 只有在 `CloseAcknowledged` 已进入唯一 production UDP listener
  队列后才能登记 normal reason；output failure 必须 fail closed，不能让客户端失败与
  服务端正常关闭证据同时成立。
- Close client 在同一 cleanup deadline 内重放 exact sealed request，server 在销毁
  session 前排队三个独立认证 acknowledgement copy；任何重试都不得重置 deadline。
- Windows listener 在首次 receive 前关闭 `SIO_UDP_CONNRESET`，使发往已关闭旧
  客户端端口产生的 ICMP 只终结该 datagram，而不能关闭 node-global socket；该
  socket policy 无法建立时 listener 必须启动失败，不能降级运行。

## Evidence 与清理

每次运行只在 `.local/battle-qualification/<run-id>/` 保存原始日志、低敏 packet metadata
和 run index。该目录被 Git 忽略，不得手工改写后复用。显式最终 `qualify` 的长期低敏 report 与
profile implementation overlay 写入
`shared/contracts/evidence/battle-network/`，并继续受 schema、secret/path 和 digest
gate 约束。该目录是资格输出而不是候选源码，不参与 `sourceSha256`，避免报告绑定自身；
报告中的其余 source、binary、config、environment、fault 与 workload identity 仍必须
完整匹配。

入口只清理自己持有的精确 Go/C++ process、临时 listener、storage run 与可复用
credential。任何 cleanup failure 必须与主失败同时保留，不能通过按进程名终止、删除
宽泛目录或手工标记 cleanup pass 掩盖。Authenticated close、关闭后 control 采样与
逆序资源释放共同消费 manifest 登记的单个独立 cleanup deadline；不得继承已经接近
耗尽的 scenario deadline，也不得为每个 cleanup 阶段重新计时。

## 失败定位与回滚

失败时先读取 run index 的首个失败 stage，再按 gateway、client、control snapshot 和
process sampler 的同窗口 evidence 定位。不得通过降低 loss/jitter/duration、减少 actor、
扩大 budget/tolerance、改变 MTU/lane/KCP 参数或跳过 unsupported gate 获得绿色结果。

若冻结 profile 与真实实现确实冲突，必须以独立 OpenSpec change 提供反例、参数推导、
迁移和完整回归。回滚必须同时恢复 profile、registry、runtime/client、manifest 与所有
下游 binding；禁止只改一个 expiry 常量或复用旧 report。
