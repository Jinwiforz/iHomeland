#include "ihomeland/sim/transport/secure_datagram.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <limits>
#include <stdexcept>
#include <string_view>
#include <vector>

namespace {

constexpr std::uint64_t NowUnixMs = 5'000'000;

/// Require 把 secure datagram contract drift 转换为 test failure。
void Require(
    const bool condition,
    const std::string_view message) {
    if (!condition) {
        throw std::runtime_error(std::string(message));
    }
}

/// Sequence 返回固定公开测试 bytes。
template <std::size_t Size>
[[nodiscard]] std::array<std::uint8_t, Size> Sequence(
    const std::uint8_t start) {
    std::array<std::uint8_t, Size> result{};
    for (std::size_t index = 0; index < Size; ++index) {
        result[index] = static_cast<std::uint8_t>(
            start + index);
    }
    return result;
}

/// Identity 返回与 canonical secure-header fixture 一致的 binding。
[[nodiscard]] ihomeland::sim::BattleSecureIdentity
Identity() {
    return {
        .session_id_digest = Sequence<8>(0x00),
        .battle_session_generation = 9,
        .endpoint_generation = 2,
        .binding_discriminator = Sequence<8>(0x10),
    };
}

/// Payload 返回指定长度的稳定非秘密测试 payload。
[[nodiscard]] std::vector<std::uint8_t> Payload(
    const std::size_t size,
    const std::uint8_t start = 0x20) {
    std::vector<std::uint8_t> result(size);
    for (std::size_t index = 0; index < size; ++index) {
        result[index] = static_cast<std::uint8_t>(
            start + index);
    }
    return result;
}

/// MakeChannel 使用相同 session seed 构造可互通的 client/server endpoint。
[[nodiscard]] std::unique_ptr<
    ihomeland::sim::BattleSecureChannel>
MakeChannel(
    ihomeland::sim::CryptoProvider& crypto,
    const ihomeland::sim::BattleTransportRole role,
    const std::uint64_t next_sequence = 1,
    const std::uint64_t sent_packets = 0,
    const std::uint32_t key_epoch = 0x01020304,
    const std::uint64_t epoch_started_unix_ms =
        NowUnixMs) {
    return std::make_unique<
        ihomeland::sim::BattleSecureChannel>(
        crypto,
        Sequence<32>(0x80),
        role,
        Identity(),
        key_epoch,
        next_sequence,
        epoch_started_unix_ms,
        sent_packets);
}

/// TestNonceAndCanonicalHeader 验证epoch||sequence大端nonce与48-byte AAD。
void TestNonceAndCanonicalHeader() {
    const auto nonce =
        ihomeland::sim::BattlePacketNonce(
            0x01020304,
            0x0102030405060708);
    Require(
        nonce ==
            std::array<std::uint8_t, 12>{
                0x01, 0x02, 0x03, 0x04,
                0x01, 0x02, 0x03, 0x04,
                0x05, 0x06, 0x07, 0x08},
        "packet nonce is not epoch-u32-be||sequence-u64-be");

    ihomeland::sim::CryptoProvider crypto;
    auto sender = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Client,
        0x0102030405060708);
    const auto sealed = sender->Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        Payload(22),
        NowUnixMs);
    const std::array<std::uint8_t, 48> expected{
        0x49, 0x48, 0x42, 0x54, 0x01, 0x01, 0x00, 0x00,
        0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
        0x00, 0x00, 0x00, 0x09, 0x01, 0x02, 0x03, 0x04,
        0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
        0x00, 0x16, 0x00, 0x00, 0x00, 0x02, 0x10, 0x11,
        0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x00, 0x00};
    Require(
        sealed.disposition ==
                ihomeland::sim::BattleSealDisposition::Sealed &&
            std::ranges::equal(
                expected,
                std::span(sealed.datagram).first(48)),
        "secure header drifted from canonical fixture");
}

/// TestDirectionAndReplay 验证独立方向key、乱序、duplicate与too-old。
void TestDirectionAndReplay() {
    ihomeland::sim::CryptoProvider crypto;
    auto client = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Client);
    auto server = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Server);
    const auto first_payload = Payload(16);
    const auto first = client->Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        first_payload,
        NowUnixMs);
    const auto accepted = server->Open(
        first.datagram,
        NowUnixMs);
    Require(
        accepted.disposition ==
                ihomeland::sim::BattleOpenDisposition::Accepted &&
            accepted.packet_sequence == 1 &&
            accepted.packet_kind ==
                ihomeland::sim::BattlePacketKind::Raw &&
            accepted.plaintext == first_payload,
        "valid C2S packet was not accepted");

    const auto second = client->Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        Payload(16, 0x30),
        NowUnixMs);
    const auto third = client->Seal(
        ihomeland::sim::BattlePacketKind::Kcp,
        Payload(24, 0x40),
        NowUnixMs);
    Require(
        server->Open(third.datagram, NowUnixMs).disposition ==
                ihomeland::sim::BattleOpenDisposition::Accepted &&
            server->Open(second.datagram, NowUnixMs).disposition ==
                ihomeland::sim::BattleOpenDisposition::Accepted,
        "in-window reorder was rejected");
    Require(
        server->Open(second.datagram, NowUnixMs).disposition ==
            ihomeland::sim::BattleOpenDisposition::Duplicate,
        "duplicate packet was accepted");

    for (std::uint64_t sequence = 4; sequence <= 260;
         ++sequence) {
        const auto packet = client->Seal(
            ihomeland::sim::BattlePacketKind::Raw,
            Payload(1, static_cast<std::uint8_t>(sequence)),
            NowUnixMs);
        Require(
            server->Open(packet.datagram, NowUnixMs).disposition ==
                ihomeland::sim::BattleOpenDisposition::Accepted,
            "monotonic packet was rejected");
    }
    Require(
        server->Open(second.datagram, NowUnixMs).disposition ==
            ihomeland::sim::BattleOpenDisposition::TooOld,
        "packet outside 256-window was not too-old");

    const auto server_packet = server->Seal(
        ihomeland::sim::BattlePacketKind::Control,
        Payload(8, 0x70),
        NowUnixMs);
    Require(
        server->Open(server_packet.datagram, NowUnixMs).disposition ==
            ihomeland::sim::BattleOpenDisposition::
                AuthenticationFailed,
        "S2C packet authenticated with C2S receive key");
    Require(
        client->Open(server_packet.datagram, NowUnixMs).disposition ==
            ihomeland::sim::BattleOpenDisposition::Accepted,
        "client did not accept S2C direction");
}

/// TestTamperDoesNotCommit 验证AAD/ciphertext/tag失败不推进window。
void TestTamperDoesNotCommit() {
    ihomeland::sim::CryptoProvider crypto;
    auto client = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Client);
    auto server = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Server);
    const auto first = client->Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        Payload(12),
        NowUnixMs);
    auto aad_tamper = first.datagram;
    aad_tamper[5] = static_cast<std::uint8_t>(
        ihomeland::sim::BattlePacketKind::Kcp);
    Require(
        server->Open(aad_tamper, NowUnixMs).disposition ==
            ihomeland::sim::BattleOpenDisposition::
                AuthenticationFailed,
        "AAD tamper was authenticated");
    Require(
        server->Open(first.datagram, NowUnixMs).disposition ==
            ihomeland::sim::BattleOpenDisposition::Accepted,
        "AAD failure committed replay window");

    const auto second = client->Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        Payload(12, 0x40),
        NowUnixMs);
    auto ciphertext_tamper = second.datagram;
    ciphertext_tamper[48] ^= 1;
    Require(
        server->Open(ciphertext_tamper, NowUnixMs).disposition ==
            ihomeland::sim::BattleOpenDisposition::
                AuthenticationFailed,
        "ciphertext tamper was authenticated");
    auto tag_tamper = second.datagram;
    tag_tamper.back() ^= 1;
    Require(
        server->Open(tag_tamper, NowUnixMs).disposition ==
            ihomeland::sim::BattleOpenDisposition::
                AuthenticationFailed,
        "tag tamper was authenticated");
    Require(
        server->Open(second.datagram, NowUnixMs).disposition ==
            ihomeland::sim::BattleOpenDisposition::Accepted,
        "AEAD failure committed replay window");
}

/// TestFutureJumpAndExhaustion 验证authenticated jump关闭与send不wrap。
void TestFutureJumpAndExhaustion() {
    ihomeland::sim::CryptoProvider crypto;
    auto initial = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Client);
    auto receiver = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Server);
    const auto first = initial->Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        Payload(1),
        NowUnixMs);
    Require(
        receiver->Open(first.datagram, NowUnixMs).disposition ==
            ihomeland::sim::BattleOpenDisposition::Accepted,
        "future-jump setup failed");
    auto jump_sender = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Client,
        258);
    const auto jump = jump_sender->Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        Payload(1),
        NowUnixMs);
    Require(
        receiver->Open(jump.datagram, NowUnixMs).disposition ==
            ihomeland::sim::BattleOpenDisposition::FutureJump,
        "authenticated future jump did not require close");

    auto exhausted = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Client,
        std::numeric_limits<std::uint64_t>::max());
    const auto final_packet = exhausted->Seal(
        ihomeland::sim::BattlePacketKind::Control,
        Payload(1),
        NowUnixMs);
    const auto rejected = exhausted->Seal(
        ihomeland::sim::BattlePacketKind::Control,
        Payload(1),
        NowUnixMs);
    Require(
        final_packet.disposition ==
                ihomeland::sim::BattleSealDisposition::Sealed &&
            final_packet.packet_sequence ==
                std::numeric_limits<std::uint64_t>::max() &&
            rejected.disposition ==
                ihomeland::sim::BattleSealDisposition::
                    SequenceExhausted &&
            rejected.datagram.empty(),
        "send sequence wrapped or reused exhausted key");
}

/// TestAuthenticatedRollover 验证control认证、epoch切换与previous overlap。
void TestAuthenticatedRollover() {
    ihomeland::sim::CryptoProvider crypto;
    auto client = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Client);
    auto server = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Server);
    const auto old_delayed = client->Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        Payload(8, 0x20),
        NowUnixMs);
    const auto trigger_at =
        NowUnixMs +
        ihomeland::sim::BattleSecureChannel::
            RekeyIntervalMilliseconds;
    const auto proposal_payload = Payload(32, 0x50);
    const auto proposal = client->Seal(
        ihomeland::sim::BattlePacketKind::Control,
        proposal_payload,
        trigger_at);
    Require(
        client->RolloverRequired(trigger_at) &&
            proposal.disposition ==
                ihomeland::sim::BattleSealDisposition::Sealed,
        "time trigger did not permit authenticated control");
    const auto opened_proposal = server->Open(
        proposal.datagram,
        trigger_at);
    Require(
        opened_proposal.disposition ==
                ihomeland::sim::BattleOpenDisposition::Accepted &&
            opened_proposal.packet_kind ==
                ihomeland::sim::BattlePacketKind::Control &&
            opened_proposal.plaintext == proposal_payload,
        "rollover proposal was not authenticated by current key");
    const auto confirm_payload = Payload(32, 0x60);
    const auto confirm = server->Seal(
        ihomeland::sim::BattlePacketKind::Control,
        confirm_payload,
        trigger_at);
    const auto opened_confirm = client->Open(
        confirm.datagram,
        trigger_at);
    Require(
        confirm.disposition ==
                ihomeland::sim::BattleSealDisposition::Sealed &&
            opened_confirm.disposition ==
                ihomeland::sim::BattleOpenDisposition::Accepted &&
            opened_confirm.packet_kind ==
                ihomeland::sim::BattlePacketKind::Control &&
            opened_confirm.plaintext == confirm_payload,
        "rollover confirm was not authenticated by peer current key");
    const auto old_expired = client->Seal(
        ihomeland::sim::BattlePacketKind::Control,
        Payload(8, 0x70),
        trigger_at);
    const auto rekey_nonce = Sequence<32>(0xa0);
    const auto commit_at = trigger_at + 1;
    Require(
        client->CommitAuthenticatedRollover(
            rekey_nonce,
            commit_at) &&
            server->CommitAuthenticatedRollover(
                rekey_nonce,
                commit_at),
        "authenticated rollover did not commit");

    Require(
        server->Open(
            proposal.datagram,
            commit_at).disposition ==
            ihomeland::sim::BattleOpenDisposition::Duplicate,
        "rollover reset previous epoch replay window");
    Require(
        server->Open(
            old_delayed.datagram,
            commit_at +
                ihomeland::sim::BattleSecureChannel::
                    PreviousEpochOverlapMilliseconds -
                1).disposition ==
            ihomeland::sim::BattleOpenDisposition::Accepted,
        "previous epoch packet inside overlap was rejected");
    Require(
        server->Open(
            old_expired.datagram,
            commit_at +
                ihomeland::sim::BattleSecureChannel::
                    PreviousEpochOverlapMilliseconds).disposition ==
            ihomeland::sim::BattleOpenDisposition::InvalidHeader,
        "previous epoch survived overlap deadline");

    const auto next = client->Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        Payload(8, 0x90),
        commit_at);
    Require(
        next.disposition ==
                ihomeland::sim::BattleSealDisposition::Sealed &&
            next.packet_sequence == 1 &&
            next.datagram[20] == 0x01 &&
            next.datagram[21] == 0x02 &&
            next.datagram[22] == 0x03 &&
            next.datagram[23] == 0x05 &&
            server->Open(
                next.datagram,
                commit_at).disposition ==
                ihomeland::sim::BattleOpenDisposition::Accepted &&
            !std::ranges::equal(
                std::span(old_delayed.datagram).subspan(20, 12),
                std::span(next.datagram).subspan(20, 12)),
        "new epoch reused old nonce or failed direction parity");
}

/// TestRolloverLimits 验证packet trigger、deadline与epoch wrap终态。
void TestRolloverLimits() {
    ihomeland::sim::CryptoProvider crypto;
    auto packet_due = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Client,
        10,
        ihomeland::sim::BattleSecureChannel::
            RekeyPacketLimit);
    Require(
        packet_due->Seal(
            ihomeland::sim::BattlePacketKind::Raw,
            Payload(1),
            NowUnixMs).disposition ==
            ihomeland::sim::BattleSealDisposition::
                RolloverRequired,
        "2^20 packet trigger allowed gameplay send");
    Require(
        packet_due->Seal(
            ihomeland::sim::BattlePacketKind::Control,
            Payload(1),
            NowUnixMs).disposition ==
            ihomeland::sim::BattleSealDisposition::Sealed,
        "packet trigger blocked rollover control");

    auto deadline = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Client,
        1,
        ihomeland::sim::BattleSecureChannel::
            RekeyPacketLimit);
    Require(
        deadline->RolloverRequired(NowUnixMs),
        "rollover deadline setup failed");
    Require(
        deadline->Seal(
            ihomeland::sim::BattlePacketKind::Control,
            Payload(1),
            NowUnixMs +
                ihomeland::sim::BattleSecureChannel::
                    RolloverDeadlineMilliseconds).disposition ==
            ihomeland::sim::BattleSealDisposition::Closed,
        "rollover deadline did not close session");

    auto wrapped = MakeChannel(
        crypto,
        ihomeland::sim::BattleTransportRole::Client,
        1,
        0,
        std::numeric_limits<std::uint32_t>::max());
    Require(
        !wrapped->CommitAuthenticatedRollover(
            Sequence<32>(0xc0),
            NowUnixMs) &&
            wrapped->Seal(
                ihomeland::sim::BattlePacketKind::Control,
                Payload(1),
                NowUnixMs).disposition ==
                ihomeland::sim::BattleSealDisposition::Closed,
        "epoch wrap did not close session");
}

}  // namespace

int main() {
    TestNonceAndCanonicalHeader();
    TestDirectionAndReplay();
    TestTamperDoesNotCommit();
    TestFutureJumpAndExhaustion();
    TestAuthenticatedRollover();
    TestRolloverLimits();
    return 0;
}
