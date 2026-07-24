#pragma once

#include <compare>
#include <cstdint>
#include <limits>

namespace ihomeland::sim {

/// EntityGeneration 是 slot 复用计数；达到最大值后 slot 必须永久 retired。
using EntityGeneration = std::uint16_t;

/// EntityID 以固定宽度 world/index/generation 标识单个 SimulationWorld 中的一代实体。
///
/// 零值无效；规范排序依次比较 world、index、generation，不依赖地址或容器顺序。
struct EntityID final {
    /// world 标识 registry/reset generation，禁止跨 world 使用 handle。
    std::uint32_t world;
    /// index 是 registry 固定 slot 下标。
    std::uint32_t index;
    /// generation 在 slot 每次复用前 checked increment。
    EntityGeneration generation;

    /// operator== 比较完整 world/index/generation identity。
    [[nodiscard]] bool operator==(const EntityID&) const noexcept = default;

    /// operator<=> 提供 canonical identity 排序。
    [[nodiscard]] auto operator<=>(const EntityID&) const noexcept = default;

    /// IsValid 判断 ID 是否包含非零 world 与 generation。
    [[nodiscard]] bool IsValid() const noexcept {
        return world != 0 && generation != 0;
    }
};

/// MaxEntityGeneration 是 generation wrap 前最后一个可分配值。
inline constexpr EntityGeneration MaxEntityGeneration =
    std::numeric_limits<EntityGeneration>::max();

}  // namespace ihomeland::sim
