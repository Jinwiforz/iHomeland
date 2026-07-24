#include "ihomeland/sim/core/sha256.hpp"
#include "ihomeland/sim/fixture/recorded_navigation_world.hpp"
#include "ihomeland/sim/navigation/detour_navigation_world.hpp"

#include <DetourAlloc.h>
#include <DetourNavMeshBuilder.h>

#include <array>
#include <cstddef>
#include <cstdint>
#include <cstdlib>
#include <cstring>
#include <iostream>
#include <stdexcept>
#include <string_view>
#include <vector>

namespace {

/// Require 把 Detour live/parity 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// BuildTwoPolygonAsset 生成仅供测试的内存 tile；production adapter 不含烘焙入口。
[[nodiscard]] std::vector<std::byte> BuildTwoPolygonAsset() {
    constexpr std::array<unsigned short, 18> vertices{
        0, 0, 0,
        10, 0, 0,
        20, 0, 0,
        0, 0, 10,
        10, 0, 10,
        20, 0, 10};
    constexpr std::array<unsigned short, 16> polygons{
        0, 1, 4, 3, 0x800f, 1, 0x800f, 0x800f,
        1, 2, 5, 4, 0x800f, 0x800f, 0x800f, 0};
    constexpr std::array<unsigned short, 2> flags{1, 1};
    constexpr std::array<unsigned char, 2> areas{0, 0};
    dtNavMeshCreateParams params{};
    params.verts = vertices.data();
    params.vertCount = 6;
    params.polys = polygons.data();
    params.polyFlags = flags.data();
    params.polyAreas = areas.data();
    params.polyCount = 2;
    params.nvp = 4;
    params.bmin[0] = 0.0F;
    params.bmin[1] = 0.0F;
    params.bmin[2] = 0.0F;
    params.bmax[0] = 20.0F;
    params.bmax[1] = 1.0F;
    params.bmax[2] = 10.0F;
    params.walkableHeight = 2.0F;
    params.walkableRadius = 0.5F;
    params.walkableClimb = 0.5F;
    params.cs = 1.0F;
    params.ch = 1.0F;
    params.buildBvTree = true;
    unsigned char* raw = nullptr;
    int size = 0;
    Require(
        dtCreateNavMeshData(&params, &raw, &size) && raw != nullptr && size > 0,
        "Detour test nav tile creation failed");
    std::vector<std::byte> data(static_cast<std::size_t>(size));
    std::memcpy(data.data(), raw, data.size());
    dtFree(raw);
    return data;
}

/// Identity 为给定 bytes 生成登记 asset identity。
[[nodiscard]] ihomeland::sim::NavigationAssetIdentity Identity(
    const std::vector<std::byte>& data) {
    return {
        .map_id = 7,
        .version = 1,
        .sha256 = ihomeland::sim::Sha256Bytes(data),
        .coordinate_scale_mm = 1000};
}

/// Config 返回固定 node/polygon/point/坐标预算。
[[nodiscard]] ihomeland::sim::DetourNavigationConfig Config(
    const ihomeland::sim::NavigationAssetIdentity& identity) {
    return {
        .expected_asset = identity,
        .maximum_nodes = 128,
        .maximum_polygons = 8,
        .maximum_points = 8,
        .nearest_half_extent_mm = 3000,
        .maximum_coordinate_mm = 1'000'000};
}

/// PathQuery 构造穿过两个相邻 polygons 的完整项目 query。
[[nodiscard]] ihomeland::sim::NavigationQuery PathQuery() {
    return {
        .query_id = 1,
        .tick = 1,
        .kind = ihomeland::sim::NavigationQueryKind::FindPath,
        .actor_id = 42,
        .start_mm = {.x = 1000, .y = 0, .z = 5000},
        .end_mm = {.x = 19'000, .y = 0, .z = 5000},
        .maximum_points = 8};
}

/// RequirePointTolerance 验证 live/recorded 每轴量化误差不超过闭合 tolerance。
void RequirePointTolerance(
    const ihomeland::sim::NavigationPointMm& expected,
    const ihomeland::sim::NavigationPointMm& actual,
    const std::int64_t tolerance_mm) {
    const auto within = [tolerance_mm](
                            const std::int64_t left,
                            const std::int64_t right) {
        return std::llabs(left - right) <= tolerance_mm;
    };
    if (!within(expected.x, actual.x) || !within(expected.y, actual.y) ||
        !within(expected.z, actual.z)) {
        throw std::runtime_error("Detour parity exceeded quantization tolerance");
    }
}

/// TestLiveAndRecordedParity 验证 path、equal-candidate nearest 与 canonical identity。
void TestLiveAndRecordedParity() {
    const auto data = BuildTwoPolygonAsset();
    const auto identity = Identity(data);
    ihomeland::sim::DetourNavigationWorld live(Config(identity), data);
    live.LoadAsset(identity);
    const auto query = PathQuery();
    const auto live_path = live.Query(query);
    const std::vector<ihomeland::sim::NavigationPointMm> expected_points{
        query.start_mm, query.end_mm};
    Require(
        live_path.points_mm == expected_points,
        "Detour live straight path drifted");

    ihomeland::sim::RecordedNavigationWorld recorded(
        identity,
        {{.query = query, .points_mm = expected_points}},
        1,
        8);
    recorded.LoadAsset(identity);
    const auto recorded_path = recorded.Query(query);
    Require(
        recorded_path.points_mm == live_path.points_mm &&
            recorded_path.path_identity == live_path.path_identity,
        "recorded/Detour path parity drifted");

    auto nearest = query;
    nearest.query_id = 2;
    nearest.kind = ihomeland::sim::NavigationQueryKind::NearestPoly;
    nearest.start_mm = {.x = 10'000, .y = 0, .z = 5000};
    nearest.end_mm = nearest.start_mm;
    const auto first = live.Query(nearest);
    const auto second = live.Query(nearest);
    Require(
        first.points_mm == second.points_mm &&
            first.path_identity == second.path_identity &&
            first.points_mm == std::vector{nearest.start_mm},
        "Detour equal-candidate nearest result was not canonical");
}

/// TestDriftCapacityAndTolerance 验证 asset/status/budget/tolerance fail closed。
void TestDriftCapacityAndTolerance() {
    const auto data = BuildTwoPolygonAsset();
    const auto identity = Identity(data);
    ihomeland::sim::DetourNavigationWorld world(Config(identity), data);
    auto drifted = identity;
    drifted.map_id = 8;
    try {
        world.LoadAsset(drifted);
        throw std::runtime_error("Detour map identity drift was accepted");
    } catch (const ihomeland::sim::DetourNavigationError& error) {
        Require(
            error.Code() ==
                ihomeland::sim::DetourNavigationErrorCode::AssetDrift,
            "Detour asset drift returned wrong code");
    }
    world.LoadAsset(identity);
    auto too_small = PathQuery();
    too_small.maximum_points = 1;
    try {
        static_cast<void>(world.Query(too_small));
        throw std::runtime_error("Detour point budget overflow was accepted");
    } catch (const ihomeland::sim::DetourNavigationError& error) {
        Require(
            error.Code() ==
                ihomeland::sim::DetourNavigationErrorCode::Capacity,
            "Detour point budget returned wrong code");
    }
    try {
        RequirePointTolerance(
            {.x = 0, .y = 0, .z = 0},
            {.x = 2, .y = 0, .z = 0},
            1);
        throw std::runtime_error("Detour tolerance overflow was accepted");
    } catch (const std::runtime_error& error) {
        Require(
            std::string_view(error.what()) ==
                "Detour parity exceeded quantization tolerance",
            "Detour tolerance failure returned wrong reason");
    }
}

}  // namespace

/// main 执行 Detour live、recorded parity、identity、budget 与 tolerance 回归。
int main() {
    try {
        TestLiveAndRecordedParity();
        TestDriftCapacityAndTolerance();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
