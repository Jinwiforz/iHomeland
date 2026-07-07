# 本地 Redis 命令

本文档记录本地 Redis 检查、扫描、TTL、读取和清理命令。它是命令参考，不是自动化脚本；执行删除或清库命令前必须确认端口、DB 和环境。

## 连接检查

native Redis 默认端口：

```powershell
redis-cli -h 127.0.0.1 -p 6379 PING
```

Docker Redis 默认端口：

```powershell
redis-cli -h 127.0.0.1 -p 36379 PING
```

## 查看 key

查看当前 DB 的 key 数量：

```powershell
redis-cli -h 127.0.0.1 -p 6379 DBSIZE
```

本地开发扫描 iHomeland key：

```powershell
redis-cli -h 127.0.0.1 -p 6379 --scan --pattern "ih:dev:*"
```

生产环境禁止直接使用 `KEYS` 扫描全库；需要排查时必须使用 `SCAN` 分批处理，并经过对应环境的运维流程。

## 账号 Session

本地账号 session key 格式：

```text
ih:dev:account:session:{sessionToken}
```

查看账号 session TTL：

```powershell
redis-cli -h 127.0.0.1 -p 6379 TTL "ih:dev:account:session:{sessionToken}"
```

查看账号 session value：

```powershell
redis-cli -h 127.0.0.1 -p 6379 GET "ih:dev:account:session:{sessionToken}"
```

删除单个本地账号 session key：

```powershell
redis-cli -h 127.0.0.1 -p 6379 DEL "ih:dev:account:session:{sessionToken}"
```

## 本地清理

清理本地开发 Redis DB：

```powershell
redis-cli -h 127.0.0.1 -p 6379 FLUSHDB
```

`FLUSHDB` 只允许本地开发使用。执行前必须确认正在连接 `127.0.0.1` 的本地开发端口，禁止对共享、测试、预发或生产 Redis 执行。
