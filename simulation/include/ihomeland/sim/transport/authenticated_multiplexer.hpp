#pragma once

#include "ihomeland/sim/transport/battle_route.hpp"
#include "ihomeland/sim/transport/handshake_cookie.hpp"
#include "ihomeland/sim/transport/secure_datagram.hpp"

#include <cstdint>
#include <functional>
#include <span>

namespace ihomeland::sim {

class BattleKcpAdapter;

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

    /// 构造函数冻结唯一 active endpoint 与 authenticated channel owner。
    BattleAuthenticatedMultiplexer(
        BattleSecureChannel& channel,
        BattleRemoteEndpoint active_endpoint,
        BattleRawDispatcher& raw_dispatcher,
        AuthorityValidator authority_validator,
        BattleKcpAdapter* kcp_adapter = nullptr);

    /// Handle 在解密前验证 endpoint/authority，认证后按 packet kind 唯一分流。
    [[nodiscard]] BattleMultiplexerResult Handle(
        std::span<const std::uint8_t> datagram,
        const BattleRemoteEndpoint& remote,
        const BattleRawDispatchContext& raw_context,
        std::uint64_t now_unix_ms);

private:
    /// channel_ 同时验证 session/generation/binding/epoch/sequence。
    BattleSecureChannel& channel_;
    /// active_endpoint_ 是当前唯一允许的 remote IP/port。
    BattleRemoteEndpoint active_endpoint_;
    /// raw_dispatcher_ 是 raw numeric route 唯一 owner。
    BattleRawDispatcher& raw_dispatcher_;
    /// kcp_adapter_ 是可选但唯一的 reliable lane owner。
    BattleKcpAdapter* kcp_adapter_;
    /// authority_validator_ 每包确认 session、assignment 与 target current。
    AuthorityValidator authority_validator_;
};

}  // namespace ihomeland::sim
