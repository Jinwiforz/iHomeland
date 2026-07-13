## 1. Session 值类型与安全原语

- [x] 1.1 在 `server/internal/session` 建立 session/principal/epoch/status、channel、endpoint、scope、AuthContext 与稳定错误 kind，完整校验长度、零值、去重和不可扩权语义
- [x] 1.2 建立 session/token/ticket Policy 值对象及交叉校验，固定 TTL 安全上下界与 `ticket < access < refresh <= session` 关系，但不向正式启动配置加入无人消费字段
- [x] 1.3 复用 runtime 的 `crypto/rand` ID generator，实现 token secret generator、带类型前缀的无 padding base64url codec、16-byte ticket nonce 与固定长度 SHA-256 digest，确保敏感值默认输出始终脱敏
- [x] 1.4 为值类型、policy、错误分类、secret entropy/codec/digest/redaction 增加 table-driven 与 fuzz tests，拒绝 malformed、错类型、超长和零值输入

## 2. 原子 SessionStore 与 Token 生命周期

- [x] 2.1 在 session 消费侧定义窄 `SessionStore`，用显式 outcome 表达 create、access resolve、refresh rotate/replay、ticket issue/consume 和 epoch invalidation 所需的原子操作，不暴露通用 CRUD 或存储命令
- [x] 2.2 创建只存在于测试代码的并发安全 store，模拟 expiry、refresh tombstone、compare-and-consume、epoch 递增和依赖失败，确认正式 Composition Root 无法构造该实现
- [x] 2.3 实现 session 创建与 access authentication，绑定经过验证的 principal、epoch、状态和 expiry，并从 store 当前事实构造不含 raw secret 的 AuthContext
- [x] 2.4 实现 refresh token 原子轮换，在同一操作中撤销上一枚 access digest、消费旧 refresh digest并写入新 token pair；并发刷新最多一个成功，旧 digest tombstone 保留到 session expiry
- [x] 2.5 实现 consumed refresh replay 处理，原子递增 epoch并撤销旧 token/ticket，不向调用方泄漏 session、principal 或 token 是否存在的差异

## 3. Connection Ticket 与授权矩阵

- [x] 3.1 定义 `EndpointProvider` 消费接口和经过验证的 channel/scope policy，WSS 只授予 control，TLS/TCP 只授予 gameplay connection capability，客户端不能提交 endpoint 或 scopes
- [x] 3.2 实现 ticket issue，绑定当前 session id/epoch、受信 endpoint、唯一 channel、规范化 scopes、16-byte nonce 与短 expiry，向协议 adapter 返回完整领域投影并只保存 nonce digest record
- [x] 3.3 实现 ticket 原子消费，校验 16-byte nonce、expiry、当前 epoch、listener channel 与 endpoint identity，再构造只读 AuthContext
- [x] 3.4 增加 wrong-channel、wrong-endpoint、expired、wrong-epoch、重复与并发消费测试，证明最多一个消费者成功且失败不会进入业务 dispatcher

## 4. Epoch 失效与连接撤销边界

- [x] 4.1 定义不持有 socket 的 `ConnectionInvalidator` 和安全 invalidation reason，输入只包含 session id、新 epoch 与必要关联信息
- [x] 4.2 以 `Logout(AuthContext)`、`ForceLogout(SessionID)` 和 principal ban 区分失效输入：先原子递增目标 session epochs并撤销旧资格，再逐 session 幂等通知连接边界；未来登录许可仍由账号域负责
- [x] 4.3 覆盖通知成功、通知失败、重复通知、并发 invalidation 与旧 epoch 后续认证，确认通知失败不回滚权威状态且返回可诊断依赖错误
- [x] 4.4 验证 session application API 只接收 AuthContext/principal/value input，不依赖 raw token、HTTP/WebSocket/TCP 类型、generated protocol type、MySQL 或 Redis client

## 5. 集成边界、文档与门禁

- [x] 5.1 使用 fake clock、确定性 generator、并发测试 store 和 fake invalidator 完成 session/token/ticket 全流程测试，并运行 race detector 覆盖 refresh、consume 与 invalidation 竞态
- [x] 5.2 增加日志/error/metrics 安全测试和 secret 扫描，确保 raw token/ticket、digest 全值、principal 敏感输入与存储 key/value 不进入输出
- [x] 5.3 更新 `server/README.md`、`docs/file-structure.md`、`docs/architecture.md`、`docs/roadmap.md` 与必要安全说明，明确 session core 已具备的行为和仍未开放的 listener/storage 边界
- [x] 5.4 验证正式 Composition Root 未注入 test store、未新增业务 listener、未修改协议 schema/registry/fixtures，且没有第二套 token/ticket/epoch 实现
- [x] 5.5 执行 protocol verify、Go format/vet/unit/fuzz/race、依赖与 secret scan、`git diff --check` 和 OpenSpec strict，并复核全部手写注释符合 `docs/code-comment-convention.md`
- [x] 5.6 同步 `server-session` 主 spec，确认 artifacts、tasks、文档与实际行为一致后再归档
