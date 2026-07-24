#pragma once

#include "ihomeland/sim/ecs/entity.hpp"

#include <cstddef>
#include <cstdint>
#include <stdexcept>
#include <vector>

namespace ihomeland::sim {

/// EcsErrorCode 是 registry、component storage 与 structural barrier 共享的稳定失败分类。
enum class EcsErrorCode : std::uint8_t {
    /// Capacity 表示预分配 hard limit 已耗尽。
    Capacity,
    /// InvalidEntity 表示零值、越界或从未分配的 handle。
    InvalidEntity,
    /// StaleEntity 表示 slot 已销毁或 generation 已变化。
    StaleEntity,
    /// CrossWorld 表示 handle 不属于当前 world/reset generation。
    CrossWorld,
    /// Duplicate 表示同一 component 或 structural intent 已存在。
    Duplicate,
    /// Missing 表示目标 component 不存在。
    Missing,
    /// IterationMutation 表示 view 迭代期间尝试结构变化。
    IterationMutation,
    /// Conflict 表示同一 barrier 内的结构命令无法同时成立。
    Conflict,
};

/// EcsError 携带稳定失败码；异常发生前不得产生部分结构 mutation。
class EcsError final : public std::runtime_error {
public:
    /// 构造函数保存 code 与低敏诊断。
    EcsError(EcsErrorCode code, const char* message);

    /// Code 返回可由 gameplay rejection 和测试判定的错误类别。
    [[nodiscard]] EcsErrorCode Code() const noexcept;

private:
    /// code_ 在异常构造后不可变。
    EcsErrorCode code_;
};

/// EntityRegistry 唯一拥有 per-world slot generation、free-list 与 retirement。
///
/// Registry 不拥有 component、协议或持久化；调用方必须在销毁 slot 前通过唯一 barrier
/// 移除 components。该类型只允许 simulation worker 访问，不提供内部锁。
class EntityRegistry final {
public:
    /// 构造函数预分配 capacity 个 slot；world 必须非零且 capacity 必须为正数。
    EntityRegistry(std::uint32_t world, std::uint32_t capacity);

    /// Create 从有界 free-list 分配 handle；容量耗尽时不扩容。
    [[nodiscard]] EntityID Create();

    /// Destroy 使 handle 立即 stale，并在 generation 未耗尽时回收 slot。
    ///
    /// generation 达到 MaxEntityGeneration 时 slot 永久 retired，不发生 wrap。
    void Destroy(EntityID entity);

    /// RequireAlive 验证完整 world/index/generation 且 slot 当前存活。
    void RequireAlive(EntityID entity) const;

    /// IsAlive 对有效当前 handle 返回 true；invalid、stale 或 cross-world 均返回 false。
    [[nodiscard]] bool IsAlive(EntityID entity) const noexcept;

    /// Reset 清空当前 world 并绑定新的非零 world identity，使全部旧 handle 稳定失效。
    ///
    /// new_world 必须不同于当前 world；reset 后 slot generation 从 1 开始，retirement 清零。
    void Reset(std::uint32_t new_world);

    /// World 返回当前 registry/reset generation identity。
    [[nodiscard]] std::uint32_t World() const noexcept;

    /// Capacity 返回构造时固定的 slot 数。
    [[nodiscard]] std::uint32_t Capacity() const noexcept;

    /// AliveCount 返回当前存活实体数。
    [[nodiscard]] std::uint32_t AliveCount() const noexcept;

    /// RetiredCount 返回因 generation 耗尽而永久不可复用的 slot 数。
    [[nodiscard]] std::uint32_t RetiredCount() const noexcept;

private:
    /// Slot 保存单个 index 的当前 generation 与生命周期标志。
    struct Slot final {
        /// generation 为下一次或当前分配使用的非零值。
        EntityGeneration generation{1};
        /// alive 表示当前 generation 已分配。
        bool alive{false};
        /// retired 表示 generation 已耗尽且禁止再次进入 free-list。
        bool retired{false};
    };

    /// RebuildFreeList 重建确定性的逆序 free-list，使 Create 首先分配最小 index。
    void RebuildFreeList();

    /// world_ 标识当前 registry/reset generation。
    std::uint32_t world_;
    /// slots_ 在构造后长度固定，不随 gameplay 扩容。
    std::vector<Slot> slots_;
    /// free_list_ 只包含非 alive、非 retired slot，back 是下一分配 index。
    std::vector<std::uint32_t> free_list_;
    /// alive_count_ 避免扫描 slots 计算当前用量。
    std::uint32_t alive_count_{0};
    /// retired_count_ 记录永久容量损失并参与 overload evidence。
    std::uint32_t retired_count_{0};
};

}  // namespace ihomeland::sim
