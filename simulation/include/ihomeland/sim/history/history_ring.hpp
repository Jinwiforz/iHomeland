#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <stdexcept>
#include <vector>

namespace ihomeland::sim {

/// HistoryErrorCode 是 history commit/query 的稳定失败分类。
enum class HistoryErrorCode : std::uint8_t {
    /// InvalidConfig 表示容量、generation 或 hard byte target 非法。
    InvalidConfig,
    /// NonMonotonicTick 表示 commit 没有推进 SimulationTick。
    NonMonotonicTick,
    /// Capacity 表示 frame actor/byte hard limit 被超过。
    Capacity,
    /// StaleGeneration 表示 assignment 或 mapping generation 已切换。
    StaleGeneration,
    /// Expired 表示 requested Tick 已被 ring 淘汰或超出 policy window。
    Expired,
    /// Missing 表示窗口内没有该 Tick 或 target actor 投影。
    Missing,
    /// ActorPolicy 表示 requester 没有 history query 权限。
    ActorPolicy,
    /// InvalidQuery 表示 query identity/kind/current Tick 不合法。
    InvalidQuery,
};

/// HistoryError 保留可机器判断的 history 失败原因。
class HistoryError final : public std::runtime_error {
public:
    /// 构造函数绑定稳定错误码和低敏诊断文本。
    HistoryError(HistoryErrorCode code, const char* message);

    /// Code 返回不依赖文本的稳定错误码。
    [[nodiscard]] HistoryErrorCode Code() const noexcept;

private:
    /// code_ 是当前 history 失败分类。
    HistoryErrorCode code_;
};

/// HistoryQueryKind 是首版延迟补偿允许的闭合只读查询集。
enum class HistoryQueryKind : std::uint8_t {
    /// MeleeSweep 查询 actor 历史 hit projection。
    MeleeSweep,
    /// ProjectileRay 查询 actor 历史 ray projection。
    ProjectileRay,
};

/// HistoryActorProjection 是延迟查询所需的最小固定宽度状态。
struct HistoryActorProjection final {
    /// actor_id 是 frame 内非零稳定主键。
    std::uint64_t actor_id;
    /// x_mm 是 world X。
    std::int64_t x_mm;
    /// y_mm 是 world Y。
    std::int64_t y_mm;
    /// z_mm 是 world Z。
    std::int64_t z_mm;
    /// yaw_millidegrees 是量化朝向。
    std::int32_t yaw_millidegrees;
    /// hit_volume_id 是 content 中非零稳定 collision volume。
    std::uint32_t hit_volume_id;
    /// movement_mode 是历史 query 所需的闭合状态 token。
    std::uint8_t movement_mode;
    /// alive 表示该 Tick Death stage 后 actor 是否存活。
    bool alive;

    /// operator== 用于验证 query 不回写 ring。
    bool operator==(const HistoryActorProjection&) const = default;
};

/// HistoryQuery 绑定 generation、requester、kind、当前 Tick 与回看窗口。
struct HistoryQuery final {
    /// assignment_generation 必须等于 ring 当前 generation。
    std::uint64_t assignment_generation;
    /// mapping_generation 必须等于 ring 当前 generation。
    std::uint64_t mapping_generation;
    /// requester_actor_id 是已授权发起者。
    std::uint64_t requester_actor_id;
    /// target_actor_id 是只读查询目标。
    std::uint64_t target_actor_id;
    /// kind 是允许的 query policy。
    HistoryQueryKind kind;
    /// requested_tick 是客户端观察经 mapping 后的候选 Tick。
    std::uint64_t requested_tick;
    /// current_tick 是当前 HitDetection pipeline Tick。
    std::uint64_t current_tick;
    /// maximum_lookback_ticks 是当前 Ability/query kind 的正整数窗口。
    std::uint64_t maximum_lookback_ticks;
};

/// HistoryQueryResult 是 future clamp 后的只读 actor projection copy。
struct HistoryQueryResult final {
    /// effective_tick 是 min(requested_tick, current_tick)。
    std::uint64_t effective_tick;
    /// future_clamped 表示请求指向未来且已 clamp。
    bool future_clamped;
    /// actor 是 ring 中 projection 的值复制，不暴露 slot 引用。
    HistoryActorProjection actor;
};

/// HistoryCapacitySnapshot 是 ring hard accounting 的规范 token。
struct HistoryCapacitySnapshot final {
    /// frame_limit 是 [1, 16] 内的固定 slot 数。
    std::uint32_t frame_limit;
    /// actor_limit_per_frame 是每帧预留的固定 actor 数。
    std::uint32_t actor_limit_per_frame;
    /// byte_limit 是不超过 8 MiB 的配置硬上限。
    std::size_t byte_limit;
    /// reserved_bytes 是构造时固定预留 accounting。
    std::size_t reserved_bytes;
    /// used_bytes 是当前有效 frame/actor 的固定宽度 accounting。
    std::size_t used_bytes;
    /// committed_frames 是当前有效 slot 数。
    std::uint32_t committed_frames;
};

/// HistoryRing 保存最多 16 Tick 的 generation-safe 固定宽度投影。
class HistoryRing final {
public:
    /// 构造函数预留全部 slots/actors 并拒绝超过 8 MiB hard target。
    HistoryRing(
        std::uint64_t assignment_generation,
        std::uint64_t mapping_generation,
        std::uint32_t frame_capacity,
        std::uint32_t actor_capacity_per_frame,
        std::size_t byte_limit,
        std::span<const std::uint64_t> authorized_requesters);

    /// Commit 复制借用的 actor projection，按 ActorID 规范化并原子覆盖 ring slot。
    ///
    /// @param tick 已提交且严格递增的非零 SimulationTick。
    /// @param actors 只在调用期间借用，数量不得超过构造时 hard capacity。
    void Commit(
        std::uint64_t tick,
        std::span<const HistoryActorProjection> actors);

    /// Query 对 future 做 clamp，对 stale/expired/missing/policy 稳定拒绝。
    [[nodiscard]] HistoryQueryResult Query(const HistoryQuery& query) const;

    /// Reset 清空 slots 并绑定新 assignment/mapping generation。
    void Reset(
        std::uint64_t assignment_generation,
        std::uint64_t mapping_generation);

    /// CapacitySnapshot 返回不暴露 gameplay values 的 hard accounting。
    [[nodiscard]] HistoryCapacitySnapshot CapacitySnapshot() const noexcept;

    /// LatestTick 返回最后提交 Tick，空 ring 返回零。
    [[nodiscard]] std::uint64_t LatestTick() const noexcept;

private:
    /// SlotIdentity 防止 overwrite/reset 后误读 stale vector。
    struct SlotIdentity final {
        /// assignment_generation 是写入时 generation。
        std::uint64_t assignment_generation;
        /// mapping_generation 是写入时 generation。
        std::uint64_t mapping_generation;
        /// tick 是该 slot 当前代表的 Tick。
        std::uint64_t tick;
        /// write_generation 每次 commit/reset 单调增加。
        std::uint64_t write_generation;
    };

    /// Slot 组合 generation-safe identity 与只读 frame value。
    struct Slot final {
        /// identity 为空表示 slot 尚未写入。
        std::optional<SlotIdentity> identity;
        /// actors 预留到固定 hard capacity。
        std::vector<HistoryActorProjection> actors;
    };

    /// assignment_generation_ 是当前实例 assignment fence。
    std::uint64_t assignment_generation_;
    /// mapping_generation_ 是当前 InputTick mapping fence。
    std::uint64_t mapping_generation_;
    /// frame_capacity_ 是不超过 16 的固定 ring slots。
    std::uint32_t frame_capacity_;
    /// actor_capacity_per_frame_ 是每帧固定 hard limit。
    std::uint32_t actor_capacity_per_frame_;
    /// byte_limit_ 是配置的 hard accounting 上限。
    std::size_t byte_limit_;
    /// reserved_bytes_ 是构造时预留的固定 accounting。
    std::size_t reserved_bytes_;
    /// slots_ 在构造后不扩容。
    std::vector<Slot> slots_;
    /// authorized_requesters_ 是规范排序的 query actor policy。
    std::vector<std::uint64_t> authorized_requesters_;
    /// latest_tick_ 是最后成功提交 Tick。
    std::uint64_t latest_tick_{0};
    /// write_generation_ 防止 reset/overwrite stale slot identity。
    std::uint64_t write_generation_{0};
    /// committed_frames_ 是当前 generation 有效 slot 数。
    std::uint32_t committed_frames_{0};
    /// used_actor_count_ 用于固定宽度 byte accounting。
    std::size_t used_actor_count_{0};
};

}  // namespace ihomeland::sim
