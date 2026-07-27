# 安全战斗传输部署示例

## Owner 与启动边界

Go Composition Root 依次启动 MySQL/Redis、exact C++ child 与私有 stdio control。C++
`SimulationNode` 成功绑定唯一 UDP listener 后，Go 才允许公开
`issueBattleTicket`。任一阶段失败都按相反顺序回滚；端口冲突必须使启动失败，禁止自动
递增、随机 fallback 或复用 diagnostic、HTTP/WSS、TLS/TCP listener。

## 本地配置

仓库 `server/config/local.yaml` 使用 `127.0.0.1:58445` 作为 bind 与 advertised
endpoint。开发机冲突时通过白名单环境变量显式覆盖：

```powershell
$env:IHOMELAND_BATTLE_UDP_BIND_ADDRESS = "127.0.0.1:59445"
$env:IHOMELAND_BATTLE_UDP_ADVERTISED_HOST = "127.0.0.1"
$env:IHOMELAND_BATTLE_UDP_ADVERTISED_PORT = "59445"
```

启动前可执行 `Get-NetUDPEndpoint -LocalPort 58445` 检查本机冲突；最终权威判断仍是
child 的真实 bind 结果。隔离 loopback 测试可以使用 `127.0.0.1:0` 并从 listener
回读实际端口；port `0` 不得进入可部署环境或 BattleTicket。

## 容器与生产映射

以下 Docker Compose 片段只说明 bind、NAT 与 advertised endpoint 的映射关系；显式
最终网络资格通过前它只能用于部署预演，不代表 production 网络资格已经通过：

```yaml
services:
  server:
    ports:
      - "58445:58445/udp"
    environment:
      IHOMELAND_BATTLE_UDP_BIND_ADDRESS: "0.0.0.0:58445"
      IHOMELAND_BATTLE_UDP_ADVERTISED_HOST: "battle.example.invalid"
      IHOMELAND_BATTLE_UDP_ADVERTISED_PORT: "58445"
      IHOMELAND_BATTLE_DERIVATION_KEY_SECRET: "file:/run/secrets/battle-derivation-key"
    secrets:
      - battle-derivation-key
```

生产环境必须把 bind、NAT/负载均衡映射与 advertised endpoint 作为同一部署单元审计。
只有 advertised endpoint 可进入 HTTPS BattleTicket；diagnostic、Redis、MySQL 与
stdio control 均不得随 UDP 映射暴露。防火墙只允许预期 client ingress，且不允许 UDP
response 放大、跨 node ticket 或未认证业务 payload。

## 本地资格与故障排查

工具必须使用仓库锁定版本；缺失依赖只允许由统一入口恢复到 `.local/`。Unity 使用
`client/ProjectSettings/ProjectVersion.txt` 锁定的 Editor，CMake、第三方 C++ source
和 evidence 均不得从系统默认路径隐式替换。

普通 change 先预览并执行自身的 closed validation plan：

```powershell
.\tools\quality\quality.ps1 impact -Change <change-name>
.\tools\quality\quality.ps1 check-change -Change <change-name>
```

已知网络问题使用 `quality.ps1 diagnose -Scenario <scenario>` 单独定位。只有使用者明确
冻结里程碑、提交全部预期变化且 worktree clean 后，才执行
`quality.ps1 qualify -Candidate (git rev-parse HEAD)`；该入口会统一调度 C++、服务端、
客户端契约和 battle qualification owners。不得手工重放历史 B0.x verify/finalize，
也不得把旧报告交给下游门禁冒充 current report。

真实协议客户端若以 EOF 终结，先检查 server 是否已因 ticket/session/assignment revoke
删除对应 session，再检查同 listener send counter、KCP 10 ms periodic update 与
endpoint/key generation。不得通过增加第二 socket、延长无界 timeout 或关闭 replay
校验规避失败。MTU、tamper 与 replay 负例必须在后续合法 datagram 仍可处理时才算通过；
只观察“没有响应”不足以证明 listener 或 session 仍健康。
