#include "ihomeland/sim/fixture/recorded_navigation_world.hpp"

#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

/// Require 把 recorded NavigationWorld 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// Asset 返回固定 map/version/digest/scale identity。
[[nodiscard]] ihomeland::sim::NavigationAssetIdentity Asset() {
    return {
        .map_id = 7,
        .version = 1,
        .sha256 = std::string(64, 'a'),
        .coordinate_scale_mm = 1000};
}

/// Query 构造 nearest/path 两类稳定项目 query。
[[nodiscard]] ihomeland::sim::NavigationQuery Query(
    const std::uint64_t id,
    const ihomeland::sim::NavigationQueryKind kind,
    const std::size_t maximum_points = 4) {
    return {
        .query_id = id,
        .tick = 12,
        .kind = kind,
        .actor_id = 42,
        .start_mm = {.x = 10'000, .y = 0, .z = 0},
        .end_mm = {.x = 6000, .y = 0, .z = -1000},
        .maximum_points = maximum_points};
}

/// Exchanges 返回 nearest-poly 与有序 path 的完整 trace。
[[nodiscard]] std::vector<ihomeland::sim::RecordedNavigationExchange> Exchanges() {
    return {
        {
            .query = Query(1, ihomeland::sim::NavigationQueryKind::NearestPoly),
            .points_mm = {{.x = 10'000, .y = 0, .z = 0}}},
        {
            .query = Query(2, ihomeland::sim::NavigationQueryKind::FindPath),
            .points_mm = {
                {.x = 10'000, .y = 0, .z = 0},
                {.x = 8000, .y = 0, .z = -500},
                {.x = 6000, .y = 0, .z = -1000}}}};
}

/// TestAssetAndPath 验证 asset gate、nearest/path、稳定 identity 与 reset。
void TestAssetAndPath() {
    ihomeland::sim::RecordedNavigationWorld world(Asset(), Exchanges(), 2, 4);
    bool not_loaded_rejected = false;
    try {
        static_cast<void>(world.Query(Query(1, ihomeland::sim::NavigationQueryKind::NearestPoly)));
    } catch (const ihomeland::sim::RecordedNavigationError& error) {
        Require(
            error.Code() == ihomeland::sim::RecordedNavigationErrorCode::NotLoaded,
            "navigation query before load returned wrong code");
        not_loaded_rejected = true;
    }
    Require(not_loaded_rejected, "navigation query bypassed asset load");
    world.LoadAsset(Asset());
    const auto nearest =
        world.Query(Query(1, ihomeland::sim::NavigationQueryKind::NearestPoly));
    const auto path = world.Query(Query(2, ihomeland::sim::NavigationQueryKind::FindPath));
    Require(
        nearest.points_mm.size() == 1 && path.points_mm.size() == 3 &&
            path.path_identity.size() == 64 && world.Consumed() == 2,
        "recorded navigation path result drifted");
    world.Reset();
    world.LoadAsset(Asset());
    static_cast<void>(
        world.Query(Query(1, ihomeland::sim::NavigationQueryKind::NearestPoly)));
    const auto repeated =
        world.Query(Query(2, ihomeland::sim::NavigationQueryKind::FindPath));
    Require(
        repeated.path_identity == path.path_identity,
        "recorded navigation path identity is unstable");
}

/// TestAssetDriftAndCapacity 验证 digest 漂移与 query point limit fail closed。
void TestAssetDriftAndCapacity() {
    ihomeland::sim::RecordedNavigationWorld world(Asset(), Exchanges(), 2, 4);
    auto drift = Asset();
    drift.sha256 = std::string(64, 'b');
    bool drift_rejected = false;
    try {
        world.LoadAsset(drift);
    } catch (const ihomeland::sim::RecordedNavigationError& error) {
        Require(
            error.Code() == ihomeland::sim::RecordedNavigationErrorCode::AssetDrift,
            "navigation asset drift returned wrong code");
        drift_rejected = true;
    }
    Require(drift_rejected, "navigation asset drift was accepted");
    auto exchanges = Exchanges();
    exchanges.erase(exchanges.begin());
    exchanges[0].query.maximum_points = 2;
    ihomeland::sim::RecordedNavigationWorld limited(Asset(), exchanges, 1, 4);
    limited.LoadAsset(Asset());
    try {
        static_cast<void>(limited.Query(exchanges[0].query));
    } catch (const ihomeland::sim::RecordedNavigationError& error) {
        Require(
            error.Code() == ihomeland::sim::RecordedNavigationErrorCode::Capacity,
            "navigation query capacity returned wrong code");
        return;
    }
    throw std::runtime_error("recorded navigation query capacity expanded");
}

}  // namespace

/// main 执行 recorded NavigationWorld 的 asset/trace/path contract 回归。
int main() {
    try {
        TestAssetAndPath();
        TestAssetDriftAndCapacity();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
