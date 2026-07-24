#include "ihomeland/sim/fixture/recorded_physics_world.hpp"

#include <algorithm>
#include <iostream>
#include <stdexcept>
#include <vector>

namespace {

/// Require 把 recorded PhysicsWorld 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// Query 构造指定 kind 的稳定项目 query value。
[[nodiscard]] ihomeland::sim::PhysicsQuery Query(
    const std::uint64_t query_id,
    const ihomeland::sim::PhysicsQueryKind kind) {
    return {
        .query_id = query_id,
        .tick = 1,
        .kind = kind,
        .actor_id = 42,
        .start_mm = {.x = 0, .y = 0, .z = 0},
        .end_mm = {.x = 100, .y = 0, .z = 0},
        .maximum_hits = 4};
}

/// Exchanges 覆盖 ground/capsule/shape/ray/overlap/projectile 闭合 query 集。
[[nodiscard]] std::vector<ihomeland::sim::RecordedPhysicsExchange> Exchanges() {
    const std::vector kinds{
        ihomeland::sim::PhysicsQueryKind::GroundProbe,
        ihomeland::sim::PhysicsQueryKind::MoveCapsule,
        ihomeland::sim::PhysicsQueryKind::ShapeCast,
        ihomeland::sim::PhysicsQueryKind::RayCast,
        ihomeland::sim::PhysicsQueryKind::Overlap,
        ihomeland::sim::PhysicsQueryKind::ProjectileSweep};
    std::vector<ihomeland::sim::RecordedPhysicsExchange> exchanges;
    for (std::size_t index = 0; index < kinds.size(); ++index) {
        exchanges.push_back({
            .query = Query(index + 1, kinds[index]),
            .hits = {
                {.fraction_millionths = 500'000, .collider_id = 2, .subshape_id = 1, .target_actor_id = 2, .blocking = true},
                {.fraction_millionths = 500'000, .collider_id = 1, .subshape_id = 1, .target_actor_id = 1, .blocking = true}}});
    }
    return exchanges;
}

/// TestStrictTrace 验证全部 query kind、hit 排序、严格顺序与 reset。
void TestStrictTrace() {
    ihomeland::sim::RecordedPhysicsWorld world(Exchanges(), 6, 4);
    const auto first = world.Query(Query(1, ihomeland::sim::PhysicsQueryKind::GroundProbe));
    Require(
        first.hits[0].collider_id == 1 && world.Consumed() == 1,
        "recorded physics hits were not canonicalized");
    try {
        static_cast<void>(world.Query(Query(3, ihomeland::sim::PhysicsQueryKind::ShapeCast)));
    } catch (const ihomeland::sim::RecordedPhysicsError& error) {
        Require(
            error.Code() == ihomeland::sim::RecordedPhysicsErrorCode::QueryOrder &&
                world.Consumed() == 1,
            "recorded query mismatch consumed trace");
    }
    world.Reset();
    for (const auto& exchange : Exchanges()) {
        static_cast<void>(world.Query(exchange.query));
    }
    Require(world.Consumed() == 6, "recorded physics trace did not consume all query kinds");
    try {
        static_cast<void>(world.Query(Query(7, ihomeland::sim::PhysicsQueryKind::RayCast)));
    } catch (const ihomeland::sim::RecordedPhysicsError& error) {
        Require(
            error.Code() == ihomeland::sim::RecordedPhysicsErrorCode::Exhausted,
            "recorded physics exhaustion returned wrong code");
        return;
    }
    throw std::runtime_error("recorded physics trace expanded after exhaustion");
}

/// TestCapacity 验证 adapter/query 两层 hit hard limit 都 fail closed。
void TestCapacity() {
    auto exchanges = Exchanges();
    exchanges.resize(1);
    exchanges[0].query.maximum_hits = 1;
    ihomeland::sim::RecordedPhysicsWorld world(exchanges, 1, 4);
    try {
        static_cast<void>(world.Query(exchanges[0].query));
    } catch (const ihomeland::sim::RecordedPhysicsError& error) {
        Require(
            error.Code() == ihomeland::sim::RecordedPhysicsErrorCode::Capacity,
            "recorded physics query capacity returned wrong code");
        return;
    }
    throw std::runtime_error("recorded physics query capacity expanded");
}

}  // namespace

/// main 执行 recorded PhysicsWorld 的 contract/negative/determinism 回归。
int main() {
    try {
        TestStrictTrace();
        TestCapacity();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
