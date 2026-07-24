#include "ihomeland/sim/ecs/entity_registry.hpp"

#include <algorithm>
#include <limits>

namespace ihomeland::sim {

EcsError::EcsError(const EcsErrorCode code, const char* message)
    : std::runtime_error(message), code_(code) {}

EcsErrorCode EcsError::Code() const noexcept {
    return code_;
}

EntityRegistry::EntityRegistry(const std::uint32_t world, const std::uint32_t capacity)
    : world_(world), slots_(capacity) {
    if (world == 0) {
        throw EcsError(EcsErrorCode::InvalidEntity, "world identity must be nonzero");
    }
    if (capacity == 0) {
        throw EcsError(EcsErrorCode::Capacity, "entity capacity must be positive");
    }
    RebuildFreeList();
}

EntityID EntityRegistry::Create() {
    if (free_list_.empty()) {
        throw EcsError(EcsErrorCode::Capacity, "entity capacity exhausted");
    }
    const auto index = free_list_.back();
    free_list_.pop_back();
    auto& slot = slots_[index];
    if (slot.alive || slot.retired || slot.generation == 0) {
        throw EcsError(EcsErrorCode::Conflict, "entity free-list invariant failed");
    }
    slot.alive = true;
    ++alive_count_;
    return EntityID{.world = world_, .index = index, .generation = slot.generation};
}

void EntityRegistry::Destroy(const EntityID entity) {
    RequireAlive(entity);
    auto& slot = slots_[entity.index];
    slot.alive = false;
    --alive_count_;
    if (slot.generation == MaxEntityGeneration) {
        slot.retired = true;
        ++retired_count_;
        return;
    }
    ++slot.generation;
    free_list_.push_back(entity.index);
}

void EntityRegistry::RequireAlive(const EntityID entity) const {
    if (!entity.IsValid() || entity.index >= slots_.size()) {
        throw EcsError(EcsErrorCode::InvalidEntity, "entity identity is invalid");
    }
    if (entity.world != world_) {
        throw EcsError(EcsErrorCode::CrossWorld, "entity belongs to another world");
    }
    const auto& slot = slots_[entity.index];
    if (!slot.alive || slot.generation != entity.generation) {
        throw EcsError(EcsErrorCode::StaleEntity, "entity generation is stale");
    }
}

bool EntityRegistry::IsAlive(const EntityID entity) const noexcept {
    return entity.IsValid() && entity.world == world_ && entity.index < slots_.size() &&
           slots_[entity.index].alive &&
           slots_[entity.index].generation == entity.generation;
}

void EntityRegistry::Reset(const std::uint32_t new_world) {
    if (new_world == 0 || new_world == world_) {
        throw EcsError(EcsErrorCode::CrossWorld, "reset requires a new nonzero world identity");
    }
    world_ = new_world;
    std::fill(slots_.begin(), slots_.end(), Slot{});
    alive_count_ = 0;
    retired_count_ = 0;
    RebuildFreeList();
}

std::uint32_t EntityRegistry::World() const noexcept {
    return world_;
}

std::uint32_t EntityRegistry::Capacity() const noexcept {
    return static_cast<std::uint32_t>(slots_.size());
}

std::uint32_t EntityRegistry::AliveCount() const noexcept {
    return alive_count_;
}

std::uint32_t EntityRegistry::RetiredCount() const noexcept {
    return retired_count_;
}

void EntityRegistry::RebuildFreeList() {
    free_list_.clear();
    free_list_.reserve(slots_.size());
    for (std::size_t index = slots_.size(); index > 0; --index) {
        free_list_.push_back(static_cast<std::uint32_t>(index - 1));
    }
}

}  // namespace ihomeland::sim
