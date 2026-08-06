#include "ihomeland/sim/gameplay/battle_movement_replication.hpp"

#include <algorithm>
#include <cstdint>
#include <limits>
#include <utility>

namespace ihomeland::sim {
namespace {

/// FractionScale 是 PhysicsHitValue movement fraction 的固定百万分比。
constexpr std::int64_t FractionScale = 1'000'000;
/// MaximumQualifiedActors 与 current battle model hard cap一致。
constexpr std::size_t MaximumQualifiedActors = 8;

/// NormalizeYaw 把已验证 aim intent 映射到 wire 唯一半开区间。
[[nodiscard]] std::int32_t NormalizeYaw(
    const std::int32_t value) noexcept {
    auto normalized = value % 360'000;
    if (normalized >= 180'000) {
        normalized -= 360'000;
    } else if (normalized < -180'000) {
        normalized += 360'000;
    }
    return normalized;
}

/// ScaleDelta 以百万分比 toward-zero缩放已验证的单 Tick位移。
[[nodiscard]] std::int64_t ScaleDelta(
    const std::int64_t value,
    const std::uint32_t fraction) {
    if (fraction > FractionScale ||
        value >
            std::numeric_limits<std::int64_t>::max() /
                FractionScale ||
        value <
            std::numeric_limits<std::int64_t>::min() /
                FractionScale) {
        throw BattleMovementReplicationError(
            BattleMovementReplicationErrorCode::Physics,
            "movement fraction scaling overflow");
    }
    return value *
        static_cast<std::int64_t>(fraction) /
        FractionScale;
}

/// QueryIdentity 为每 Tick/actor 的 move和ground query生成稳定非零identity。
[[nodiscard]] std::uint64_t QueryIdentity(
    const std::uint64_t server_tick,
    const std::size_t actor_index,
    const std::size_t actor_count,
    const std::uint64_t query_offset) {
    if (server_tick == 0 ||
        actor_count == 0 ||
        actor_index >= actor_count ||
        query_offset > 1 ||
        server_tick - 1 >
            (std::numeric_limits<std::uint64_t>::max() -
             query_offset - 1) /
                (actor_count * 2)) {
        throw BattleMovementReplicationError(
            BattleMovementReplicationErrorCode::InvalidCommit,
            "movement query identity overflow");
    }
    return (server_tick - 1) *
               static_cast<std::uint64_t>(
                   actor_count * 2) +
           static_cast<std::uint64_t>(
               actor_index * 2) +
           query_offset + 1;
}

/// BlockingFraction 验证PhysicsWorld result并返回首个blocking fraction。
[[nodiscard]] std::optional<std::uint32_t>
BlockingFraction(
    const PhysicsQuery& query,
    const PhysicsQueryResult& result) {
    if (result.query_id != query.query_id ||
        result.hits.size() >
            query.maximum_hits) {
        throw BattleMovementReplicationError(
            BattleMovementReplicationErrorCode::Physics,
            "movement physics result identity or capacity drifted");
    }
    for (const auto& hit : result.hits) {
        if (hit.fraction_millionths >
                FractionScale ||
            hit.collider_id == 0 ||
            hit.subshape_id == 0) {
            throw BattleMovementReplicationError(
                BattleMovementReplicationErrorCode::Physics,
                "movement physics hit is invalid");
        }
        if (hit.blocking) {
            return hit.fraction_millionths;
        }
    }
    return std::nullopt;
}

/// ToProjection 复制一个 Physics committed actor state。
template <typename RuntimeActor>
[[nodiscard]] StateProjectionToken ToProjection(
    const RuntimeActor& actor) {
    return {
        .actor_id = actor.actor_id,
        .x_mm = actor.movement.position_x_mm,
        .y_mm = actor.movement.position_y_mm,
        .z_mm = actor.movement.position_z_mm,
        .yaw_millidegrees =
            actor.yaw_millidegrees,
        .velocity_x_mm_per_second =
            actor.movement
                .velocity_x_mm_per_second,
        .velocity_y_mm_per_second =
            actor.movement
                .velocity_y_mm_per_second,
        .velocity_z_mm_per_second =
            actor.movement
                .velocity_z_mm_per_second,
        .health_scaled = actor.health_scaled,
        .phase = actor.phase,
        .alive = actor.alive,
        .grounded = actor.movement.grounded,
    };
}

}  // namespace

BattleMovementReplicationError::
    BattleMovementReplicationError(
        const BattleMovementReplicationErrorCode code,
        const char* message)
    : std::runtime_error(message), code_(code) {}

BattleMovementReplicationErrorCode
BattleMovementReplicationError::Code() const noexcept {
    return code_;
}

BattleMovementReplicationStore::
    BattleMovementReplicationStore(
        BattleMovementReplicationConfig config,
        std::vector<std::uint64_t> actor_ids,
        const std::int64_t initial_health_scaled,
        std::shared_ptr<PhysicsWorld> physics_world,
        std::vector<Vector3Mm> initial_positions)
    : config_(config),
      physics_world_(std::move(physics_world)) {
    ValidateMovementConfig(config_.movement);
    std::sort(actor_ids.begin(), actor_ids.end());
    if (config_.mapping_generation == 0 ||
        config_.maximum_actors == 0 ||
        config_.maximum_actors >
            MaximumQualifiedActors ||
        actor_ids.empty() ||
        actor_ids.size() >
            config_.maximum_actors ||
        actor_ids.front() == 0 ||
        std::adjacent_find(
            actor_ids.begin(),
            actor_ids.end()) !=
            actor_ids.end() ||
        initial_health_scaled <= 0 ||
        (!initial_positions.empty() && initial_positions.size() != actor_ids.size()) ||
        !physics_world_) {
        throw BattleMovementReplicationError(
            BattleMovementReplicationErrorCode::InvalidConfig,
            "battle movement replication configuration is invalid");
    }
    actors_.reserve(actor_ids.size());
    candidates_.reserve(actor_ids.size());
    published_states_.reserve(actor_ids.size());
    candidate_states_.reserve(actor_ids.size());
    published_acknowledgements_.reserve(
        actor_ids.size());
    player_actor_ids_ = actor_ids;
    for (std::size_t index = 0; index < actor_ids.size(); ++index) {
        const auto actor_id = actor_ids[index];
        const auto initial = initial_positions.empty() ? Vector3Mm{} : initial_positions[index];
        actors_.push_back(
            RuntimeActorState{
                .actor_id = actor_id,
                .movement =
                    {
                        .position_x_mm = initial.x,
                        .position_y_mm = initial.y,
                        .position_z_mm = initial.z,
                        .velocity_x_mm_per_second =
                            0,
                        .velocity_y_mm_per_second =
                            0,
                        .velocity_z_mm_per_second =
                            0,
                        .grounded = true,
                    },
                .yaw_millidegrees = 0,
                .health_scaled =
                    initial_health_scaled,
                .phase = 0,
                .alive = true,
            });
    }
}

std::vector<StateProjectionToken>
BattleMovementReplicationStore::Commit(
    const std::uint64_t server_tick,
    const std::span<
        const ActorInputResolution> resolutions,
    const std::span<
        const InputAcknowledgementProjection>
        acknowledgements,
    const std::span<const std::uint64_t>
        active_actor_ids,
    const bool publish) {
    if (server_tick == 0 ||
        server_tick != committed_tick_ + 1 ||
        resolutions.size() != actors_.size() ||
        acknowledgements.size() !=
            actors_.size() ||
        active_actor_ids.size() > actors_.size() ||
        !std::ranges::is_sorted(active_actor_ids) ||
        std::adjacent_find(
            active_actor_ids.begin(),
            active_actor_ids.end()) !=
            active_actor_ids.end() ||
        std::ranges::any_of(
            active_actor_ids,
            [&](const std::uint64_t actor_id) {
                return !std::ranges::binary_search(
                    actors_,
                    actor_id,
                    {},
                    &RuntimeActorState::actor_id);
            })) {
        throw BattleMovementReplicationError(
            BattleMovementReplicationErrorCode::InvalidCommit,
            "battle movement commit set is incomplete");
    }
    candidates_ = actors_;
    for (std::size_t index = 0;
         index < actors_.size();
         ++index) {
        const auto& resolution = resolutions[index];
        const auto& acknowledgement =
            acknowledgements[index];
        const auto& current = actors_[index];
        auto& candidate = candidates_[index];
        if (resolution.actor_id !=
                current.actor_id ||
            acknowledgement.actor_id !=
                current.actor_id ||
            acknowledgement.mapping_generation !=
                config_.mapping_generation ||
            (index != 0 &&
             resolutions[index - 1].actor_id >=
                 resolution.actor_id)) {
            throw BattleMovementReplicationError(
                BattleMovementReplicationErrorCode::
                    InvalidCommit,
                "battle movement actor projection is not canonical");
        }
        if (!std::ranges::binary_search(
                active_actor_ids,
                current.actor_id)) {
            candidate.movement = current.movement;
            candidate.movement
                .velocity_x_mm_per_second = 0;
            candidate.movement
                .velocity_y_mm_per_second = 0;
            candidate.movement
                .velocity_z_mm_per_second = 0;
            continue;
        }
        const auto movement =
            IntegrateMovement(
                config_.movement,
                current.movement,
                MovementIntent{
                    .x_milli =
                        resolution.move_x_permille,
                    .z_milli =
                        resolution.move_z_permille,
                    .jump_pressed =
                        resolution.jump_pressed,
                });
        candidate.movement = movement.state;
        if (resolution
                .aim_yaw_millidegrees
                .has_value()) {
            candidate.yaw_millidegrees =
                NormalizeYaw(
                    *resolution
                         .aim_yaw_millidegrees);
        }

        const auto move_query =
            PhysicsQuery{
                .query_id = QueryIdentity(
                    server_tick,
                    index,
                    actors_.size(),
                    0),
                .tick = server_tick,
                .kind =
                    PhysicsQueryKind::
                        MoveCapsule,
                .actor_id = current.actor_id,
                .start_mm =
                    {
                        .x = current.movement
                                 .position_x_mm,
                        .y = current.movement
                                 .position_y_mm,
                        .z = current.movement
                                 .position_z_mm,
                    },
                .end_mm =
                    {
                        .x = movement.state
                                 .position_x_mm,
                        .y = movement.state
                                 .position_y_mm,
                        .z = movement.state
                                 .position_z_mm,
                    },
                .maximum_hits = 1,
            };
        const auto move_fraction =
            BlockingFraction(
                move_query,
                physics_world_->Query(
                    move_query));
        if (move_fraction.has_value()) {
            candidate.movement.position_x_mm =
                current.movement.position_x_mm +
                ScaleDelta(
                    movement.delta_x_mm,
                    *move_fraction);
            candidate.movement.position_y_mm =
                current.movement.position_y_mm +
                ScaleDelta(
                    movement.delta_y_mm,
                    *move_fraction);
            candidate.movement.position_z_mm =
                current.movement.position_z_mm +
                ScaleDelta(
                    movement.delta_z_mm,
                    *move_fraction);
            if (movement.delta_y_mm < 0) {
                candidate.movement
                    .velocity_y_mm_per_second = 0;
            }
        }

        auto contact = GroundContact{
            .blocking = false,
            .slope_millirad = 0,
            .step_height_mm = 0};
        if (candidate.movement
                .velocity_y_mm_per_second <= 0) {
            const auto ground_query =
                PhysicsQuery{
                    .query_id = QueryIdentity(
                        server_tick,
                        index,
                        actors_.size(),
                        1),
                    .tick = server_tick,
                    .kind =
                        PhysicsQueryKind::
                            GroundProbe,
                    .actor_id =
                        current.actor_id,
                    .start_mm =
                        {
                            .x = candidate
                                     .movement
                                     .position_x_mm,
                            .y = candidate
                                     .movement
                                     .position_y_mm,
                            .z = candidate
                                     .movement
                                     .position_z_mm,
                        },
                    .end_mm =
                        {
                            .x = candidate
                                     .movement
                                     .position_x_mm,
                            .y = candidate
                                     .movement
                                     .position_y_mm -
                                 1,
                            .z = candidate
                                     .movement
                                     .position_z_mm,
                        },
                    .maximum_hits = 1,
                };
            contact.blocking =
                BlockingFraction(
                    ground_query,
                    physics_world_->Query(
                        ground_query))
                    .has_value();
        }
        candidate.movement =
            ApplyGroundContact(
                config_.movement,
                candidate.movement,
                contact);
    }

    candidate_states_.clear();
    for (const auto& actor : candidates_) {
        candidate_states_.push_back(
            ToProjection(actor));
    }
    if (publish) {
        std::scoped_lock lock(snapshot_mutex_);
        published_states_.assign(
            candidate_states_.begin(),
            candidate_states_.end());
        published_acknowledgements_.assign(
            acknowledgements.begin(),
            acknowledgements.end());
        published_tick_ = server_tick;
    }
    actors_ = candidates_;
    committed_tick_ = server_tick;
    return candidate_states_;
}

void BattleMovementReplicationStore::PublishAuthoritative(
    const std::uint64_t server_tick,
    const std::span<const StateProjectionToken> states,
    const std::span<const InputAcknowledgementProjection> acknowledgements,
    const std::span<const CombatAbilityEvent> ability_events,
    const std::span<const CombatLifecycleEvent> lifecycle_events,
    const bool encounter_complete) {
    if (server_tick == 0 || server_tick != committed_tick_ || states.empty() ||
        acknowledgements.size() != actors_.size() ||
        !std::ranges::is_sorted(states, {}, &StateProjectionToken::actor_id) ||
        std::adjacent_find(states.begin(), states.end(), [](const auto& left, const auto& right) {
            return left.actor_id == right.actor_id;
        }) != states.end()) {
        throw BattleMovementReplicationError(
            BattleMovementReplicationErrorCode::InvalidCommit,
            "battle authoritative projection is invalid");
    }
    for (const auto& state : states) {
        if (state.actor_id == 0 || state.archetype_id == 0 ||
            state.max_health_scaled == 0 || state.health_scaled < 0 ||
            state.health_scaled > state.max_health_scaled ||
            (!state.alive && state.health_scaled != 0)) {
            throw BattleMovementReplicationError(
                BattleMovementReplicationErrorCode::InvalidCommit,
                "battle authoritative entity state is invalid");
        }
    }
    std::scoped_lock lock(snapshot_mutex_);
    published_states_.assign(states.begin(), states.end());
    published_acknowledgements_.assign(acknowledgements.begin(), acknowledgements.end());
    published_ability_events_.insert(
        published_ability_events_.end(), ability_events.begin(), ability_events.end());
    published_lifecycle_events_.insert(
        published_lifecycle_events_.end(), lifecycle_events.begin(), lifecycle_events.end());
    constexpr std::size_t EventJournalLimit = 256;
    if (published_ability_events_.size() > EventJournalLimit) {
        published_ability_events_.erase(
            published_ability_events_.begin(),
            published_ability_events_.end() - EventJournalLimit);
    }
    if (published_lifecycle_events_.size() > EventJournalLimit) {
        published_lifecycle_events_.erase(
            published_lifecycle_events_.begin(),
            published_lifecycle_events_.end() - EventJournalLimit);
    }
    published_encounter_complete_ = encounter_complete;
    published_tick_ = server_tick;
}

std::optional<BattleMovementReplicationSnapshot>
BattleMovementReplicationStore::Freeze(
    const std::uint64_t actor_id,
    const std::uint64_t mapping_generation) const {
    if (actor_id == 0 ||
        mapping_generation !=
            config_.mapping_generation) {
        return std::nullopt;
    }
    std::scoped_lock lock(snapshot_mutex_);
    if (published_tick_ == 0) {
        return std::nullopt;
    }
    const auto acknowledgement =
        std::lower_bound(
            published_acknowledgements_.begin(),
            published_acknowledgements_.end(),
            actor_id,
            [](const auto& candidate,
               const std::uint64_t value) {
                return candidate.actor_id < value;
            });
    if (acknowledgement ==
            published_acknowledgements_.end() ||
        acknowledgement->actor_id != actor_id) {
        return std::nullopt;
    }
    return BattleMovementReplicationSnapshot{
        .server_tick = published_tick_,
        .acknowledgement =
            *acknowledgement,
        .states = published_states_,
        .ability_events = published_ability_events_,
        .lifecycle_events = published_lifecycle_events_,
        .player_actor_ids = player_actor_ids_,
        .encounter_complete = published_encounter_complete_,
    };
}

}  // namespace ihomeland::sim
