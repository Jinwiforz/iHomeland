#include "ihomeland/sim/transport/kcp_adapter.hpp"

#include "ihomeland/battle/v1/battle.pb.h"

#include <algorithm>
#include <array>
#include <cstdint>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace {

constexpr std::uint32_t Conversation = 0x01020304;
constexpr std::uint64_t NowUnixMs =
    1'800'000'000'000;

/// Require 把KCP profile或lifecycle漂移转换为test failure。
void Require(
    const bool condition,
    const std::string_view message) {
    if (!condition) {
        throw std::runtime_error(std::string(message));
    }
}

/// Bytes 序列化lite Protobuf为byte vector。
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

/// Ability 返回canonical s2c reliable event。
[[nodiscard]] std::vector<std::uint8_t> Ability() {
    ihomeland::battle::v1::
        BattleAbilityReliableEvent message;
    message.set_event_id(1);
    message.set_server_tick(2);
    message.set_source_entity_id(3);
    message.set_source_entity_generation(1);
    message.set_ability_id(7);
    message.set_phase(
        ihomeland::battle::v1::
            BATTLE_ABILITY_PHASE_STARTED);
    return Bytes(message);
}

/// ResyncRequest 返回canonical c2s reliable request。
[[nodiscard]] std::vector<std::uint8_t>
ResyncRequest() {
    ihomeland::battle::v1::BattleResyncRequest message;
    message.set_request_sequence(1);
    message.set_latest_server_tick(2);
    message.set_reason(
        ihomeland::battle::v1::
            BATTLE_RESYNC_REASON_MISSING_BASELINE);
    return Bytes(message);
}

/// TestProfileAndGolden 验证exact KCP参数、route catalog与wire bytes。
void TestProfileAndGolden() {
    using ihomeland::sim::BattleKcpAdapter;
    const auto* ability =
        ihomeland::sim::FindBattleKcpRoutePolicy(3004);
    const auto* lifecycle =
        ihomeland::sim::FindBattleKcpRoutePolicy(3005);
    const auto* request =
        ihomeland::sim::FindBattleKcpRoutePolicy(3006);
    const auto* response =
        ihomeland::sim::FindBattleKcpRoutePolicy(3007);
    Require(
        BattleKcpAdapter::UpdateIntervalMilliseconds ==
                10 &&
            BattleKcpAdapter::WindowSegments == 64 &&
            BattleKcpAdapter::FastResend == 2 &&
            BattleKcpAdapter::MinimumRtoMilliseconds ==
                30 &&
            BattleKcpAdapter::MaximumRtoMilliseconds ==
                200 &&
            BattleKcpAdapter::DeadLinkRetransmits == 10 &&
            BattleKcpAdapter::
                    MaximumSegmentPayloadBytes ==
                1000 &&
            BattleKcpAdapter::QueueItems == 64 &&
            BattleKcpAdapter::MessageExpiryMilliseconds ==
                500,
        "KCP exact profile constants drifted");
    Require(
        ability != nullptr &&
            ability->maximum_payload_bytes == 512 &&
            lifecycle != nullptr &&
            lifecycle->maximum_rate_per_second == 20 &&
            request != nullptr &&
            request->direction ==
                ihomeland::sim::BattleRouteDirection::
                    ClientToServer &&
            response != nullptr &&
            response->maximum_payload_bytes == 768 &&
            ihomeland::sim::FindBattleKcpRoutePolicy(
                3008) == nullptr,
        "KCP route projection drifted from registry");

    const std::array<std::uint8_t, 52> golden{
        0x04, 0x03, 0x02, 0x01,
        0x51, 0x00, 0x40, 0x00,
        0x0d, 0x0c, 0x0b, 0x0a,
        0x01, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00,
        0x1c, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x0b, 0xbc,
        0x00, 0x0c, 0x00, 0x00,
        0x21, 0x22, 0x23, 0x24,
        0x25, 0x26, 0x27, 0x28,
        0x08, 0x01, 0x10, 0x02,
        0x18, 0x03, 0x20, 0x01,
        0x28, 0x07, 0x30, 0x01,
    };
    Require(
        BattleKcpAdapter::ValidateSegment(
            golden,
            Conversation),
        "canonical KCP/route/protobuf golden rejected");
    auto malformed = golden;
    malformed[0] ^= 0x01;
    Require(
        !BattleKcpAdapter::ValidateSegment(
            malformed,
            Conversation),
        "wrong KCP conversation accepted");
    malformed = golden;
    malformed[4] = 0xff;
    Require(
        !BattleKcpAdapter::ValidateSegment(
            malformed,
            Conversation),
        "unknown KCP command accepted");
    malformed = golden;
    malformed[6] = 65;
    Require(
        !BattleKcpAdapter::ValidateSegment(
            malformed,
            Conversation),
        "oversized KCP window accepted");
    malformed = golden;
    malformed[20] = 0xe9;
    malformed[21] = 0x03;
    Require(
        !BattleKcpAdapter::ValidateSegment(
            malformed,
            Conversation),
        "truncated KCP segment accepted");
}

/// TestRoundTripAndParity 验证两端KCP交互、ACK回收与application envelope。
void TestRoundTripAndParity() {
    std::vector<std::vector<std::uint8_t>>
        server_segments;
    std::vector<std::vector<std::uint8_t>>
        client_segments;
    std::uint32_t received_message_id = 0;
    std::uint64_t received_sequence = 0;
    auto server = ihomeland::sim::BattleKcpAdapter(
        Conversation,
        ihomeland::sim::BattleTransportRole::Server,
        [&](const std::span<const std::uint8_t> segment) {
            server_segments.emplace_back(
                segment.begin(),
                segment.end());
        },
        [](const ihomeland::sim::BattleKcpMessageView&) {
        });
    auto client = ihomeland::sim::BattleKcpAdapter(
        Conversation,
        ihomeland::sim::BattleTransportRole::Client,
        [&](const std::span<const std::uint8_t> segment) {
            client_segments.emplace_back(
                segment.begin(),
                segment.end());
        },
        [&](const ihomeland::sim::BattleKcpMessageView&
                message) {
            received_message_id =
                message.policy->message_id;
            received_sequence =
                message.application_sequence;
        });
    const auto payload = Ability();
    Require(
        server.Queue(
            3004,
            0x2122232425262728,
            payload,
            NowUnixMs) ==
            ihomeland::sim::BattleKcpDisposition::Queued,
        "valid KCP message was not queued");
    const auto update_disposition =
        server.Update(NowUnixMs);
    Require(
        update_disposition ==
                ihomeland::sim::BattleKcpDisposition::
                    Accepted &&
            server_segments.size() == 1,
        "KCP sender did not emit one valid segment");
    Require(
        ihomeland::sim::BattleKcpAdapter::
            ValidateSegment(
                server_segments.front(),
                Conversation),
        "KCP sender emitted invalid segment");
    const auto& segment = server_segments.front();
    Require(
        segment.size() == 52 &&
            std::equal(
                segment.begin() + 24,
                segment.end(),
                std::array<std::uint8_t, 28>{
                    0x00, 0x00, 0x0b, 0xbc,
                    0x00, 0x0c, 0x00, 0x00,
                    0x21, 0x22, 0x23, 0x24,
                    0x25, 0x26, 0x27, 0x28,
                    0x08, 0x01, 0x10, 0x02,
                    0x18, 0x03, 0x20, 0x01,
                    0x28, 0x07, 0x30, 0x01,
                }.begin()),
        "KCP adapter output drifted from route/protobuf golden");
    Require(
        client.Input(segment, NowUnixMs) ==
                ihomeland::sim::BattleKcpDisposition::
                    Accepted &&
            received_message_id == 3004 &&
            received_sequence == 0x2122232425262728,
        "KCP receiver did not reassemble reliable event");
    Require(
        client.Update(NowUnixMs) ==
                ihomeland::sim::BattleKcpDisposition::
                    Accepted &&
            !client_segments.empty(),
        "KCP receiver did not emit ACK");
    for (const auto& ack : client_segments) {
        Require(
            server.Input(ack, NowUnixMs) ==
                ihomeland::sim::BattleKcpDisposition::
                    Accepted,
            "KCP sender rejected peer ACK");
    }
    Require(
        server.Status().inflight_messages == 0 &&
            server.Status().waiting_segments == 0 &&
            client.Status().received_messages == 1,
        "KCP ACK did not release inflight budget");
}

/// TestQueueAndExpiry 验证64项hard cap、方向gate与500ms终结。
void TestQueueAndExpiry() {
    const auto payload = Ability();
    auto adapter = ihomeland::sim::BattleKcpAdapter(
        Conversation,
        ihomeland::sim::BattleTransportRole::Server,
        [](const std::span<const std::uint8_t>) {},
        [](const ihomeland::sim::BattleKcpMessageView&) {
        });
    Require(
        adapter.Queue(
            3006,
            1,
            ResyncRequest(),
            NowUnixMs) ==
            ihomeland::sim::BattleKcpDisposition::
                RouteRejected,
        "wrong-direction KCP route was accepted");
    for (std::uint64_t sequence = 1;
         sequence <= 64;
         ++sequence) {
        Require(
            adapter.Queue(
                3004,
                sequence,
                payload,
                NowUnixMs) ==
                ihomeland::sim::BattleKcpDisposition::
                    Queued,
            "KCP queue rejected an item below hard cap");
    }
    Require(
        adapter.Queue(
            3004,
            65,
            payload,
            NowUnixMs) ==
            ihomeland::sim::BattleKcpDisposition::
                QueueFull,
        "KCP queue exceeded 64-item hard cap");

    auto queued_expiry =
        ihomeland::sim::BattleKcpAdapter(
            Conversation,
            ihomeland::sim::BattleTransportRole::Server,
            [](const std::span<const std::uint8_t>) {},
            [](const ihomeland::sim::
                   BattleKcpMessageView&) {});
    Require(
        queued_expiry.Queue(
            3004,
            1,
            payload,
            NowUnixMs) ==
                ihomeland::sim::BattleKcpDisposition::
                    Queued &&
            queued_expiry.Update(
                NowUnixMs + 500) ==
                ihomeland::sim::BattleKcpDisposition::
                    Expired &&
            queued_expiry.Status().queued_messages == 0 &&
            queued_expiry.Status().emitted_segments == 0,
        "queued KCP message was not terminal at 500ms");

    auto inflight_expiry =
        ihomeland::sim::BattleKcpAdapter(
            Conversation,
            ihomeland::sim::BattleTransportRole::Server,
            [](const std::span<const std::uint8_t>) {},
            [](const ihomeland::sim::
                   BattleKcpMessageView&) {});
    Require(
        inflight_expiry.Queue(
            3004,
            1,
            payload,
            NowUnixMs) ==
                ihomeland::sim::BattleKcpDisposition::
                    Queued &&
            inflight_expiry.Update(NowUnixMs) ==
                ihomeland::sim::BattleKcpDisposition::
                    Accepted &&
            inflight_expiry.Update(
                NowUnixMs + 500) ==
                ihomeland::sim::BattleKcpDisposition::
                    Expired &&
            inflight_expiry.Status().closed,
        "unacknowledged KCP message did not close at deadline");
}

}  // namespace

int main() {
    TestProfileAndGolden();
    TestRoundTripAndParity();
    TestQueueAndExpiry();
    return 0;
}
