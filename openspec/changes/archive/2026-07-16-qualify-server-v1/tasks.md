## 1. 资格 manifest 与冻结基线

- [x] 1.1 在 `shared/contracts/fixtures/qualification` 定义 schema-versioned 场景 manifest，登记稳定 ID、group、mandatory、phase、execution、预算、公开 outcome 和证据层级，并提供严格 JSON loader/validation tests
- [x] 1.2 建立显式 scenario registry 与 manifest 双向 completeness test，拒绝重复 ID、未知枚举、无界预算、漏实现和隐藏 runner
- [x] 1.3 定义排序后的 v1 contract freeze 输入清单与 aggregate SHA-256 算法，排除 generated/descriptor/report/digest record 自身，并覆盖 clean recompute、增删文件和内容漂移测试
- [x] 1.4 增加 endpoint manifest 交付示例与 contract 校验，确保 advertised WSS/TLS-TCP、protocol version 和资源上限来自现有公开 schema而非第二套配置语义

## 2. 独立 Go 协议客户端基础

- [x] 2.1 创建 `server/internal/testclient` 及 import allowlist architecture test，只允许标准库、锁定网络/Protobuf runtime和generated Protobuf，禁止服务端app/domain/storage/protocol/transport依赖
- [x] 2.2 实现默认脱敏的actor/session/credential/connection状态、CSPRNG correlation/idempotency identity与显式清零/关闭生命周期，并覆盖格式化和日志泄漏测试
- [x] 2.3 实现严格 HTTPS client，覆盖10个冻结operation、closed JSON projection、bearer/idempotency、request deadline和稳定公开错误，不复制服务端domain policy
- [x] 2.4 实现WSS control client的TLS/subprotocol/ticket握手、binary envelope、单receive pump、sequence/heartbeat、push解码和有界关闭
- [x] 2.5 实现TLS/TCP gameplay client的TLS 1.3、`IHTP` preface、4-byte frame、serialized writer、receive pump、pending correlation、response/error/push及safe-return关闭
- [x] 2.6 实现多actor scenario context与显式资源栈，使HTTP/WSS/TCP失败可逆序关闭且迟到frame不能写回已替换target
- [x] 2.7 为client JSON/envelope/preface/frame、partial I/O、unknown message/kind、oversize、timeout和credential grammar增加unit/table/fuzz/race tests

## 3. 正向业务竖切场景

- [x] 3.1 实现register/login/refresh、bootstrap config、WSS ticket/connect与session invalidation场景，验证endpoint只来自公开manifest
- [x] 3.2 实现own-world bootstrap、重复/并发bootstrap、GAMEPLAY ticket、OWN_WORLD admission、TCP snapshot和credential单次消费场景
- [x] 3.3 实现Owner创建VisitSession/invite、目标Visitor经WSS接收、HTTPS accept、JOIN admission/TCP command及双方snapshot收敛场景
- [x] 3.4 分别实现Visitor LEAVE、Owner KICK、Owner CLOSE与多Visitor稳定safe-return场景，验证精确target、revision和response/push correlation
- [x] 3.5 实现Owner意外断线/grace内恢复、Visitor断线/显式RECONNECT、grace到期terminal close和旧binding callback抑制场景
- [x] 3.6 增加非目标actor、payload identity、Visitor执行Owner command、低revision/重复push和错误correlation负向场景，确保默认拒绝且不扩大channel

## 4. 陈旧资格、依赖故障与恢复

- [x] 4.1 覆盖ticket/admission replay、expired、wrong purpose/channel/endpoint、stale session epoch和已logout连接，验证无法降级为其他credential
- [x] 4.2 在保留真实MySQL/Redis时重启独立server process，验证新RuntimeNodeID replacement、generation/fence单调、旧assignment/admission/connection fail closed
- [x] 4.3 对当前run执行owner校验的Redis flush/restart，验证旧session/ticket/admission/VisitSession/assignment不从MySQL、payload或client memory恢复，重新认证路径可重建合法运行态
- [x] 4.4 对当前run执行owner校验的MySQL restart，验证dependency窗口安全失败、已提交Account/PersonalWorld恢复和Redis credential不因持久库恢复而越权复活
- [x] 4.5 将duplicate instance、lease expiry、commit-unknown、response-loss replay和storage corruption的既有mandatory domain/storage证据映射到资格manifest/report，补齐缺失测试而不伪造wire注入能力

## 5. 资源、背压、观测与关闭

- [x] 5.1 以 manifest 有限 profile 和分层证据目录登记 WSS/TCP slow consumer owner tests，验证精确关闭、事实不回滚、其他连接隔离和 queue 最终释放
- [x] 5.2 实现partial/slow frame、并发response/push和serialized writer场景，验证frame完整、sequence单调、pending有界与timeout回收
- [x] 5.3 以有限 profile 和稳定 owner tests 覆盖 connection storm、rate/backpressure，固定尝试/并发/字节/时间预算并断言守恒而非机器吞吐排名
- [x] 5.4 以独立进程恢复场景及 lifecycle owner tests 覆盖 graceful shutdown、shutdown deadline 和立即 process failure，验证新入口拒绝、精确退出和可重复 restart
- [x] 5.5 从公开 diagnostic 读取前后 WSS/TCP active/in-flight gauges；由 owner tests 验证低基数 label 与 queue/runtime/deadline 收敛；扫描 server logs 中已知 secret，并对 client/report 拒绝路径、IP、identity 字段和 backend 文本泄漏

## 6. 单一 qualification 编排入口

- [x] 6.1 创建 `tools/qualification/qualification.ps1`，实现严格参数、全局/阶段deadline、run-id路径边界、稳定阶段输出和ignored `.local/qualification` ownership
- [x] 6.2 通过storage owner执行up/status/down，生成临时TLS与动态loopback server config，确保secret只以文件/env reference传递且不进入command line或异常
- [x] 6.3 通过Go wrapper构建并启动精确`cmd/server`隐藏子进程，异步排空stdout/stderr、轮询readiness、记录精确PID并处理unexpected exit
- [x] 6.4 实现只允许当前run且先验证Docker ownership label的封闭Redis flush/restart与MySQL restart fault driver，不提供production HTTP管理入口或模糊资源操作
- [x] 6.5 按 manifest phase/execution 执行 client scenarios 与分层 gate，生成低敏 schema-versioned report 并正确区分 pass/fail/skipped/cleanup_failure；任何 mandatory skipped 均失败
- [x] 6.6 覆盖success、test failure、server crash、native timeout、Ctrl+C和cleanup failure，证明精确PID终止、独立cleanup budget、可重复down与主失败不被覆盖

## 7. 分层质量门与完整资格运行

- [x] 7.1 将`proto verify`、contract/fixture/freeze校验和clean重复生成纳入mandatory阶段，确认tracked generated/descriptor/projection始终为空
- [x] 7.2 运行`go test -count=1 ./...`、真实storage verify、MySQL migration/restart与全套black-box functional/recovery矩阵，修复全部失败
- [x] 7.3 维护显式fuzz target清单并逐项运行固定非零fuzz time，覆盖account/session/contracts/world/visit/storage/WSS/TCP输入且任何target缺失或未执行均失败
- [x] 7.4 在受影响及所有并发owner packages运行race，覆盖account/session/placement/VisitSession/app/WSS/TCP/testclient并修复竞态、泄漏或非确定性断言
- [x] 7.5 按共享证据目录运行 resource/storm/slow-consumer/shutdown 矩阵，并在同一提交连续执行两次完整资格入口，验证场景集合、execution、稳定 outcome、freeze digest 与 cleanup 结果可重复
- [x] 7.6 审计production graph、message/table/key/listener/interface owner、默认配置、日志/metrics、Git ignore与工作区，拒绝memory/fake adapter、secret/cache和未确认contract漂移

## 8. 资格报告、长期文档与归档门

- [x] 8.1 生成并评审 `docs/server-v1-qualification.md`，记录manifest版本、freeze digest、命令、mandatory gate、证据层级、非目标、清理结果与明确C0解锁结论
- [x] 8.2 更新roadmap、architecture、file structure、protocol compatibility、client integration、server/client README和相关验收命令，明确qualification client按公开capability长期分组演进、保留旧回归且不替代Unity，并只声明Q0已证明的v1边界
- [x] 8.3 按注释规范补齐testclient、PowerShell、manifest schema、fault/cleanup和报告字段的职责、安全、单位、生命周期与失败语义注释
- [x] 8.4 在完整入口内执行 governance gate，并补充 `go vet ./...`、`go mod verify`；确认 tasks/specs/docs/report 与实际资格结果一致后才允许归档
