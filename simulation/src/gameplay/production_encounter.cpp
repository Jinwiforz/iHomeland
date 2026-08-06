#include "ihomeland/sim/gameplay/production_encounter.hpp"

#include "ihomeland/sim/gameplay/hit_detection.hpp"

#include <algorithm>
#include <cmath>
#include <limits>
#include <stdexcept>
#include <string_view>
#include <tuple>

namespace ihomeland::sim {
namespace {

constexpr std::uint64_t MonsterActorBase = 1001;
constexpr std::uint64_t BossActorId = 2001;

/// ByRole 返回 package 中唯一匹配的 authority object。
[[nodiscard]] const GameplayAuthorityObject& ByRole(
    const GameplayPackageCatalog& catalog,
    const std::string_view role) {
    const auto iterator = std::ranges::find(catalog.objects, role, &GameplayAuthorityObject::role);
    if (iterator == catalog.objects.end()) {
        throw std::runtime_error("production encounter required role is missing");
    }
    return *iterator;
}

/// Numeric 返回 authority object 中唯一匹配的 int64 数值。
[[nodiscard]] std::int64_t Numeric(
    const GameplayAuthorityObject& object,
    const std::string_view field) {
    const auto iterator = std::ranges::find(object.numeric_values, field, &GameplayNumericValue::field);
    if (iterator == object.numeric_values.end()) {
        throw std::runtime_error("production encounter numeric field is missing");
    }
    return iterator->value;
}

/// Mapping 返回 semantic identity 对应的非零 wire numeric identity。
[[nodiscard]] std::uint32_t Mapping(
    const GameplayPackageCatalog& catalog,
    const std::string_view semantic_id) {
    const auto iterator = std::ranges::find(catalog.mappings, semantic_id, &GameplayWireMapping::semantic_id);
    if (iterator == catalog.mappings.end()) {
        throw std::runtime_error("production encounter wire mapping is missing");
    }
    return iterator->numeric_id;
}

/// Spawn 返回 arena 中唯一匹配的确定性出生锚点。
[[nodiscard]] const ArenaSpawnPoint& Spawn(
    const PersonalWorldArenaCatalog& arena,
    const std::string_view id) {
    const auto iterator = std::ranges::find(arena.spawn_points, id, &ArenaSpawnPoint::id);
    if (iterator == arena.spawn_points.end()) {
        throw std::runtime_error("production encounter spawn point is missing");
    }
    return *iterator;
}

/// DistanceSquared 以 checked uint64 返回两个权威坐标的三维距离平方。
[[nodiscard]] std::uint64_t DistanceSquared(
    const Vector3Mm& left,
    const Vector3Mm& right) {
    const auto dx = left.x - right.x;
    const auto dy = left.y - right.y;
    const auto dz = left.z - right.z;
    const auto value = static_cast<long double>(dx) * dx +
                       static_cast<long double>(dy) * dy +
                       static_cast<long double>(dz) * dz;
    if (value > std::numeric_limits<std::uint64_t>::max()) {
        throw std::overflow_error("production encounter distance overflow");
    }
    return static_cast<std::uint64_t>(value);
}

/// Within 判断两个权威坐标的三维距离是否落在闭合范围内。
[[nodiscard]] bool Within(
    const Vector3Mm& left,
    const Vector3Mm& right,
    const std::int64_t range) {
    return DistanceSquared(left, right) <= static_cast<std::uint64_t>(range * range);
}

/// WithinHorizontal 判断两个权威坐标的水平距离是否落在闭合范围内。
[[nodiscard]] bool WithinHorizontal(
    const Vector3Mm& left,
    const Vector3Mm& right,
    const std::int64_t range) {
    const auto dx = left.x - right.x;
    const auto dz = left.z - right.z;
    return static_cast<long double>(dx) * dx + static_cast<long double>(dz) * dz <=
           static_cast<long double>(range) * range;
}

/// StepAxis 以 toward-target 语义推进单轴且不越过 maximum step。
[[nodiscard]] std::int64_t StepAxis(
    const std::int64_t current,
    const std::int64_t target,
    const std::int64_t maximum_step) {
    const auto delta = target - current;
    return current + std::clamp(delta, -maximum_step, maximum_step);
}

}  // namespace

/// Content 保存 instance 启动时从 production package 冻结的 typed authority 数值。
struct ProductionEncounterRuntime::Content final {
    /// player_archetype 是 player wire archetype identity。
    std::uint32_t player_archetype;
    /// monster_archetype 是 ordinary monster wire archetype identity。
    std::uint32_t monster_archetype;
    /// boss_archetype 是 Boss wire archetype identity。
    std::uint32_t boss_archetype;
    /// projectile_archetype 是 Fan projectile wire archetype identity。
    std::uint32_t projectile_archetype;
    /// sword_weapon 是 Sword wire weapon identity。
    std::uint32_t sword_weapon;
    /// fan_weapon 是 Fan wire weapon identity。
    std::uint32_t fan_weapon;
    /// sword_ability 是 Sword primary wire ability identity。
    std::uint32_t sword_ability;
    /// fan_ability 是 Fan primary wire ability identity。
    std::uint32_t fan_ability;
    /// monster_ability 是 ordinary monster wire ability identity。
    std::uint32_t monster_ability;
    /// boss_ability 是 Boss wire ability identity。
    std::uint32_t boss_ability;
    /// player_health 是 player 初始与最大生命 milli 值。
    std::int64_t player_health;
    /// monster_health 是 ordinary monster 初始与最大生命 milli 值。
    std::int64_t monster_health;
    /// boss_health 是 Boss 初始与最大生命 milli 值。
    std::int64_t boss_health;
    /// sword_damage 是 Sword 每次有效命中的正 damage milli 值。
    std::int64_t sword_damage;
    /// fan_damage 是 Fan projectile 每次有效命中的正 damage milli 值。
    std::int64_t fan_damage;
    /// monster_damage 是 ordinary monster 每次攻击的正 damage milli 值。
    std::int64_t monster_damage;
    /// boss_damage 是 Boss 每次攻击的正 damage milli 值。
    std::int64_t boss_damage;
    /// sword_range 是 Sword authority sweep 的最大毫米距离。
    std::int64_t sword_range;
    /// fan_speed_per_tick 是 Fan projectile 每 Tick 最大毫米位移。
    std::int64_t fan_speed_per_tick;
    /// fan_radius 是 Fan projectile authority 命中半径毫米值。
    std::int64_t fan_radius;
    /// fan_lifetime 是 Fan projectile 的最大生存 Tick 数。
    std::uint64_t fan_lifetime;
    /// monster_recovery 是 ordinary monster 攻击后的恢复 Tick 数。
    std::uint64_t monster_recovery;
    /// boss_recovery 是 Boss 攻击后的恢复 Tick 数。
    std::uint64_t boss_recovery;
    /// monster_acquire_range 是 ordinary monster 的目标获取毫米距离。
    std::int64_t monster_acquire_range;
    /// boss_acquire_range 是 Boss 的目标获取毫米距离。
    std::int64_t boss_acquire_range;
    /// monster_attack_range 是 ordinary monster 的攻击毫米距离。
    std::int64_t monster_attack_range;
    /// boss_attack_range 是 Boss 的攻击毫米距离。
    std::int64_t boss_attack_range;
    /// monster_corpse_lifetime 是 ordinary monster corpse 的保留 Tick 数。
    std::uint64_t monster_corpse_lifetime;
    /// boss_corpse_lifetime 是 Boss corpse 的保留 Tick 数。
    std::uint64_t boss_corpse_lifetime;
    /// maximum_projectiles 是 instance 内同时存在的 projectile hard cap。
    std::size_t maximum_projectiles;
    /// player_abilities 保存 Sword/Fan 的冻结 typed AbilitySpec。
    std::vector<AbilitySpec> player_abilities;
};

/// Actor 保存 encounter worker 独占的 current actor mutable state。
struct ProductionEncounterRuntime::Actor final {
    /// id 是 instance 内稳定且唯一的 actor identity。
    std::uint64_t id;
    /// archetype_id 是 current generation 不可变 wire archetype。
    std::uint32_t archetype_id;
    /// generation 是同 actor identity 单调 lifecycle generation。
    std::uint32_t generation;
    /// health 是 current signed milli health。
    std::int64_t health;
    /// maximum_health 是 current generation 不可变 milli health 上限。
    std::int64_t maximum_health;
    /// team 是 friendly-fire policy 使用的闭合 team identity。
    std::int64_t team;
    /// position 是服务器权威整数毫米坐标。
    Vector3Mm position;
    /// yaw_millidegrees 是服务器权威规范朝向。
    std::int32_t yaw_millidegrees;
    /// capsule_radius 是 authority hit/navigation 使用的毫米半径。
    std::int64_t capsule_radius;
    /// ability 是 current weapon grant 与 ability timeline state。
    AbilityState ability;
    /// ai_state 是非 player 的确定性 AI state。
    AiState ai_state;
    /// boss_phase 是 Boss phase timeline；非 Boss 保持初始值。
    BossPhaseState boss_phase;
    /// target_actor_id 是 AI 当前保留的 player target；零表示无目标。
    std::uint64_t target_actor_id;
    /// recovery_until_tick 是 AI 下一次允许攻击的 Tick 边界。
    std::uint64_t recovery_until_tick;
    /// dead_tick 是唯一 Death owner 首次提交死亡的 Tick。
    std::uint64_t dead_tick;
    /// corpse_lifetime 是死亡后延迟 despawn 的 Tick 数。
    std::uint64_t corpse_lifetime;
    /// player 标识预留 BattleSession actor slot。
    bool player;
    /// participating 表示本 Tick barrier 已冻结 active BattleSession generation。
    bool participating;
    /// boss 标识唯一 Boss actor。
    bool boss;
    /// alive 是 current authority 存活事实。
    bool alive;
    /// despawned 标识 corpse lifecycle 已完成且不再投影。
    bool despawned;
};

/// Projectile 保存 encounter worker 独占的 Fan projectile mutable state。
struct ProductionEncounterRuntime::Projectile final {
    /// id 是 instance 内稳定且唯一的 projectile identity。
    std::uint64_t id;
    /// generation 是同 projectile identity 的 lifecycle generation。
    std::uint32_t generation;
    /// source_actor_id 是创建 projectile 的 player actor identity。
    std::uint64_t source_actor_id;
    /// target_actor_id 是创建时服务器选择的 enemy identity。
    std::uint64_t target_actor_id;
    /// activation_id 关联 source ability activation。
    std::uint64_t activation_id;
    /// ability_id 是生成该 projectile 的 Fan ability identity。
    std::uint32_t ability_id;
    /// position 是服务器权威整数毫米坐标。
    Vector3Mm position;
    /// created_tick 是 deferred spawn 实际提交的 Tick。
    std::uint64_t created_tick;
    /// expiry_tick 是 projectile 必须终止的 Tick 边界。
    std::uint64_t expiry_tick;
    /// damage 是首次有效命中提交的正 milli damage 值。
    std::int64_t damage;
    /// alive 标识 projectile 是否仍可移动或命中。
    bool alive;
};

ProductionEncounterRuntime::ProductionEncounterRuntime(
    std::shared_ptr<const GameplayPackageCatalog> catalog,
    const std::size_t player_capacity,
    const std::uint64_t seed,
    std::shared_ptr<PhysicsWorld> physics_world,
    std::shared_ptr<NavigationWorld> navigation_world)
    : catalog_(std::move(catalog)),
      physics_world_(std::move(physics_world)),
      navigation_world_(std::move(navigation_world)),
      content_(std::make_unique<Content>()),
      seed_(seed) {
    if (!catalog_ || !catalog_->arena || !physics_world_ || !navigation_world_ ||
        player_capacity == 0 || player_capacity > 8 || seed_ == 0) {
        throw std::runtime_error("production encounter startup binding is invalid");
    }
    const auto& player = ByRole(*catalog_, "owner-visitor-player");
    const auto& monster = ByRole(*catalog_, "ordinary-monster");
    const auto& boss = ByRole(*catalog_, "boss");
    const auto& sword = ByRole(*catalog_, "sword-primary");
    const auto& fan = ByRole(*catalog_, "fan-primary");
    const auto& projectile = *std::ranges::find(
        catalog_->objects,
        std::string("personal-world-combat/projectile/fan-blade"),
        &GameplayAuthorityObject::id);
    const auto& sword_effect = *std::ranges::find(
        catalog_->objects,
        std::string("personal-world-combat/effect/sword-damage"),
        &GameplayAuthorityObject::id);
    const auto& fan_effect = *std::ranges::find(
        catalog_->objects,
        std::string("personal-world-combat/effect/fan-damage"),
        &GameplayAuthorityObject::id);
    const auto& monster_effect = *std::ranges::find(
        catalog_->objects,
        std::string("personal-world-combat/effect/monster-damage"),
        &GameplayAuthorityObject::id);
    const auto& boss_effect = *std::ranges::find(
        catalog_->objects,
        std::string("personal-world-combat/effect/boss-damage"),
        &GameplayAuthorityObject::id);
    const auto& encounter = ByRole(*catalog_, "personal-world-encounter");
    const auto& monster_ai = *std::ranges::find(
        catalog_->objects,
        std::string("personal-world-combat/ai/ordinary-monster"),
        &GameplayAuthorityObject::id);
    const auto& boss_ai = *std::ranges::find(
        catalog_->objects,
        std::string("personal-world-combat/ai/boss"),
        &GameplayAuthorityObject::id);
    content_->player_archetype = Mapping(*catalog_, player.id);
    content_->monster_archetype = Mapping(*catalog_, monster.id);
    content_->boss_archetype = Mapping(*catalog_, boss.id);
    content_->projectile_archetype = Mapping(*catalog_, projectile.id);
    content_->sword_weapon = Mapping(*catalog_, "personal-world-combat/weapon/sword");
    content_->fan_weapon = Mapping(*catalog_, "personal-world-combat/weapon/fan");
    content_->sword_ability = Mapping(*catalog_, sword.id);
    content_->fan_ability = Mapping(*catalog_, fan.id);
    content_->monster_ability = Mapping(*catalog_, "personal-world-combat/ability/monster-strike");
    content_->boss_ability = Mapping(*catalog_, "personal-world-combat/ability/boss-slam");
    content_->player_health = Numeric(player, "health");
    content_->monster_health = Numeric(monster, "health");
    content_->boss_health = Numeric(boss, "health");
    content_->sword_damage = -Numeric(sword_effect, "magnitude");
    content_->fan_damage = -Numeric(fan_effect, "magnitude");
    content_->monster_damage = -Numeric(monster_effect, "magnitude");
    content_->boss_damage = -Numeric(boss_effect, "magnitude");
    content_->sword_range = Numeric(sword, "melee-range");
    content_->fan_speed_per_tick = Numeric(projectile, "speed") / 20;
    content_->fan_radius = Numeric(projectile, "radius");
    content_->fan_lifetime = static_cast<std::uint64_t>(Numeric(projectile, "lifetime"));
    content_->monster_recovery = static_cast<std::uint64_t>(Numeric(monster_ai, "recovery"));
    content_->boss_recovery = static_cast<std::uint64_t>(Numeric(boss_ai, "recovery"));
    content_->monster_acquire_range = Numeric(monster_ai, "acquire-range");
    content_->boss_acquire_range = Numeric(boss_ai, "acquire-range");
    content_->monster_attack_range = Numeric(monster_ai, "attack-range");
    content_->boss_attack_range = Numeric(boss_ai, "attack-range");
    content_->monster_corpse_lifetime = static_cast<std::uint64_t>(Numeric(monster, "corpse-lifetime"));
    content_->boss_corpse_lifetime = static_cast<std::uint64_t>(Numeric(boss, "corpse-lifetime"));
    content_->maximum_projectiles = static_cast<std::size_t>(Numeric(encounter, "peak-projectiles"));
    content_->player_abilities = {
        {.ability_id = content_->sword_ability,
         .weapon = WeaponKind::Sword,
         .cost_scaled = Numeric(sword, "cost"),
         .windup_ticks = static_cast<std::uint64_t>(Numeric(sword, "windup")),
         .active_ticks = static_cast<std::uint64_t>(Numeric(sword, "active")),
         .recovery_ticks = static_cast<std::uint64_t>(Numeric(sword, "recovery")),
         .cooldown_ticks = static_cast<std::uint64_t>(Numeric(sword, "cooldown")),
         .required_tags = 0,
         .blocked_tags = 1},
        {.ability_id = content_->fan_ability,
         .weapon = WeaponKind::Fan,
         .cost_scaled = Numeric(fan, "cost"),
         .windup_ticks = static_cast<std::uint64_t>(Numeric(fan, "windup")),
         .active_ticks = static_cast<std::uint64_t>(Numeric(fan, "active")),
         .recovery_ticks = static_cast<std::uint64_t>(Numeric(fan, "recovery")),
         .cooldown_ticks = static_cast<std::uint64_t>(Numeric(fan, "cooldown")),
         .required_tags = 0,
         .blocked_tags = 1}};
    ValidateAbilitySpecs(content_->player_abilities);

    actors_.reserve(player_capacity + 4);
    for (std::size_t index = 0; index < player_capacity; ++index) {
        const auto& spawn = Spawn(
            *catalog_->arena,
            index == 0 ? std::string("owner")
                       : "visitor-" + std::to_string(((index - 1) % 3) + 1));
        actors_.push_back({
            .id = index + 1,
            .archetype_id = content_->player_archetype,
            .generation = 1,
            .health = content_->player_health,
            .maximum_health = content_->player_health,
            .team = Numeric(player, "team"),
            .position = spawn.position_mm,
            .yaw_millidegrees = spawn.yaw_millidegrees,
            .capsule_radius = Numeric(player, "capsule-radius"),
            .ability = {
                .weapon = WeaponKind::Sword,
                .granted_ability_id = content_->sword_ability,
                .phase = AbilityPhase::Idle,
                .resource_scaled = content_->player_health,
                .alive = true},
            .ai_state = AiState::Idle,
            .boss_phase = {.current_phase = 0},
            .target_actor_id = 0,
            .corpse_lifetime = 0,
            .player = true,
            .participating = false,
            .boss = false,
            .alive = true,
            .despawned = false});
    }
    for (std::size_t index = 0; index < 3; ++index) {
        const auto& spawn = Spawn(*catalog_->arena, "monster-" + std::to_string(index + 1));
        actors_.push_back({
            .id = MonsterActorBase + index,
            .archetype_id = content_->monster_archetype,
            .generation = 1,
            .health = content_->monster_health,
            .maximum_health = content_->monster_health,
            .team = Numeric(monster, "team"),
            .position = spawn.position_mm,
            .yaw_millidegrees = spawn.yaw_millidegrees,
            .capsule_radius = Numeric(monster, "capsule-radius"),
            .ability = {.weapon = WeaponKind::None, .phase = AbilityPhase::Idle, .alive = true},
            .ai_state = AiState::Idle,
            .boss_phase = {.current_phase = 0},
            .target_actor_id = 0,
            .corpse_lifetime = content_->monster_corpse_lifetime,
            .player = false,
            .participating = false,
            .boss = false,
            .alive = true,
            .despawned = false});
    }
    const auto& boss_spawn = Spawn(*catalog_->arena, "boss");
    actors_.push_back({
        .id = BossActorId,
        .archetype_id = content_->boss_archetype,
        .generation = 1,
        .health = content_->boss_health,
        .maximum_health = content_->boss_health,
        .team = Numeric(boss, "team"),
        .position = boss_spawn.position_mm,
        .yaw_millidegrees = boss_spawn.yaw_millidegrees,
        .capsule_radius = Numeric(boss, "capsule-radius"),
        .ability = {.weapon = WeaponKind::None, .phase = AbilityPhase::Idle, .alive = true},
        .ai_state = AiState::Idle,
        .boss_phase = {.current_phase = 1},
        .target_actor_id = 0,
        .corpse_lifetime = content_->boss_corpse_lifetime,
        .player = false,
        .participating = false,
        .boss = true,
        .alive = true,
        .despawned = false});
    std::ranges::sort(actors_, {}, &Actor::id);
    const auto initial_states = ProjectStates();
    for (const auto& actor : actors_) {
        const auto state = std::ranges::find(
            initial_states, actor.id, &StateProjectionToken::actor_id);
        if (state == initial_states.end()) {
            throw std::runtime_error("production encounter initial projection is missing");
        }
        pending_lifecycle_events_.push_back({
            .event_sequence = next_event_sequence_++,
            .tick = 0,
            .kind = CombatLifecycleKind::Spawn,
            .entity_id = actor.id,
            .archetype_id = actor.archetype_id,
            .entity_generation = actor.generation,
            .initial_state = *state});
    }
}

ProductionEncounterRuntime::~ProductionEncounterRuntime() = default;

ProductionEncounterRuntime::Actor& ProductionEncounterRuntime::FindActor(const std::uint64_t actor_id) {
    const auto iterator = std::ranges::find(actors_, actor_id, &Actor::id);
    if (iterator == actors_.end()) {
        throw std::runtime_error("production encounter actor is missing");
    }
    return *iterator;
}

const ProductionEncounterRuntime::Actor& ProductionEncounterRuntime::FindActor(const std::uint64_t actor_id) const {
    const auto iterator = std::ranges::find(actors_, actor_id, &Actor::id);
    if (iterator == actors_.end()) {
        throw std::runtime_error("production encounter actor is missing");
    }
    return *iterator;
}

void ProductionEncounterRuntime::EmitAbility(
    const std::uint64_t tick,
    const Actor& source,
    const CombatAbilityEventPhase phase,
    std::vector<std::uint64_t> targets) {
    std::sort(targets.begin(), targets.end());
    targets.erase(std::unique(targets.begin(), targets.end()), targets.end());
    pending_ability_events_.push_back({
        .event_sequence = next_event_sequence_++,
        .tick = tick,
        .source_actor_id = source.id,
        .source_generation = source.generation,
        .ability_id = source.ability.active_ability_id,
        .activation_id = source.ability.activation_id,
        .phase = phase,
        .target_actor_ids = std::move(targets)});
}

void ProductionEncounterRuntime::ApplyDamage(
    const std::uint64_t tick,
    const std::uint64_t source_actor_id,
    const std::uint64_t target_actor_id,
    const std::int64_t damage,
    const std::uint64_t activation_id) {
    static_cast<void>(tick);
    const auto& source = FindActor(source_actor_id);
    const auto& target = FindActor(target_actor_id);
    if (!source.alive || !target.alive || source.team == target.team || damage <= 0) {
        return;
    }
    if (target.player && !target.participating) {
        return;
    }
    pending_damage_.push_back({
        .target_actor_id = target_actor_id,
        .source_actor_id = source_actor_id,
        .activation_id = activation_id,
        .stable_sequence = pending_damage_.size() + 1,
        .damage_scaled = damage,
        .immune = false});
}

ProductionEncounterSnapshot ProductionEncounterRuntime::Commit(
    const std::uint64_t tick,
    const std::span<const IngressCommand> commands,
    const std::span<const StateProjectionToken> player_movement_states,
    const std::span<const std::uint64_t> active_player_actor_ids) {
    if (tick == 0 || tick != committed_tick_ + 1 ||
        !std::ranges::is_sorted(active_player_actor_ids) ||
        std::adjacent_find(
            active_player_actor_ids.begin(),
            active_player_actor_ids.end()) !=
            active_player_actor_ids.end() ||
        std::ranges::any_of(
            active_player_actor_ids,
            [&](const std::uint64_t actor_id) {
                const auto actor = std::ranges::find(
                    actors_, actor_id, &Actor::id);
                return actor == actors_.end() || !actor->player;
            })) {
        throw std::runtime_error("production encounter Tick is not a successor");
    }
    pending_ability_events_.clear();
    pending_damage_.clear();
    for (auto& actor : actors_) {
        if (!actor.player) {
            continue;
        }
        const auto participating =
            std::ranges::binary_search(
                active_player_actor_ids,
                actor.id);
        if (actor.participating && !participating) {
            actor.ability.phase = AbilityPhase::Idle;
            actor.ability.active_ability_id = 0;
            actor.ability.activation_id = 0;
        }
        actor.participating = participating;
    }
    for (const auto& state : player_movement_states) {
        const auto iterator = std::ranges::find(actors_, state.actor_id, &Actor::id);
        if (iterator != actors_.end() && iterator->player &&
            iterator->participating && iterator->alive) {
            iterator->position = {.x = state.x_mm, .y = state.y_mm, .z = state.z_mm};
            iterator->yaw_millidegrees = state.yaw_millidegrees;
        }
    }

    for (auto& actor : actors_) {
        if (actor.despawned) {
            continue;
        }
        if (!actor.alive && actor.corpse_lifetime > 0 && tick >= actor.dead_tick + actor.corpse_lifetime) {
            actor.despawned = true;
            pending_lifecycle_events_.push_back({
                .event_sequence = next_event_sequence_++, .tick = tick,
                .kind = CombatLifecycleKind::Despawn, .entity_id = actor.id,
                .archetype_id = actor.archetype_id, .entity_generation = actor.generation});
            continue;
        }
        if (actor.player && actor.participating && actor.alive) {
            const auto previous = actor.ability;
            actor.ability = AdvanceAbilityPhase(actor.ability, tick);
            if (previous.phase == AbilityPhase::Requested && actor.ability.phase == AbilityPhase::Active) {
                std::vector<std::uint64_t> targets;
                if (actor.ability.active_ability_id == content_->sword_ability) {
                    const auto end = Vector3Mm{.x = actor.position.x + content_->sword_range, .y = actor.position.y, .z = actor.position.z};
                    static_cast<void>(physics_world_->Query({
                        .query_id = tick * 1000 + actor.id, .tick = tick,
                        .kind = PhysicsQueryKind::ShapeCast, .actor_id = actor.id,
                        .start_mm = actor.position, .end_mm = end, .maximum_hits = 32}));
                    for (const auto& target : actors_) {
                        if (target.alive && !target.despawned && target.team != actor.team &&
                            Within(actor.position, target.position, content_->sword_range + target.capsule_radius)) {
                            targets.push_back(target.id);
                            ApplyDamage(tick, actor.id, target.id, content_->sword_damage, actor.ability.activation_id);
                        }
                    }
                    EmitAbility(tick, actor, CombatAbilityEventPhase::Committed, targets);
                    EmitAbility(tick, actor, CombatAbilityEventPhase::Completed, targets);
                } else if (actor.ability.active_ability_id == content_->fan_ability) {
                    if (projectiles_.size() >= content_->maximum_projectiles) {
                        throw std::length_error("production encounter projectile capacity exhausted");
                    }
                    const auto target = std::ranges::min_element(actors_, [&](const Actor& left, const Actor& right) {
                        const auto left_distance = left.alive && left.team != actor.team ? DistanceSquared(actor.position, left.position) : std::numeric_limits<std::uint64_t>::max();
                        const auto right_distance = right.alive && right.team != actor.team ? DistanceSquared(actor.position, right.position) : std::numeric_limits<std::uint64_t>::max();
                        return std::tie(left_distance, left.id) < std::tie(right_distance, right.id);
                    });
                    const auto target_id = target != actors_.end() && target->alive && target->team != actor.team ? target->id : 0;
                    projectiles_.push_back({
                        .id = next_projectile_id_++, .generation = 1,
                        .source_actor_id = actor.id, .target_actor_id = target_id,
                        .activation_id = actor.ability.activation_id,
                        .ability_id = actor.ability.active_ability_id,
                        .position = {.x = actor.position.x, .y = actor.position.y + 1000, .z = actor.position.z},
                        .created_tick = tick,
                        .expiry_tick = tick + content_->fan_lifetime,
                        .damage = content_->fan_damage, .alive = true});
                    const auto projected = ProjectStates();
                    const auto projectile_state = std::ranges::find(
                        projected,
                        projectiles_.back().id,
                        &StateProjectionToken::actor_id);
                    if (projectile_state == projected.end()) {
                        throw std::runtime_error("production projectile projection is missing");
                    }
                    pending_lifecycle_events_.push_back({
                        .event_sequence = next_event_sequence_++, .tick = tick,
                        .kind = CombatLifecycleKind::Spawn, .entity_id = projectiles_.back().id,
                        .archetype_id = content_->projectile_archetype, .entity_generation = 1,
                        .initial_state = *projectile_state});
                    EmitAbility(tick, actor, CombatAbilityEventPhase::Committed, {});
                }
            }
        }
        if (actor.boss && actor.alive) {
            actor.boss_phase = CommitBossPhase(actor.boss_phase, tick);
        }
    }

    for (const auto& command : commands) {
        auto& actor = FindActor(command.actor_id);
        if (!actor.player || !actor.participating || !actor.alive) {
            continue;
        }
        if (command.kind == static_cast<std::uint8_t>(GameplayCommandKind::SwitchWeapon)) {
            const auto next = actor.ability.weapon == WeaponKind::Sword ? WeaponKind::Fan : WeaponKind::Sword;
            const auto switched = SwitchWeapon(content_->player_abilities, actor.ability, next, tick);
            if (switched.canceled_activation_id != 0) {
                EmitAbility(tick, actor, CombatAbilityEventPhase::Cancelled, {});
            }
            actor.ability = switched.state;
        } else if (command.kind == static_cast<std::uint8_t>(GameplayCommandKind::ActivateAbility) &&
                   command.canonical_payload == "3|1") {
            const auto spec = std::ranges::find(
                content_->player_abilities, actor.ability.granted_ability_id, &AbilitySpec::ability_id);
            if (spec != content_->player_abilities.end()) {
                const auto requested = RequestAbility(*spec, actor.ability, tick, next_activation_id_++, 0);
                if (requested.accepted) {
                    actor.ability = requested.state;
                    EmitAbility(tick, actor, CombatAbilityEventPhase::Started, {});
                }
            }
        }
    }

    for (auto& actor : actors_) {
        if (actor.player || !actor.alive || actor.despawned) {
            continue;
        }
        std::optional<std::uint64_t> selected;
        if (actor.target_actor_id != 0) {
            const auto retained = std::ranges::find(
                actors_, actor.target_actor_id, &Actor::id);
            if (retained != actors_.end() && retained->player &&
                retained->participating && retained->alive &&
                !retained->despawned) {
                selected = retained->id;
            }
        }
        if (!selected.has_value()) {
            std::vector<AiTargetCandidate> candidates;
            const auto acquire_range = actor.boss
                ? content_->boss_acquire_range
                : content_->monster_acquire_range;
            for (const auto& target : actors_) {
                if (target.player && target.participating &&
                    target.alive && !target.despawned &&
                    Within(actor.position, target.position, acquire_range)) {
                    candidates.push_back({
                        .actor_id = target.id, .threat_scaled = 0,
                        .distance_squared_mm = DistanceSquared(actor.position, target.position),
                        .alive = true});
                }
            }
            selected = SelectAiTarget(candidates);
            actor.target_actor_id = selected.value_or(0);
        }
        const auto attack_range = actor.boss ? content_->boss_attack_range : content_->monster_attack_range;
        const auto in_range = selected && Within(actor.position, FindActor(*selected).position, attack_range);
        const auto intent = DecideAiIntent({
            .current = actor.ai_state, .alive = actor.alive, .has_target = selected.has_value(),
            .target_in_attack_range = in_range, .attack_ready = tick >= actor.recovery_until_tick,
            .recovery_complete = tick >= actor.recovery_until_tick});
        actor.ai_state = intent.next_state;
        if (intent.request_navigation && selected) {
            const auto& target = FindActor(*selected);
            const auto path = navigation_world_->Query({
                .query_id = tick * 10'000 + actor.id, .tick = tick,
                .kind = NavigationQueryKind::FindPath, .actor_id = actor.id,
                .start_mm = {.x = actor.position.x, .y = actor.position.y, .z = actor.position.z},
                .end_mm = {.x = target.position.x, .y = target.position.y, .z = target.position.z},
                .maximum_points = 32});
            if (!path.points_mm.empty()) {
                const auto destination = path.points_mm.back();
                actor.position.x = StepAxis(actor.position.x, destination.x, actor.boss ? 125 : 150);
                actor.position.z = StepAxis(actor.position.z, destination.z, actor.boss ? 125 : 150);
            }
        }
        if (intent.request_attack && selected) {
            actor.ability.active_ability_id = actor.boss ? content_->boss_ability : content_->monster_ability;
            actor.ability.activation_id = next_activation_id_++;
            actor.ability.phase = AbilityPhase::Active;
            EmitAbility(tick, actor, CombatAbilityEventPhase::Started, {});
            ApplyDamage(tick, actor.id, *selected, actor.boss ? content_->boss_damage : content_->monster_damage, actor.ability.activation_id);
            EmitAbility(tick, actor, CombatAbilityEventPhase::Committed, {*selected});
            EmitAbility(tick, actor, CombatAbilityEventPhase::Completed, {*selected});
            actor.recovery_until_tick = tick + (actor.boss ? content_->boss_recovery : content_->monster_recovery);
            actor.ai_state = AiState::Recover;
            actor.ability.phase = AbilityPhase::Idle;
        }
    }

    for (auto& projectile : projectiles_) {
        if (!projectile.alive || tick <= projectile.created_tick) {
            continue;
        }
        auto destroy = tick >= projectile.expiry_tick;
        std::vector<std::uint64_t> targets;
        if (!destroy && projectile.target_actor_id != 0) {
            auto& target = FindActor(projectile.target_actor_id);
            if (!target.alive || target.despawned) {
                destroy = true;
            } else {
                const auto next = Vector3Mm{
                    .x = StepAxis(projectile.position.x, target.position.x, content_->fan_speed_per_tick),
                    .y = projectile.position.y,
                    .z = StepAxis(projectile.position.z, target.position.z, content_->fan_speed_per_tick)};
                const auto world_hits = physics_world_->Query({
                    .query_id = tick * 100'000 + projectile.id, .tick = tick,
                    .kind = PhysicsQueryKind::ProjectileSweep, .actor_id = projectile.source_actor_id,
                    .start_mm = projectile.position, .end_mm = next, .maximum_hits = 32});
                if (!world_hits.hits.empty()) {
                    destroy = true;
                } else {
                    projectile.position = next;
                    if (WithinHorizontal(projectile.position, target.position, content_->fan_radius + target.capsule_radius)) {
                        targets.push_back(target.id);
                        ApplyDamage(tick, projectile.source_actor_id, target.id, projectile.damage, projectile.activation_id);
                        destroy = true;
                    }
                }
            }
        }
        if (destroy) {
            projectile.alive = false;
            const auto& source = FindActor(projectile.source_actor_id);
            auto event_source = source;
            event_source.ability.active_ability_id = projectile.ability_id;
            event_source.ability.activation_id = projectile.activation_id;
            EmitAbility(tick, event_source, CombatAbilityEventPhase::Completed, targets);
            pending_lifecycle_events_.push_back({
                .event_sequence = next_event_sequence_++, .tick = tick,
                .kind = CombatLifecycleKind::Despawn, .entity_id = projectile.id,
                .archetype_id = content_->projectile_archetype, .entity_generation = projectile.generation});
        }
    }

    if (!pending_damage_.empty()) {
        std::vector<ActorVitalState> vitals;
        for (const auto& actor : actors_) {
            if (!actor.despawned) {
                vitals.push_back({.actor_id = actor.id, .health_scaled = actor.health, .alive = actor.alive});
            }
        }
        const auto result = ApplyDamageBatch(vitals, pending_damage_);
        for (const auto& vital : result.actors) {
            auto& actor = FindActor(vital.actor_id);
            actor.health = vital.health_scaled;
            actor.alive = vital.alive;
            actor.ability.alive = vital.alive;
        }
        for (const auto& death : result.deaths) {
            auto& actor = FindActor(death.target_actor_id);
            actor.dead_tick = tick;
            actor.ai_state = AiState::Dead;
            actor.ability.phase = AbilityPhase::Idle;
            if (actor.boss) {
                encounter_complete_ = true;
            }
        }
    }
    auto& boss = FindActor(BossActorId);
    if (boss.alive) {
        if (boss.health * 100 <= boss.maximum_health * 30 && boss.boss_phase.current_phase < 3) {
            boss.boss_phase = ScheduleBossPhase(boss.boss_phase, boss.health, boss.maximum_health, 3, 30, tick);
        } else if (boss.health * 100 <= boss.maximum_health * 65 && boss.boss_phase.current_phase < 2) {
            boss.boss_phase = ScheduleBossPhase(boss.boss_phase, boss.health, boss.maximum_health, 2, 65, tick);
        }
    }
    projectiles_.erase(
        std::remove_if(projectiles_.begin(), projectiles_.end(), [](const Projectile& value) { return !value.alive; }),
        projectiles_.end());
    for (auto& event : pending_lifecycle_events_) {
        if (event.tick == 0) {
            event.tick = tick;
        }
    }
    committed_tick_ = tick;
    auto snapshot = ProductionEncounterSnapshot{
        .server_tick = tick,
        .states = ProjectStates(),
        .ability_events = pending_ability_events_,
        .lifecycle_events = pending_lifecycle_events_,
        .encounter_complete = encounter_complete_};
    pending_lifecycle_events_.clear();
    return snapshot;
}

std::vector<StateProjectionToken> ProductionEncounterRuntime::ProjectStates() const {
    std::vector<StateProjectionToken> states;
    states.reserve(actors_.size() + projectiles_.size());
    for (const auto& actor : actors_) {
        if (actor.despawned) {
            continue;
        }
        states.push_back({
            .actor_id = actor.id,
            .entity_generation = actor.generation,
            .x_mm = actor.position.x, .y_mm = actor.position.y, .z_mm = actor.position.z,
            .yaw_millidegrees = actor.yaw_millidegrees,
            .health_scaled = actor.health,
            .phase = actor.boss ? actor.boss_phase.current_phase : static_cast<std::uint32_t>(actor.ability.phase),
            .alive = actor.alive,
            .grounded = true,
            .archetype_id = actor.archetype_id,
            .equipped_weapon_id = actor.player
                ? (actor.ability.weapon == WeaponKind::Sword ? content_->sword_weapon : content_->fan_weapon)
                : 0,
            .max_health_scaled = static_cast<std::uint32_t>(actor.maximum_health)});
    }
    for (const auto& projectile : projectiles_) {
        if (projectile.alive) {
            states.push_back({
                .actor_id = projectile.id,
                .entity_generation = projectile.generation,
                .x_mm = projectile.position.x, .y_mm = projectile.position.y, .z_mm = projectile.position.z,
                .health_scaled = 1, .phase = 0, .alive = true, .grounded = false,
                .archetype_id = content_->projectile_archetype,
                .equipped_weapon_id = 0, .max_health_scaled = 1});
        }
    }
    std::ranges::sort(states, {}, &StateProjectionToken::actor_id);
    return states;
}

ProductionEncounterSnapshot ProductionEncounterRuntime::InitialSnapshot() const {
    return {
        .server_tick = 0,
        .states = ProjectStates(),
        .ability_events = {},
        .lifecycle_events = pending_lifecycle_events_,
        .encounter_complete = encounter_complete_};
}

}  // namespace ihomeland::sim
