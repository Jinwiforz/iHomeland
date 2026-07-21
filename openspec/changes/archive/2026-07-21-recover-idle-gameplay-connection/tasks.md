## 1. 协议与服务端 heartbeat

- [x] 1.1 在 common Proto、message/route registry、冻结 catalog 与 fixtures 中登记 TLS/TCP gameplay heartbeat request/response，并保持唯一通道、correlation、rate 和 deadline 契约一致
- [x] 1.2 在服务端 codec/dispatcher 中实现只允许 active connection 的 transport-owned heartbeat 响应，并补充稳定 dispatch/close 可观测结果
- [x] 1.3 补充服务端 codec、dispatcher 与真实 connection idle/heartbeat 测试，证明 heartbeat 不触发业务 application 且能刷新 read deadline

## 2. 客户端 connection generation

- [x] 2.1 在 typed gameplay catalog 中登记 heartbeat operation，并为每个 active generation 实现单 owner、单 pending、可撤销且可等待的 heartbeat 生命周期
- [x] 2.2 统一 reader、writer、connect、heartbeat 与 close 的安全诊断事件，保留 stage/exception type 且不泄露 credential、payload、endpoint 或 identity
- [x] 2.3 补充纯 C# gameplay channel 测试，覆盖静默保活、heartbeat timeout、显式 close、generation replacement 与 shutdown 竞态

## 3. PersonalWorld 有界恢复

- [x] 3.1 为显式 `RetryConnectionAsync` 增加 45 秒总 deadline，并确保 timeout/cancellation 关闭未提交 gameplay generation、提交稳定失败 snapshot 且恢复按钮可操作
- [x] 3.2 补充 presentation/coordinator 纯 C# 测试，覆盖永久 pending、重复点击、deadline 与旧 generation 迟到回调
- [x] 3.3 修正重连 control run 的 cancellation ownership，确保恢复前可撤销、连接提交后不再受 `ConnectionLost` route token 影响，并验证成功关闭 modal 后不会自断重开

## 4. 交付与验收

- [x] 4.1 更新网络、客户端接入和运行时 owner 文档，记录 heartbeat、server-first 发布与恢复 terminal 语义
- [x] 4.2 执行协议/Go 格式化、服务端构建、OpenSpec strict、`git diff --check` 与 `.meta` 清单检查；协议 generated 按用户要求直接覆盖且不手工编辑 `.meta`，不运行 Unity/EditMode/PlayMode、不打包客户端
- [x] 4.3 给出 Windows Development 双客户端手动验收矩阵，覆盖静默超过 30 分钟、断网、server restart、重试超时、Owner/Visitor 与 shutdown
