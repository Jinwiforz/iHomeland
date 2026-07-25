#include "ihomeland/sim/transport/endpoint_rebind.hpp"

#include <array>
#include <cstdint>
#include <stdexcept>
#include <string>
#include <string_view>

namespace {

constexpr std::uint64_t NowUnixMs = 9'000'000;

/// Require 把rebind安全或连续性漂移转换为test failure。
void Require(
    const bool condition,
    const std::string_view message) {
    if (!condition) {
        throw std::runtime_error(std::string(message));
    }
}

/// Sequence 返回稳定公开fixture bytes。
template <std::size_t Size>
[[nodiscard]] std::array<std::uint8_t, Size>
Sequence(const std::uint8_t start) {
    std::array<std::uint8_t, Size> result{};
    for (std::size_t index = 0;
         index < Size;
         ++index) {
        result[index] = static_cast<std::uint8_t>(
            start + index);
    }
    return result;
}

/// Endpoint 返回canonical IPv4-mapped test endpoint。
[[nodiscard]] ihomeland::sim::BattleRemoteEndpoint
Endpoint(
    const std::uint8_t address,
    const std::uint16_t port) {
    auto result =
        ihomeland::sim::BattleRemoteEndpoint{};
    result.address[10] = 0xff;
    result.address[11] = 0xff;
    result.address[12] = 192;
    result.address[13] = 0;
    result.address[14] = 2;
    result.address[15] = address;
    result.port = port;
    return result;
}

/// Identity 返回指定endpoint generation的session AAD binding。
[[nodiscard]] ihomeland::sim::BattleSecureIdentity
Identity(const std::uint32_t generation) {
    return {
        .session_id_digest = Sequence<8>(1),
        .battle_session_generation = 7,
        .endpoint_generation = generation,
        .binding_discriminator = Sequence<8>(0x20),
    };
}

/// TestAuthenticatedRebind 验证cookie、single-active与secure sequence连续。
void TestAuthenticatedRebind() {
    auto crypto = ihomeland::sim::CryptoProvider{};
    const auto seed = Sequence<32>(0x30);
    auto server =
        ihomeland::sim::BattleSecureChannel(
            crypto,
            seed,
            ihomeland::sim::BattleTransportRole::Server,
            Identity(1),
            1,
            1,
            NowUnixMs,
            0);
    auto client =
        ihomeland::sim::BattleSecureChannel(
            crypto,
            seed,
            ihomeland::sim::BattleTransportRole::Client,
            Identity(1),
            1,
            1,
            NowUnixMs,
            0);
    const auto initial = Endpoint(1, 40'000);
    const auto candidate = Endpoint(2, 40'001);
    auto rebinder =
        ihomeland::sim::BattleEndpointRebinder(
            crypto,
            server,
            initial,
            1,
            Sequence<32>(0x70));

    const auto before = client.Seal(
        ihomeland::sim::BattlePacketKind::Control,
        std::array<std::uint8_t, 1>{1},
        NowUnixMs);
    Require(
        before.packet_sequence == 1 &&
            server.Open(
                before.datagram,
                NowUnixMs).disposition ==
                ihomeland::sim::
                    BattleOpenDisposition::Accepted,
        "baseline secure sequence failed before rebind");

    Require(
        rebinder.Begin(
            initial,
            candidate,
            Sequence<16>(1),
            false,
            NowUnixMs).disposition ==
            ihomeland::sim::
                BattleRebindDisposition::
                    AuthenticationRequired,
        "unauthenticated rebind request accepted");
    auto challenge = rebinder.Begin(
        initial,
        candidate,
        Sequence<16>(1),
        true,
        NowUnixMs);
    Require(
        challenge.disposition ==
                ihomeland::sim::
                    BattleRebindDisposition::
                        ChallengeIssued &&
            challenge.challenge.has_value(),
        "authenticated rebind did not issue challenge");
    Require(
        rebinder.Begin(
            initial,
            Endpoint(3, 40'002),
            Sequence<16>(2),
            true,
            NowUnixMs).disposition ==
            ihomeland::sim::
                BattleRebindDisposition::Concurrent,
        "concurrent rebind created a second candidate");
    Require(
        rebinder.Confirm(
            Endpoint(99, 40'001),
            *challenge.challenge,
            true,
            NowUnixMs + 1) ==
            ihomeland::sim::
                BattleRebindDisposition::
                    EndpointMismatch,
        "different remote confirmed rebind cookie");
    Require(
        rebinder.Confirm(
            candidate,
            *challenge.challenge,
            true,
            NowUnixMs + 1) ==
            ihomeland::sim::
                BattleRebindDisposition::Committed,
        "valid candidate failed to commit rebind");
    Require(
        rebinder.EndpointGeneration() == 2 &&
            rebinder.ActiveEndpoint().address ==
                candidate.address &&
            rebinder.Confirm(
                candidate,
                *challenge.challenge,
                true,
                NowUnixMs + 2) ==
                ihomeland::sim::
                    BattleRebindDisposition::
                        InvalidChallenge,
        "rebind replay changed single-active endpoint");

    Require(
        client.CommitEndpointGeneration(1, 2),
        "peer failed to commit authenticated endpoint generation");
    const auto after = client.Seal(
        ihomeland::sim::BattlePacketKind::Control,
        std::array<std::uint8_t, 1>{2},
        NowUnixMs + 2);
    Require(
        after.packet_sequence == 2 &&
            server.Open(
                after.datagram,
                NowUnixMs + 2).disposition ==
                ihomeland::sim::
                    BattleOpenDisposition::Accepted,
        "rebind reset key, replay window or packet sequence");
}

/// TestExpiryAndTamper 验证expired/cookie mutation保持原endpoint。
void TestExpiryAndTamper() {
    auto crypto = ihomeland::sim::CryptoProvider{};
    const auto seed = Sequence<32>(0x30);
    auto server =
        ihomeland::sim::BattleSecureChannel(
            crypto,
            seed,
            ihomeland::sim::BattleTransportRole::Server,
            Identity(1),
            1,
            1,
            NowUnixMs,
            0);
    const auto initial = Endpoint(1, 40'000);
    const auto candidate = Endpoint(2, 40'001);
    auto rebinder =
        ihomeland::sim::BattleEndpointRebinder(
            crypto,
            server,
            initial,
            1,
            Sequence<32>(0x70));
    auto issued = rebinder.Begin(
        initial,
        candidate,
        Sequence<16>(1),
        true,
        NowUnixMs);
    auto tampered = *issued.challenge;
    tampered.cookie[0] ^= 1;
    Require(
        rebinder.Confirm(
            candidate,
            tampered,
            true,
            NowUnixMs + 1) ==
            ihomeland::sim::
                BattleRebindDisposition::
                    InvalidChallenge &&
            rebinder.EndpointGeneration() == 1,
        "tampered rebind changed generation");
    Require(
        rebinder.Confirm(
            candidate,
            *issued.challenge,
            true,
            NowUnixMs + 3'000) ==
            ihomeland::sim::
                BattleRebindDisposition::Expired &&
            rebinder.ActiveEndpoint().address ==
                initial.address,
        "expired rebind changed active endpoint");
}

}  // namespace

int main() {
    TestAuthenticatedRebind();
    TestExpiryAndTamper();
    return 0;
}
