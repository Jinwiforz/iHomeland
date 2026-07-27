#include "ihomeland/sim/transport/battle_transport_control.hpp"

#include "ihomeland/sim/transport/secure_datagram.hpp"

#include <algorithm>
#include <array>
#include <limits>
#include <ranges>
#include <stdexcept>
#include <utility>
#include <vector>

namespace ihomeland::sim {
namespace {

constexpr std::array<std::uint8_t, 4> ControlMagic{
    'I', 'H', 'B', 'C'};
constexpr std::uint8_t ControlVersion = 1;
constexpr std::size_t KindOffset = 5;
constexpr std::size_t PayloadLengthOffset = 6;
constexpr std::size_t PayloadOffset =
    BattleTransportControl::EnvelopeBytes;
constexpr std::size_t AddressBytes = 16;
constexpr std::size_t PortOffset = AddressBytes;
constexpr std::size_t RebindNonceOffset =
    PortOffset + sizeof(std::uint16_t);
constexpr std::size_t CurrentGenerationOffset = 0;
constexpr std::size_t NextGenerationOffset = 4;
constexpr std::size_t ChallengeNonceOffset = 8;
constexpr std::size_t ChallengeExpiryOffset = 24;
constexpr std::size_t ChallengeCookieOffset = 32;

/// SameEndpoint 对 canonical address 与 UDP port 执行 exact compare。
[[nodiscard]] bool SameEndpoint(
    const BattleRemoteEndpoint& left,
    const BattleRemoteEndpoint& right) noexcept {
    return left.port == right.port &&
           left.address == right.address;
}

/// IsAllZero 拒绝会弱化 transition identity 的空 nonce。
template <std::size_t Size>
[[nodiscard]] bool IsAllZero(
    const std::array<std::uint8_t, Size>& value) noexcept {
    return std::ranges::all_of(
        value,
        [](const std::uint8_t item) {
            return item == 0;
        });
}

/// ReadUint16BE 解码 control payload length 或 UDP port。
[[nodiscard]] std::uint16_t ReadUint16BE(
    const std::uint8_t* input) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(input[0]) << 8U) |
        static_cast<std::uint16_t>(input[1]));
}

/// ReadUint32BE 解码 endpoint generation。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::uint8_t* input) noexcept {
    return
        (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// ReadUint64BE 解码 challenge deadline。
[[nodiscard]] std::uint64_t ReadUint64BE(
    const std::uint8_t* input) noexcept {
    std::uint64_t value = 0;
    for (std::size_t index = 0;
         index < sizeof(value);
         ++index) {
        value =
            (value << 8U) |
            static_cast<std::uint64_t>(input[index]);
    }
    return value;
}

/// WriteUint16BE 编码 control payload length 或 UDP port。
void WriteUint16BE(
    std::uint8_t* output,
    const std::uint16_t value) noexcept {
    output[0] =
        static_cast<std::uint8_t>(value >> 8U);
    output[1] = static_cast<std::uint8_t>(value);
}

/// WriteUint32BE 编码 endpoint generation。
void WriteUint32BE(
    std::uint8_t* output,
    const std::uint32_t value) noexcept {
    output[0] =
        static_cast<std::uint8_t>(value >> 24U);
    output[1] =
        static_cast<std::uint8_t>(value >> 16U);
    output[2] =
        static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// WriteUint64BE 编码 challenge deadline。
void WriteUint64BE(
    std::uint8_t* output,
    const std::uint64_t value) noexcept {
    for (std::size_t index = 0;
         index < sizeof(value);
         ++index) {
        output[sizeof(value) - 1U - index] =
            static_cast<std::uint8_t>(
                value >> (index * 8U));
    }
}

/// EncodeControl 构造唯一 versioned control plaintext。
[[nodiscard]] std::vector<std::uint8_t>
EncodeControl(
    const BattleTransportControlKind kind,
    const std::span<const std::uint8_t> payload) {
    if (payload.empty() ||
        payload.size() >
            std::numeric_limits<std::uint16_t>::max()) {
        throw std::invalid_argument(
            "battle control payload is invalid");
    }
    std::vector<std::uint8_t> output(
        BattleTransportControl::EnvelopeBytes +
        payload.size());
    std::ranges::copy(ControlMagic, output.begin());
    output[4] = ControlVersion;
    output[KindOffset] =
        static_cast<std::uint8_t>(kind);
    WriteUint16BE(
        output.data() + PayloadLengthOffset,
        static_cast<std::uint16_t>(payload.size()));
    std::ranges::copy(
        payload,
        output.begin() + PayloadOffset);
    return output;
}

/// ParseKind 严格验证 envelope 并返回已登记 kind。
[[nodiscard]] std::optional<BattleTransportControlKind>
ParseKind(
    const std::span<const std::uint8_t> plaintext) {
    if (plaintext.size() <
            BattleTransportControl::EnvelopeBytes ||
        !std::ranges::equal(
            ControlMagic,
            plaintext.first(ControlMagic.size())) ||
        plaintext[4] != ControlVersion ||
        ReadUint16BE(
            plaintext.data() +
            PayloadLengthOffset) !=
            plaintext.size() -
                BattleTransportControl::EnvelopeBytes) {
        return std::nullopt;
    }
    const auto kind = plaintext[KindOffset];
    if (kind <
            static_cast<std::uint8_t>(
                BattleTransportControlKind::
                    RebindRequest) ||
        kind >
            static_cast<std::uint8_t>(
                BattleTransportControlKind::
                    CloseAcknowledged)) {
        return std::nullopt;
    }
    return static_cast<
        BattleTransportControlKind>(kind);
}

}  // namespace

/// BattleTransportControl::Impl 保存单 session transport transition state。
struct BattleTransportControl::Impl final {
    /// Queue 封装 control、AEAD seal 与唯一 listener output。
    [[nodiscard]] bool Queue(
        const BattleTransportControlKind kind,
        const std::span<const std::uint8_t> payload,
        const BattleRemoteEndpoint& target,
        const std::uint64_t now_unix_ms) {
        const auto plaintext =
            EncodeControl(kind, payload);
        const auto sealed = channel->Seal(
            BattlePacketKind::Control,
            plaintext,
            now_unix_ms);
        return sealed.disposition ==
                   BattleSealDisposition::Sealed &&
               sender(
                   sealed.datagram,
                   target) ==
                   BattleUdpSendDisposition::Queued;
    }

    /// HandleRebindRequest 签发与 candidate endpoint 绑定的短期 challenge。
    [[nodiscard]] BattleTransportControlResult
    HandleRebindRequest(
        const std::span<const std::uint8_t> payload,
        const BattleRemoteEndpoint& remote,
        const std::uint64_t now_unix_ms) {
        if (payload.size() !=
                RebindRequestPayloadBytes ||
            !SameEndpoint(
                remote,
                rebinder->ActiveEndpoint())) {
            return Rejected();
        }
        auto candidate = BattleRemoteEndpoint{};
        std::ranges::copy_n(
            payload.begin(),
            candidate.address.size(),
            candidate.address.begin());
        candidate.port = ReadUint16BE(
            payload.data() + PortOffset);
        std::array<std::uint8_t, 16> nonce{};
        std::ranges::copy_n(
            payload.begin() + RebindNonceOffset,
            nonce.size(),
            nonce.begin());
        const auto begun = rebinder->Begin(
            remote,
            candidate,
            nonce,
            true,
            now_unix_ms);
        if (begun.disposition !=
                BattleRebindDisposition::
                    ChallengeIssued ||
            !begun.challenge.has_value()) {
            return Rejected();
        }
        std::array<
            std::uint8_t,
            RebindChallengePayloadBytes> encoded{};
        WriteUint32BE(
            encoded.data() +
                CurrentGenerationOffset,
            begun.challenge->current_generation);
        WriteUint32BE(
            encoded.data() + NextGenerationOffset,
            begun.challenge->next_generation);
        std::ranges::copy(
            begun.challenge->nonce,
            encoded.begin() +
                ChallengeNonceOffset);
        WriteUint64BE(
            encoded.data() + ChallengeExpiryOffset,
            begun.challenge->expires_at_unix_ms);
        std::ranges::copy(
            begun.challenge->cookie,
            encoded.begin() +
                ChallengeCookieOffset);
        if (!Queue(
                BattleTransportControlKind::
                    RebindChallenge,
                encoded,
                candidate,
                now_unix_ms)) {
            return Fatal();
        }
        return {
            .disposition =
                BattleTransportControlDisposition::
                    RebindChallengeQueued,
            .close_session = false,
        };
    }

    /// HandleRebindConfirm 原子排队旧generation ack并切换唯一endpoint。
    [[nodiscard]] BattleTransportControlResult
    HandleRebindConfirm(
        const std::span<const std::uint8_t> payload,
        const BattleRemoteEndpoint& remote,
        const std::uint64_t now_unix_ms) {
        if (payload.size() !=
            RebindChallengePayloadBytes) {
            return Rejected();
        }
        auto challenge = BattleRebindChallenge{
            .candidate = remote,
            .current_generation =
                ReadUint32BE(
                    payload.data() +
                    CurrentGenerationOffset),
            .next_generation =
                ReadUint32BE(
                    payload.data() +
                    NextGenerationOffset),
            .nonce = {},
            .expires_at_unix_ms =
                ReadUint64BE(
                    payload.data() +
                    ChallengeExpiryOffset),
            .cookie = {},
        };
        std::ranges::copy_n(
            payload.begin() + ChallengeNonceOffset,
            challenge.nonce.size(),
            challenge.nonce.begin());
        std::ranges::copy_n(
            payload.begin() + ChallengeCookieOffset,
            challenge.cookie.size(),
            challenge.cookie.begin());
        const auto disposition = rebinder->Confirm(
            remote,
            challenge,
            true,
            now_unix_ms,
            [this, &remote, now_unix_ms](
                const BattleRebindChallenge&
                    accepted) {
                std::array<
                    std::uint8_t,
                    RebindCommittedPayloadBytes>
                    acknowledgement{};
                WriteUint32BE(
                    acknowledgement.data(),
                    accepted.next_generation);
                std::ranges::copy(
                    accepted.nonce,
                    acknowledgement.begin() + 4);
                return Queue(
                    BattleTransportControlKind::
                        RebindCommitted,
                    acknowledgement,
                    remote,
                    now_unix_ms);
            });
        if (disposition ==
            BattleRebindDisposition::Committed) {
            return {
                .disposition =
                    BattleTransportControlDisposition::
                        RebindCommitted,
                .close_session = false,
            };
        }
        return disposition ==
                   BattleRebindDisposition::
                       OutputUnavailable ?
            Fatal() :
            Rejected();
    }

    /// HandleRekeyProposal 排队旧epoch ack后原子切换双方约定的next epoch。
    [[nodiscard]] BattleTransportControlResult
    HandleRekeyProposal(
        const std::span<const std::uint8_t> payload,
        const BattleRemoteEndpoint& remote,
        const std::uint64_t now_unix_ms) {
        if (payload.size() != RekeyPayloadBytes ||
            !SameEndpoint(
                remote,
                rebinder->ActiveEndpoint())) {
            return Rejected();
        }
        CryptoProvider::Key32 nonce{};
        std::ranges::copy(
            payload,
            nonce.begin());
        if (IsAllZero(nonce) ||
            !Queue(
                BattleTransportControlKind::
                    RekeyCommitted,
                nonce,
                remote,
                now_unix_ms)) {
            crypto->SecureZero(nonce);
            return Fatal();
        }
        const auto committed =
            channel->CommitAuthenticatedRollover(
                nonce,
                now_unix_ms);
        crypto->SecureZero(nonce);
        return committed ?
            BattleTransportControlResult{
                .disposition =
                    BattleTransportControlDisposition::
                        RekeyCommitted,
                .close_session = false,
            } :
            Fatal();
    }

    /// HandleCloseRequest 仅在ack进入唯一listener队列后标记正常关闭。
    [[nodiscard]] BattleTransportControlResult
    HandleCloseRequest(
        const std::span<const std::uint8_t> payload,
        const BattleRemoteEndpoint& remote,
        const std::uint64_t now_unix_ms) {
        if (payload.size() != ClosePayloadBytes ||
            payload.front() !=
                static_cast<std::uint8_t>(
                    BattleTransportCloseCode::
                        ClientRequested) ||
            !SameEndpoint(
                remote,
                rebinder->ActiveEndpoint())) {
            return Rejected();
        }
        for (std::size_t copy = 0;
             copy <
             BattleTransportControl::
                 CloseAcknowledgementCopies;
             ++copy) {
            if (!Queue(
                    BattleTransportControlKind::
                        CloseAcknowledged,
                    payload,
                    remote,
                    now_unix_ms)) {
                return Fatal();
            }
        }
        return {
            .disposition =
                BattleTransportControlDisposition::
                    CloseRequested,
            .close_session = true,
        };
    }

    /// Rejected 返回authenticated protocol violation。
    [[nodiscard]] static
    BattleTransportControlResult Rejected() noexcept {
        return {
            .disposition =
                BattleTransportControlDisposition::
                    Rejected,
            .close_session = true,
        };
    }

    /// Fatal 返回output/crypto不可恢复失败。
    [[nodiscard]] static
    BattleTransportControlResult Fatal() noexcept {
        return {
            .disposition =
                BattleTransportControlDisposition::
                    Fatal,
            .close_session = true,
        };
    }

    /// crypto 是 nonce 清零 owner。
    CryptoProvider* crypto;
    /// channel 独占当前/previous traffic epoch。
    BattleSecureChannel* channel;
    /// sender 只指向同一个 node listener。
    DatagramSender sender;
    /// rebinder 独占 active endpoint、generation 与 cookie key。
    std::unique_ptr<BattleEndpointRebinder> rebinder;
};

BattleTransportControl::BattleTransportControl(
    CryptoProvider& crypto,
    BattleSecureChannel& channel,
    const BattleRemoteEndpoint initial_endpoint,
    const std::uint32_t initial_endpoint_generation,
    DatagramSender sender,
    BattleRuntimeMetrics* runtime_metrics)
    : impl_(std::make_unique<Impl>()) {
    if (!sender) {
        throw std::invalid_argument(
            "battle control sender is missing");
    }
    impl_->crypto = &crypto;
    impl_->channel = &channel;
    impl_->sender = std::move(sender);
    impl_->rebinder =
        std::make_unique<BattleEndpointRebinder>(
            crypto,
            channel,
            initial_endpoint,
            initial_endpoint_generation,
            runtime_metrics);
}

BattleTransportControl::~BattleTransportControl() =
    default;

BattleTransportControlResult
BattleTransportControl::Handle(
    const std::span<const std::uint8_t> plaintext,
    const BattleRemoteEndpoint& remote,
    const std::uint64_t now_unix_ms) {
    const auto kind = ParseKind(plaintext);
    if (!kind.has_value() ||
        now_unix_ms == 0) {
        return Impl::Rejected();
    }
    const auto payload =
        plaintext.subspan(EnvelopeBytes);
    switch (*kind) {
        case BattleTransportControlKind::
            RebindRequest:
            return impl_->HandleRebindRequest(
                payload,
                remote,
                now_unix_ms);
        case BattleTransportControlKind::
            RebindConfirm:
            return impl_->HandleRebindConfirm(
                payload,
                remote,
                now_unix_ms);
        case BattleTransportControlKind::
            RekeyProposal:
            return impl_->HandleRekeyProposal(
                payload,
                remote,
                now_unix_ms);
        case BattleTransportControlKind::
            CloseRequest:
            return impl_->HandleCloseRequest(
                payload,
                remote,
                now_unix_ms);
        case BattleTransportControlKind::
            RebindChallenge:
        case BattleTransportControlKind::
            RebindCommitted:
        case BattleTransportControlKind::
            RekeyCommitted:
        case BattleTransportControlKind::
            CloseAcknowledged:
            return Impl::Rejected();
    }
    return Impl::Rejected();
}

BattleRemoteEndpoint
BattleTransportControl::ActiveEndpoint() const {
    return impl_->rebinder->ActiveEndpoint();
}

}  // namespace ihomeland::sim
