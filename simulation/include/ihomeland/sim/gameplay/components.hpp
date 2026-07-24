#pragma once

#include "ihomeland/sim/ecs/entity.hpp"

#include <cstdint>

namespace ihomeland::sim {

/// TransformComponent 保存 core 的整数位置与朝向，不接受客户端最终 Transform。
struct TransformComponent final {
    /// x_mm 是世界 X 坐标，单位 millimeters。
    std::int64_t x_mm;
    /// y_mm 是世界 Y 坐标，单位 millimeters。
    std::int64_t y_mm;
    /// z_mm 是世界 Z 坐标，单位 millimeters。
    std::int64_t z_mm;
    /// yaw_millidegrees 是规范化朝向，单位 millidegrees。
    std::int32_t yaw_millidegrees;
};

/// MotionComponent 保存由 Movement/Physics stage 唯一修改的整数运动状态。
struct MotionComponent final {
    /// velocity_x_mm_per_second 是 X 轴速度。
    std::int64_t velocity_x_mm_per_second;
    /// velocity_y_mm_per_second 是 Y 轴速度。
    std::int64_t velocity_y_mm_per_second;
    /// velocity_z_mm_per_second 是 Z 轴速度。
    std::int64_t velocity_z_mm_per_second;
    /// grounded 只来自 PhysicsWorld 的规范 ground result。
    bool grounded;
};

/// ActorComponent 绑定稳定 ActorID 与 player/AI 分类，不保存账号或连接身份。
struct ActorComponent final {
    /// actor_id 是 assignment 内规范排序使用的稳定非零 ID。
    std::uint64_t actor_id;
    /// player 表示 actor 是否由已绑定 player command source 驱动。
    bool player;
};

/// AttributeComponent 保存 signed 64-bit scaled gameplay attributes。
struct AttributeComponent final {
    /// health_scaled 是当前生命值，scale 由冻结 content config 定义。
    std::int64_t health_scaled;
    /// maximum_health_scaled 是同一 scale 下的生命上限。
    std::int64_t maximum_health_scaled;
    /// resource_scaled 是 Ability cost 消费的当前资源。
    std::int64_t resource_scaled;
};

/// AbilityComponent 保存单个 actor 的当前武器 grant 与 Tick phase 状态。
struct AbilityComponent final {
    /// granted_ability_id 是当前武器授予的稳定 content ID，零表示无 grant。
    std::uint32_t granted_ability_id;
    /// cooldown_until_tick 是允许再次激活的首个 SimulationTick。
    std::uint64_t cooldown_until_tick;
    /// phase 是冻结 AbilityPhase token 对应的项目内枚举值。
    std::uint8_t phase;
};

/// EffectComponent 保存单个 active effect 的 Tick 生命周期与 stack。
struct EffectComponent final {
    /// effect_id 是冻结 content 中的稳定 effect identity。
    std::uint32_t effect_id;
    /// expires_at_tick 在进入该 Tick 的 Effect stage 时先执行 expiry。
    std::uint64_t expires_at_tick;
    /// stacks 是经过 maximum stack policy 校验的正数。
    std::uint16_t stacks;
};

/// ProjectileComponent 保存 deferred spawn 后从下一 Tick 才能移动的权威投射物状态。
struct ProjectileComponent final {
    /// owner 是创建投射物的 generation-safe actor entity。
    EntityID owner;
    /// activation_id 在 owner 的 Ability stream 内唯一。
    std::uint64_t activation_id;
    /// created_tick 用于禁止创建 Tick 提前移动或命中。
    std::uint64_t created_tick;
};

/// AiComponent 保存普通怪物/Boss 的确定状态与 entity-local PRNG state。
struct AiComponent final {
    /// state 是冻结 AI state token 的项目内枚举值。
    std::uint8_t state;
    /// target_actor_id 是 tie-break 后的稳定目标，零表示无目标。
    std::uint64_t target_actor_id;
    /// random_state 只属于当前 entity/AI stream。
    std::uint64_t random_state;
};

}  // namespace ihomeland::sim
