#include "ihomeland/sim/ecs/structural_commands.hpp"

#include <algorithm>
#include <cstdint>
#include <iostream>
#include <optional>
#include <stdexcept>
#include <vector>

namespace {

/// Require 把 structural ECS 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// RequireEcsError 验证 enqueue/capacity 失败的稳定错误码。
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

/// WorldConfig 返回 tests 使用的显式容量，不依赖生产默认值。
[[nodiscard]] ihomeland::sim::SimulationWorldConfig WorldConfig(
    const std::uint32_t entities,
    const std::uint32_t transforms) {
    return {
        .entity_capacity = entities,
        .transform_capacity = transforms,
        .motion_capacity = entities,
        .actor_capacity = entities,
        .attribute_capacity = entities,
        .ability_capacity = entities,
        .effect_capacity = entities,
        .projectile_capacity = entities,
        .ai_capacity = entities};
}

/// CreateCommand 构造不携带 target/payload 的合法 create intent。
[[nodiscard]] ihomeland::sim::StructuralCommand CreateCommand(
    const std::uint64_t tick,
    const std::uint64_t sequence) {
    return {
        .target_tick = tick,
        .kind = ihomeland::sim::StructuralCommandKind::Create,
        .source_entity = {},
        .stable_sequence = sequence,
        .target_entity = {},
        .payload = std::nullopt,
        .remove_kind = std::nullopt};
}

/// DestroyCommand 构造绑定 generation-safe target 的 destroy intent。
[[nodiscard]] ihomeland::sim::StructuralCommand DestroyCommand(
    const std::uint64_t tick,
    const std::uint64_t sequence,
    const ihomeland::sim::EntityID entity) {
    return {
        .target_tick = tick,
        .kind = ihomeland::sim::StructuralCommandKind::Destroy,
        .source_entity = entity,
        .stable_sequence = sequence,
        .target_entity = entity,
        .payload = std::nullopt,
        .remove_kind = std::nullopt};
}

/// TransformCommand 构造 typed TransformComponent add intent。
[[nodiscard]] ihomeland::sim::StructuralCommand TransformCommand(
    const std::uint64_t tick,
    const std::uint64_t sequence,
    const ihomeland::sim::EntityID entity,
    const std::int64_t x_mm) {
    return {
        .target_tick = tick,
        .kind = ihomeland::sim::StructuralCommandKind::Add,
        .source_entity = entity,
        .stable_sequence = sequence,
        .target_entity = entity,
        .payload = ihomeland::sim::TransformComponent{
            .x_mm = x_mm,
            .y_mm = 0,
            .z_mm = 0,
            .yaw_millidegrees = 0},
        .remove_kind = std::nullopt};
}

/// TestBarrier 覆盖 canonical sort、future retention、duplicate、conflict 与 component capacity。
void TestBarrier() {
    ihomeland::sim::SimulationWorld world(41, WorldConfig(3, 1));
    ihomeland::sim::StructuralCommandBuffer buffer(3);
    buffer.Enqueue(CreateCommand(1, 2));
    buffer.Enqueue(CreateCommand(1, 1));
    RequireEcsError(
        [&] { buffer.Enqueue(CreateCommand(1, 1)); },
        ihomeland::sim::EcsErrorCode::Duplicate,
        "duplicate structural tuple was accepted");
    const auto created = buffer.Commit(1, world);
    Require(created.size() == 2, "create result count drifted");
    Require(created[0].stable_sequence == 1 && created[1].stable_sequence == 2, "canonical sequence sort drifted");
    Require(created[0].accepted && created[1].accepted, "create command was rejected");
    const auto first = *created[0].created_entity;
    const auto second = *created[1].created_entity;
    Require(first.index == 0 && second.index == 1, "canonical create allocation drifted");

    buffer.Enqueue(TransformCommand(2, 1, first, 100));
    buffer.Enqueue(TransformCommand(2, 2, second, 200));
    const auto additions = buffer.Commit(2, world);
    Require(additions[0].accepted, "first component capacity token was rejected");
    Require(
        !additions[1].accepted &&
            additions[1].rejection == ihomeland::sim::EcsErrorCode::Capacity,
        "component overflow did not produce stable rejection");
    Require(world.Transform(first).x_mm == 100, "accepted component mutation drifted");

    buffer.Enqueue(CreateCommand(4, 10));
    Require(buffer.Commit(3, world).empty(), "future structural command committed early");
    Require(buffer.CapacitySnapshot().queued == 1, "future structural command was not retained");
    const auto future = buffer.Commit(4, world);
    Require(future.size() == 1 && future[0].accepted, "future structural command did not commit");

    buffer.Enqueue(TransformCommand(5, 20, first, 300));
    buffer.Enqueue(DestroyCommand(5, 30, first));
    const auto conflict = buffer.Commit(5, world);
    Require(conflict.size() == 2 && conflict[0].stable_sequence == 30, "command kind order drifted");
    Require(conflict[0].accepted, "destroy command failed");
    Require(
        !conflict[1].accepted &&
            conflict[1].rejection == ihomeland::sim::EcsErrorCode::StaleEntity,
        "post-destroy add did not reject stale target");
    Require(!world.IsAlive(first), "destroy barrier left entity alive");

    buffer.Enqueue(CreateCommand(6, 1));
    buffer.Enqueue(CreateCommand(6, 2));
    buffer.Enqueue(CreateCommand(6, 3));
    RequireEcsError(
        [&] { buffer.Enqueue(CreateCommand(6, 4)); },
        ihomeland::sim::EcsErrorCode::Capacity,
        "structural buffer expanded past hard capacity");
    const auto snapshot = buffer.CapacitySnapshot();
    Require(
        snapshot.limit == 3 && snapshot.queued == 3 &&
            snapshot.rejected_capacity_total == 1,
        "structural capacity accounting drifted");
}

/// NextRandom 使用固定 xorshift64*，使 fuzz-like sequence 可完全重演。
[[nodiscard]] std::uint64_t NextRandom(std::uint64_t& state) {
    state ^= state >> 12U;
    state ^= state << 25U;
    state ^= state >> 27U;
    return state * 2'685'821'657'736'338'717ULL;
}

/// RunRandomizedSequence 执行固定 seed 的 create/destroy/reset 序列并返回规范结果 token。
[[nodiscard]] std::vector<std::uint64_t> RunRandomizedSequence(const std::uint64_t seed) {
    ihomeland::sim::SimulationWorld world(100, WorldConfig(32, 32));
    ihomeland::sim::StructuralCommandBuffer buffer(4);
    std::vector<ihomeland::sim::EntityID> active;
    std::vector<std::uint64_t> tokens;
    std::uint64_t random = seed;
    std::uint64_t tick = 1;
    std::uint64_t sequence = 1;
    std::uint32_t world_id = 100;
    for (std::uint32_t iteration = 0; iteration < 5'000; ++iteration, ++tick) {
        if (iteration != 0 && iteration % 500 == 0) {
            active.clear();
            buffer.Reset();
            world.Reset(++world_id);
            tokens.push_back(world_id);
        }
        const bool create = active.empty() ||
                            (active.size() < 32 && (NextRandom(random) & 1ULL) == 0);
        if (create) {
            buffer.Enqueue(CreateCommand(tick, sequence++));
        } else {
            const auto selected =
                static_cast<std::size_t>(NextRandom(random) % active.size());
            buffer.Enqueue(DestroyCommand(tick, sequence++, active[selected]));
            active.erase(active.begin() + static_cast<std::ptrdiff_t>(selected));
        }
        const auto results = buffer.Commit(tick, world);
        Require(results.size() == 1 && results[0].accepted, "randomized structural command failed");
        if (results[0].created_entity) {
            active.push_back(*results[0].created_entity);
        }
        const auto entity_token = results[0].created_entity
                                      ? (static_cast<std::uint64_t>(results[0].created_entity->world) << 32U) |
                                            results[0].created_entity->index
                                      : 0;
        tokens.push_back(entity_token ^ results[0].stable_sequence);
    }
    Require(world.AliveCount() == active.size(), "randomized alive accounting drifted");
    return tokens;
}

/// TestRandomizedReset 验证固定 seed 重放和连续 reset 不产生 stale mutation。
void TestRandomizedReset() {
    const auto first = RunRandomizedSequence(0x4f3c'2b1a'9876'5432ULL);
    const auto second = RunRandomizedSequence(0x4f3c'2b1a'9876'5432ULL);
    Require(first == second, "fuzz-like structural sequence is not deterministic");
}

}  // namespace

/// main 执行 structural barrier、capacity 与 deterministic reset 回归。
int main() {
    try {
        TestBarrier();
        TestRandomizedReset();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
