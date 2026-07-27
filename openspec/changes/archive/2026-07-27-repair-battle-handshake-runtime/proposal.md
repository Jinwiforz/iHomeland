## Why

B0.5 当前只有分离的 handshake、secure transport、multiplexer 与 UDP listener
单元实现，真实 `ihomeland-sim-server` listener 的 callback 不处理 datagram，也
不能用同一 socket 回包；同时客户端无法仅凭公开 HTTPS BattleTicket response
派生 installed transcript proof。B0.6 因而无法通过真实进程和 socket 验证已经
声明 qualified 的安全传输，必须先修复 B0.5 端到端契约与运行时组合。

## What Changes

- **BREAKING** 将 BattleTicket proof key 的客户端派生 salt 从客户端不可见的完整
  binding fingerprint 改为公开 ticket ID，并以 versioned domain 绑定该算法；
  ticket secret 本身仍由服务端完整 binding 确定性派生。
- 让独立客户端只从 HTTPS ticket ID/secret 派生 proof，成功解密 ServerAccept 后
  接收并锁定完整 binding fingerprint，不允许 Go parent 注入 proof key 或 binding。
- 为唯一 `BattleUdpListener` 增加有界、线程安全的同 socket send owner，并把
  ClientHello/ClientAuth、secure raw/KCP/control、rebind/rekey/close 分派组合到
  node-global authenticated runtime。
- 为 handshake 成功结果增加 move-only secret ownership transfer，使 session seed
  只能进入唯一 `BattleSecureChannel`，exact ServerAccept replay 继续由 handshake
  owner 持有。
- 补齐启动回滚、deadline、queue/capacity、revoke、drain、listener failure 和
  secret cleanup，禁止第二 listener 或 qualification-only bypass。
- 更新 wire/crypto fixtures、Go/C++/C# parity、真实 child/UDP integration 与 B0.5
  qualification report；旧 report 在新 identity 下失效。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `secure-battle-transport`：修正 BattleTicket 客户端 proof 派生契约，并要求唯一
  production UDP listener 真正组合 handshake、session、secure raw/KCP/control
  收发与生命周期。

## Impact

- Go：`internal/battleticket` derivation、BattleTicket fixtures/control install 与
  B0.5 qualification harness。
- C++：authenticated handshake secret transfer、UDP listener send、node-global
  battle runtime/session owner、control composition 和 integration tests。
- C#：BattleTicket proof derivation fixture parity；不实现 Unity runtime。
- Contracts：battle wire crypto vectors、canonical golden、secret policy、manifest
  与 B0.5 qualification report identity。
- B0.6：完成本 change 并重新资格 B0.5 前，真实 network matrix/soak 保持阻塞。
