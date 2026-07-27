#include "ihomeland/sim/transport/battle_session.hpp"

#include "ihomeland/sim/simulation/input_timeline.hpp"
#include "ihomeland/sim/simulation/simulation_instance.hpp"
#include "ihomeland/sim/transport/battle_route.hpp"
#include "ihomeland/sim/transport/secure_datagram.hpp"

#include "ihomeland/battle/v1/battle.pb.h"

#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <iostream>
#include <limits>
#include <memory>
#include <mutex>
#include <ranges>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace {

using namespace std::chrono_literals;
constexpr std::uint64_t NowUnixMs = 10'000'000;
constexpr auto InstanceTransitionDeadline = 2s;

/// Require 把session/ingress/replication漂移转换为test failure。
void Require(
    const bool condition,
    const std::string_view message) {
    if (!condition) {
        throw std::runtime_error(std::string(message));
    }
}

/// TestIdentity 返回稳定非零128-bit identity。
[[nodiscard]] ihomeland::sim::Identity128
TestIdentity(const char suffix) {
    return ihomeland::sim::Identity128::ParseLowerHex(
        std::string(
            "00112233445566778899aabbccddee0") +
        suffix);
}

/// InstanceIdentity 构造完整assignment/mapping/build binding。
[[nodiscard]] ihomeland::sim::
    SimulationInstanceIdentity
InstanceIdentity() {
    return ihomeland::sim::SimulationInstanceIdentity(
        TestIdentity('1'),
        ihomeland::sim::AssignmentStamp(
            TestIdentity('2'),
            7,
            9,
            TestIdentity('3')),
        11,
        ihomeland::sim::BuildConfigIdentity(
            std::string(64, 'a'),
            std::string(64, 'b'),
            std::string(64, 'c'),
            std::string(64, 'd'),
            std::string(64, 'e'),
            std::string(64, 'f')));
}

/// Context 构造绑定actor 42与exact assignment的immutable session。
[[nodiscard]] ihomeland::sim::BattleSessionContext
Context(
    const ihomeland::sim::SimulationInstance&
        instance,
    const std::shared_ptr<
        ihomeland::sim::BattleSessionAuthority>&
        authority,
    const std::uint8_t actor_slot = 0) {
    return {
        "account-session-1",
        3,
        101,
        5,
        1,
        instance.Identity().Assignment().Fingerprint(),
        instance.Identity().InstanceId().ToLowerHex(),
        instance.Identity().MappingGeneration(),
        7,
        {
            .player_id = "player-1",
            .role =
                ihomeland::sim::BattleActorRole::Owner,
            .actor_id = 42,
            .actor_slot = actor_slot,
        },
        authority,
    };
}

/// Bytes 序列化lite message。
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

/// TestContextAndIngress 验证actor只来自context且全部intent进入唯一inbox。
void TestContextAndIngress() {
    auto clock =
        std::make_shared<
            ihomeland::sim::ManualTickClock>();
    std::vector<ihomeland::sim::IngressCommand>
        observed;
    std::mutex observed_mutex;
    std::condition_variable observed_condition;
    auto instance =
        ihomeland::sim::SimulationInstance(
            InstanceIdentity(),
            {
                .tick_step = 50ms,
                .inbox_capacity = 256,
                .hard_tick_debt = 4,
            },
            clock,
             [&](const ihomeland::sim::TickObservation&
                     tick) {
                 std::lock_guard lock(observed_mutex);
                 observed.insert(
                     observed.end(),
                     tick.commands.begin(),
                     tick.commands.end());
                 observed_condition.notify_all();
             });
    const std::vector<ihomeland::sim::StartupStep>
        steps{{
            .stage =
                ihomeland::sim::StartupStage::Fixture,
            .initialize = [] {},
            .rollback = [] {},
        }};
    instance.Start(steps);
    auto authority =
        std::make_shared<
            ihomeland::sim::BattleSessionAuthority>();
    auto context =
        Context(instance, authority);
    auto command_ingress =
        ihomeland::sim::CommandIngress(
            instance,
            {
                .generation = 11,
                .base_input_tick = 1,
                .base_simulation_tick = 1,
                .input_step_ns = 25'000'000,
                .simulation_step_ns = 50'000'000,
                .early_window_ticks = 2,
                .late_window_ticks = 6,
            },
            {42},
            256);
    auto resources =
        ihomeland::sim::BattleResourceGovernor{};
    auto session_resources =
        ihomeland::sim::
            BattleSessionResourceGovernor(
                resources,
                context.BattleSessionHandle(),
                201);
    auto ingress = ihomeland::sim::BattleInputIngress(
        context,
        command_ingress,
        session_resources);

    ihomeland::battle::v1::BattleInputBundle bundle;
    bundle.set_newest_input_tick(1);
    bundle.set_latest_observed_server_tick(1);
    auto* move = bundle.add_commands();
    move->set_command_sequence(1);
    move->set_kind(
        ihomeland::battle::v1::
            BATTLE_INPUT_KIND_MOVE);
    move->set_move_x_milli(100);
    move->set_move_y_milli(-100);
    auto* aim = bundle.add_commands();
    aim->set_command_sequence(2);
    aim->set_kind(
        ihomeland::battle::v1::
            BATTLE_INPUT_KIND_AIM);
    aim->set_aim_yaw_millidegrees(1'000);
    aim->set_aim_pitch_millidegrees(-500);
    auto* interact = bundle.add_commands();
    interact->set_command_sequence(3);
    interact->set_kind(
        ihomeland::battle::v1::
            BATTLE_INPUT_KIND_INTERACT);
    interact->set_interaction_slot(2);
    const auto payload = Bytes(bundle);
    const auto* policy =
        ihomeland::sim::FindBattleRawRoutePolicy(3000);
    const auto frame =
        ihomeland::sim::BattleRawFrameView{
            .policy = policy,
            .partition_index = 0,
            .partition_count = 1,
            .application_sequence = 1,
            .application_tick = 1,
            .payload = payload,
        };
    const auto accepted =
        ingress.Handle(frame, NowUnixMs);
    Require(
        accepted.disposition ==
                ihomeland::sim::
                    BattleIngressDisposition::Accepted &&
            accepted.accepted_commands == 3,
        "valid intent bundle did not enter inbox");

    auto unsafe = bundle;
    unsafe.mutable_commands(0)->
        set_aim_yaw_millidegrees(1);
    const auto unsafe_payload = Bytes(unsafe);
    auto unsafe_frame = frame;
    unsafe_frame.payload = unsafe_payload;
    Require(
        ingress.Handle(
            unsafe_frame,
            NowUnixMs + 100).disposition ==
            ihomeland::sim::
                BattleIngressDisposition::
                    InvalidPayload,
        "mixed unsafe input fields reached simulation");

    authority->Invalidate(
        ihomeland::sim::
            BattleSessionInvalidationReason::
                Assignment);
    authority->Invalidate(
        ihomeland::sim::
            BattleSessionInvalidationReason::Target);
    Require(
        ingress.Handle(
            frame,
            NowUnixMs + 200).disposition ==
                ihomeland::sim::
                    BattleIngressDisposition::
                        AuthorityRejected &&
            authority->Reason() ==
                ihomeland::sim::
                    BattleSessionInvalidationReason::
                        Assignment,
        "session invalidation was overwritten or revived");

    clock->Advance();
    bool observed_valid = false;
    {
        std::unique_lock lock(observed_mutex);
        observed_valid =
            observed_condition.wait_for(
                lock,
                InstanceTransitionDeadline,
                [&] {
                    return observed.size() == 3;
                }) &&
            std::ranges::all_of(
                observed,
                [](const auto& command) {
                    return command.actor_id == 42;
                });
    }
    instance.BeginDrain();
    clock->Advance();
    const auto stopped =
        instance.Stop(InstanceTransitionDeadline);
    Require(
        observed_valid,
        "UDP payload changed context actor binding");
    Require(
        stopped,
        "session ingress instance failed to drain");

    bool ninth_rejected = false;
    try {
        static_cast<void>(
            Context(instance, authority, 8));
    } catch (const std::invalid_argument&) {
        ninth_rejected = true;
    }
    Require(
        ninth_rejected,
        "ninth actor slot was accepted");
}

/// TestReplication 验证raw latest-wins、KCP唯一lane、expiry与invalidation。
void TestReplication() {
    auto clock =
        std::make_shared<
            ihomeland::sim::ManualTickClock>();
    auto instance =
        ihomeland::sim::SimulationInstance(
            InstanceIdentity(),
            {
                .tick_step = 50ms,
                .inbox_capacity = 1,
                .hard_tick_debt = 1,
            },
            clock,
            [](const ihomeland::sim::
                   TickObservation&) {});
    auto authority =
        std::make_shared<
            ihomeland::sim::BattleSessionAuthority>();
    auto context = Context(instance, authority);
    auto resources =
        ihomeland::sim::BattleResourceGovernor{};
    auto session_resources =
        ihomeland::sim::
            BattleSessionResourceGovernor(
                resources,
                context.BattleSessionHandle(),
                201);
    auto replication =
        ihomeland::sim::BattleReplicationQueue(
            context,
            session_resources);
    const std::vector<
        ihomeland::sim::StateProjectionToken>
        states{{
            .actor_id = 42,
            .x_mm = 1,
            .y_mm = 2,
            .z_mm = 3,
            .health_scaled = 1000,
            .phase = 2,
            .alive = true,
        }};
    const auto acknowledgement =
        ihomeland::sim::
            InputAcknowledgementProjection{
                .actor_id = 42,
                .mapping_generation = 11,
                .last_processed_input_tick = 0,
            };
    Require(
        replication.QueueFullSnapshot(
            10,
            1,
            7,
            acknowledgement,
            states,
            NowUnixMs) &&
            replication.QueueDeltaSnapshot(
                11,
                2,
                7,
                acknowledgement,
                states,
                NowUnixMs) &&
            replication.QueueDeltaSnapshot(
                12,
                3,
                7,
                acknowledgement,
                states,
                NowUnixMs),
        "snapshot projection failed");
    const auto event =
        ihomeland::sim::EventProjectionToken{
            .tick = 12,
            .kind = 1,
            .source_actor_id = 42,
            .target_actor_id = 43,
            .activation_id = 9,
            .value_scaled = 0,
        };
    Require(
        replication.QueueAbilityEvent(
            event,
            1,
            7,
            1,
            NowUnixMs) &&
            replication.QueueEntityLifecycle(
                event,
                1,
                1,
                5,
                NowUnixMs) &&
            replication.QueueResyncResponse(
                10,
                12,
                1,
                8,
                NowUnixMs),
        "reliable projection failed");
    const auto metrics = replication.Metrics();
    Require(
        metrics.queued == 5 &&
            metrics.kcp_queued == 3 &&
            metrics.replaced_snapshots == 1,
        "snapshot replacement or KCP accounting drifted");
    auto full = replication.Pop(NowUnixMs + 1);
    auto delta = replication.Pop(NowUnixMs + 1);
    auto ability = replication.Pop(NowUnixMs + 1);
    ihomeland::battle::v1::BattleFullSnapshot
        full_message;
    ihomeland::battle::v1::BattleDeltaSnapshot
        delta_message;
    Require(
        full.has_value() &&
            full->message_id == 3002 &&
            full->lane ==
                ihomeland::sim::
                    BattleReplicationLane::Raw &&
            delta.has_value() &&
            delta->message_id == 3003 &&
            delta->application_sequence == 3 &&
            ability.has_value() &&
            ability->message_id == 3004 &&
            ability->lane ==
                ihomeland::sim::
                    BattleReplicationLane::Kcp &&
            full_message.ParseFromArray(
                full->payload.data(),
                static_cast<int>(
                    full->payload.size())) &&
            full_message
                .has_last_processed_input_tick() &&
            full_message
                    .last_processed_input_tick() ==
                0 &&
            delta_message.ParseFromArray(
                delta->payload.data(),
                static_cast<int>(
                    delta->payload.size())) &&
            delta_message
                .has_last_processed_input_tick() &&
            delta_message
                    .last_processed_input_tick() ==
                0,
        "replication violated unique lane or latest snapshot");

    auto expiring =
        ihomeland::sim::BattleReplicationQueue(
            context,
            session_resources);
    Require(
        expiring.QueueAbilityEvent(
            event,
            1,
            7,
            1,
            NowUnixMs) &&
            !expiring.Pop(
                NowUnixMs + 500).has_value() &&
            expiring.Metrics().expired == 1,
        "expired reliable event was not terminated");
    auto resync_alive =
        ihomeland::sim::BattleReplicationQueue(
            context,
            session_resources);
    Require(
        resync_alive.QueueResyncResponse(
            11,
            12,
            1,
            9,
            NowUnixMs) &&
            resync_alive.Pop(
                NowUnixMs +
                ihomeland::sim::BattleKcpRoutePolicy::
                    ReliableEventExpiryMilliseconds)
                .has_value(),
        "resync response inherited reliable-event expiry");
    auto resync_expired =
        ihomeland::sim::BattleReplicationQueue(
            context,
            session_resources);
    Require(
        resync_expired.QueueResyncResponse(
            12,
            12,
            1,
            10,
            NowUnixMs) &&
            !resync_expired.Pop(
                 NowUnixMs +
                 ihomeland::sim::BattleKcpRoutePolicy::
                     ResyncExpiryMilliseconds)
                 .has_value() &&
            resync_expired.Metrics().expired == 1,
        "resync response ignored route-owned expiry");
    auto predecessor_acknowledgement =
        acknowledgement;
    predecessor_acknowledgement
        .mapping_generation = 10;
    Require(
        !replication.QueueDeltaSnapshot(
            13,
            4,
            7,
            predecessor_acknowledgement,
            states,
            NowUnixMs + 10),
        "predecessor mapping acknowledgement reached publisher");
    authority->Invalidate(
        ihomeland::sim::
            BattleSessionInvalidationReason::
                AccountSession);
    Require(
        !replication.QueueDeltaSnapshot(
            13,
            4,
            7,
            acknowledgement,
            states,
            NowUnixMs + 10),
        "invalidated session continued replication");
}

/// TestSnapshotPartitionBudget 验证最大 ack varint 仍按既有 payload/MTU 预算切分。
void TestSnapshotPartitionBudget() {
    constexpr std::size_t StateCount = 96;
    auto clock =
        std::make_shared<
            ihomeland::sim::ManualTickClock>();
    auto instance =
        ihomeland::sim::SimulationInstance(
            InstanceIdentity(),
            {
                .tick_step = 50ms,
                .inbox_capacity = 1,
                .hard_tick_debt = 1,
            },
            clock,
            [](const ihomeland::sim::
                   TickObservation&) {});
    auto authority =
        std::make_shared<
            ihomeland::sim::
                BattleSessionAuthority>();
    auto context = Context(instance, authority);
    auto resources =
        ihomeland::sim::BattleResourceGovernor{};
    auto session_resources =
        ihomeland::sim::
            BattleSessionResourceGovernor(
                resources,
                context.BattleSessionHandle(),
                201);
    auto replication =
        ihomeland::sim::BattleReplicationQueue(
            context,
            session_resources);
    std::vector<
        ihomeland::sim::StateProjectionToken>
        states;
    states.reserve(StateCount);
    for (std::size_t index = 0;
         index < StateCount;
         ++index) {
        states.push_back({
            .actor_id =
                static_cast<std::uint64_t>(
                    index + 1),
            .x_mm =
                std::numeric_limits<
                    std::int32_t>::max(),
            .y_mm =
                std::numeric_limits<
                    std::int32_t>::min(),
            .z_mm =
                std::numeric_limits<
                    std::int32_t>::max(),
            .health_scaled =
                std::numeric_limits<
                    std::uint32_t>::max(),
            .phase =
                std::numeric_limits<
                    std::uint32_t>::max(),
            .alive = false,
        });
    }
    const auto acknowledgement =
        ihomeland::sim::
            InputAcknowledgementProjection{
                .actor_id = 42,
                .mapping_generation = 11,
                .last_processed_input_tick =
                    std::numeric_limits<
                        std::uint64_t>::max(),
            };
    Require(
        replication.QueueFullSnapshot(
            20,
            1,
            7,
            acknowledgement,
            states,
            NowUnixMs),
        "large full snapshot was not partitioned");
    const auto* policy =
        ihomeland::sim::
            FindBattleRawRoutePolicy(3'002);
    Require(
        policy != nullptr,
        "full snapshot route policy is missing");

    std::size_t observed_states = 0;
    std::size_t observed_partitions = 0;
    std::uint8_t partition_count = 0;
    while (const auto item =
               replication.Pop(NowUnixMs + 1)) {
        ihomeland::battle::v1::
            BattleFullSnapshot message;
        Require(
            item->message_id == 3'002 &&
                item->payload.size() <=
                    policy->maximum_payload_bytes &&
                item->payload.size() +
                        ihomeland::sim::
                            BattleRawDispatcher::
                                RawHeaderBytes +
                        ihomeland::sim::
                            BattleSecureChannel::
                                SecureHeaderBytes +
                        ihomeland::sim::
                            BattleSecureChannel::
                                AeadTagBytes <=
                    ihomeland::sim::
                        BattleSecureChannel::
                            MaximumDatagramBytes &&
                message.ParseFromArray(
                    item->payload.data(),
                    static_cast<int>(
                        item->payload.size())) &&
                message
                    .has_last_processed_input_tick() &&
                message
                        .last_processed_input_tick() ==
                    acknowledgement
                        .last_processed_input_tick &&
                message.partition_index() ==
                    item->partition_index &&
                message.partition_count() ==
                    item->partition_count &&
                item->partition_index ==
                    observed_partitions,
            "snapshot partition exceeded budget or drifted");
        partition_count = item->partition_count;
        observed_states +=
            static_cast<std::size_t>(
                message.entities_size());
        ++observed_partitions;
    }
    Require(
        observed_partitions > 1 &&
            observed_partitions ==
                partition_count &&
            observed_states == StateCount,
        "snapshot partition boundary lost state");
}

}  // namespace

/// main 统一报告测试不变量，避免未处理异常退化为无诊断的 fast-fail。
int main() {
    try {
        TestContextAndIngress();
        TestReplication();
        TestSnapshotPartitionBudget();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << "battle_session_test: "
                  << error.what() << '\n';
        return 1;
    }
}
