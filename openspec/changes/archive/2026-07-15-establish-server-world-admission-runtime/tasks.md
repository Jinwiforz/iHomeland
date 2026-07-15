## 1. Admission domain 与安全类型

- [x] 1.1 建立 `internal/worldadmission` 的 role、purpose、identity、credential、binding、qualification、outcome 与脱敏错误类型，并覆盖构造/格式化/fuzz 测试
- [x] 1.2 实现 keyed deterministic credential derivation、完整 binding fingerprint、短期 policy 与幂等 issuer service
- [x] 1.3 实现原子 consume 编排、AuthContext/endpoint/purpose 比较、current full assignment 复核与 commit-unknown/replay 决议

## 2. VisitSession 受信桥接

- [x] 2.1 为 `JoinQualification` 增加受校验 JOIN/RECONNECT hydration 与 purpose 门禁，保持默认脱敏
- [x] 2.2 让 Join 与 VisitorReconnect 都要求 qualification，并补齐错误 purpose、membership 缺失/过期、旧 lineage 与旧 assignment 测试
- [x] 2.3 增加 structure test，限制 production qualification hydration 的调用 owner且禁止 transport/payload 直接构造

## 3. Production Redis adapter

- [x] 3.1 登记 worldadmission issuance/credential definitions、key schema、字段预算与共享 registry 组合
- [x] 3.2 实现严格 Hash codec、issue/consume owner Lua scripts、绝对业务 expiry、physical replay retention 与保守错误分类
- [x] 3.3 实现借用共享 Redis client/keyspace 的 Store，并覆盖 schema corruption、missing TTL、oversize、idempotency、response-loss 与 restart/flush 集成测试

## 4. Semantic fixtures 与工程门禁

- [x] 4.1 将 admission semantic manifest 标记为 runtime 已实现并更新 generator/validator/deterministic baseline
- [x] 4.2 用 production issuer/verifier 与 VisitSession 组合测试逐项执行 semantic corpus，并保持 fixture 不含 claims/raw credential/内部 binding
- [x] 4.3 更新 storage harness、架构/文件结构/Redis key/server README 文档，明确 owner、字段、恢复、secret 与未接线边界

## 5. 验证与复盘

- [x] 5.1 运行 worldadmission、VisitSession、storage、fixtures 定向 unit/integration/fuzz/race 测试
- [x] 5.2 运行全量 Go test/vet、storage verify、`git diff --check` 与 OpenSpec 全量 strict 验证
- [x] 5.3 复盘文档一致性、注释规范、安全边界、重复设计与 clean code，并确认当前 server 未开放 world/visit transport
