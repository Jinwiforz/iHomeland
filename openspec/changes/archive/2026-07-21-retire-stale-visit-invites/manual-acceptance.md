# 邀请退役验收记录

## 自动验证

2026-07-20 在仓库根目录执行以下验证：

| 验证 | 结果 |
|---|---|
| `tools/proto/proto.ps1 verify` | 通过 9/9 阶段；包含协议格式、兼容性、Go/C# 确定性生成、fixture parity 与服务端全量 Go tests |
| `tools/go/go.ps1 test ./internal/visitsession ./internal/storage/visitsession ./internal/app -count=1` | 3 个目标 package 全部通过 |
| `openspec validate retire-stale-visit-invites --strict` | 通过 |
| `openspec validate --all --strict` | 33 项通过，0 项失败 |
| `git diff --check` | 通过，无空白错误 |

2026-07-20，操作者在包含本 change 最终源码的 Unity 工程中确认全量 EditMode 与 PlayMode tests 均通过。该结果作为人工执行证据单独记录，不虚构未提供的测试数量或报告文件路径。

## 验收版本

| 产物 | 路径 | SHA-256 |
|---|---|---|
| 首次邀请退役服务端 | `.local/server-build/retire-stale-visit-invites/server.exe` | `72E452672C01291AD5F300DAEB29DEC72199D48F5778D07769225AF0BFD08C72` |
| 包含后续修复的当前服务端 | `.local/server-build/recover-idle-gameplay-heartbeat-metrics/server.exe` | `D2AEF553F169A3C1D9CB25A00BD03C12E3562B0E426D2546C41C7A6FF3C6C981` |
| 当前 Windows Development Player | `.local/client-build/windows-development/iHomeland.exe` | `34A412B81651ED571B97F4D1BA71A9CA79457FF5779D56969B8F0C4772AD2CEE` |

`.local` 只保存本机可重建产物，不进入 Git；本记录固定产物 identity，避免仅凭目录名称误认版本。
各证据目录记录对应手测轮次，当前 Player 路径记录审计时仍存在的最新构建；二者不得脱离各自时间戳被解释为同一次构建产物。

## 双客户端人工验收

Owner 与 Visitor 使用两个真实 Windows Development Player 进程完成以下矩阵，并由操作者逐项确认：

| 场景 | 结果 |
|---|---|
| Owner 创建邀请，Visitor 在线收到邀请 | 通过；列表和按钮与当前权威状态一致 |
| Owner 撤销邀请后 Visitor 列表收敛 | 通过；退役项被移除，不能继续接受 |
| Owner 撤销后重新邀请 | 通过；旧 identity 不复活，新邀请可正常接受 |
| Owner 关闭访问 | 通过；Visitor 返回自己的世界，两端无 Visitor、邀请或 VisitSession 残留 |
| 多个历史失效邀请 | 通过；列表不再保留可接受的失效项 |
| 断线窗口内点击残留邀请 | 通过；服务端明确拒绝时保持 OwnWorld，不再伪装成接受成功 |
| 服务端停止、重启并显式重连 | 通过；双方回到自己的世界且可重新邀请，旧状态不回写 |

本地证据位于：

- `.local/client-acceptance/20260720-authority-final/`：Owner/Visitor 日志、撤销/关闭/返回 OwnWorld 与服务端恢复截图。
- `.local/client-acceptance/20260720-commercial-final/`：accept、leave、reinvite、revoke/reinvite 与断线恢复日志和截图。
- `.local/client-acceptance/20260720-state-consistency-final/`：重连、焦点、页面状态及双端日志。

日志与截图不得复制 credential、ticket、admission、access token 或完整协议 payload 到 OpenSpec artifact。
