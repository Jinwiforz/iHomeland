#include "ihomeland/sim/transport/authenticated_multiplexer.hpp"
#include "ihomeland/sim/transport/battle_route.hpp"
#include "ihomeland/sim/transport/crypto_provider.hpp"

#include "ihomeland/battle/v1/battle.pb.h"

#include <algorithm>
#include <array>
#include <cstdint>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace {

constexpr std::uint64_t NowUnixMs = 5'000'000;
constexpr std::uint64_t NowUnixUs =
    NowUnixMs * 1'000;

/// Require 把 raw/multiplexer contract drift 转换为 test failure。
void Require(
    const bool condition,
    const std::string_view message) {
    if (!condition) {
        throw std::runtime_error(std::string(message));
    }
}

/// Sequence 返回稳定公开测试 bytes。
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

/// Bytes 序列化 lite Protobuf 为 byte vector。
template <typename Message>
[[nodiscard]] std::vector<std::uint8_t> Bytes(
    const Message& message) {
    const auto encoded = message.SerializeAsString();
    return {
        reinterpret_cast<const std::uint8_t*>(
            encoded.data()),
        reinterpret_cast<const std::uint8_t*>(
            encoded.data()) +
            encoded.size(),
    };
}

/// Probe 返回 registry 合法的 c2s raw payload。
[[nodiscard]] std::vector<std::uint8_t> Probe(
    const std::uint64_t sequence) {
    ihomeland::battle::v1::BattleProbe message;
    message.set_probe_sequence(sequence);
    message.set_latest_snapshot_sequence(2);
    message.set_client_monotonic_time_us(3);
    return Bytes(message);
}

/// Input 返回指定 newest input tick 的 c2s raw payload。
[[nodiscard]] std::vector<std::uint8_t> Input(
    const std::uint64_t tick) {
    ihomeland::battle::v1::BattleInputBundle message;
    message.set_newest_input_tick(tick);
    message.set_latest_observed_server_tick(90);
    return Bytes(message);
}

/// FullSnapshot 返回与 outer partition 一致的 s2c raw payload。
[[nodiscard]] std::vector<std::uint8_t> FullSnapshot(
    const std::uint64_t sequence,
    const std::uint64_t tick,
    const std::uint32_t partition_index = 0,
    const std::uint32_t partition_count = 1) {
    ihomeland::battle::v1::BattleFullSnapshot message;
    message.set_server_tick(tick);
    message.set_snapshot_sequence(sequence);
    message.set_baseline_id(7);
    message.set_partition_index(partition_index);
    message.set_partition_count(partition_count);
    return Bytes(message);
}

/// Context 返回新鲜且允许 90..110 tick 的 trusted fence。
[[nodiscard]] ihomeland::sim::BattleRawDispatchContext
Context() {
    return {
        .now_unix_microseconds = NowUnixUs,
        .enqueued_unix_microseconds = NowUnixUs,
        .oldest_accepted_tick = 90,
        .newest_accepted_tick = 110,
    };
}

/// Endpoint 返回 canonical IPv4-mapped loopback endpoint。
[[nodiscard]] ihomeland::sim::BattleRemoteEndpoint
Endpoint(const std::uint16_t port) {
    ihomeland::sim::BattleRemoteEndpoint endpoint{};
    endpoint.address[10] = 0xff;
    endpoint.address[11] = 0xff;
    endpoint.address[12] = 127;
    endpoint.address[15] = 1;
    endpoint.port = port;
    return endpoint;
}

/// Identity 返回 multiplexer/session 使用的 immutable AAD binding。
[[nodiscard]] ihomeland::sim::BattleSecureIdentity
Identity() {
    return {
        .session_id_digest = Sequence<8>(1),
        .battle_session_generation = 9,
        .endpoint_generation = 2,
        .binding_discriminator = Sequence<8>(0x10),
    };
}

/// TestRegistryAndGolden 验证 route catalog 与 canonical raw probe bytes。
void TestRegistryAndGolden() {
    const auto* input =
        ihomeland::sim::FindBattleRawRoutePolicy(3000);
    const auto* probe =
        ihomeland::sim::FindBattleRawRoutePolicy(3001);
    const auto* full =
        ihomeland::sim::FindBattleRawRoutePolicy(3002);
    const auto* delta =
        ihomeland::sim::FindBattleRawRoutePolicy(3003);
    Require(
        input != nullptr &&
            input->maximum_payload_bytes == 384 &&
            input->maximum_rate_per_second == 40 &&
            input->expiry_microseconds == 300'000 &&
            probe != nullptr &&
            probe->maximum_payload_bytes == 96 &&
            probe->maximum_rate_per_second == 4 &&
            full != nullptr &&
            full->direction ==
                ihomeland::sim::BattleRouteDirection::
                    ServerToClient &&
            delta != nullptr &&
            delta->maximum_payload_bytes == 900 &&
            ihomeland::sim::FindBattleRawRoutePolicy(3004) ==
                nullptr,
        "C++ raw route projection drifted from registry");

    const auto payload = Probe(1);
    const auto frame =
        ihomeland::sim::BattleRawDispatcher::Encode(
            ihomeland::sim::BattleRouteDirection::
                ClientToServer,
            3001,
            0,
            1,
            0x1112131415161718,
            payload);
    const std::array<std::uint8_t, 22> golden{
        0x00, 0x00, 0x0b, 0xb9,
        0x00, 0x06, 0x00, 0x01,
        0x11, 0x12, 0x13, 0x14,
        0x15, 0x16, 0x17, 0x18,
        0x08, 0x01, 0x10, 0x02,
        0x18, 0x03,
    };
    Require(
        frame.size() == golden.size() &&
            std::equal(
                frame.begin(),
                frame.end(),
                golden.begin()),
        "raw probe encoding drifted from canonical golden");
}

/// TestRawPolicy 验证 direction、split、tick、expiry、sequence 与 rate gate。
void TestRawPolicy() {
    std::size_t accepted = 0;
    ihomeland::sim::BattleRawDispatcher dispatcher(
        ihomeland::sim::BattleRouteDirection::
            ClientToServer,
        [&](const ihomeland::sim::BattleRawFrameView& frame) {
            Require(
                frame.policy != nullptr &&
                    !frame.payload.empty(),
                "dispatcher delivered incomplete frame");
            ++accepted;
        });

    const auto input = Input(100);
    const auto first =
        ihomeland::sim::BattleRawDispatcher::Encode(
            ihomeland::sim::BattleRouteDirection::
                ClientToServer,
            3000,
            0,
            2,
            10,
            input);
    const auto second =
        ihomeland::sim::BattleRawDispatcher::Encode(
            ihomeland::sim::BattleRouteDirection::
                ClientToServer,
            3000,
            1,
            2,
            10,
            input);
    Require(
        dispatcher.Dispatch(first, Context()) ==
                ihomeland::sim::BattleRawDisposition::
                    Accepted &&
            dispatcher.Dispatch(second, Context()) ==
                ihomeland::sim::BattleRawDisposition::
                    Accepted &&
            dispatcher.Dispatch(first, Context()) ==
                ihomeland::sim::BattleRawDisposition::
                    InvalidPartition,
        "bounded input partition policy failed");

    const auto stale =
        ihomeland::sim::BattleRawDispatcher::Encode(
            ihomeland::sim::BattleRouteDirection::
                ClientToServer,
            3000,
            0,
            1,
            9,
            input);
    Require(
        dispatcher.Dispatch(stale, Context()) ==
            ihomeland::sim::BattleRawDisposition::
                StaleSequence,
        "stale application sequence was accepted");

    auto stale_tick_context = Context();
    stale_tick_context.oldest_accepted_tick = 101;
    Require(
        dispatcher.Dispatch(
            ihomeland::sim::BattleRawDispatcher::Encode(
                ihomeland::sim::BattleRouteDirection::
                    ClientToServer,
                3000,
                0,
                1,
                11,
                input),
            stale_tick_context) ==
            ihomeland::sim::BattleRawDisposition::
                TickRejected,
        "out-of-fence input tick was accepted");

    auto expired_context = Context();
    expired_context.enqueued_unix_microseconds =
        NowUnixUs - 300'001;
    Require(
        dispatcher.Dispatch(
            ihomeland::sim::BattleRawDispatcher::Encode(
                ihomeland::sim::BattleRouteDirection::
                    ClientToServer,
                3000,
                0,
                1,
                11,
                input),
            expired_context) ==
            ihomeland::sim::BattleRawDisposition::Expired,
        "expired raw message was accepted");

    const auto snapshot =
        ihomeland::sim::BattleRawDispatcher::Encode(
            ihomeland::sim::BattleRouteDirection::
                ServerToClient,
            3002,
            0,
            1,
            1,
            FullSnapshot(1, 100));
    Require(
        dispatcher.Dispatch(snapshot, Context()) ==
            ihomeland::sim::BattleRawDisposition::
                DirectionMismatch,
        "wrong-direction raw route was accepted");

    ihomeland::sim::BattleRawDispatcher probe_dispatcher(
        ihomeland::sim::BattleRouteDirection::
            ClientToServer,
        [](const auto&) {});
    for (std::uint64_t sequence = 1;
         sequence <= 4;
         ++sequence) {
        const auto frame =
            ihomeland::sim::BattleRawDispatcher::Encode(
                ihomeland::sim::BattleRouteDirection::
                    ClientToServer,
                3001,
                0,
                1,
                sequence,
                Probe(sequence));
        Require(
            probe_dispatcher.Dispatch(frame, Context()) ==
                ihomeland::sim::BattleRawDisposition::
                    Accepted,
            "probe below registry rate was rejected");
    }
    const auto limited =
        ihomeland::sim::BattleRawDispatcher::Encode(
            ihomeland::sim::BattleRouteDirection::
                ClientToServer,
            3001,
            0,
            1,
            5,
            Probe(5));
    Require(
        probe_dispatcher.Dispatch(limited, Context()) ==
            ihomeland::sim::BattleRawDisposition::
                RateLimited,
        "probe registry rate was not enforced");
    Require(
        accepted == 2,
        "rejected raw frame reached application handler");
}

/// TestAuthenticatedMultiplexer 验证 endpoint/authority/AEAD/replay 后才进入 raw。
void TestAuthenticatedMultiplexer() {
    ihomeland::sim::CryptoProvider crypto;
    const auto seed = Sequence<32>(0x80);
    ihomeland::sim::BattleSecureChannel client(
        crypto,
        seed,
        ihomeland::sim::BattleTransportRole::Client,
        Identity(),
        7,
        1,
        NowUnixMs,
        0);
    ihomeland::sim::BattleSecureChannel server(
        crypto,
        seed,
        ihomeland::sim::BattleTransportRole::Server,
        Identity(),
        7,
        1,
        NowUnixMs,
        0);

    std::size_t accepted = 0;
    ihomeland::sim::BattleRawDispatcher dispatcher(
        ihomeland::sim::BattleRouteDirection::
            ClientToServer,
        [&](const auto&) { ++accepted; });
    bool authority_current = false;
    ihomeland::sim::BattleAuthenticatedMultiplexer multiplexer(
        server,
        Endpoint(40000),
        dispatcher,
        [&] { return authority_current; });
    const auto raw =
        ihomeland::sim::BattleRawDispatcher::Encode(
            ihomeland::sim::BattleRouteDirection::
                ClientToServer,
            3001,
            0,
            1,
            10,
            Probe(10));
    const auto sealed = client.Seal(
        ihomeland::sim::BattlePacketKind::Raw,
        raw,
        NowUnixMs);
    Require(
        sealed.disposition ==
            ihomeland::sim::BattleSealDisposition::Sealed,
        "client failed to seal raw fixture");

    Require(
        multiplexer.Handle(
            sealed.datagram,
            Endpoint(40001),
            Context(),
            NowUnixMs).disposition ==
            ihomeland::sim::BattleMultiplexerDisposition::
                EndpointMismatch,
        "wrong endpoint reached secure channel");
    Require(
        multiplexer.Handle(
            sealed.datagram,
            Endpoint(40000),
            Context(),
            NowUnixMs).disposition ==
            ihomeland::sim::BattleMultiplexerDisposition::
                AuthorityRejected,
        "stale target authority reached secure channel");
    authority_current = true;
    Require(
        multiplexer.Handle(
            sealed.datagram,
            Endpoint(40000),
            Context(),
            NowUnixMs).disposition ==
                ihomeland::sim::BattleMultiplexerDisposition::
                    RawAccepted &&
            accepted == 1,
        "authenticated raw packet was not dispatched");
    Require(
        multiplexer.Handle(
            sealed.datagram,
            Endpoint(40000),
            Context(),
            NowUnixMs).disposition ==
                ihomeland::sim::BattleMultiplexerDisposition::
                    SecureRejected &&
            accepted == 1,
        "secure replay reached raw dispatcher");

    const std::array<std::uint8_t, 1> kcp_payload{1};
    const auto kcp = client.Seal(
        ihomeland::sim::BattlePacketKind::Kcp,
        kcp_payload,
        NowUnixMs);
    Require(
        multiplexer.Handle(
            kcp.datagram,
            Endpoint(40000),
            Context(),
            NowUnixMs).disposition ==
            ihomeland::sim::BattleMultiplexerDisposition::
                LaneUnavailable,
        "unimplemented KCP lane fell back to raw");
}

}  // namespace

int main() {
    TestRegistryAndGolden();
    TestRawPolicy();
    TestAuthenticatedMultiplexer();
    return 0;
}
