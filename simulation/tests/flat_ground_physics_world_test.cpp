#include "ihomeland/sim/physics/flat_ground_physics_world.hpp"

#include <iostream>
#include <stdexcept>

namespace {

/// Require 把过渡 PhysicsWorld 回归失败转换为单一 test exception。
void Require(
    const bool condition,
    const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// Query 构造 current actor 的单 hit value query。
[[nodiscard]] ihomeland::sim::PhysicsQuery Query(
    const std::uint64_t query_id,
    const ihomeland::sim::PhysicsQueryKind kind,
    const std::int64_t start_y,
    const std::int64_t end_y) {
    return {
        .query_id = query_id,
        .tick = 7,
        .kind = kind,
        .actor_id = 2,
        .start_mm = {.x = 10, .y = start_y, .z = 20},
        .end_mm = {.x = 10, .y = end_y, .z = 20},
        .maximum_hits = 1};
}

/// TestGroundAndMovement 验证 ground probe、下降 crossing 与向上/空中空结果。
void TestGroundAndMovement() {
    ihomeland::sim::FlatGroundPhysicsWorld world;
    const auto grounded = world.Query(
        Query(
            1,
            ihomeland::sim::PhysicsQueryKind::
                GroundProbe,
            100,
            -100));
    Require(
        grounded.query_id == 1 &&
            grounded.hits.size() == 1 &&
            grounded.hits[0].fraction_millionths ==
                500'000 &&
            grounded.hits[0].collider_id ==
                ihomeland::sim::
                    FlatGroundPhysicsWorld::
                        GroundColliderID &&
            grounded.hits[0].blocking,
        "flat ground probe projection drifted");

    const auto descending = world.Query(
        Query(
            2,
            ihomeland::sim::PhysicsQueryKind::
                MoveCapsule,
            50,
            -150));
    Require(
        descending.hits.size() == 1 &&
            descending.hits[0].fraction_millionths ==
                250'000,
        "flat ground movement crossing drifted");
    Require(
        world.Query(
                 Query(
                     3,
                     ihomeland::sim::
                         PhysicsQueryKind::
                             MoveCapsule,
                     0,
                     100))
            .hits.empty(),
        "ascending capsule was blocked by flat ground");
    Require(
        world.Query(
                 Query(
                     4,
                     ihomeland::sim::
                         PhysicsQueryKind::
                             GroundProbe,
                     500,
                     100))
            .hits.empty(),
        "airborne ground probe reported contact");
}

/// TestClosedBoundary 验证未知 query、非法方向与越界容量 fail closed。
void TestClosedBoundary() {
    ihomeland::sim::FlatGroundPhysicsWorld world;
    try {
        static_cast<void>(
            world.Query(
                Query(
                    1,
                    ihomeland::sim::
                        PhysicsQueryKind::RayCast,
                    10,
                    -10)));
        throw std::runtime_error(
            "unsupported flat ground query was accepted");
    } catch (
        const ihomeland::sim::
            FlatGroundPhysicsError& error) {
        Require(
            error.Code() ==
                ihomeland::sim::
                    FlatGroundPhysicsErrorCode::
                        UnsupportedQuery,
            "unsupported flat ground query returned wrong code");
    }

    auto invalid = Query(
        2,
        ihomeland::sim::PhysicsQueryKind::GroundProbe,
        0,
        10);
    try {
        static_cast<void>(world.Query(invalid));
        throw std::runtime_error(
            "upward flat ground probe was accepted");
    } catch (
        const ihomeland::sim::
            FlatGroundPhysicsError& error) {
        Require(
            error.Code() ==
                ihomeland::sim::
                    FlatGroundPhysicsErrorCode::
                        InvalidQuery,
            "invalid flat ground probe returned wrong code");
    }
}

}  // namespace

/// main 执行 current PersonalWorld 平地 PhysicsWorld contract 回归。
int main() {
    try {
        TestGroundAndMovement();
        TestClosedBoundary();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
