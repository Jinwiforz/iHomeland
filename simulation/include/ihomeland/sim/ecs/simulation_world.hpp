#pragma once

#include "ihomeland/sim/ecs/component_storage.hpp"
#include "ihomeland/sim/gameplay/components.hpp"

#include <cstdint>
#include <utility>

namespace ihomeland::sim {

class StructuralCommandBuffer;

/// SimulationWorldConfig 固定 registry 与每类 component storage 的 hard capacity。
struct SimulationWorldConfig final {
    /// entity_capacity 是 registry slot 总数。
    std::uint32_t entity_capacity;
    /// transform_capacity 是 TransformComponent 最大数量。
    std::uint32_t transform_capacity;
    /// motion_capacity 是 MotionComponent 最大数量。
    std::uint32_t motion_capacity;
    /// actor_capacity 是 ActorComponent 最大数量。
    std::uint32_t actor_capacity;
    /// attribute_capacity 是 AttributeComponent 最大数量。
    std::uint32_t attribute_capacity;
    /// ability_capacity 是 AbilityComponent 最大数量。
    std::uint32_t ability_capacity;
    /// effect_capacity 是 EffectComponent 最大数量。
    std::uint32_t effect_capacity;
    /// projectile_capacity 是 ProjectileComponent 最大数量。
    std::uint32_t projectile_capacity;
    /// ai_capacity 是 AiComponent 最大数量。
    std::uint32_t ai_capacity;
};

/// SimulationWorld 唯一组合 per-world registry 与冻结 gameplay component storages。
///
/// 结构 mutation 仅向 StructuralCommandBuffer 开放；systems 只能修改已有 component value
/// 或使用最小 view，不能在迭代中直接 create/destroy/add/remove。
class SimulationWorld final {
public:
    /// 构造函数一次性预留全部 hard capacity，任何非法容量在创建实例前失败。
    SimulationWorld(std::uint32_t world, const SimulationWorldConfig& config);

    /// RequireAlive 验证 handle 属于当前 world 且 generation 存活。
    void RequireAlive(EntityID entity) const;

    /// IsAlive 对当前有效 handle 返回 true。
    [[nodiscard]] bool IsAlive(EntityID entity) const noexcept;

    /// Transform 返回已有 TransformComponent 的可变借用，结构 mutation 后引用失效。
    [[nodiscard]] TransformComponent& Transform(EntityID entity);

    /// Attribute 返回已有 AttributeComponent 的可变借用，结构 mutation 后引用失效。
    [[nodiscard]] AttributeComponent& Attribute(EntityID entity);

    /// ForEachTransform 在禁止 transform 结构 mutation 的 view 内调用 callback。
    template <typename Callback>
    void ForEachTransform(Callback&& callback) {
        transforms_.ForEach(std::forward<Callback>(callback));
    }

    /// AliveCount 返回 registry 当前存活实体数。
    [[nodiscard]] std::uint32_t AliveCount() const noexcept;

    /// Reset 先清空全部 components，再把 registry 绑定到新的 world identity。
    void Reset(std::uint32_t new_world);

private:
    friend class StructuralCommandBuffer;

    /// CreateEntity 只允许 structural barrier 分配 slot。
    [[nodiscard]] EntityID CreateEntity();

    /// DestroyEntity 先删除全部 components，再使 registry handle stale。
    void DestroyEntity(EntityID entity);

    /// AddComponent 按 payload type 进入对应有界 storage。
    template <typename Component>
    void AddComponent(const EntityID entity, Component component) {
        Storage<Component>().Add(entity, std::move(component));
    }

    /// RemoveComponent 删除登记类型；missing component 稳定失败。
    template <typename Component>
    void RemoveComponent(const EntityID entity) {
        Storage<Component>().Remove(entity);
    }

    /// Storage 为每个冻结 component type 返回唯一 storage；未登记 type 在编译期失败。
    template <typename Component>
    [[nodiscard]] ComponentStorage<Component>& Storage();

    /// RemoveAllComponents 在 entity destroy 前按固定 type 顺序清理已有 values。
    void RemoveAllComponents(EntityID entity);

    /// registry_ 唯一拥有 EntityID slot/generation。
    EntityRegistry registry_;
    /// transforms_ 由 Movement/Physics stage 写 value。
    ComponentStorage<TransformComponent> transforms_;
    /// motions_ 由 Movement/Physics stage 写 value。
    ComponentStorage<MotionComponent> motions_;
    /// actors_ 保存稳定 ActorID 与 player/AI 分类。
    ComponentStorage<ActorComponent> actors_;
    /// attributes_ 由 Attribute/Death stage 写 value。
    ComponentStorage<AttributeComponent> attributes_;
    /// abilities_ 由 AbilityActivation stage 写 value。
    ComponentStorage<AbilityComponent> abilities_;
    /// effects_ 由 Effect stage 写 value。
    ComponentStorage<EffectComponent> effects_;
    /// projectiles_ 由 deferred spawn 与 projectile lifecycle 使用。
    ComponentStorage<ProjectileComponent> projectiles_;
    /// ai_ 由 AIIntent stage 写 value。
    ComponentStorage<AiComponent> ai_;
};

/// Storage specialization 返回 TransformComponent storage。
template <>
inline ComponentStorage<TransformComponent>& SimulationWorld::Storage<TransformComponent>() {
    return transforms_;
}

/// Storage specialization 返回 MotionComponent storage。
template <>
inline ComponentStorage<MotionComponent>& SimulationWorld::Storage<MotionComponent>() {
    return motions_;
}

/// Storage specialization 返回 ActorComponent storage。
template <>
inline ComponentStorage<ActorComponent>& SimulationWorld::Storage<ActorComponent>() {
    return actors_;
}

/// Storage specialization 返回 AttributeComponent storage。
template <>
inline ComponentStorage<AttributeComponent>& SimulationWorld::Storage<AttributeComponent>() {
    return attributes_;
}

/// Storage specialization 返回 AbilityComponent storage。
template <>
inline ComponentStorage<AbilityComponent>& SimulationWorld::Storage<AbilityComponent>() {
    return abilities_;
}

/// Storage specialization 返回 EffectComponent storage。
template <>
inline ComponentStorage<EffectComponent>& SimulationWorld::Storage<EffectComponent>() {
    return effects_;
}

/// Storage specialization 返回 ProjectileComponent storage。
template <>
inline ComponentStorage<ProjectileComponent>& SimulationWorld::Storage<ProjectileComponent>() {
    return projectiles_;
}

/// Storage specialization 返回 AiComponent storage。
template <>
inline ComponentStorage<AiComponent>& SimulationWorld::Storage<AiComponent>() {
    return ai_;
}

}  // namespace ihomeland::sim
