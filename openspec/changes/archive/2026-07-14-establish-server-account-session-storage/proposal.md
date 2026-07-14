## Why

Account 与 Session Core 已冻结消费侧接口，但仓库仍只有测试 reference store；正式进程缺少 MySQL AccountRepository、production password hasher 和 Redis SessionStore。若直接实现 `add-server-http-bootstrap`，会把密码安全、跨存储原子语义与公开 HTTP adapter 混入同一 change，并可能开放无法由生产依赖支撑的认证入口。

## What Changes

- 为 Account owner 增加最小 MySQL schema 与 repository，在单一事务中保存 account、player、canonical username、credential hash、状态和 UTC 微秒创建时间，并显式区分唯一冲突、未提交与 commit unknown。
- 增加受限并发的 production Argon2id credential hasher，使用自描述编码、每条凭据独立随机 salt、固定受治理参数和同成本 dummy hash；plaintext、完整 hash、salt 与安全配置不得进入日志、错误或 metrics。
- 为 Session owner 增加 standalone Redis adapter，以 typed codec、owner key definitions 和原子 Lua scripts 实现 session/token/ticket 创建、认证、refresh 轮换、replay tombstone、epoch invalidation 与 principal 全量撤销。
- 固定 Redis TTL、索引、清理、损坏数据和 flush 语义：运行态丢失后所有旧 token/ticket fail closed 并要求重新登录，不能从 MySQL 伪造恢复 session。
- 扩展 Docker storage harness 与 contract/integration tests，覆盖并发注册、提交结果不确定、进程/依赖重启、refresh/ticket 竞争、replay、epoch、Redis flush、codec corruption、敏感信息脱敏和 schema 注释。
- 更新路线图、存储 schema/Redis key 文档与交付顺序；本 change 不增加 Gin handler、业务 listener、连接 registry、VisitSession store、world admission 或 production Account/Session service graph。

## Capabilities

### New Capabilities

- `server-account-session-storage`: 定义 Account 持久事实、密码哈希安全边界与 Session 可失效 Redis 运行态的生产适配、原子操作、恢复和验收要求。

### Modified Capabilities

- `delivery-sequencing`: 在公开 HTTPS/WSS/TLS-TCP adapter 之前加入 production Account/Session storage 与 credential hashing 资格门，防止 transport change 顺带创造未验收的安全存储实现。

## Impact

- 影响 `server/internal/storage/` 下的 account/session owner adapters、MySQL forward migration、Redis definitions/codecs/scripts、storage integration harness 与测试。
- 复用现有 MySQL/Redis clients、transaction policy、clock、CSPRNG、secret boundary 和低基数 observability，不新增独立连接池、后台 goroutine 或 memory fallback。
- 需要把 Go Argon2id implementation 作为受治理的直接依赖写入 `versions.yaml`、`server/go.mod` 与校验入口；不改变已发布 HTTP/Protobuf contract。
- MySQL 仅新增一张最小账号表；Session 只进入带 TTL 或显式清理路径的 Redis 运行态，不新增 session MySQL 表。
