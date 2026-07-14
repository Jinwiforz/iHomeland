## 1. World/visit Protobuf 源

- [x] 1.1 创建 `shared/proto/ihomeland/world/v1/world.proto`，定义有界 PersonalWorld、client-safe assignment、endpoint、snapshot request/response/push 与 assignment-changed projection；所有 message/enum/field 使用中文契约注释，wire 绝对时间字段统一以 `_at_ms`/`_expires_at_ms` 标明 Unix milliseconds
- [x] 1.2 创建 `shared/proto/ihomeland/visit/v1/visit.proto`，定义 lifecycle/visitor/invite/snapshot、open/create/revoke/join/leave/kick/reconnect/close、control notice 与 safe-return payload；admission grant 仅由 HTTPS OpenAPI 持有，确保 realtime command 不含 actor/session/world/role/endpoint/fence 等可覆盖受信上下文的字段
- [x] 1.3 复核 proto import、package/options、field number、reserved、enum `UNSPECIFIED`、128-byte identity/正 revision约束和 public projection，证明 runtime NodeID、FencingToken、完整 AssignmentStamp、SessionID/epoch、ConnectionBindingID、nonce/fingerprint 未进入客户端可见 schema
- [x] 1.4 扩展统一 proto 工具的 source discovery、lint/format/descriptor/generation summary 输入，确保 world/visit 源参与生成验证但可重新生成的 Go/C# projection仍不提交

## 2. HTTPS 与 stable error 合同

- [x] 2.1 扩展 OpenAPI 公共 schema与受认证 `GET /v1/world/bootstrap`，固定0-byte body、5000 ms timeout、`SAFE`幂等、client-safe world/assignment response和统一错误响应
- [x] 2.2 定义必填 `Idempotency-Key` header与 `POST /v1/visits/{visitSessionId}/invites/{inviteId}/accept`，body只含正expected revision，固定4096-byte body、5000 ms timeout、target Visitor/path identity和首次结果重放语义
- [x] 2.3 定义 `POST /v1/world/admissions` 的封闭 own-world/visit-world target one-of与opaque grant response，固定4096-byte body、5000 ms timeout、idempotency-key语义，并禁止payload声明actor/world/instance/role/endpoint/fence
- [x] 2.4 扩展 `errors.json`，登记design固定的2000-2006与2100-2109 stable error code/name/HTTP/retryability mapping；复用shared auth/validation/dependency错误且所有public metadata默认脱敏
- [x] 2.5 扩展 OpenAPI/registry loader和validator，识别 `IDEMPOTENCY_KEY_REQUIRED`、path/header/body组合、operation metadata、error引用、one-of互斥及禁止identity字段，并添加成功与畸形合同单元测试
- [x] 2.6 扩展 versioned HTTP cases，覆盖bootstrap、target invite accept、own/visit admission、同key同义replay、同key异义conflict、stale revision、forbidden actor字段、missing membership与dependency fail-closed

## 3. Realtime 编号与唯一路由

- [x] 3.1 在 `messages.json` 登记 `world=2000-2099`、`visit=2100-2299` owner range及design固定的2000-2003、2100-2122 message ID/full name/kind/direction，保持已有编号不变并验证删除编号只能进入reserved
- [x] 3.2 在 `routes.json` 为每个新增message登记唯一WSS或TLS/TCP route、CONTROL/GAMEPLAY scope、`RELIABLE_ORDERED`、4/16/64 KiB完整envelope上限、`server_control`/`world_read`/`visit_command`/`server_world` policy reference、5/10秒request timeout与push零timeout
- [x] 3.3 扩展registry validator，校验owner range不重叠、message/route一一对应、channel-kind-direction组合，以及request=`REQUEST_ID`、command=`COMMAND_ID`、response=`CORRELATION_ID`、push=`NONE`的幂等/correlation规则
- [x] 3.4 增加command payload descriptor安全扫描，只禁止actor/account/player/session/epoch/world/instance/role/endpoint/fencing授权字段并显式允许Owner控制面的`target_visitor_id`，添加嵌套字段、近似命名与false-positive测试
- [x] 3.5 增加结构/registry测试，证明WSS notice不能映射gameplay mutation或safe-return、TLS/TCP command不能注册WSS第二入口、system lifecycle callback没有C2S message，并且没有generic interaction/action/mutation owner或payload

## 4. Golden、negative 与 semantic fixtures

- [x] 4.1 为2000-2003与2100-2122每个新增message增加deterministic realtime golden envelope/payload，固定canonical bytes、field number、kind/direction、correlation、Unix millisecond时间和client-safe projection
- [x] 4.2 扩展realtime negative fixtures，覆盖当前 codec/registry 实际拒绝的错误channel、kind/direction/correlation/idempotency、unknown envelope enum、越界identity/frame、forbidden actor/internal assignment字段和未登记message
- [x] 4.3 增加admission semantic cases，描述reserved+JOIN与reconnecting+RECONNECT合法binding，以及purpose错配、expired/replayed nonce、旧session epoch、旧full assignment、错误endpoint/channel、缺失membership对应stable error；fixture只保存验收输入/期望，不实现或宣称production issuer/verifier
- [x] 4.4 扩展fixture validator，证明golden可descriptor decode并canonical re-encode、fixture message/route/error引用存在、negative case命中预期gate、semantic binding组合完整且不会把opaque credential当作可公开claims JSON
- [x] 4.5 添加descriptor/registry/fixture table与targeted fuzz tests，覆盖新增schema、ID边界、unknown field/enum、size/correlation组合、JSON/YAML one-of和canonical bytes漂移

## 5. 安全边界与兼容性验收

- [x] 5.1 添加长期静态依赖守卫，证明PersonalWorld/placement/VisitSession domain/application不import generated protobuf；执行归档前公开面扫描，确认production graph未注册world/visit handler/listener/dispatcher/admission组件且未新增table、Redis key、goroutine或memory fallback
- [x] 5.2 添加合同级credential分层测试/文档断言，证明bearer、`ConnectionTicket`、invite、AdmissionIntent与OpenAPI opaque world admission语义互不替代，JOIN/RECONNECT purpose不能跨membership状态复用，GAMEPLAY scope本身不授予Owner/Visitor role
- [x] 5.3 添加compatibility baseline检查，保证已有common/account/session/control proto、message/error/route和HTTP operation保持兼容，新增field/enum遵守reserved与unknown-value规则，已分配编号不被复用
- [x] 5.4 运行统一proto `format`/`verify`与generation cleanliness检查，确认source、descriptor、registry、OpenAPI、fixtures、summary一致且工作树没有generated Go/C# projection

## 6. 文档与质量门

- [x] 6.1 更新 `docs/protocol-compatibility.md` 与registry README，记录world/visit owner range、stable error、公开projection、Unix milliseconds、idempotency-key、三层credential、reserved和未登记interaction默认拒绝规则
- [x] 6.2 更新 `docs/network-transport-architecture.md`，逐条记录WSS control notice与TLS/TCP authoritative world/visit message边界，解释close notice/safe-return不同职责且不产生双入口
- [x] 6.3 更新 `docs/file-structure.md`、`docs/roadmap.md` 与 `server/README.md`，标记P0 protocol合同产出、后续HTTP/WSS/TCP进入条件，以及production Composition Root、admission和客户端仍未实现
- [x] 6.4 复核全部新增手写Go/PowerShell/Protobuf注释，确保中文定位职责并解释单位、identity、授权、兼容性、安全、失败和生命周期，无逐行复述、过期注释或无上下文TODO
- [x] 6.5 使用项目入口执行全量Go unit、targeted fuzz、race、vet、mod verify、`git diff --check`、secret/generated/cache/dependency hygiene和全量OpenSpec strict validation
- [x] 6.6 将 `server-personal-world-protocol` delta同步到主spec，复核proposal/design/tasks与实际合同一致并再次strict，满足归档条件但不提前实现transport、admission runtime、Go协议客户端或Unity
