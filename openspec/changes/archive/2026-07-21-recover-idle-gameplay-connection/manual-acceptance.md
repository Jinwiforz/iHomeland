# Windows Development 双客户端手动验收

## 准备

1. 结束旧服务端，再使用 `.local/server-build/recover-idle-gameplay-heartbeat-metrics/server.exe` 和原 `server/config/local.yaml` 启动新服务端。
2. 打开 Unity，让 Unity 为直接覆盖的 ignored protocol generated 目录重新生成 `.meta`；确认 Console 没有编译错误。
3. 构建 Development Client，并启动 Owner、Visitor 两个独立进程。

## 验收矩阵

| 场景 | 操作 | 必须观察到的结果 |
|---|---|---|
| 基线 | 两端登录、进入 own world，Owner 开放访问并邀请 Visitor | 两端角色、VisitSession、visitor/invite 列表都与服务端响应一致；按钮不会因重复点击进入未知状态 |
| 短时静默 | 两端在世界内不操作至少 2 分钟，再各执行一次允许的操作 | 连接保持可用；没有网络错误、自动返回或状态残留 |
| 完整 idle | 两端在世界内不操作超过 31 分钟，再各执行一次允许的操作 | heartbeat 持续保活，服务端 30 分钟 safety idle 不会误关健康连接 |
| 断网 | 在世界内断开网络，保持至少 30 秒 | 最迟在 heartbeat interval + deadline 内进入唯一 `ConnectionLost`；不得永久停在 `EnteringOwnWorld`，不得继续显示可操作的旧 target |
| 服务端停止 | 在世界内停止服务端 | 两端收敛到可重试断线界面；Development log 只出现 generation/stage/close reason/exception type，不出现 credential、payload、账号或 endpoint |
| 重复重试 | 保持服务端关闭，点击一次重试并继续点击 | 只有第一笔恢复取得 single-flight；约 45 秒后回到 `ConnectionLost`，按钮重新可操作，不创建后台无限重试 |
| 服务端恢复 | 重启服务端后点击重试 | 重新解析并进入 own world；旧 gameplay generation、旧 visitor role 和旧 target UI 不得回写。Owner 持久化的开放访问会话仍以服务端当前事实为准 |
| 重连页面收敛 | 在 `ConnectionLost` modal 点击重新连接并成功进入 own world，保持至少 30 秒 | modal 在 Scene/HUD 提交后关闭且不在下一帧重新出现；control WSS 保持 `Connected`，关闭旧 route 不得触发新的 control disconnect |
| Owner/Visitor 终态 | 再次建立邀请访问，分别执行撤销邀请、Visitor 离开、Owner 移除、Owner 关闭访问 | 失效邀请不可接受；双方最终角色、列表和按钮与服务端当前 snapshot 一致 |
| Client shutdown | 在正常 idle、heartbeat 等待和重试 pending 三种时机分别关闭 Client | 进程正常退出，无未观察异常、对象销毁后回写或旧 generation 日志持续出现 |

## 失败取证

若任何场景失败，保留两个 Client 的 `Player.log`、服务端结构化日志、发生时间和当时操作顺序。不要复制 ticket、admission、access token 或完整协议 payload。
