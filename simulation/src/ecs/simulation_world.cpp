#include "ihomeland/sim/ecs/simulation_world.hpp"

namespace ihomeland::sim {

SimulationWorld::SimulationWorld(
    const std::uint32_t world,
    const SimulationWorldConfig& config)
    : registry_(world, config.entity_capacity),
      transforms_(registry_, config.transform_capacity),
      motions_(registry_, config.motion_capacity),
      actors_(registry_, config.actor_capacity),
      attributes_(registry_, config.attribute_capacity),
      abilities_(registry_, config.ability_capacity),
      effects_(registry_, config.effect_capacity),
      projectiles_(registry_, config.projectile_capacity),
      ai_(registry_, config.ai_capacity) {}

void SimulationWorld::RequireAlive(const EntityID entity) const {
    registry_.RequireAlive(entity);
}

bool SimulationWorld::IsAlive(const EntityID entity) const noexcept {
    return registry_.IsAlive(entity);
}

TransformComponent& SimulationWorld::Transform(const EntityID entity) {
    return transforms_.Get(entity);
}

AttributeComponent& SimulationWorld::Attribute(const EntityID entity) {
    return attributes_.Get(entity);
}

std::uint32_t SimulationWorld::AliveCount() const noexcept {
    return registry_.AliveCount();
}

void SimulationWorld::Reset(const std::uint32_t new_world) {
    transforms_.Reset();
    motions_.Reset();
    actors_.Reset();
    attributes_.Reset();
    abilities_.Reset();
    effects_.Reset();
    projectiles_.Reset();
    ai_.Reset();
    registry_.Reset(new_world);
}

EntityID SimulationWorld::CreateEntity() {
    return registry_.Create();
}

void SimulationWorld::DestroyEntity(const EntityID entity) {
    registry_.RequireAlive(entity);
    RemoveAllComponents(entity);
    registry_.Destroy(entity);
}

void SimulationWorld::RemoveAllComponents(const EntityID entity) {
    if (transforms_.Has(entity)) {
        transforms_.Remove(entity);
    }
    if (motions_.Has(entity)) {
        motions_.Remove(entity);
    }
    if (actors_.Has(entity)) {
        actors_.Remove(entity);
    }
    if (attributes_.Has(entity)) {
        attributes_.Remove(entity);
    }
    if (abilities_.Has(entity)) {
        abilities_.Remove(entity);
    }
    if (effects_.Has(entity)) {
        effects_.Remove(entity);
    }
    if (projectiles_.Has(entity)) {
        projectiles_.Remove(entity);
    }
    if (ai_.Has(entity)) {
        ai_.Remove(entity);
    }
}

}  // namespace ihomeland::sim
