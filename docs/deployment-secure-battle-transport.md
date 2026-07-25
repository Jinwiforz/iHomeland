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

以下 Docker Compose 片段只说明 bind、NAT 与 advertised endpoint 的映射关系；B0.6
完成前它只能用于部署预演，不代表 production 网络资格已经通过：

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
