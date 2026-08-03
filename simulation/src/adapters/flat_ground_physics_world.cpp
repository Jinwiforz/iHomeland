#include "ihomeland/sim/physics/flat_ground_physics_world.hpp"

#include <algorithm>
#include <cstdint>
#include <limits>

namespace ihomeland::sim {
namespace {

/// FractionScale 是 PhysicsHitValue 使用的固定百万分比单位。
constexpr std::int64_t FractionScale = 1'000'000;

/// CoordinateValid 拒绝会扩大过渡 adapter 数值域的坐标。
[[nodiscard]] bool CoordinateValid(
    const std::int64_t value) noexcept {
    return value >=
               -FlatGroundPhysicsWorld::
                   MaximumCoordinateMagnitudeMillimeters &&
           value <=
               FlatGroundPhysicsWorld::
                   MaximumCoordinateMagnitudeMillimeters;
}

/// QueryValid 验证 identity、actor、capacity 与全部坐标。
[[nodiscard]] bool QueryValid(
    const PhysicsQuery& query) noexcept {
    return query.query_id != 0 &&
           query.tick != 0 &&
           query.actor_id != 0 &&
           query.maximum_hits >= 1 &&
           query.maximum_hits <=
               FlatGroundPhysicsWorld::
                   MaximumHitsPerQuery &&
           CoordinateValid(query.start_mm.x) &&
           CoordinateValid(query.start_mm.y) &&
           CoordinateValid(query.start_mm.z) &&
           CoordinateValid(query.end_mm.x) &&
           CoordinateValid(query.end_mm.y) &&
           CoordinateValid(query.end_mm.z);
}

/// GroundFraction 量化 start-to-end 首次经过 Y=0 的 fraction。
[[nodiscard]] std::uint32_t GroundFraction(
    const std::int64_t start_y,
    const std::int64_t end_y) {
    if (start_y < 0 ||
        end_y > 0 ||
        end_y > start_y) {
        throw FlatGroundPhysicsError(
            FlatGroundPhysicsErrorCode::InvalidQuery,
            "flat ground crossing is invalid");
    }
    if (start_y == end_y) {
        return 0;
    }
    const auto distance = start_y - end_y;
    const auto scaled =
        start_y * FractionScale / distance;
    return static_cast<std::uint32_t>(
        std::clamp(
            scaled,
            std::int64_t{0},
            FractionScale));
}

}  // namespace

FlatGroundPhysicsError::FlatGroundPhysicsError(
    const FlatGroundPhysicsErrorCode code,
    const char* message)
    : std::runtime_error(message), code_(code) {}

FlatGroundPhysicsErrorCode
FlatGroundPhysicsError::Code() const noexcept {
    return code_;
}

PhysicsQueryResult FlatGroundPhysicsWorld::Query(
    const PhysicsQuery& query) {
    if (!QueryValid(query)) {
        throw FlatGroundPhysicsError(
            FlatGroundPhysicsErrorCode::InvalidQuery,
            "flat ground physics query is invalid");
    }
    switch (query.kind) {
        case PhysicsQueryKind::GroundProbe:
            if (query.start_mm.x != query.end_mm.x ||
                query.start_mm.z != query.end_mm.z ||
                query.end_mm.y > query.start_mm.y) {
                throw FlatGroundPhysicsError(
                    FlatGroundPhysicsErrorCode::
                        InvalidQuery,
                    "flat ground probe must be vertical and downward");
            }
            break;
        case PhysicsQueryKind::MoveCapsule:
            if (query.start_mm.y < GroundLevelMillimeters) {
                throw FlatGroundPhysicsError(
                    FlatGroundPhysicsErrorCode::
                        InvalidQuery,
                    "flat ground capsule starts below ground");
            }
            break;
        default:
            throw FlatGroundPhysicsError(
                FlatGroundPhysicsErrorCode::
                    UnsupportedQuery,
                "flat ground query kind is unsupported");
    }

    PhysicsQueryResult result{
        .query_id = query.query_id,
        .hits = {}};
    if (query.start_mm.y <
            GroundLevelMillimeters ||
        query.end_mm.y >
            GroundLevelMillimeters ||
        (query.kind ==
             PhysicsQueryKind::MoveCapsule &&
         query.end_mm.y >=
             GroundLevelMillimeters)) {
        return result;
    }
    result.hits.reserve(1);
    result.hits.push_back(
        PhysicsHitValue{
            .fraction_millionths =
                GroundFraction(
                    query.start_mm.y,
                    query.end_mm.y),
            .collider_id = GroundColliderID,
            .subshape_id = GroundSubshapeID,
            .target_actor_id = 0,
            .blocking = true});
    return result;
}

}  // namespace ihomeland::sim
