#include "ihomeland/sim/gameplay/hit_detection.hpp"

#include <algorithm>
#include <iostream>
#include <stdexcept>
#include <vector>

namespace {

/// Require 把 HitDetection 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// TestSweepDedup 验证 callback reorder 与同 activation-target 多 subshape 去重。
void TestSweepDedup() {
    const std::vector<ihomeland::sim::PhysicsHitValue> ordered{
        {
            .fraction_millionths = 500'000,
            .collider_id = 10,
            .subshape_id = 1,
            .target_actor_id = 99,
            .blocking = true},
        {
            .fraction_millionths = 500'000,
            .collider_id = 10,
            .subshape_id = 2,
            .target_actor_id = 99,
            .blocking = true}};
    auto reversed = ordered;
    std::reverse(reversed.begin(), reversed.end());
    const auto first = ihomeland::sim::ResolveSweepHits(7, 42, ordered, 2);
    const auto second = ihomeland::sim::ResolveSweepHits(7, 42, reversed, 2);
    Require(
        first.size() == 1 && second.size() == 1 &&
            first[0].physics.subshape_id == 1 &&
            second[0].physics.subshape_id == 1,
        "activation-target dedup depends on callback order");
    const auto cue = ihomeland::sim::ProjectGameplayCue(first[0]);
    Require(
        cue.activation_id == 7 && cue.target_actor_id == 99,
        "authoritative cue projection drifted");
}

/// TestDeferredProjectile 验证创建 Tick 不 query、下一 Tick 首阻挡命中并终止。
void TestDeferredProjectile() {
    const ihomeland::sim::ProjectileState projectile{
        .projectile_id = 1,
        .source_actor_id = 42,
        .activation_id = 7,
        .created_tick = 1,
        .expiry_tick = 10,
        .alive = true};
    const std::vector<ihomeland::sim::PhysicsHitValue> callbacks{
        {
            .fraction_millionths = 900'000,
            .collider_id = 20,
            .subshape_id = 1,
            .target_actor_id = 99,
            .blocking = true},
        {
            .fraction_millionths = 100'000,
            .collider_id = 5,
            .subshape_id = 1,
            .target_actor_id = 0,
            .blocking = false}};
    const auto created = ihomeland::sim::StepProjectile(projectile, 1, callbacks);
    Require(
        created.state.alive && !created.queried && !created.hit,
        "projectile queried or hit on creation Tick");
    const auto next = ihomeland::sim::StepProjectile(projectile, 2, callbacks);
    Require(
        !next.state.alive && next.queried && next.hit &&
            next.hit->target_actor_id == 99 &&
            next.destroy_reason ==
                ihomeland::sim::ProjectileDestroyReason::FirstBlockingHit,
        "projectile did not terminate on first canonical blocking hit");
}

/// TestExpiryAndCapacity 验证 expiry 优先于 query 且 sweep target cap fail closed。
void TestExpiryAndCapacity() {
    const ihomeland::sim::ProjectileState projectile{
        .projectile_id = 1,
        .source_actor_id = 42,
        .activation_id = 7,
        .created_tick = 1,
        .expiry_tick = 2,
        .alive = true};
    const auto expired = ihomeland::sim::StepProjectile(projectile, 2, {});
    Require(
        !expired.state.alive && !expired.queried &&
            expired.destroy_reason == ihomeland::sim::ProjectileDestroyReason::Expiry,
        "projectile expiry did not precede query");
    const std::vector<ihomeland::sim::PhysicsHitValue> hits{
        {.fraction_millionths = 1, .collider_id = 1, .subshape_id = 1, .target_actor_id = 1, .blocking = true},
        {.fraction_millionths = 2, .collider_id = 2, .subshape_id = 1, .target_actor_id = 2, .blocking = true}};
    try {
        static_cast<void>(ihomeland::sim::ResolveSweepHits(1, 42, hits, 1));
    } catch (const ihomeland::sim::HitDetectionError& error) {
        Require(
            error.Code() == ihomeland::sim::HitDetectionErrorCode::Capacity,
            "hit capacity returned wrong error");
        return;
    }
    throw std::runtime_error("hit target capacity expanded");
}

}  // namespace

/// main 执行 sweep、projectile、cue 与容量的确定性回归。
int main() {
    try {
        TestSweepDedup();
        TestDeferredProjectile();
        TestExpiryAndCapacity();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
