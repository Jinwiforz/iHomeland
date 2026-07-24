#include "ihomeland/sim/ecs/component_storage.hpp"
#include "ihomeland/sim/ecs/entity_registry.hpp"
#include "ihomeland/sim/gameplay/components.hpp"

#include <cstdint>
#include <iostream>
#include <stdexcept>

namespace {

/// Require 把 ECS 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// RequireEcsError 验证操作 fail closed 且产生期待的稳定错误码。
template <typename Callback>
void RequireEcsError(
    Callback&& callback,
    const ihomeland::sim::EcsErrorCode expected,
    const char* message) {
    try {
        callback();
    } catch (const ihomeland::sim::EcsError& error) {
        Require(error.Code() == expected, message);
        return;
    }
    throw std::runtime_error(message);
}

/// TestRegistry 覆盖 slot 复用、stale handle、容量、generation retirement 与 reset。
void TestRegistry() {
    ihomeland::sim::EntityRegistry registry(11, 2);
    const auto first = registry.Create();
    const auto second = registry.Create();
    Require(first.index == 0 && second.index == 1, "registry allocation order drifted");
    Require(registry.AliveCount() == 2, "registry alive accounting drifted");
    RequireEcsError(
        [&] { static_cast<void>(registry.Create()); },
        ihomeland::sim::EcsErrorCode::Capacity,
        "registry capacity overflow was accepted");

    registry.Destroy(first);
    const auto reused = registry.Create();
    Require(reused.index == first.index && reused.generation == first.generation + 1, "slot generation was not incremented");
    RequireEcsError(
        [&] { registry.RequireAlive(first); },
        ihomeland::sim::EcsErrorCode::StaleEntity,
        "stale entity was accepted after reuse");

    registry.Reset(12);
    Require(registry.AliveCount() == 0 && registry.RetiredCount() == 0, "registry reset accounting failed");
    RequireEcsError(
        [&] { registry.RequireAlive(reused); },
        ihomeland::sim::EcsErrorCode::CrossWorld,
        "pre-reset entity crossed world generation");

    ihomeland::sim::EntityRegistry retirement(21, 1);
    for (std::uint32_t generation = 1;
         generation <= ihomeland::sim::MaxEntityGeneration;
         ++generation) {
        const auto entity = retirement.Create();
        Require(entity.generation == generation, "entity generation sequence drifted");
        retirement.Destroy(entity);
    }
    Require(retirement.RetiredCount() == 1, "generation wrap did not retire slot");
    RequireEcsError(
        [&] { static_cast<void>(retirement.Create()); },
        ihomeland::sim::EcsErrorCode::Capacity,
        "retired slot returned to free-list");
}

/// TestComponentStorage 覆盖类型安全访问、cross-world、容量、swap-remove 和迭代失效。
void TestComponentStorage() {
    ihomeland::sim::EntityRegistry registry(31, 3);
    ihomeland::sim::ComponentStorage<ihomeland::sim::TransformComponent> transforms(registry, 2);
    ihomeland::sim::ComponentStorage<ihomeland::sim::MotionComponent> motions(registry, 3);
    const auto first = registry.Create();
    const auto second = registry.Create();
    const auto third = registry.Create();
    transforms.Add(first, {.x_mm = 1, .y_mm = 2, .z_mm = 3, .yaw_millidegrees = 4});
    transforms.Add(second, {.x_mm = 10, .y_mm = 20, .z_mm = 30, .yaw_millidegrees = 40});
    motions.Add(first, {
        .velocity_x_mm_per_second = 0,
        .velocity_y_mm_per_second = 0,
        .velocity_z_mm_per_second = 0,
        .grounded = true});
    Require(transforms.Has(first) && motions.Has(first), "typed component lookup failed");
    Require(transforms.Get(second).x_mm == 10, "component value drifted");
    RequireEcsError(
        [&] { transforms.Add(first, {.x_mm = 0, .y_mm = 0, .z_mm = 0, .yaw_millidegrees = 0}); },
        ihomeland::sim::EcsErrorCode::Duplicate,
        "duplicate component was accepted");
    RequireEcsError(
        [&] { transforms.Add(third, {.x_mm = 0, .y_mm = 0, .z_mm = 0, .yaw_millidegrees = 0}); },
        ihomeland::sim::EcsErrorCode::Capacity,
        "component hard capacity expanded");

    const auto size_before_iteration = transforms.Size();
    RequireEcsError(
        [&] {
            transforms.ForEach([&](const ihomeland::sim::EntityID entity, ihomeland::sim::TransformComponent&) {
                transforms.Remove(entity);
            });
        },
        ihomeland::sim::EcsErrorCode::IterationMutation,
        "view iteration allowed structural mutation");
    Require(transforms.Size() == size_before_iteration, "failed iteration mutation changed storage");

    transforms.Remove(first);
    Require(transforms.Get(second).x_mm == 10 && transforms.Size() == 1, "swap-remove corrupted dense mapping");
    RequireEcsError(
        [&] { static_cast<void>(transforms.Get(first)); },
        ihomeland::sim::EcsErrorCode::Missing,
        "removed component remained visible");

    ihomeland::sim::EntityRegistry other_registry(32, 1);
    const auto foreign = other_registry.Create();
    RequireEcsError(
        [&] { static_cast<void>(transforms.Has(foreign)); },
        ihomeland::sim::EcsErrorCode::CrossWorld,
        "component storage accepted cross-world handle");

    transforms.Reset();
    motions.Reset();
    registry.Reset(33);
    RequireEcsError(
        [&] { static_cast<void>(motions.Has(first)); },
        ihomeland::sim::EcsErrorCode::CrossWorld,
        "component storage accepted pre-reset handle");
}

}  // namespace

/// main 执行 generation-safe registry 与 typed storage 回归。
int main() {
    try {
        TestRegistry();
        TestComponentStorage();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
