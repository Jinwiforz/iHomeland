#include "ihomeland/sim/gameplay/battle_movement_replication.hpp"
#include "ihomeland/sim/physics/flat_ground_physics_world.hpp"

#include <iostream>
#include <memory>
#include <stdexcept>
#include <vector>

namespace {

/// Require 把权威 movement projection 回归失败转换为单一 test exception。
void Require(
    const bool condition,
    const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// ActiveActors 返回双 player fixture 的规范参战集合。
[[nodiscard]] const std::vector<std::uint64_t>&
ActiveActors() {
    static const std::vector<std::uint64_t> actors{1, 2};
    return actors;
}

/// Config 返回 current 50 ms server/client movement contract。
[[nodiscard]]
ihomeland::sim::BattleMovementReplicationConfig
Config() {
    return {
        .mapping_generation = 11,
        .movement =
            {
                .tick_step_ns = 50'000'000,
                .input_scale = 1'000,
                .maximum_horizontal_speed_mm_per_second =
                    3'000,
                .acceleration_mm_per_second_squared =
                    60'000,
                .deceleration_mm_per_second_squared =
                    60'000,
                .gravity_mm_per_second_squared =
                    10'000,
                .jump_speed_mm_per_second =
                    5'000,
                .maximum_ground_slope_millirad =
                    785,
                .maximum_step_height_mm = 400,
            },
        .maximum_actors = 2,
    };
}

/// Resolution 构造一个 Tick 的完整 typed actor input。
[[nodiscard]]
ihomeland::sim::ActorInputResolution
Resolution(
    const std::uint64_t actor_id,
    const std::int16_t move_x,
    const std::int16_t move_z,
    const bool jump,
    const std::optional<std::int32_t> yaw,
    const std::uint64_t last_processed) {
    return {
        .actor_id = actor_id,
        .continuous_payload = "0|0|0",
        .move_x_permille = move_x,
        .move_z_permille = move_z,
        .held = false,
        .jump_pressed = jump,
        .aim_yaw_millidegrees = yaw,
        .discrete_sequences = {},
        .last_processed_input_tick =
            last_processed,
    };
}

/// Acknowledgement 构造 current mapping generation 的 actor frontier。
[[nodiscard]]
ihomeland::sim::InputAcknowledgementProjection
Acknowledgement(
    const std::uint64_t actor_id,
    const std::uint64_t frontier) {
    return {
        .actor_id = actor_id,
        .mapping_generation = 11,
        .last_processed_input_tick = frontier,
    };
}

/// TestMovementJumpAndActorProjection 验证 move/aim/jump 与 per-session ack同 Tick发布。
void TestMovementJumpAndActorProjection() {
    auto physics = std::make_shared<
        ihomeland::sim::
            FlatGroundPhysicsWorld>();
    ihomeland::sim::
        BattleMovementReplicationStore store(
            Config(),
            {2, 1},
            100'000,
            physics);
    const std::vector<
        ihomeland::sim::ActorInputResolution>
        first_resolutions{
            Resolution(
                1,
                1'000,
                0,
                true,
                180'000,
                2),
            Resolution(
                2,
                0,
                0,
                false,
                std::nullopt,
                1),
        };
    const std::vector<
        ihomeland::sim::
            InputAcknowledgementProjection>
        first_acknowledgements{
            Acknowledgement(1, 2),
            Acknowledgement(2, 1),
        };
    store.Commit(
        1,
        first_resolutions,
        first_acknowledgements,
        ActiveActors());

    const auto owner = store.Freeze(1, 11);
    const auto visitor = store.Freeze(2, 11);
    Require(
        owner.has_value() &&
            visitor.has_value() &&
            owner->server_tick == 1 &&
            visitor->server_tick == 1 &&
            owner->acknowledgement
                    .last_processed_input_tick ==
                2 &&
            visitor->acknowledgement
                    .last_processed_input_tick ==
                1 &&
            owner->states.size() == 2 &&
            visitor->states ==
                owner->states,
        "movement state and actor acknowledgement were not one commit");
    const auto& local = owner->states[0];
    const auto& remote = owner->states[1];
    Require(
        local.actor_id == 1 &&
            local.x_mm == 150 &&
            local.y_mm == 250 &&
            local.velocity_x_mm_per_second ==
                3'000 &&
            local.velocity_y_mm_per_second ==
                5'000 &&
            local.yaw_millidegrees ==
                -180'000 &&
            !local.grounded &&
            remote.actor_id == 2 &&
            remote.x_mm == 0 &&
            remote.y_mm == 0 &&
            remote.grounded,
        "authority move/aim/grounded jump projection drifted");

    const std::vector<
        ihomeland::sim::ActorInputResolution>
        second_resolutions{
            Resolution(
                1,
                1'000,
                0,
                true,
                std::nullopt,
                4),
            Resolution(
                2,
                0,
                0,
                false,
                std::nullopt,
                3),
        };
    const std::vector<
        ihomeland::sim::
            InputAcknowledgementProjection>
        second_acknowledgements{
            Acknowledgement(1, 4),
            Acknowledgement(2, 3),
        };
    store.Commit(
        2,
        second_resolutions,
        second_acknowledgements,
        ActiveActors());
    const auto airborne = store.Freeze(1, 11);
    Require(
        airborne.has_value() &&
            airborne->states[0].x_mm == 300 &&
            airborne->states[0].y_mm == 475 &&
            airborne->states[0]
                    .velocity_y_mm_per_second ==
                4'500 &&
            !airborne->states[0].grounded,
        "airborne repeat jump applied another impulse");
}

/// TestFailedCommitDoesNotPublish 验证 invalid generation 不暴露半 Tick state。
void TestFailedCommitDoesNotPublish() {
    auto physics = std::make_shared<
        ihomeland::sim::
            FlatGroundPhysicsWorld>();
    ihomeland::sim::
        BattleMovementReplicationStore store(
            Config(),
            {1, 2},
            100'000,
            physics);
    const std::vector<
        ihomeland::sim::ActorInputResolution>
        resolutions{
            Resolution(
                1,
                0,
                0,
                false,
                std::nullopt,
                0),
            Resolution(
                2,
                0,
                0,
                false,
                std::nullopt,
                0),
        };
    auto acknowledgements =
        std::vector<
            ihomeland::sim::
                InputAcknowledgementProjection>{
            Acknowledgement(1, 0),
            Acknowledgement(2, 0),
        };
    store.Commit(
        1,
        resolutions,
        acknowledgements,
        ActiveActors());
    acknowledgements[1].mapping_generation = 12;
    try {
        store.Commit(
            2,
            resolutions,
            acknowledgements,
            ActiveActors());
        throw std::runtime_error(
            "invalid movement acknowledgement was committed");
    } catch (
        const ihomeland::sim::
            BattleMovementReplicationError& error) {
        Require(
            error.Code() ==
                ihomeland::sim::
                    BattleMovementReplicationErrorCode::
                        InvalidCommit,
            "invalid movement commit returned wrong code");
    }
    const auto snapshot = store.Freeze(1, 11);
    Require(
        snapshot.has_value() &&
            snapshot->server_tick == 1,
        "failed movement commit published a half Tick");
    Require(
        !store.Freeze(1, 12).has_value() &&
            !store.Freeze(3, 11).has_value(),
        "movement freeze accepted stale generation or actor");
}

/// TestDeferredCommitPublishesOnlyAuthoritativeState 验证 production overlay 不暴露中间 movement projection。
void TestDeferredCommitPublishesOnlyAuthoritativeState() {
    auto physics = std::make_shared<
        ihomeland::sim::
            FlatGroundPhysicsWorld>();
    ihomeland::sim::
        BattleMovementReplicationStore store(
            Config(),
            {1, 2},
            100'000,
            physics);
    const std::vector<
        ihomeland::sim::ActorInputResolution>
        resolutions{
            Resolution(1, 0, 0, false, std::nullopt, 0),
            Resolution(2, 0, 0, false, std::nullopt, 0),
        };
    const std::vector<
        ihomeland::sim::InputAcknowledgementProjection>
        acknowledgements{
            Acknowledgement(1, 0),
            Acknowledgement(2, 0),
        };
    auto states = store.Commit(
        1,
        resolutions,
        acknowledgements,
        ActiveActors(),
        false);
    Require(
        !store.Freeze(1, 11).has_value(),
        "deferred movement commit exposed an intermediate projection");
    for (auto& state : states) {
        state.max_health_scaled = 100'000;
    }
    store.PublishAuthoritative(
        1,
        states,
        acknowledgements,
        {},
        {},
        false);
    const auto snapshot = store.Freeze(1, 11);
    Require(
        snapshot.has_value() &&
            snapshot->server_tick == 1 &&
            snapshot->states.size() == 2 &&
            snapshot->states[0].max_health_scaled == 100'000 &&
            snapshot->states[1].max_health_scaled == 100'000,
        "authoritative overlay was not published atomically");
}

/// TestInactiveActorStopsAtBarrier 验证 session 撤销后不保留输入或运动惯性。
void TestInactiveActorStopsAtBarrier() {
    auto physics = std::make_shared<
        ihomeland::sim::FlatGroundPhysicsWorld>();
    ihomeland::sim::BattleMovementReplicationStore store(
        Config(),
        {1},
        100'000,
        physics);
    const std::vector<ihomeland::sim::ActorInputResolution>
        resolutions{
            Resolution(1, 1'000, 0, false, std::nullopt, 0),
        };
    const std::vector<
        ihomeland::sim::InputAcknowledgementProjection>
        acknowledgements{
            Acknowledgement(1, 0),
        };
    const std::vector<std::uint64_t> active{1};
    const std::vector<std::uint64_t> inactive;
    const auto moving = store.Commit(
        1,
        resolutions,
        acknowledgements,
        active);
    const auto stopped = store.Commit(
        2,
        resolutions,
        acknowledgements,
        inactive);
    Require(
        moving[0].x_mm > 0 &&
            stopped[0].x_mm == moving[0].x_mm &&
            stopped[0].velocity_x_mm_per_second == 0 &&
            stopped[0].velocity_y_mm_per_second == 0 &&
            stopped[0].velocity_z_mm_per_second == 0,
        "inactive actor retained movement input or velocity");
}

}  // namespace

/// main 执行权威 movement/physics state 与 acknowledgement原子projection回归。
int main() {
    try {
        TestMovementJumpAndActorProjection();
        TestFailedCommitDoesNotPublish();
        TestDeferredCommitPublishesOnlyAuthoritativeState();
        TestInactiveActorStopsAtBarrier();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
