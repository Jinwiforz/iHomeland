#include "ihomeland/sim/transport/battle_session.hpp"

#include "ihomeland/sim/simulation/input_timeline.hpp"
#include "ihomeland/sim/simulation/simulation_instance.hpp"

#include "ihomeland/battle/v1/battle.pb.h"

#include <chrono>
#include <cstdint>
#include <memory>
#include <ranges>
#include <stdexcept>
#include <string>
#include <string_view>
#include <thread>
#include <vector>

namespace {

using namespace std::chrono_literals;
constexpr std::uint64_t NowUnixMs = 10'000'000;

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
                observed.insert(
                    observed.end(),
                    tick.commands.begin(),
                    tick.commands.end());
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
    auto ingress = ihomeland::sim::BattleInputIngress(
        context,
        command_ingress,
        resources,
        201);

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
    for (std::uint32_t attempt = 0;
         attempt < 10'000 &&
         instance.CommittedTick() == 0;
         ++attempt) {
        std::this_thread::yield();
    }
    Require(
        observed.size() == 3 &&
            std::ranges::all_of(
                observed,
                [](const auto& command) {
                    return command.actor_id == 42;
                }),
        "UDP payload changedcontext actor binding");
    instance.BeginDrain();
    clock->Advance();
    Require(
        instance.Stop(2s),
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
    auto replication =
        ihomeland::sim::BattleReplicationQueue(
            context,
            resources);
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
    Require(
        replication.QueueFullSnapshot(
            10,
            1,
            7,
            states,
            NowUnixMs) &&
            replication.QueueDeltaSnapshot(
                11,
                2,
                7,
                states,
                NowUnixMs) &&
            replication.QueueDeltaSnapshot(
                12,
                3,
                7,
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
                    BattleReplicationLane::Kcp,
        "replication violated unique lane or latest snapshot");

    auto expiring =
        ihomeland::sim::BattleReplicationQueue(
            context,
            resources);
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
    authority->Invalidate(
        ihomeland::sim::
            BattleSessionInvalidationReason::
                AccountSession);
    Require(
        !replication.QueueDeltaSnapshot(
            13,
            4,
            7,
            states,
            NowUnixMs + 10),
        "invalidated session continued replication");
}

}  // namespace

int main() {
    TestContextAndIngress();
    TestReplication();
    return 0;
}
