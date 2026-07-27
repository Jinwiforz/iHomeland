#include "ihomeland/sim/transport/battle_transport_control.hpp"

#include "ihomeland/sim/transport/secure_datagram.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <ranges>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace {

constexpr std::uint64_t NowUnixMs = 12'000'000;
constexpr std::array<std::uint8_t, 4> ControlMagic{
    'I', 'H', 'B', 'C'};
constexpr std::uint8_t ControlVersion = 1;
constexpr std::size_t PayloadLengthOffset = 6;

/// Require 把control composition漂移转换为test failure。
void Require(
    const bool condition,
    const std::string_view message) {
    if (!condition) {
        throw std::runtime_error(std::string(message));
    }
}

/// Sequence 生成非秘密固定fixture bytes。
template <std::size_t Size>
[[nodiscard]] std::array<std::uint8_t, Size>
Sequence(const std::uint8_t first) {
    std::array<std::uint8_t, Size> output{};
    for (std::size_t index = 0;
         index < output.size();
         ++index) {
        output[index] =
            static_cast<std::uint8_t>(
                first + index);
    }
    return output;
}

/// Endpoint 构造canonical IPv4-mapped loopback变体。
[[nodiscard]] ihomeland::sim::BattleRemoteEndpoint
Endpoint(
    const std::uint8_t host,
    const std::uint16_t port) {
    auto endpoint =
        ihomeland::sim::BattleRemoteEndpoint{};
    endpoint.address[10] = 0xff;
    endpoint.address[11] = 0xff;
    endpoint.address[12] = 127;
    endpoint.address[15] = host;
    endpoint.port = port;
    return endpoint;
}

/// Identity 返回client/server共享的secure route identity。
[[nodiscard]] ihomeland::sim::BattleSecureIdentity
Identity() {
    return {
        .session_id_digest = Sequence<8>(0x10),
        .battle_session_generation = 3,
        .endpoint_generation = 1,
        .binding_discriminator = Sequence<8>(0x20),
    };
}

/// WriteUint16BE 编码control payload length或candidate port。
void WriteUint16BE(
    std::uint8_t* output,
    const std::uint16_t value) {
    output[0] =
        static_cast<std::uint8_t>(value >> 8U);
    output[1] = static_cast<std::uint8_t>(value);
}

/// ReadUint32BE 解码rebind committed generation。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::uint8_t* input) {
    return
        (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// EncodeControl 构造独立client使用的closed control envelope。
[[nodiscard]] std::vector<std::uint8_t>
EncodeControl(
    const ihomeland::sim::
        BattleTransportControlKind kind,
    const std::span<const std::uint8_t> payload) {
    std::vector<std::uint8_t> output(
        ihomeland::sim::
            BattleTransportControl::EnvelopeBytes +
        payload.size());
    std::ranges::copy(
        ControlMagic,
        output.begin());
    output[4] = ControlVersion;
    output[5] =
        static_cast<std::uint8_t>(kind);
    WriteUint16BE(
        output.data() + PayloadLengthOffset,
        static_cast<std::uint16_t>(
            payload.size()));
    std::ranges::copy(
        payload,
        output.begin() +
            ihomeland::sim::
                BattleTransportControl::
                    EnvelopeBytes);
    return output;
}

/// QueuedDatagram 保存同listener output与目标remote。
struct QueuedDatagram final {
    /// bytes 是secure datagram owned copy。
    std::vector<std::uint8_t> bytes;
    /// target 是sender收到的canonical remote。
    ihomeland::sim::BattleRemoteEndpoint target;
};

/// TestRebindComposition 验证challenge、ack、generation和endpoint原子连续。
void TestRebindComposition() {
    ihomeland::sim::CryptoProvider crypto;
    const auto seed = Sequence<32>(0x30);
    auto client =
        ihomeland::sim::BattleSecureChannel(
            crypto,
            seed,
            ihomeland::sim::
                BattleTransportRole::Client,
            Identity(),
            1,
            1,
            NowUnixMs,
            0);
    auto server =
        ihomeland::sim::BattleSecureChannel(
            crypto,
            seed,
            ihomeland::sim::
                BattleTransportRole::Server,
            Identity(),
            1,
            1,
            NowUnixMs,
            0);
    const auto initial = Endpoint(1, 40'000);
    const auto candidate = Endpoint(2, 40'001);
    std::vector<QueuedDatagram> queued;
    auto control =
        ihomeland::sim::BattleTransportControl(
            crypto,
            server,
            initial,
            1,
            [&](const std::span<const std::uint8_t>
                    datagram,
                const ihomeland::sim::
                    BattleRemoteEndpoint& target) {
                queued.push_back({
                    .bytes = {
                        datagram.begin(),
                        datagram.end()},
                    .target = target,
                });
                return ihomeland::sim::
                    BattleUdpSendDisposition::Queued;
            });

    std::array<
        std::uint8_t,
        ihomeland::sim::BattleTransportControl::
            RebindRequestPayloadBytes> request{};
    std::ranges::copy(
        candidate.address,
        request.begin());
    WriteUint16BE(
        request.data() + candidate.address.size(),
        candidate.port);
    const auto nonce = Sequence<16>(0x50);
    std::ranges::copy(
        nonce,
        request.begin() +
            candidate.address.size() +
            sizeof(candidate.port));
    const auto request_plaintext = EncodeControl(
        ihomeland::sim::
            BattleTransportControlKind::
                RebindRequest,
        request);
    Require(
        control.Handle(
            request_plaintext,
            initial,
            NowUnixMs).disposition ==
                ihomeland::sim::
                    BattleTransportControlDisposition::
                        RebindChallengeQueued &&
            queued.size() == 1 &&
            queued.front().target.address ==
                candidate.address &&
            queued.front().target.port ==
                candidate.port,
        "rebind request did not queue candidate challenge");

    const auto challenge = client.Open(
        queued.front().bytes,
        NowUnixMs);
    Require(
        challenge.disposition ==
                ihomeland::sim::
                    BattleOpenDisposition::Accepted &&
            challenge.packet_kind ==
                ihomeland::sim::
                    BattlePacketKind::Control &&
            challenge.plaintext.size() ==
                ihomeland::sim::
                    BattleTransportControl::
                        EnvelopeBytes +
                ihomeland::sim::
                    BattleTransportControl::
                        RebindChallengePayloadBytes &&
            challenge.plaintext[5] ==
                static_cast<std::uint8_t>(
                    ihomeland::sim::
                        BattleTransportControlKind::
                            RebindChallenge),
        "candidate could not authenticate rebind challenge");
    auto confirm = challenge.plaintext;
    confirm[5] =
        static_cast<std::uint8_t>(
            ihomeland::sim::
                BattleTransportControlKind::
                    RebindConfirm);
    Require(
        control.Handle(
            confirm,
            candidate,
            NowUnixMs + 1).disposition ==
                ihomeland::sim::
                    BattleTransportControlDisposition::
                        RebindCommitted &&
            queued.size() == 2 &&
            control.ActiveEndpoint().address ==
                candidate.address &&
            control.ActiveEndpoint().port ==
                candidate.port,
        "candidate confirm did not atomically commit rebind");

    const auto committed = client.Open(
        queued.back().bytes,
        NowUnixMs + 1);
    Require(
        committed.disposition ==
                ihomeland::sim::
                    BattleOpenDisposition::Accepted &&
            committed.plaintext[5] ==
                static_cast<std::uint8_t>(
                    ihomeland::sim::
                        BattleTransportControlKind::
                            RebindCommitted),
        "client could not authenticate pre-commit rebind ack");
    const auto next_generation = ReadUint32BE(
        committed.plaintext.data() +
        ihomeland::sim::
            BattleTransportControl::EnvelopeBytes);
    Require(
        client.CommitEndpointGeneration(
            1,
            next_generation),
        "client could not commit acknowledged endpoint generation");
    const auto after = client.Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        std::array<std::uint8_t, 1>{1},
        NowUnixMs + 2);
    Require(
        server.Open(
            after.datagram,
            NowUnixMs + 2).disposition ==
            ihomeland::sim::
                BattleOpenDisposition::Accepted,
        "rebind reset secure state or diverged generation");
}

/// TestRekeyAndCloseComposition 验证旧epoch ack、双方commit与terminal close。
void TestRekeyAndCloseComposition() {
    ihomeland::sim::CryptoProvider crypto;
    const auto seed = Sequence<32>(0x60);
    auto client =
        ihomeland::sim::BattleSecureChannel(
            crypto,
            seed,
            ihomeland::sim::
                BattleTransportRole::Client,
            Identity(),
            1,
            1,
            NowUnixMs,
            0);
    auto server =
        ihomeland::sim::BattleSecureChannel(
            crypto,
            seed,
            ihomeland::sim::
                BattleTransportRole::Server,
            Identity(),
            1,
            1,
            NowUnixMs,
            0);
    const auto endpoint = Endpoint(1, 41'000);
    std::vector<QueuedDatagram> queued;
    auto control =
        ihomeland::sim::BattleTransportControl(
            crypto,
            server,
            endpoint,
            1,
            [&](const std::span<const std::uint8_t>
                    datagram,
                const ihomeland::sim::
                    BattleRemoteEndpoint& target) {
                queued.push_back({
                    .bytes = {
                        datagram.begin(),
                        datagram.end()},
                    .target = target,
                });
                return ihomeland::sim::
                    BattleUdpSendDisposition::Queued;
            });
    auto nonce = Sequence<32>(0x70);
    const auto proposal = EncodeControl(
        ihomeland::sim::
            BattleTransportControlKind::
                RekeyProposal,
        nonce);
    Require(
        control.Handle(
            proposal,
            endpoint,
            NowUnixMs + 1).disposition ==
                ihomeland::sim::
                    BattleTransportControlDisposition::
                        RekeyCommitted &&
            queued.size() == 1,
        "authenticated rekey proposal did not commit");
    const auto acknowledged = client.Open(
        queued.back().bytes,
        NowUnixMs + 1);
    Require(
        acknowledged.disposition ==
                ihomeland::sim::
                    BattleOpenDisposition::Accepted &&
            acknowledged.plaintext[5] ==
                static_cast<std::uint8_t>(
                    ihomeland::sim::
                        BattleTransportControlKind::
                            RekeyCommitted) &&
            client.CommitAuthenticatedRollover(
                nonce,
                NowUnixMs + 1),
        "client could not authenticate and commit rekey ack");
    crypto.SecureZero(nonce);
    const auto after = client.Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        std::array<std::uint8_t, 1>{1},
        NowUnixMs + 2);
    Require(
        server.Open(
            after.datagram,
            NowUnixMs + 2).disposition ==
            ihomeland::sim::
                BattleOpenDisposition::Accepted,
        "rekey diverged client/server current epoch");

    const std::array<std::uint8_t, 1> close_payload{
        static_cast<std::uint8_t>(
            ihomeland::sim::
                BattleTransportCloseCode::
                    ClientRequested)};
    const auto close = EncodeControl(
        ihomeland::sim::
            BattleTransportControlKind::
                CloseRequest,
        close_payload);
    const auto close_result = control.Handle(
        close,
        endpoint,
        NowUnixMs + 3);
    Require(
        close_result.disposition ==
                ihomeland::sim::
                    BattleTransportControlDisposition::
                        CloseRequested &&
            close_result.close_session &&
            queued.size() ==
                1 +
                    ihomeland::sim::
                        BattleTransportControl::
                            CloseAcknowledgementCopies,
        "authenticated close did not request terminal cleanup");
    for (std::size_t index = 1;
         index < queued.size();
         ++index) {
        const auto close_acknowledged =
            client.Open(
                queued[index].bytes,
                NowUnixMs + 3);
        Require(
            close_acknowledged.disposition ==
                    ihomeland::sim::
                        BattleOpenDisposition::Accepted &&
                close_acknowledged.plaintext[5] ==
                    static_cast<std::uint8_t>(
                        ihomeland::sim::
                            BattleTransportControlKind::
                                CloseAcknowledged),
            "redundant close acknowledgement was not independently authenticated");
    }

    auto failing_server =
        ihomeland::sim::BattleSecureChannel(
            crypto,
            seed,
            ihomeland::sim::
                BattleTransportRole::Server,
            Identity(),
            1,
            1,
            NowUnixMs,
            0);
    auto failing_control =
        ihomeland::sim::BattleTransportControl(
            crypto,
            failing_server,
            endpoint,
            1,
            [](const std::span<const std::uint8_t>,
               const ihomeland::sim::
                   BattleRemoteEndpoint&) {
                return ihomeland::sim::
                    BattleUdpSendDisposition::QueueFull;
            });
    const auto failed_close =
        failing_control.Handle(
            close,
            endpoint,
            NowUnixMs + 3);
    Require(
        failed_close.disposition ==
                ihomeland::sim::
                    BattleTransportControlDisposition::
                        Fatal &&
            failed_close.close_session,
        "failed close acknowledgement was misclassified as normal close");

    auto malformed = close;
    malformed[4] = 2;
    Require(
        control.Handle(
            malformed,
            endpoint,
            NowUnixMs + 4).close_session,
        "authenticated malformed control was not fail closed");
}

}  // namespace

int main() {
    TestRebindComposition();
    TestRekeyAndCloseComposition();
    return 0;
}
