#include "ihomeland/sim/transport/authenticated_multiplexer.hpp"

#include "ihomeland/sim/observability/battle_runtime_metrics.hpp"
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
    ActiveEndpointProvider active_endpoint_provider,
    BattleRawDispatcher& raw_dispatcher,
    AuthorityValidator authority_validator,
    ControlHandler control_handler,
    BattleKcpAdapter* kcp_adapter,
    BattleRuntimeMetrics* runtime_metrics)
    : channel_(channel),
      active_endpoint_provider_(
          std::move(active_endpoint_provider)),
      raw_dispatcher_(raw_dispatcher),
      kcp_adapter_(kcp_adapter),
      authority_validator_(std::move(
          authority_validator)),
      control_handler_(std::move(
          control_handler)),
      runtime_metrics_(runtime_metrics) {
    if (!active_endpoint_provider_ ||
        !authority_validator_ ||
        !control_handler_ ||
        active_endpoint_provider_().port == 0) {
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
    const auto active_endpoint =
        active_endpoint_provider_();
    const auto from_active_endpoint =
        SameEndpoint(remote, active_endpoint);
    const auto declared_control =
        datagram.size() >
            BattleSecureChannel::SecureHeaderBytes &&
        datagram[5] ==
            static_cast<std::uint8_t>(
                BattlePacketKind::Control);
    if (!from_active_endpoint &&
        !declared_control) {
        if (runtime_metrics_ != nullptr) {
            runtime_metrics_->RecordReject();
        }
        return result;
    }
    if (!authority_validator_()) {
        result.disposition =
            BattleMultiplexerDisposition::
                AuthorityRejected;
        if (runtime_metrics_ != nullptr) {
            runtime_metrics_->RecordReject();
        }
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
        if (runtime_metrics_ != nullptr) {
            runtime_metrics_->RecordReject();
        }
        return result;
    }
    if (opened.packet_kind ==
        BattlePacketKind::Control) {
        const auto control_disposition =
            control_handler_(
                opened.plaintext,
                remote,
                now_unix_ms);
        result.disposition =
            control_disposition ==
                    BattleControlDispatchDisposition::
                        Accepted ?
                BattleMultiplexerDisposition::
                    ControlAccepted :
            control_disposition ==
                    BattleControlDispatchDisposition::
                        CloseRequested ?
                BattleMultiplexerDisposition::
                    CloseRequested :
                BattleMultiplexerDisposition::
                    ControlRejected;
        if (runtime_metrics_ != nullptr &&
            result.disposition ==
                BattleMultiplexerDisposition::
                    ControlRejected) {
            runtime_metrics_->RecordReject();
        }
        return result;
    }
    if (!from_active_endpoint) {
        result.disposition =
            BattleMultiplexerDisposition::
                EndpointMismatch;
        if (runtime_metrics_ != nullptr) {
            runtime_metrics_->RecordReject();
        }
        return result;
    }
    if (opened.packet_kind == BattlePacketKind::Kcp) {
        if (kcp_adapter_ == nullptr) {
            result.disposition =
                BattleMultiplexerDisposition::
                    LaneUnavailable;
            if (runtime_metrics_ != nullptr) {
                runtime_metrics_->RecordReject();
            }
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
        if (runtime_metrics_ != nullptr) {
            runtime_metrics_->RecordReject();
        }
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
    if (runtime_metrics_ != nullptr &&
        result.disposition ==
            BattleMultiplexerDisposition::RawRejected) {
        runtime_metrics_->RecordReject();
    }
    return result;
}

}  // namespace ihomeland::sim
