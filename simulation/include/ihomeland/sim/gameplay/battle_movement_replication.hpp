#pragma once

#include "ihomeland/sim/gameplay/movement.hpp"
#include "ihomeland/sim/gameplay/projection.hpp"
#include "ihomeland/sim/physics/physics_world.hpp"
#include "ihomeland/sim/simulation/input_timeline.hpp"

#include <cstddef>
#include <cstdint>
#include <memory>
#include <mutex>
#include <optional>
#include <span>
#include <stdexcept>
#include <vector>

namespace ihomeland::sim {

/// BattleMovementReplicationErrorCode 是 live movement commit 的稳定失败分类。
enum class BattleMovementReplicationErrorCode : std::uint8_t {
    /// InvalidConfig 表示 mapping、actor、movement 或 PhysicsWorld binding 非法。
    InvalidConfig,
    /// InvalidCommit 表示 Tick、resolution 或 acknowledgement 不是完整 successor。
    InvalidCommit,
    /// Physics 表示 PhysicsWorld 返回的 identity、hit 或 fraction 不合法。
    Physics,
};

/// BattleMovementReplicationError 携带低敏、可机器判断的 commit failure。
class BattleMovementReplicationError final : public std::runtime_error {
public:
    /// 构造函数绑定稳定错误码。
    BattleMovementReplicationError(
        BattleMovementReplicationErrorCode code,
        const char* message);

    /// Code 返回失败分类。
    [[nodiscard]] BattleMovementReplicationErrorCode Code() const noexcept;

private:
    /// code_ 在异常构造后不可变。
    BattleMovementReplicationErrorCode code_;
};

/// BattleMovementReplicationConfig 冻结 live runtime mapping 与 physics query policy。
struct BattleMovementReplicationConfig final {
    /// mapping_generation 绑定 current InputTick epoch。
    std::uint64_t mapping_generation;
    /// movement 是 50 ms 整数 kinematic policy。
    MovementConfig movement;
    /// maximum_actors 与 current qualification hard cap 一致。
    std::size_t maximum_actors;
};

/// BattleMovementReplicationSnapshot 是一个 committed Tick 的 owned 只读副本。
struct BattleMovementReplicationSnapshot final {
    /// server_tick 是 state 与 acknowledgement 共同所属的 commit。
    std::uint64_t server_tick;
    /// acknowledgement 只属于请求的 current actor/generation。
    InputAcknowledgementProjection acknowledgement;
    /// states 是同 Tick 全部公开 actor state，按 ActorID 排序。
    std::vector<StateProjectionToken> states;
    /// ability_events 是尚在有界 journal 内的 instance-global 有序事件。
    std::vector<CombatAbilityEvent> ability_events;
    /// lifecycle_events 是尚在有界 journal 内的 instance-global 有序事件。
    std::vector<CombatLifecycleEvent> lifecycle_events;
    /// player_actor_ids 标识预留 player slots，transport 据 active session 过滤空 slot。
    std::vector<std::uint64_t> player_actor_ids;
    /// encounter_complete 是不可结算的暂态 Boss defeat projection。
    bool encounter_complete;
};

/// BattleMovementReplicationStore 是 live runtime 唯一 movement state 与复制冻结 owner。
///
/// Commit 只能由 SimulationInstance 唯一 worker 串行调用；Freeze 可由网络线程并发调用，
/// 且只返回同一临界区发布的 state/acknowledgement owned copy。
class BattleMovementReplicationStore final {
public:
    /// 构造函数冻结 actor binding、初始状态、mapping generation 与 PhysicsWorld。
    BattleMovementReplicationStore(
        BattleMovementReplicationConfig config,
        std::vector<std::uint64_t> actor_ids,
        std::int64_t initial_health_scaled,
        std::shared_ptr<PhysicsWorld> physics_world,
        std::vector<Vector3Mm> initial_positions = {});

    /// Commit 原子推进完整 actor set并返回 worker-owned movement projection。
    /// publish 为 false 时，调用方必须在同一 Tick 用 PublishAuthoritative 一次发布完整状态。
    std::vector<StateProjectionToken> Commit(
        std::uint64_t server_tick,
        std::span<const ActorInputResolution> resolutions,
        std::span<
            const InputAcknowledgementProjection>
            acknowledgements,
        std::span<const std::uint64_t> active_actor_ids,
        bool publish = true);

    /// PublishAuthoritative 在同一 committed Tick 原子发布 combat pipeline 的完整 entity set。
    void PublishAuthoritative(
        std::uint64_t server_tick,
        std::span<const StateProjectionToken> states,
        std::span<const InputAcknowledgementProjection> acknowledgements,
        std::span<const CombatAbilityEvent> ability_events,
        std::span<const CombatLifecycleEvent> lifecycle_events,
        bool encounter_complete);

    /// Freeze 返回 exact actor/generation 的同 Tick state 与 acknowledgement。
    [[nodiscard]] std::optional<
        BattleMovementReplicationSnapshot>
    Freeze(
        std::uint64_t actor_id,
        std::uint64_t mapping_generation) const;

private:
    /// RuntimeActorState 保存 worker-only movement state与其公开字段。
    struct RuntimeActorState final {
        /// actor_id 是构造时冻结的排序 identity。
        std::uint64_t actor_id;
        /// movement 是 Physics stage 提交的当前状态。
        MovementState movement;
        /// yaw_millidegrees 是最后已提交的规范 aim yaw。
        std::int32_t yaw_millidegrees;
        /// health_scaled 由后续 Attribute owner替换前保持初始值。
        std::int64_t health_scaled;
        /// phase 由后续 Ability/AI owner替换前保持零。
        std::uint32_t phase;
        /// alive 由后续 Death owner替换前保持 true。
        bool alive;
    };

    /// config_ 在构造后不可变。
    BattleMovementReplicationConfig config_;
    /// physics_world_ 由 instance owner共享并只在 worker调用。
    std::shared_ptr<PhysicsWorld> physics_world_;
    /// actors_ 是唯一worker已提交的current mutable state。
    std::vector<RuntimeActorState> actors_;
    /// candidates_ 预留到 hard actor cap，用于失败前完整计算。
    std::vector<RuntimeActorState> candidates_;
    /// candidate_states_ 复用固定容量构造下一次 immutable projection。
    std::vector<StateProjectionToken> candidate_states_;
    /// committed_tick_ 只由 Commit 串行推进。
    std::uint64_t committed_tick_{0};
    /// snapshot_mutex_ 原子保护以下三个 published fields。
    mutable std::mutex snapshot_mutex_;
    /// published_tick_ 为零表示尚无 committed projection。
    std::uint64_t published_tick_{0};
    /// published_states_ 与 published_acknowledgements_ 属于同一 Tick。
    std::vector<StateProjectionToken> published_states_;
    /// published_acknowledgements_ 按 actor identity排序并固定长度。
    std::vector<InputAcknowledgementProjection>
        published_acknowledgements_;
    /// published_ability_events_ 是跨 Tick 保留的有界 reliable journal。
    std::vector<CombatAbilityEvent> published_ability_events_;
    /// published_lifecycle_events_ 是跨 Tick 保留的有界 reliable journal。
    std::vector<CombatLifecycleEvent> published_lifecycle_events_;
    /// player_actor_ids_ 是构造时冻结的预留 slot identity。
    std::vector<std::uint64_t> player_actor_ids_;
    /// published_encounter_complete_ 只作为诊断/表现的暂态状态。
    bool published_encounter_complete_{false};
};

}  // namespace ihomeland::sim
