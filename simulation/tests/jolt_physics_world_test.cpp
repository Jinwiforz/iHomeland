#include "ihomeland/sim/fixture/recorded_physics_world.hpp"
#include "ihomeland/sim/gameplay/hit_detection.hpp"
#include "ihomeland/sim/physics/jolt_physics_world.hpp"

#include <algorithm>
#include <array>
#include <cstdlib>
#include <iostream>
#include <stdexcept>
#include <string_view>
#include <vector>

namespace {

/// Require 把 live Jolt PhysicsWorld 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// Config 返回 B0.3 固定单位、shape、solver、allocator 与容量。
[[nodiscard]] ihomeland::sim::JoltPhysicsConfig Config() {
    return {
        .millimeters_per_jolt_unit = 1000,
        .fraction_quantization = 1'000'000,
        .query_capsule_radius_mm = 250,
        .query_capsule_half_height_mm = 500,
        .solver_velocity_steps = 10,
        .solver_position_steps = 2,
        .temporary_allocator_bytes = 2 * 1024 * 1024,
        .maximum_bodies = 16,
        .maximum_body_pairs = 16,
        .maximum_contact_constraints = 16,
        .maximum_hits_per_query = 8};
}

/// Colliders 返回两个重叠 boxes，用于 equal-fraction ColliderID tie-break。
[[nodiscard]] std::vector<ihomeland::sim::JoltBoxCollider> Colliders() {
    return {
        {
            .collider_id = 2,
            .subshape_id = 1,
            .target_actor_id = 99,
            .center_mm = {.x = 5000, .y = 0, .z = 0},
            .half_extent_mm = {.x = 500, .y = 500, .z = 500},
            .blocking = true},
        {
            .collider_id = 1,
            .subshape_id = 1,
            .target_actor_id = 98,
            .center_mm = {.x = 5000, .y = 0, .z = 0},
            .half_extent_mm = {.x = 500, .y = 500, .z = 500},
            .blocking = true}};
}

/// Query 构造指定 kind 的 live integer query。
[[nodiscard]] ihomeland::sim::PhysicsQuery Query(
    const std::uint64_t id,
    const ihomeland::sim::PhysicsQueryKind kind) {
    return {
        .query_id = id,
        .tick = 1,
        .kind = kind,
        .actor_id = 42,
        .start_mm = {.x = 0, .y = 0, .z = 0},
        .end_mm = {.x = 10'000, .y = 0, .z = 0},
        .maximum_hits = 8};
}

/// TestLiveQueries 验证 ray/capsule/projectile 与 equal-fraction stable sorting。
void TestLiveQueries() {
    ihomeland::sim::JoltPhysicsWorld world(Config(), Colliders());
    const auto ray = world.Query(Query(1, ihomeland::sim::PhysicsQueryKind::RayCast));
    Require(
        ray.hits.size() == 2 && ray.hits[0].collider_id == 1 &&
            ray.hits[1].collider_id == 2 &&
            ray.hits[0].fraction_millionths == ray.hits[1].fraction_millionths,
        "Jolt equal-fraction hit sorting drifted");
    const auto capsule =
        world.Query(Query(2, ihomeland::sim::PhysicsQueryKind::MoveCapsule));
    const auto projectile =
        world.Query(Query(3, ihomeland::sim::PhysicsQueryKind::ProjectileSweep));
    Require(
        capsule.hits.size() == 2 && projectile.hits.size() == 2,
        "Jolt capsule/projectile query did not hit fixed scene");
}

/// TestOverlapAndConfigFailure 验证 overlap project values 与配置漂移 fail closed。
void TestOverlapAndConfigFailure() {
    ihomeland::sim::JoltPhysicsWorld world(Config(), Colliders());
    auto overlap = Query(1, ihomeland::sim::PhysicsQueryKind::Overlap);
    overlap.start_mm = {.x = 5000, .y = 0, .z = 0};
    overlap.end_mm = overlap.start_mm;
    const auto result = world.Query(overlap);
    Require(
        result.hits.size() == 2 && result.hits[0].fraction_millionths == 0,
        "Jolt overlap query was not normalized");
    auto invalid = Config();
    invalid.millimeters_per_jolt_unit = 1;
    try {
        static_cast<void>(ihomeland::sim::JoltPhysicsWorld(invalid, Colliders()));
    } catch (const ihomeland::sim::JoltPhysicsError& error) {
        Require(
            error.Code() == ihomeland::sim::JoltPhysicsErrorCode::InvalidConfig,
            "Jolt config drift returned wrong code");
        return;
    }
    throw std::runtime_error("Jolt coordinate unit drift was accepted");
}

/// RequireParity 验证 metadata 完全一致且 fraction 不超过登记量化容差。
void RequireParity(
    const ihomeland::sim::PhysicsHitValue& expected,
    const ihomeland::sim::PhysicsHitValue& actual,
    const std::int64_t tolerance_millionths) {
    const auto fraction_delta =
        static_cast<std::int64_t>(expected.fraction_millionths) -
        static_cast<std::int64_t>(actual.fraction_millionths);
    if (std::llabs(fraction_delta) > tolerance_millionths ||
        expected.collider_id != actual.collider_id ||
        expected.subshape_id != actual.subshape_id ||
        expected.target_actor_id != actual.target_actor_id ||
        expected.blocking != actual.blocking) {
        throw std::runtime_error("Jolt parity exceeded quantization tolerance");
    }
}

/// TestRecordedParityAndCallbackOrder 验证 live/recorded parity、callback reorder 与 tolerance fail closed。
void TestRecordedParityAndCallbackOrder() {
    ihomeland::sim::JoltPhysicsWorld live(Config(), Colliders());
    const auto query = Query(4, ihomeland::sim::PhysicsQueryKind::RayCast);
    const std::vector<ihomeland::sim::PhysicsHitValue> expected{
        {.fraction_millionths = 450'000, .collider_id = 1, .subshape_id = 1, .target_actor_id = 98, .blocking = true},
        {.fraction_millionths = 450'000, .collider_id = 2, .subshape_id = 1, .target_actor_id = 99, .blocking = true}};
    ihomeland::sim::RecordedPhysicsWorld recorded(
        {{
            .query = query,
            .hits = {expected[1], expected[0]},
        }},
        1,
        8);
    const auto live_result = live.Query(query);
    const auto recorded_result = recorded.Query(query);
    Require(
        live_result.hits.size() == expected.size() &&
            recorded_result.hits.size() == expected.size(),
        "Jolt/recorded parity hit count drifted");
    for (std::size_t index = 0; index < expected.size(); ++index) {
        RequireParity(recorded_result.hits[index], live_result.hits[index], 1);
    }

    const std::array<ihomeland::sim::PhysicsHitValue, 2> reordered{{
        ihomeland::sim::PhysicsHitValue{
            .fraction_millionths = 1,
            .collider_id = 1,
            .subshape_id = 2,
            .target_actor_id = 1,
            .blocking = true},
        ihomeland::sim::PhysicsHitValue{
            .fraction_millionths = 1,
            .collider_id = 1,
            .subshape_id = 1,
            .target_actor_id = 1,
            .blocking = true},
    }};
    const auto canonical =
        ihomeland::sim::CanonicalizePhysicsHits(reordered);
    Require(
        canonical[0].subshape_id == 1 && canonical[1].subshape_id == 2,
        "equal fraction/ColliderID SubshapeID tie-break drifted");

    auto outside_tolerance = expected[0];
    outside_tolerance.fraction_millionths += 2;
    try {
        RequireParity(outside_tolerance, live_result.hits[0], 1);
        throw std::runtime_error("Jolt tolerance overflow was accepted");
    } catch (const std::runtime_error& error) {
        Require(
            std::string_view(error.what()) ==
                "Jolt parity exceeded quantization tolerance",
            "Jolt tolerance failure returned wrong reason");
    }
}

}  // namespace

/// main 执行 Jolt live query/config/quantization/sorting 回归。
int main() {
    try {
        TestLiveQueries();
        TestOverlapAndConfigFailure();
        TestRecordedParityAndCallbackOrder();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
