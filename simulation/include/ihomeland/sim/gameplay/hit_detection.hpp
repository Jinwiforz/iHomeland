#pragma once

#include "ihomeland/sim/physics/physics_world.hpp"

#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <stdexcept>
#include <vector>

namespace ihomeland::sim {

/// HitDetectionErrorCode 是 query value validation 与容量失败的稳定分类。
enum class HitDetectionErrorCode : std::uint8_t {
    /// InvalidHit 表示 adapter value 超出规范范围或缺少稳定 identity。
    InvalidHit,
    /// Capacity 表示 activation-target 结果超过实例硬上限。
    Capacity,
};

/// HitDetectionError 使 adapter/hit 失败可由 harness 稳定分类。
class HitDetectionError final : public std::runtime_error {
public:
    /// 构造函数绑定稳定错误码与低敏诊断文本。
    HitDetectionError(HitDetectionErrorCode code, const char* message);

    /// Code 返回不依赖文本的稳定错误码。
    [[nodiscard]] HitDetectionErrorCode Code() const noexcept;

private:
    /// code_ 是当前 hit detection 失败的稳定分类。
    HitDetectionErrorCode code_;
};

/// AuthoritativeHit 是 HitDetection 交给 Effect/Attribute 和 cue projection 的唯一事实。
struct AuthoritativeHit final {
    /// activation_id 绑定产生命中的非零 Ability activation。
    std::uint64_t activation_id;
    /// source_actor_id 是 authority pipeline 中的攻击方。
    std::uint64_t source_actor_id;
    /// target_actor_id 是去重后的非零目标。
    std::uint64_t target_actor_id;
    /// physics 是选中的规范 query hit。
    PhysicsHitValue physics;
};

/// GameplayCueProjection 只从 AuthoritativeHit 派生，不具备反向提交伤害的接口。
struct GameplayCueProjection final {
    /// activation_id 用于客户端表现相关性。
    std::uint64_t activation_id;
    /// source_actor_id 是 cue 表现来源。
    std::uint64_t source_actor_id;
    /// target_actor_id 是 cue 表现目标。
    std::uint64_t target_actor_id;
};

/// ProjectileDestroyReason 是 deferred projectile lifecycle 的稳定结束原因。
enum class ProjectileDestroyReason : std::uint8_t {
    /// None 表示 projectile 继续存活。
    None,
    /// Expiry 表示当前 Tick 已到达 expiry boundary。
    Expiry,
    /// FirstBlockingHit 表示规范排序后的首个 blocking hit 终止 projectile。
    FirstBlockingHit,
};

/// ProjectileState 是 deferred spawn 后的最小权威 lifecycle value。
struct ProjectileState final {
    /// projectile_id 是实例内 generation-safe entity 的稳定投影。
    std::uint64_t projectile_id;
    /// source_actor_id 是创建 projectile 的 actor。
    std::uint64_t source_actor_id;
    /// activation_id 是创建 projectile 的 Ability activation。
    std::uint64_t activation_id;
    /// created_tick 是 deferred barrier 实际提交 spawn 的 Tick。
    std::uint64_t created_tick;
    /// expiry_tick 在 Tick 开始时到期并优先于 query。
    std::uint64_t expiry_tick;
    /// alive 表示 projectile 尚未被 deferred destroy。
    bool alive;
};

/// ProjectileStepResult 描述一个 projectile 在 HitDetection stage 的完整裁决。
struct ProjectileStepResult final {
    /// state 是当前 Tick 裁决后的 lifecycle state。
    ProjectileState state;
    /// queried 表示当前 Tick 合法消费了 projectile sweep。
    bool queried;
    /// hit 只在 first blocking hit 指向 actor 时存在。
    std::optional<AuthoritativeHit> hit;
    /// destroy_reason 表示调用方应写入 deferred destroy 的原因。
    ProjectileDestroyReason destroy_reason;
};

/// CanonicalizePhysicsHits 验证并按 fraction/ColliderID/SubshapeID/ActorID 排序。
[[nodiscard]] std::vector<PhysicsHitValue> CanonicalizePhysicsHits(
    std::span<const PhysicsHitValue> hits);

/// ResolveSweepHits 按 activation-target 去重并拒绝超过 hard target capacity。
[[nodiscard]] std::vector<AuthoritativeHit> ResolveSweepHits(
    std::uint64_t activation_id,
    std::uint64_t source_actor_id,
    std::span<const PhysicsHitValue> hits,
    std::size_t maximum_targets);

/// StepProjectile 禁止创建 Tick query，并在 expiry 或首个 blocking hit 后终止。
[[nodiscard]] ProjectileStepResult StepProjectile(
    const ProjectileState& projectile,
    std::uint64_t tick,
    std::span<const PhysicsHitValue> hits);

/// ProjectGameplayCue 只读地从已存在 AuthoritativeHit 产生表现投影。
[[nodiscard]] GameplayCueProjection ProjectGameplayCue(
    const AuthoritativeHit& hit) noexcept;

}  // namespace ihomeland::sim
