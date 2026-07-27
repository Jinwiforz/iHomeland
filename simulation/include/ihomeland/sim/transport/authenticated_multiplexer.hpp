#pragma once

#include "ihomeland/sim/transport/battle_route.hpp"
#include "ihomeland/sim/transport/handshake_cookie.hpp"
#include "ihomeland/sim/transport/secure_datagram.hpp"

#include <cstdint>
#include <functional>
#include <span>

namespace ihomeland::sim {

class BattleKcpAdapter;
class BattleRuntimeMetrics;

/// BattleMultiplexerDisposition 是 endpoint/authority/AEAD/raw dispatch 的闭合结果。
enum class BattleMultiplexerDisposition : std::uint8_t {
    /// RawAccepted 表示 authenticated raw payload 已交给唯一 route owner。
    RawAccepted = 1,
    /// EndpointMismatch 表示 datagram 不来自 current active endpoint。
    EndpointMismatch = 2,
    /// AuthorityRejected 表示 session、assignment 或 target 已失效。
    AuthorityRejected = 3,
    /// SecureRejected 表示 header、AEAD、epoch 或 replay gate 拒绝。
    SecureRejected = 4,
    /// RawRejected 表示认证成功但 raw route policy 拒绝。
    RawRejected = 5,
    /// LaneUnavailable 表示该session未安装对应lane owner，禁止fallback。
    LaneUnavailable = 6,
    /// KcpAccepted 表示authenticated KCP segment已交给唯一adapter。
    KcpAccepted = 7,
    /// KcpRejected 表示KCP segment/profile/deadline gate拒绝。
    KcpRejected = 8,
    /// ControlAccepted 表示authenticated transport transition已处理。
    ControlAccepted = 9,
    /// ControlRejected 表示authenticated control违反closed state machine。
    ControlRejected = 10,
    /// CloseRequested 表示authenticated close要求终结session。
    CloseRequested = 11,
};

/// BattleControlDispatchDisposition 是 multiplexer 与 session control owner 的窄契约。
enum class BattleControlDispatchDisposition : std::uint8_t {
    /// Accepted 表示control state transition成功且session保持active。
    Accepted = 1,
    /// Rejected 表示认证后的control payload或transition非法。
    Rejected = 2,
    /// CloseRequested 表示control owner要求立即销毁session。
    CloseRequested = 3,
};

/// BattleMultiplexerResult 保存不泄漏 secret/binding 的低敏 dispatch disposition。
struct BattleMultiplexerResult final {
    /// disposition 是最外层 closed decision。
    BattleMultiplexerDisposition disposition;
    /// secure_disposition 记录 AEAD/replay gate 结果。
    BattleOpenDisposition secure_disposition;
    /// raw_disposition 只在 raw packet 认证成功后有效。
    BattleRawDisposition raw_disposition;
};

/// BattleAuthenticatedMultiplexer 为一个 session 绑定 current endpoint、authority 与 channel。
class BattleAuthenticatedMultiplexer final {
public:
    /// AuthorityValidator 必须查询 current session/assignment/target，不得信任 UDP payload。
    using AuthorityValidator = std::function<bool()>;
    /// ActiveEndpointProvider 返回rebind owner当前唯一remote快照。
    using ActiveEndpointProvider =
        std::function<BattleRemoteEndpoint()>;
    /// ControlHandler 只接收AEAD认证后的control plaintext与observed remote。
    using ControlHandler =
        std::function<BattleControlDispatchDisposition(
            std::span<const std::uint8_t>,
            const BattleRemoteEndpoint&,
            std::uint64_t)>;

    /// 构造函数绑定唯一 endpoint provider、control owner与authenticated channel。
    BattleAuthenticatedMultiplexer(
        BattleSecureChannel& channel,
        ActiveEndpointProvider active_endpoint_provider,
        BattleRawDispatcher& raw_dispatcher,
        AuthorityValidator authority_validator,
        ControlHandler control_handler,
        BattleKcpAdapter* kcp_adapter = nullptr,
        BattleRuntimeMetrics* runtime_metrics = nullptr);

    /// Handle 对raw/KCP先校验endpoint；candidate仅允许进入AEAD认证的control。
    [[nodiscard]] BattleMultiplexerResult Handle(
        std::span<const std::uint8_t> datagram,
        const BattleRemoteEndpoint& remote,
        const BattleRawDispatchContext& raw_context,
        std::uint64_t now_unix_ms);

private:
    /// channel_ 同时验证 session/generation/binding/epoch/sequence。
    BattleSecureChannel& channel_;
    /// active_endpoint_provider_ 读取rebind owner当前唯一remote。
    ActiveEndpointProvider active_endpoint_provider_;
    /// raw_dispatcher_ 是 raw numeric route 唯一 owner。
    BattleRawDispatcher& raw_dispatcher_;
    /// kcp_adapter_ 是可选但唯一的 reliable lane owner。
    BattleKcpAdapter* kcp_adapter_;
    /// authority_validator_ 每包确认 session、assignment 与 target current。
    AuthorityValidator authority_validator_;
    /// control_handler_ 独占rebind/rekey/close state transition。
    ControlHandler control_handler_;
    /// runtime_metrics_ 可选借用 node 生命周期内的低敏累计 owner。
    BattleRuntimeMetrics* runtime_metrics_;
};

}  // namespace ihomeland::sim
