#include "ihomeland/sim/config/personal_world_arena.hpp"
#include "ihomeland/sim/navigation/navigation_world.hpp"
#include "ihomeland/sim/physics/physics_world.hpp"

#include <filesystem>
#include <fstream>
#include <iostream>
#include <stdexcept>
#include <string>

namespace {

constexpr std::string_view MapIdentity =
    "34bb2d795d9c1bcb94059d184d3d7c9fd2e0e9741f97f670ccccd479afe3e6e8";
constexpr std::string_view NavigationIdentity =
    "14165efe5b47caf6c7c8e2f9a4794c4d238aa4badeedc3d5badb41a9c2d5d19f";
constexpr std::string_view PhysicsIdentity =
    "64d53e1803d7cb14f4953ba1978175b162577eeb064716b2630cad0acf4de02e";

void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

[[nodiscard]] ihomeland::sim::PersonalWorldArenaCatalog Load(
    const std::filesystem::path& root) {
    return ihomeland::sim::LoadPersonalWorldArenaCatalog(
        root,
        "personal-world-combat/map/arena",
        std::string(MapIdentity),
        std::string(NavigationIdentity),
        std::string(PhysicsIdentity));
}

void TestProductionQueries() {
    const auto catalog = Load(IHOMELAND_PERSONAL_WORLD_ARENA_ROOT);
    Require(catalog.spawn_points.size() == 8, "arena spawn coverage differs");
    auto physics = ihomeland::sim::CreatePersonalWorldPhysicsWorld(catalog);
    const ihomeland::sim::PhysicsQuery floor{
        .query_id = 1,
        .tick = 1,
        .kind = ihomeland::sim::PhysicsQueryKind::GroundProbe,
        .actor_id = 1,
        .start_mm = {.x = -12'000, .y = 2'000, .z = -12'000},
        .end_mm = {.x = -12'000, .y = -1'000, .z = -12'000},
        .maximum_hits = 32};
    const auto floor_hits = physics->Query(floor);
    Require(!floor_hits.hits.empty() && floor_hits.hits.front().collider_id == 1000,
            "arena floor query did not use production collider");
    auto blocker = floor;
    blocker.query_id = 2;
    blocker.kind = ihomeland::sim::PhysicsQueryKind::ShapeCast;
    blocker.start_mm = {.x = -5'000, .y = 1'000, .z = 0};
    blocker.end_mm = {.x = 5'000, .y = 1'000, .z = 0};
    const auto first = physics->Query(blocker);
    blocker.query_id = 3;
    blocker.kind = ihomeland::sim::PhysicsQueryKind::ProjectileSweep;
    const auto second = physics->Query(blocker);
    Require(!first.hits.empty() && first.hits.front().collider_id == 1001 &&
                !second.hits.empty() && second.hits.front().collider_id == 1001,
            "arena shape/projectile query did not hit stable blocker");
    auto movement = floor;
    movement.query_id = 4;
    movement.kind = ihomeland::sim::PhysicsQueryKind::MoveCapsule;
    movement.start_mm = {.x = -12'000, .y = 0, .z = -12'000};
    movement.end_mm = {.x = -11'900, .y = 250, .z = -12'000};
    movement.maximum_hits = 1;
    Require(
        physics->Query(movement).hits.empty(),
        "arena movement capsule treated foot position as embedded center");

    auto navigation = ihomeland::sim::CreatePersonalWorldNavigationWorld(catalog);
    const ihomeland::sim::NavigationQuery path{
        .query_id = 1,
        .tick = 1,
        .kind = ihomeland::sim::NavigationQueryKind::FindPath,
        .actor_id = 5,
        .start_mm = {.x = -12'000, .y = 0, .z = -12'000},
        .end_mm = {.x = 15'000, .y = 0, .z = 15'000},
        .maximum_points = 32};
    const auto first_path = navigation->Query(path);
    const auto second_path = navigation->Query(path);
    Require(first_path.points_mm.size() >= 2 &&
                first_path.points_mm == second_path.points_mm &&
                first_path.path_identity == second_path.path_identity,
            "arena Detour path result is not stable");
}

void TestDriftMalformedCapacityAndReadOnly() {
    const auto source = std::filesystem::path(IHOMELAND_PERSONAL_WORLD_ARENA_ROOT);
    const auto target = std::filesystem::temp_directory_path() /
                        "ihomeland-personal-world-arena-test";
    std::error_code error;
    std::filesystem::remove_all(target, error);
    std::filesystem::create_directories(target);
    for (const auto* name : {"arena.json", "navigation.json", "physics.json"}) {
        std::filesystem::copy_file(source / name, target / name);
    }
    const auto catalog = Load(target);
    {
        std::ofstream stream(target / "physics.json", std::ios::binary | std::ios::app);
        stream << ' ';
    }
    auto physics = ihomeland::sim::CreatePersonalWorldPhysicsWorld(catalog);
    bool drift_rejected = false;
    try {
        static_cast<void>(Load(target));
    } catch (const std::runtime_error& exception) {
        drift_rejected = true;
        Require(std::string(exception.what()).find(target.string()) == std::string::npos,
                "arena failure leaked a local path");
    }
    Require(drift_rejected, "arena source drift was accepted");
    bool capacity_rejected = false;
    try {
        static_cast<void>(physics->Query({
            .query_id = 9,
            .tick = 1,
            .kind = ihomeland::sim::PhysicsQueryKind::RayCast,
            .actor_id = 1,
            .start_mm = {.x = 0, .y = 2'000, .z = 0},
            .end_mm = {.x = 0, .y = -1'000, .z = 0},
            .maximum_hits = 33}));
    } catch (const std::runtime_error&) {
        capacity_rejected = true;
    }
    Require(capacity_rejected, "arena query capacity overflow was accepted");
    std::filesystem::remove_all(target, error);
}

}  // namespace

int main() {
    try {
        TestProductionQueries();
        TestDriftMalformedCapacityAndReadOnly();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
