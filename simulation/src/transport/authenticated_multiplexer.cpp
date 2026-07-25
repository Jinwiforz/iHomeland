#include "ihomeland/sim/transport/authenticated_multiplexer.hpp"

#include "ihomeland/sim/transport/kcp_adapter.hpp"

#include <algorithm>
#include <ranges>
#include <stdexcept>
#include <utility>

namespace ihomeland::sim {
namespace {

/// SameEndpoint 对 canonical IPv6 bytes 与 UDP port 执行 exact compare。
[[nodiscard]] bool SameEndpoint(
    const BattleRemoteEndpoint& left,
    const BattleRemoteEndpoint& right) noexcept {
    return left.port == right.port &&
           std::ranges::equal(
               left.address,
               right.address);
}

}  // namespace

BattleAuthenticatedMultiplexer::
BattleAuthenticatedMultiplexer(
    BattleSecureChannel& channel,
    BattleRemoteEndpoint active_endpoint,
    BattleRawDispatcher& raw_dispatcher,
    AuthorityValidator authority_validator,
    BattleKcpAdapter* kcp_adapter)
    : channel_(channel),
      active_endpoint_(active_endpoint),
      raw_dispatcher_(raw_dispatcher),
      kcp_adapter_(kcp_adapter),
      authority_validator_(std::move(
          authority_validator)) {
    if (active_endpoint_.port == 0 ||
        !authority_validator_) {
        throw std::invalid_argument(
            "battle multiplexer binding is invalid");
    }
}

BattleMultiplexerResult
BattleAuthenticatedMultiplexer::Handle(
    const std::span<const std::uint8_t> datagram,
    const BattleRemoteEndpoint& remote,
    const BattleRawDispatchContext& raw_context,
    const std::uint64_t now_unix_ms) {
    auto result = BattleMultiplexerResult{
        .disposition =
            BattleMultiplexerDisposition::
                EndpointMismatch,
        .secure_disposition =
            BattleOpenDisposition::InvalidHeader,
        .raw_disposition =
            BattleRawDisposition::InvalidEnvelope,
    };
    if (!SameEndpoint(remote, active_endpoint_)) {
        return result;
    }
    if (!authority_validator_()) {
        result.disposition =
            BattleMultiplexerDisposition::
                AuthorityRejected;
        return result;
    }
    auto opened = channel_.Open(
        datagram,
        now_unix_ms);
    result.secure_disposition = opened.disposition;
    if (opened.disposition !=
        BattleOpenDisposition::Accepted) {
        result.disposition =
            BattleMultiplexerDisposition::
                SecureRejected;
        return result;
    }
    if (opened.packet_kind == BattlePacketKind::Kcp) {
        if (kcp_adapter_ == nullptr) {
            result.disposition =
                BattleMultiplexerDisposition::
                    LaneUnavailable;
            return result;
        }
        const auto kcp_disposition =
            kcp_adapter_->Input(
                opened.plaintext,
                now_unix_ms);
        result.disposition =
            kcp_disposition ==
                    BattleKcpDisposition::Accepted ?
                BattleMultiplexerDisposition::
                    KcpAccepted :
                BattleMultiplexerDisposition::
                    KcpRejected;
        return result;
    }
    if (opened.packet_kind != BattlePacketKind::Raw) {
        result.disposition =
            BattleMultiplexerDisposition::
                LaneUnavailable;
        return result;
    }
    result.raw_disposition =
        raw_dispatcher_.Dispatch(
            opened.plaintext,
            raw_context);
    result.disposition =
        result.raw_disposition ==
                BattleRawDisposition::Accepted ?
            BattleMultiplexerDisposition::RawAccepted :
            BattleMultiplexerDisposition::RawRejected;
    return result;
}

}  // namespace ihomeland::sim
