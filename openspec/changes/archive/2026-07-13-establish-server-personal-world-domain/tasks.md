## 1. PersonalWorld 值与 aggregate

- [x] 1.1 创建 `server/internal/personalworld` package 与 `doc.go`，实现带 `pworld_` namespace 的 PersonalWorldID、正数 Revision、受限 IdempotencyKey 和封闭 Lifecycle 值类型，并用 table/fuzz tests 拒绝零值、错前缀、超长和不安全输入
- [x] 1.2 实现 PersonalWorld 新建与 snapshot hydration constructor，固定 immutable `account.PlayerID` owner、active 初始状态、revision 1、UTC created time 和私有字段，并覆盖合法 round-trip 与 malformed snapshot
- [x] 1.3 实现唯一 `active -> archived` 领域 transition，拒绝 owner transfer、archived 恢复和运行实例状态进入 lifecycle，并验证成功 transition 只增加一次 revision
- [x] 1.4 定义稳定 ErrorKind、Operation 与 CommitPhase，确保 validation、not found、forbidden、revision/idempotency conflict、invalid state 和 dependency failure 可判定，且 key、command、record 的 error/log/string 路径不泄漏 idempotency key 或 repository snapshot

## 2. Repository 与 application 契约

- [x] 2.1 定义消费侧 Clock、IDGenerator、PersonalWorldRepository、create/mutation records、fingerprint、outcome 与 result 类型，完整表达 created/existing、not found、replay/conflict、not committed 和 commit unknown
- [x] 2.2 实现 `EnsurePrimaryWorld` 编排，复用 Composition Root clock/ID 语义，校验 owner、generator、repository outcome 和返回 snapshot，并让重复/并发调用收敛到同一 primary world
- [x] 2.3 实现 Owner-only `ArchiveWorld` 编排，以可信 actor、PersonalWorldID、expected revision 和 idempotency key 构造具体 command，不提供万能 callback、patch map 或全局 command bus
- [x] 2.4 对 repository 返回的 owner、revision、lifecycle、fingerprint、commit phase、result/error 组合执行 fail-closed 校验，覆盖 ID collision、malformed result、stale revision 和 commit-unknown，禁止伪成功或跨存储补偿

## 3. 并发、幂等与失败测试

- [x] 3.1 仅在 `_test.go` 实现带锁 reference repository、fake clock/ID generator 和 failure injection，生产 package 不包含 memory repository 或可被 Composition Root 注入的 fallback
- [x] 3.2 覆盖并发 `EnsurePrimaryWorld`，确认同一 owner 最多创建一个 world，所有成功调用返回同一 ID/snapshot，并在 race detector 下验证测试替身自身无竞态
- [x] 3.3 覆盖 ArchiveWorld 的 owner spoof、两个 key 竞争同一 revision、同 Owner scope 的 replay/conflict、不同 Owner 相同文本 key 隔离、terminal state 与响应丢失重试
- [x] 3.4 覆盖 not-committed/commit-unknown、context cancellation、dependency error、malformed snapshot/result 和安全错误输出，确认状态、revision 与提交阶段不会被错误推断
- [x] 3.5 对 ID、IdempotencyKey、snapshot hydration 和 command fingerprint 增加有界 fuzz tests，确认任意 bytes/零值/边界输入不会 panic、绕过 owner/revision 不变量或产生敏感回显

## 4. 所有权与文档边界

- [x] 4.1 更新 `server/README.md` 的当前 module 能力与限制，说明 PersonalWorld core 尚未接入 production storage、placement、协议、listener 或 Unity，并保持路线图进入条件不变
- [x] 4.2 复核 package dependency 与文件结构，确认 personalworld 只单向消费 `account.PlayerID` 和窄基础接口，不新增 identity/common manager、generated protocol dependency 或空 WorldInstance/Visit/Activity package
- [x] 4.3 复核 aggregate 字段与 API，确认不包含 endpoint、lease、presence、Visitor、地图、任务、奖励、资产、ActivityInstance、万能 map 或跨 owner 顺序双写
- [x] 4.4 按 `docs/code-comment-convention.md` 为 package、所有业务声明、字段、interface 方法、复杂并发测试和失败边界补齐中文注释，并删除复述实现或暗示临时设计的注释

## 5. 验证与归档准备

- [x] 5.1 运行 gofmt、PersonalWorld 定向 unit/fuzz tests、全量 `go test ./... -count=1`、`go vet ./...` 和 Docker/等价 CGO 环境 `go test -race ./... -count=1`
- [x] 5.2 执行 protocol verify、依赖与 credential/secret scan、`git diff --check`、tracked LF/生成物检查和 OpenSpec strict，确认本 change 未修改协议、端口、配置、storage schema 或 Unity 文件
- [x] 5.3 逐项对照 proposal/design/spec scenarios 与 tasks，确认全部任务即时勾选、长期 spec 可同步、正式 Composition Root 未接线且 change 达到可归档状态
