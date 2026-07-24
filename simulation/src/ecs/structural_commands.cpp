#include "ihomeland/sim/ecs/structural_commands.hpp"

#include <algorithm>
#include <set>
#include <tuple>
#include <utility>

namespace ihomeland::sim {
namespace {

/// CanonicalKey 返回冻结的 structural tuple。
[[nodiscard]] auto CanonicalKey(const StructuralCommand& command) {
    return std::tie(
        command.target_tick,
        command.kind,
        command.source_entity,
        command.stable_sequence);
}

}  // namespace

StructuralCommandBuffer::StructuralCommandBuffer(const std::uint32_t capacity)
    : capacity_(capacity) {
    if (capacity == 0) {
        throw EcsError(EcsErrorCode::Capacity, "structural capacity must be positive");
    }
    commands_.reserve(capacity);
}

void StructuralCommandBuffer::Enqueue(StructuralCommand command) {
    ValidateShape(command);
    if (commands_.size() >= capacity_) {
        ++rejected_capacity_total_;
        throw EcsError(EcsErrorCode::Capacity, "structural command capacity exhausted");
    }
    const auto duplicate = std::find_if(
        commands_.begin(),
        commands_.end(),
        [&](const StructuralCommand& existing) {
            return CanonicalKey(existing) == CanonicalKey(command);
        });
    if (duplicate != commands_.end()) {
        throw EcsError(EcsErrorCode::Duplicate, "structural command tuple is duplicated");
    }
    commands_.push_back(std::move(command));
    ++accepted_total_;
}

std::vector<StructuralCommandResult> StructuralCommandBuffer::Commit(
    const std::uint64_t target_tick,
    SimulationWorld& world) {
    std::stable_sort(commands_.begin(), commands_.end(), [](const auto& left, const auto& right) {
        return CanonicalKey(left) < CanonicalKey(right);
    });
    std::vector<StructuralCommand> future;
    future.reserve(capacity_);
    std::vector<StructuralCommandResult> results;
    results.reserve(commands_.size());
    for (auto& command : commands_) {
        if (command.target_tick > target_tick) {
            future.push_back(std::move(command));
            continue;
        }
        if (command.target_tick < target_tick) {
            results.push_back({
                .stable_sequence = command.stable_sequence,
                .accepted = false,
                .rejection = EcsErrorCode::Conflict,
                .created_entity = std::nullopt});
            continue;
        }
        StructuralCommandResult result{
            .stable_sequence = command.stable_sequence,
            .accepted = false,
            .rejection = std::nullopt,
            .created_entity = std::nullopt};
        try {
            switch (command.kind) {
                case StructuralCommandKind::Create:
                    result.created_entity = world.CreateEntity();
                    break;
                case StructuralCommandKind::Destroy:
                    world.DestroyEntity(command.target_entity);
                    break;
                case StructuralCommandKind::Add:
                    std::visit(
                        [&](auto&& component) {
                            world.AddComponent(
                                command.target_entity,
                                std::forward<decltype(component)>(component));
                        },
                        std::move(*command.payload));
                    break;
                case StructuralCommandKind::Remove:
                    switch (*command.remove_kind) {
                        case ComponentKind::Transform:
                            world.RemoveComponent<TransformComponent>(command.target_entity);
                            break;
                        case ComponentKind::Motion:
                            world.RemoveComponent<MotionComponent>(command.target_entity);
                            break;
                        case ComponentKind::Actor:
                            world.RemoveComponent<ActorComponent>(command.target_entity);
                            break;
                        case ComponentKind::Attribute:
                            world.RemoveComponent<AttributeComponent>(command.target_entity);
                            break;
                        case ComponentKind::Ability:
                            world.RemoveComponent<AbilityComponent>(command.target_entity);
                            break;
                        case ComponentKind::Effect:
                            world.RemoveComponent<EffectComponent>(command.target_entity);
                            break;
                        case ComponentKind::Projectile:
                            world.RemoveComponent<ProjectileComponent>(command.target_entity);
                            break;
                        case ComponentKind::Ai:
                            world.RemoveComponent<AiComponent>(command.target_entity);
                            break;
                    }
                    break;
            }
            result.accepted = true;
        } catch (const EcsError& error) {
            result.rejection = error.Code();
        }
        results.push_back(std::move(result));
    }
    commands_ = std::move(future);
    return results;
}

void StructuralCommandBuffer::Reset() noexcept {
    commands_.clear();
    accepted_total_ = 0;
    rejected_capacity_total_ = 0;
}

StructuralCapacitySnapshot StructuralCommandBuffer::CapacitySnapshot() const noexcept {
    return StructuralCapacitySnapshot{
        .limit = capacity_,
        .queued = static_cast<std::uint32_t>(commands_.size()),
        .accepted_total = accepted_total_,
        .rejected_capacity_total = rejected_capacity_total_};
}

void StructuralCommandBuffer::ValidateShape(const StructuralCommand& command) {
    if (command.target_tick == 0 || command.stable_sequence == 0) {
        throw EcsError(EcsErrorCode::Conflict, "structural command identity is invalid");
    }
    switch (command.kind) {
        case StructuralCommandKind::Create:
            if (command.target_entity.IsValid() || command.payload || command.remove_kind) {
                throw EcsError(EcsErrorCode::Conflict, "create command shape is invalid");
            }
            return;
        case StructuralCommandKind::Destroy:
            if (!command.target_entity.IsValid() || command.payload || command.remove_kind) {
                throw EcsError(EcsErrorCode::Conflict, "destroy command shape is invalid");
            }
            return;
        case StructuralCommandKind::Add:
            if (!command.target_entity.IsValid() || !command.payload || command.remove_kind) {
                throw EcsError(EcsErrorCode::Conflict, "add command shape is invalid");
            }
            return;
        case StructuralCommandKind::Remove:
            if (!command.target_entity.IsValid() || command.payload || !command.remove_kind) {
                throw EcsError(EcsErrorCode::Conflict, "remove command shape is invalid");
            }
            return;
    }
    throw EcsError(EcsErrorCode::Conflict, "unknown structural command kind");
}

}  // namespace ihomeland::sim
