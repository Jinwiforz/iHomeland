# 本地 MySQL / Redis 命令

本文档记录服务端本地开发所需的 MySQL 和 Redis 命令。所有新增数据库、缓存、迁移、排障命令都必须沉淀到项目文件，不能只停留在聊天记录或个人笔记中。

## MySQL

本地开发库、账号、授权和当前 migration 命令存放在：

```text
server/scripts/mysql-local-dev.sql
```

native MySQL 执行方式：

```powershell
cd server
mysql -u root -p -h 127.0.0.1 -P 3306 < scripts/mysql-local-dev.sql
```

Docker MySQL 执行方式：

```powershell
cd server
mysql -u root -p -h 127.0.0.1 -P 3306 < scripts/mysql-local-dev.sql
```

`mysql-local-dev.sql` 末尾使用 `SOURCE` 执行 migration。`SOURCE` 是 mysql 命令行客户端语法，不是通用 SQL 语法；如果所用 SQL IDE 不支持 `SOURCE`，可能只会创建库、用户和授权，不会执行 migration。

SQL IDE 执行方式：

1. 使用管理员账号连接本机 MySQL。
2. 执行 `server/scripts/mysql-local-dev.sql` 中 `SOURCE` 之前的建库、建用户和授权语句。
3. 切换到 `ihomeland` schema，或执行：

```sql
USE `ihomeland`;
```

4. 按顺序执行 migration 文件：

```text
server/internal/storage/migrations/0001_room_lobby_summary.up.sql
server/internal/storage/migrations/0002_account_session.up.sql
```

5. 执行：

```sql
SHOW TABLES;
```

预期至少能看到：

```text
account_player
match_summary_stub
player_profile_stub
room_summary
```

该脚本会创建或修复：

- `ihomeland` 本地开发库
- `ihomeland` 应用用户
- `ihomeland` 应用用户密码
- `localhost` 和 `127.0.0.1` 登录授权
- `0001_room_lobby_summary`
- `0002_account_session`

如果 GoLand 或服务端报错：

```text
Unknown database 'ihomeland'
```

说明本地 MySQL 还没有执行建库命令。先执行 `mysql-local-dev.sql`。

如果报错：

```text
Access denied for user 'ihomeland'@'localhost'
```

说明账号、密码或授权与 `server/.env.local` 不一致。执行 `mysql-local-dev.sql` 会把本地开发账号重置到项目默认值；如果你要保留已有密码，就需要手动更新 `server/.env.local` 或 GoLand Run Configuration。

## Redis

本地 Redis 检查、扫描、TTL、读取和清理命令存放在：

```text
docs/redis-local-dev-commands.md
```

常用 native 检查：

```powershell
redis-cli -h 127.0.0.1 -p 6379 PING
redis-cli -h 127.0.0.1 -p 6379 --scan --pattern "ih:dev:*"
```

常用 Docker 检查：

```powershell
redis-cli -h 127.0.0.1 -p 6379 PING
redis-cli -h 127.0.0.1 -p 6379 --scan --pattern "ih:dev:*"
```

本地账号 session key 格式：

```text
ih:dev:account:session:{sessionToken}
```

Redis 只保存短期运行态。Redis 丢失或清理后，玩家需要重新登录；玩家基础资料仍以 MySQL `account_player` 为事实来源。

## 长期规则

- MySQL 建库、授权、迁移、修复和排障命令必须存放在 `server/scripts/` 或明确的项目文档中。
- Redis key 检查、TTL、清理和排障命令必须存放在明确的项目文档中；只有可安全自动执行的 Redis 流程才放入 `server/scripts/`。
- 新增表必须提供 migration；新增 Redis key 必须更新 `docs/redis-keys.md`。
- MySQL migration 中所有正式表和字段必须带中文 `COMMENT`；注释优先使用名词或短组合名词，时间、大小、距离等字段必须用括号标明单位，例如 `创建时间(ms)`、`持续时间(s)`。Redis key 文档必须用中文记录 owner、用途、TTL、value、重建来源和清理触发。
- 本地开发命令必须标明适用环境，禁止把本地清库、重置密码或全库扫描命令包装成生产可执行流程。
