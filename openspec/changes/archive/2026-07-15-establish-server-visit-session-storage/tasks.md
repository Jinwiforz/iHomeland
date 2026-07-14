## 1. Redis schema 与 adapter 骨架

- [x] 1.1 新建 `internal/storage/visitsession` production package，定义复用共享 client/keyspace/clock/observer 的 `Store` 构造入口，并为所有手写声明补齐符合项目规范的中文契约注释
- [x] 1.2 登记 active、session 与 command 三类 Redis definitions，固定 owner、schema version、大小预算、TTL、恢复、清理、故障与低基数 metrics metadata
- [x] 1.3 增加不开放 AuthContext/JoinQualification 的 AuthBinding 与非凭据 AdmissionIntent 严格 hydration constructor；实现 canonical field/payload codec覆盖完整 Snapshot、CreateResult、MutationResult与SafeReturnDirective，并在读写两侧拒绝矛盾值
- [x] 1.4 增加 codec/definition table tests 与 fuzz tests，覆盖 unknown version/enum、重复或缺失 field、非规范整数/UTC微秒、越界集合、错误result kind、encoded size和脱敏格式化

## 2. 原子 VisitSessionStore

- [x] 2.1 实现 Create owner Lua script 与 reply parser，按 command replay/conflict 优先级原子维护 world active 唯一索引、candidate snapshot 和完整 create result
- [x] 2.2 实现 ResolveActive 原子读取与 FindByID，严格验证 index/session/world/lifecycle/TTL 交叉绑定且不自动修补损坏状态
- [x] 2.3 实现 Commit owner Lua script 与 reply parser，原子决议 replay、expected revision、target/result 保存和 terminal active-index 删除；只映射script实际产生的store outcomes，不在adapter重复领域policy
- [x] 2.4 实现 `session_expires_at + replay_retention` absolute TTL，校验 1分钟至24小时配置范围、Redis毫秒向后取整和缺失TTL fail-closed，确保TTL不被当作领域deadline
- [x] 2.5 接入共享 failure classifier，关闭隐式 mutation retry，区分已证明未写入的 not-committed 与无法确定写入阶段的 commit-unknown；为固定 operation/outcome 添加无 identity 观测
- [x] 2.6 增加 Store contract/table tests，覆盖created/existing/replay、adapter负责的conflict/not-found、完整result/directive replay、probe、terminal retention、cancel与矛盾script reply

## 3. 真实 Redis 与并发故障验收

- [x] 3.1 增加真实Redis integration tests，验证空库create/resolve/find、代表性transition与全部projection codec、进程重建、TTL、terminal snapshot和namespace隔离
- [x] 3.2 增加多adapter并发测试，证明同world create唯一性、同revision最多一次提交、capacity竞争与active index一致性，并纳入race验收
- [x] 3.3 使用发送前context preflight与单连接fault hook分别制造可证明未提交和提交后response loss，验证commit-unknown后相同CommandID完整replay及不同fingerprint冲突
- [x] 3.4 增加 Redis restart/flush、missing key/TTL、corrupt/unknown/oversized value 和 unknown reply 测试，证明进程重启只读取 Redis 仍保留的合法运行态，flush/丢失后无 memory fallback 或部分成功

## 4. Runtime 边界与长期文档

- [x] 4.1 将VisitSession definitions合并进共享Redis registry和storage verification构造路径，验证非法registry/config在连接前失败；保持正式Composition Root不构造VisitSession service、cleanup task或公开listener
- [x] 4.2 按字段字典模板更新 `docs/redis-keys.md`，记录三个已实现key的所有英文field、类型/编码、中文短注释、UTC微秒/字节单位、写读恢复清理和故障行为，并明确不预建player index
- [x] 4.3 更新 `docs/roadmap.md`、`docs/file-structure.md`、`server/README.md`，将本change和后续独立world admission runtime置于HTTP之前，删除“VisitSession production adapter不存在”的过期描述且不宣称visit-world可用
- [x] 4.4 复核全部新增/修改注释符合 `docs/code-comment-convention.md`，清除逐行复述、过期、重复、无owner TODO和可能泄漏identity/value的日志文本

## 5. 质量门与变更收口

- [x] 5.1 运行gofmt、VisitSession/storage相关unit/contract/fuzz/race测试及 `tools/storage/storage.ps1 -Action verify`，记录无法执行的环境限制与剩余风险
- [x] 5.2 运行 `go test -count=1 ./...`、`go vet ./...`、`go mod verify`、`git diff --check` 与生成物/密钥/缓存检查，确认没有固定端口、generated code或本机文件
- [x] 5.3 同步delta specs到主specs，执行 `openspec validate establish-server-visit-session-storage --strict` 与 `openspec validate --all --strict`，复核tasks、README/docs和实现行为一致后再归档
