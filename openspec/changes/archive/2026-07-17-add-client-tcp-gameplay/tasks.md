## 1. World admission HTTP contract

- [x] 1.1 扩展冻结 HTTP operation、请求/响应 projection 与严格 JSON codec
- [x] 1.2 在 Session owner 中实现 generation、expiry 和单次 take 保护的 admission lease
- [x] 1.3 增加 own/visit admission method、header/schema 与 credential 生命周期测试

## 2. Gameplay wire foundation

- [x] 2.1 实现 `IHTP` v1 preface、4-byte big-endian framer 与 `ReliableEnvelope` 严格 codec
- [x] 2.2 建立冻结 request/command/response/push descriptor catalog 与 route payload 上限
- [x] 2.3 实现 Windows/Mono TCP transport、TLS 1.3 policy、exact I/O 和安全关闭边界
- [x] 2.4 增加 preface fixture、partial I/O、oversize、wrong-route 与 credential redaction tests

## 3. Gameplay channel owner

- [x] 3.1 实现显式 connect、双凭据 endpoint/purpose 校验和线性化连接状态
- [x] 3.2 实现唯一 reader、serialized writer、有界 item/byte queue 与严格双向 sequence
- [x] 3.3 实现有界 pending correlation、caller cancel/deadline/disconnect 恰好一次完成和 typed push 分派
- [x] 3.4 实现 safe-return mutation gate、session invalidation 与稳定低基数 close reason
- [x] 3.5 增加 pending race、backpressure、push、invalidation 和 shutdown tests

## 4. Composition、文档与验收

- [x] 4.1 将 gameplay channel 纳入 App Scope 初始化/逆序停止且保持默认零网络副作用
- [x] 4.2 更新客户端架构、接入与路线图文档，明确本 change 与后续 PersonalWorld Services 边界
- [x] 4.3 运行静态编译、OpenSpec strict、diff 检查并给出 Unity EditMode/PlayMode/Windows build 验收步骤
