#pragma once

#include "ihomeland/sim/ecs/entity_registry.hpp"

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <type_traits>
#include <utility>
#include <vector>

namespace ihomeland::sim {

/// ComponentStorage 为一个 component type 提供有界 sparse-set 与受控最小 view。
///
/// @tparam Component 必须是项目拥有的可移动 value type；storage 不持有第三方 handle。
template <typename Component>
class ComponentStorage final {
    static_assert(std::is_move_constructible_v<Component>, "Component must be movable");

public:
    /// 构造函数绑定唯一 registry，并一次性预留 hard capacity。
    ///
    /// @param registry 调用期间保留借用，必须长于 storage。
    /// @param capacity 允许同时存在的 component 最大数量，不得超过 registry capacity。
    ComponentStorage(EntityRegistry& registry, const std::uint32_t capacity)
        : registry_(&registry),
          sparse_(registry.Capacity(), MissingIndex),
          capacity_(capacity) {
        if (capacity == 0 || capacity > registry.Capacity()) {
            throw EcsError(EcsErrorCode::Capacity, "component capacity is invalid");
        }
        entities_.reserve(capacity);
        components_.reserve(capacity);
    }

    /// Add 转移 component 到 entity；重复、stale、cross-world、迭代中 mutation 或超限均失败。
    void Add(const EntityID entity, Component component) {
        RequireMutable();
        registry_->RequireAlive(entity);
        if (sparse_[entity.index] != MissingIndex) {
            throw EcsError(EcsErrorCode::Duplicate, "component already exists");
        }
        if (components_.size() >= capacity_) {
            throw EcsError(EcsErrorCode::Capacity, "component capacity exhausted");
        }
        const auto dense_index = static_cast<std::uint32_t>(components_.size());
        entities_.push_back(entity);
        try {
            components_.push_back(std::move(component));
        } catch (...) {
            entities_.pop_back();
            throw;
        }
        sparse_[entity.index] = dense_index;
        ++mutation_epoch_;
    }

    /// Remove 删除 entity 的 component，并以 swap-remove 保持有界 storage。
    ///
    /// view 迭代顺序不属于 gameplay 裁决顺序；调用方必须按稳定 identity 另行排序。
    void Remove(const EntityID entity) {
        RequireMutable();
        const auto dense_index = RequireDenseIndex(entity);
        const auto last_index = static_cast<std::uint32_t>(components_.size() - 1);
        if (dense_index != last_index) {
            components_[dense_index] = std::move(components_[last_index]);
            entities_[dense_index] = entities_[last_index];
            sparse_[entities_[dense_index].index] = dense_index;
        }
        components_.pop_back();
        entities_.pop_back();
        sparse_[entity.index] = MissingIndex;
        ++mutation_epoch_;
    }

    /// Has 对有效当前 entity 返回 component 是否存在；stale/cross-world handle 仍明确拒绝。
    [[nodiscard]] bool Has(const EntityID entity) const {
        registry_->RequireAlive(entity);
        return sparse_[entity.index] != MissingIndex &&
               entities_[sparse_[entity.index]] == entity;
    }

    /// Get 返回 storage 拥有 component 的借用引用；下次结构 mutation 后调用方不得继续保留。
    [[nodiscard]] Component& Get(const EntityID entity) {
        return components_[RequireDenseIndex(entity)];
    }

    /// Get 返回只读借用；下次结构 mutation 后调用方不得继续保留。
    [[nodiscard]] const Component& Get(const EntityID entity) const {
        return components_[RequireDenseIndex(entity)];
    }

    /// ForEach 在禁止结构 mutation 的作用域内访问当前最小 view。
    ///
    /// callback 可以修改 component value，但不得调用当前 storage 的 Add/Remove/Reset。
    template <typename Callback>
    void ForEach(Callback&& callback) {
        ++iteration_depth_;
        try {
            for (std::size_t index = 0; index < components_.size(); ++index) {
                callback(entities_[index], components_[index]);
            }
        } catch (...) {
            --iteration_depth_;
            throw;
        }
        --iteration_depth_;
    }

    /// Reset 清空 components 并重新绑定 registry 当前 world；只能在无 view 迭代时调用。
    void Reset() {
        RequireMutable();
        std::fill(sparse_.begin(), sparse_.end(), MissingIndex);
        entities_.clear();
        components_.clear();
        ++mutation_epoch_;
    }

    /// Size 返回当前 component 数。
    [[nodiscard]] std::uint32_t Size() const noexcept {
        return static_cast<std::uint32_t>(components_.size());
    }

    /// Capacity 返回构造时固定的 hard capacity。
    [[nodiscard]] std::uint32_t Capacity() const noexcept {
        return capacity_;
    }

    /// MutationEpoch 返回每次成功结构 mutation 后递增的 view invalidation identity。
    [[nodiscard]] std::uint64_t MutationEpoch() const noexcept {
        return mutation_epoch_;
    }

private:
    /// MissingIndex 是 sparse entry 不存在的 sentinel。
    static constexpr std::uint32_t MissingIndex = std::numeric_limits<std::uint32_t>::max();

    /// RequireMutable 拒绝在当前 storage view 迭代中执行结构 mutation。
    void RequireMutable() const {
        if (iteration_depth_ != 0) {
            throw EcsError(EcsErrorCode::IterationMutation, "component mutation during view iteration");
        }
    }

    /// RequireDenseIndex 验证 entity 后返回其 dense index。
    [[nodiscard]] std::uint32_t RequireDenseIndex(const EntityID entity) const {
        registry_->RequireAlive(entity);
        const auto dense_index = sparse_[entity.index];
        if (dense_index == MissingIndex || dense_index >= entities_.size() ||
            entities_[dense_index] != entity) {
            throw EcsError(EcsErrorCode::Missing, "component is missing");
        }
        return dense_index;
    }

    /// registry_ 是构造时借用的唯一 per-world entity owner。
    EntityRegistry* registry_;
    /// sparse_ 以 entity index 定位 dense slot，长度固定为 registry capacity。
    std::vector<std::uint32_t> sparse_;
    /// entities_ 与 components_ 保持相同长度并拥有 dense identity。
    std::vector<EntityID> entities_;
    /// components_ 唯一拥有 component values。
    std::vector<Component> components_;
    /// capacity_ 是构造后不可变的 hard limit。
    std::uint32_t capacity_;
    /// iteration_depth_ 阻止 callback 内结构 mutation。
    std::uint32_t iteration_depth_{0};
    /// mutation_epoch_ 供跨调用 view/cache 检测失效。
    std::uint64_t mutation_epoch_{0};
};

}  // namespace ihomeland::sim
