## 1. 环境与契约模型

- [x] 1.1 增加 `ClientEnvironmentProfile`、不可变运行配置与 base URI/build identity 验证，覆盖 Production HTTPS、local/test loopback 明文和非法 URI tests。
- [x] 1.2 增加本阶段 8 个 HTTP operation descriptor、不可变请求/响应投影、已知 error registry 投影与安全结果类型，并以 tests 冻结 method、path、认证、status、deadline、body policy 和 response hard cap。
- [x] 1.3 增加基于 `Utf8JsonWriter`/`JsonDocument` 的显式 request codec、required field/enum/range/collection response validator 和 credential-safe formatting tests，不引入 reflection DTO 或通用任意 JSON operation 入口。

## 2. 有界 HTTP 传输

- [x] 2.1 实现可注入 handler 的共享 `HttpClient` transport、linked cancellation、`ResponseHeadersRead` 和 response hard cap，区分 caller cancellation、timeout、transport 与 oversized/malformed failure。
- [x] 2.2 实现 Bearer/content-type/request-id/retry-after 处理与强类型 success/error 解码，验证 204 空 body、status/schema 矛盾、未知 error code 和原始响应不泄漏。
- [x] 2.3 将 transport 实现为幂等 AppLifetime participant，覆盖初始化不联网、停止取消 in-flight、迟到 completion 拒绝和资源只释放一次。

## 3. 启动配置与 Session 所有权

- [x] 3.1 实现 Configuration owner 与 `ClientBootstrapService`，按 version 后 config 的顺序验证协议/最低客户端版本并原子发布启动投影。
- [x] 3.2 实现唯一 `SessionCoordinator` 的 register/login 原子替换、refresh single-flight、generation guard 和 authenticated call snapshot，确保 password/token 不进入日志、资产或普通字符串输出。
- [x] 3.3 实现 logout success/unauthenticated 清理、commit-unknown unresolved 状态、显式 forget 与 shutdown 清理，并覆盖 refresh/login/logout 并发竞态。
- [x] 3.4 实现按 channel 签发的 connection ticket generation/expiry guard 与强类型 own-world bootstrap 查询，保持 ticket 不复用且 world 投影不成为本 change 的持久事实。

## 4. Composition 与 Unity 配置接入

- [x] 4.1 扩展 `AppBootstrap`、`AppComposition` 与 composition result，以直接环境 profile 构造并显式连接 configuration、HTTP transport、bootstrap service 和 Session owner，保持 AppRoot 非 service locator。
- [x] 4.2 建立并在 BootstrapScene 引用不含秘密的 local profile，验证缺失/非法 profile 启动回滚、重复 bootstrap 不复制 HTTP/session graph，并保留 Unity 生成的必要资产元数据。

## 5. 契约与回归验收

- [x] 5.1 使用冻结 HTTP fixtures 验证本阶段 operation 的 request/response/error parity，并增加 timeout、取消、超限、malformed、session generation、ticket expiry 和脱敏负向矩阵。
- [x] 5.2 更新客户端架构、接入、文件结构与验收说明中实际新增的 owner、目录、命令和本阶段边界，不把 accept/admission、WSS/TLS-TCP 或 UI 标记为已完成。
- [x] 5.3 运行完整 protocol verify、客户端 EditMode/PlayMode、Windows Development build、静态/格式检查与 OpenSpec strict 验证，确认 generated code/DLL、本地配置秘密、缓存和构建输出未被 Git 跟踪。
