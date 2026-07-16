## 1. Control 契约与 codec

- [x] 1.1 建立封闭的 9-route control message catalog、不可变 push/close/state 模型与生成类型映射
- [x] 1.2 实现 `ReliableEnvelope`、frame/route size、kind/correlation、payload 与严格连续 sequence 校验
- [x] 1.3 增加 registry/golden parity、合法 payload、malformed/oversize 与 sequence 负向 EditMode 测试

## 2. WSS transport 与恢复

- [x] 2.1 实现只暴露 connect/receive/close 的 `ClientWebSocket` adapter、factory 和安全握手 request
- [x] 2.2 实现单 receive pump、fragment 有界重组、主线程投递、稳定 close 分类与 socket 清理
- [x] 2.3 实现显式启动、每次尝试签发新 ticket、有限可取消 backoff、代际 guard 与停止状态机

## 3. Session 与 App Scope 接线

- [x] 3.1 为 ticket use 保留来源 generation，并为 forced logout/session invalidation 增加 generation-safe Session owner 入口
- [x] 3.2 在 `AppComposition`/`AppCompositionResult` 中显式接线 control channel，保持初始化无网络副作用和逆序停止
- [x] 3.3 覆盖 ticket 单次交付、旧连接失效、新 session 保护、dispatcher 背压、重连耗尽和 shutdown/rollback 测试

## 4. 质量门禁

- [x] 4.1 按项目规范复核全部手写 C# XML 注释、敏感信息、并发所有权和无通用发送入口边界
- [x] 4.2 运行静态客户端编译、OpenSpec strict、`git diff --check` 与 tracked generated/meta 边界检查
- [x] 4.3 在 Unity 关闭时运行协议 verify，确认 generated protocol 可重复重建且 parity 继续通过
- [x] 4.4 运行 Unity EditMode、PlayMode 与 Windows Development build，确认离线启动及 control 代码平台兼容
