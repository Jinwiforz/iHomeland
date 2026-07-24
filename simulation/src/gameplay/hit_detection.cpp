#include "ihomeland/sim/gameplay/hit_detection.hpp"

#include <algorithm>
#include <tuple>
#include <unordered_set>

namespace ihomeland::sim {

HitDetectionError::HitDetectionError(
    const HitDetectionErrorCode code,
    const char* message)
    : std::runtime_error(message), code_(code) {}

HitDetectionErrorCode HitDetectionError::Code() const noexcept {
    return code_;
}

std::vector<PhysicsHitValue> CanonicalizePhysicsHits(
    const std::span<const PhysicsHitValue> hits) {
    std::vector<PhysicsHitValue> canonical(hits.begin(), hits.end());
    for (const auto& hit : canonical) {
        if (hit.fraction_millionths > 1'000'000 ||
            hit.collider_id == 0 || hit.subshape_id == 0) {
            throw HitDetectionError(
                HitDetectionErrorCode::InvalidHit,
                "physics hit value is outside the canonical contract");
        }
    }
    std::stable_sort(canonical.begin(), canonical.end(), [](const auto& left, const auto& right) {
        return std::tie(
                   left.fraction_millionths,
                   left.collider_id,
                   left.subshape_id,
                   left.target_actor_id,
                   left.blocking) <
               std::tie(
                   right.fraction_millionths,
                   right.collider_id,
                   right.subshape_id,
                   right.target_actor_id,
                   right.blocking);
    });
    return canonical;
}

std::vector<AuthoritativeHit> ResolveSweepHits(
    const std::uint64_t activation_id,
    const std::uint64_t source_actor_id,
    const std::span<const PhysicsHitValue> hits,
    const std::size_t maximum_targets) {
    if (activation_id == 0 || source_actor_id == 0 || maximum_targets == 0) {
        throw std::invalid_argument("sweep identity and capacity must be non-zero");
    }
    const auto canonical = CanonicalizePhysicsHits(hits);
    std::unordered_set<std::uint64_t> seen_targets;
    std::vector<AuthoritativeHit> resolved;
    resolved.reserve(std::min(maximum_targets, canonical.size()));
    for (const auto& hit : canonical) {
        if (hit.target_actor_id == 0 ||
            !seen_targets.insert(hit.target_actor_id).second) {
            continue;
        }
        if (resolved.size() == maximum_targets) {
            throw HitDetectionError(
                HitDetectionErrorCode::Capacity,
                "activation target capacity exceeded");
        }
        resolved.push_back({
            .activation_id = activation_id,
            .source_actor_id = source_actor_id,
            .target_actor_id = hit.target_actor_id,
            .physics = hit});
    }
    return resolved;
}

ProjectileStepResult StepProjectile(
    const ProjectileState& projectile,
    const std::uint64_t tick,
    const std::span<const PhysicsHitValue> hits) {
    if (projectile.projectile_id == 0 || projectile.source_actor_id == 0 ||
        projectile.activation_id == 0 || projectile.created_tick == 0 ||
        projectile.expiry_tick <= projectile.created_tick || tick == 0) {
        throw std::invalid_argument("projectile lifecycle value is invalid");
    }
    if (!projectile.alive) {
        return {
            .state = projectile,
            .queried = false,
            .hit = std::nullopt,
            .destroy_reason = ProjectileDestroyReason::None};
    }
    auto next = projectile;
    if (tick >= projectile.expiry_tick) {
        next.alive = false;
        return {
            .state = next,
            .queried = false,
            .hit = std::nullopt,
            .destroy_reason = ProjectileDestroyReason::Expiry};
    }
    if (tick <= projectile.created_tick) {
        return {
            .state = projectile,
            .queried = false,
            .hit = std::nullopt,
            .destroy_reason = ProjectileDestroyReason::None};
    }

    const auto canonical = CanonicalizePhysicsHits(hits);
    const auto blocking = std::find_if(canonical.begin(), canonical.end(), [](const auto& hit) {
        return hit.blocking;
    });
    if (blocking == canonical.end()) {
        return {
            .state = projectile,
            .queried = true,
            .hit = std::nullopt,
            .destroy_reason = ProjectileDestroyReason::None};
    }
    next.alive = false;
    std::optional<AuthoritativeHit> hit;
    if (blocking->target_actor_id != 0) {
        hit = AuthoritativeHit{
            .activation_id = projectile.activation_id,
            .source_actor_id = projectile.source_actor_id,
            .target_actor_id = blocking->target_actor_id,
            .physics = *blocking};
    }
    return {
        .state = next,
        .queried = true,
        .hit = hit,
        .destroy_reason = ProjectileDestroyReason::FirstBlockingHit};
}

GameplayCueProjection ProjectGameplayCue(const AuthoritativeHit& hit) noexcept {
    return {
        .activation_id = hit.activation_id,
        .source_actor_id = hit.source_actor_id,
        .target_actor_id = hit.target_actor_id};
}

}  // namespace ihomeland::sim
