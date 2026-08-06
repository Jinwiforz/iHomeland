#pragma once

#include "ihomeland/sim/config/gameplay_package.hpp"
#include "ihomeland/sim/config/personal_world_arena.hpp"
#include "ihomeland/sim/gameplay/ability.hpp"
#include "ihomeland/sim/gameplay/ai.hpp"
#include "ihomeland/sim/gameplay/effects.hpp"
#include "ihomeland/sim/gameplay/projection.hpp"
#include "ihomeland/sim/navigation/navigation_world.hpp"
#include "ihomeland/sim/physics/physics_world.hpp"
#include "ihomeland/sim/simulation/input_timeline.hpp"
#include "ihomeland/sim/simulation/simulation_instance.hpp"

#include <cstddef>
#include <cstdint>
#include <memory>
#include <span>
#include <vector>

namespace ihomeland::sim {

/// ProductionEncounterSnapshot 是同一 committed Tick 的完整只读 combat 结果。
struct ProductionEncounterSnapshot final {
    /// server_tick 是 state 与 event 共同所属的 commit。
    std::uint64_t server_tick;
    /// states 是 player、AI、Boss 与 projectile 的规范 entity set。
    std::vector<StateProjectionToken> states;
    /// ability_events 是本次 commit 新产生的有序可靠事件。
    std::vector<CombatAbilityEvent> ability_events;
    /// lifecycle_events 是本次 commit 新产生的有序可靠事件。
    std::vector<CombatLifecycleEvent> lifecycle_events;
    /// encounter_complete 只表示暂态 Boss defeat，不是 settlement receipt。
    bool encounter_complete;
};

/// ProductionEncounterRuntime 组合 production catalog、arena 与冻结 gameplay primitives。
///
/// Commit 只由 SimulationInstance worker 调用；客户端输入只提供 intent，不能提供 target、
/// damage、health、death 或 reward。对象生命周期绑定单个 assignment timeline。
class ProductionEncounterRuntime final {
public:
    /// 构造函数校验 capacity 并从 arena spawn plan 建立确定 S0。
    ProductionEncounterRuntime(
        std::shared_ptr<const GameplayPackageCatalog> catalog,
        std::size_t player_capacity,
        std::uint64_t seed,
        std::shared_ptr<PhysicsWorld> physics_world,
        std::shared_ptr<NavigationWorld> navigation_world);

    /// 析构函数在完整 private entity type 可见的 translation unit 释放容器。
    ~ProductionEncounterRuntime();

    /// Commit 按冻结 pipeline 处理玩家输入、AI、Ability、命中、伤害、死亡和复制。
    [[nodiscard]] ProductionEncounterSnapshot Commit(
        std::uint64_t tick,
        std::span<const IngressCommand> commands,
        std::span<const StateProjectionToken> player_movement_states,
        std::span<const std::uint64_t> active_player_actor_ids);

    /// InitialSnapshot 返回 S0 spawn lifecycle 和完整 entity state。
    [[nodiscard]] ProductionEncounterSnapshot InitialSnapshot() const;

private:
    struct Actor;
    struct Projectile;
    struct Content;

    [[nodiscard]] Actor& FindActor(std::uint64_t actor_id);
    [[nodiscard]] const Actor& FindActor(std::uint64_t actor_id) const;
    void EmitAbility(
        std::uint64_t tick,
        const Actor& source,
        CombatAbilityEventPhase phase,
        std::vector<std::uint64_t> targets);
    void ApplyDamage(
        std::uint64_t tick,
        std::uint64_t source_actor_id,
        std::uint64_t target_actor_id,
        std::int64_t damage,
        std::uint64_t activation_id);
    [[nodiscard]] std::vector<StateProjectionToken> ProjectStates() const;

    /// catalog_ 是 instance lifetime 内不可热替换的 production authority snapshot。
    std::shared_ptr<const GameplayPackageCatalog> catalog_;
    /// physics_world_ 是权威 sweep 与 arena collision query owner。
    std::shared_ptr<PhysicsWorld> physics_world_;
    /// navigation_world_ 是 AI path query 的 instance-exclusive adapter。
    std::shared_ptr<NavigationWorld> navigation_world_;
    /// content_ 保存从 catalog 解析的 immutable typed numeric view。
    std::unique_ptr<Content> content_;
    /// actors_ 是唯一 worker 管理的 player、monster 与 Boss runtime state。
    std::vector<Actor> actors_;
    /// projectiles_ 是有界 deferred projectile runtime state。
    std::vector<Projectile> projectiles_;
    /// pending_ability_events_ 只保存当前 commit 新产生的事件。
    std::vector<CombatAbilityEvent> pending_ability_events_;
    /// pending_lifecycle_events_ 只保存当前 commit 新产生的事件。
    std::vector<CombatLifecycleEvent> pending_lifecycle_events_;
    /// pending_damage_ 是 Attribute stage 前的规范 damage batch。
    std::vector<DamageRequest> pending_damage_;
    /// seed_ 冻结当前 assignment timeline 的确定性 seed。
    std::uint64_t seed_;
    /// committed_tick_ 强制 Commit 只接受连续 successor Tick。
    std::uint64_t committed_tick_{0};
    /// next_activation_id_ 为每个权威 activation 分配非零 identity。
    std::uint64_t next_activation_id_{1};
    /// next_projectile_id_ 与固定 actor identity range 隔离。
    std::uint64_t next_projectile_id_{3001};
    /// next_event_sequence_ 跨 ability/lifecycle 事件严格单调。
    std::uint64_t next_event_sequence_{1};
    /// encounter_complete_ 是不持久化且不可解释为奖励的 Boss defeat 摘要。
    bool encounter_complete_{false};
};

}  // namespace ihomeland::sim
