#pragma once

#include <cstdint>
#include <span>
#include <string>

namespace ihomeland::sim {

/// StateProjectionToken 是 Replication stage 的最小只读 actor state。
struct StateProjectionToken final {
    /// operator== 支持同 Tick跨会话 actor projection 一致性比较。
    bool operator==(const StateProjectionToken&) const = default;

    /// actor_id 是规范状态顺序的唯一主键。
    std::uint64_t actor_id;
    /// x_mm 是整数 world X。
    std::int64_t x_mm;
    /// y_mm 是整数 world Y。
    std::int64_t y_mm;
    /// z_mm 是整数 world Z。
    std::int64_t z_mm;
    /// yaw_millidegrees 是绕 world up axis 的规范量化角度。
    std::int32_t yaw_millidegrees{0};
    /// velocity_x_mm_per_second 是已提交的 world X 速度。
    std::int64_t velocity_x_mm_per_second{0};
    /// velocity_y_mm_per_second 是已提交的 world Y 速度。
    std::int64_t velocity_y_mm_per_second{0};
    /// velocity_z_mm_per_second 是已提交的 world Z 速度。
    std::int64_t velocity_z_mm_per_second{0};
    /// health_scaled 是当前 signed 64-bit scaled health。
    std::int64_t health_scaled;
    /// phase 是已提交的 Ability/AI/Boss phase token。
    std::uint32_t phase;
    /// alive 是 Death stage 已提交结果。
    bool alive;
    /// grounded 是 Movement/Physics stage 已提交的权威接地事实。
    bool grounded{false};
};

/// BattleEntityStateFlags 冻结 battle-wire-v1 snapshot state_flags registry。
struct BattleEntityStateFlags final {
    /// PhaseMask 保留既有低四位 gameplay phase token。
    static constexpr std::uint32_t PhaseMask = 0x0000000fU;
    /// Grounded 表示 Movement/Physics 已提交 authority ground contact。
    static constexpr std::uint32_t Grounded = 0x00000010U;
    /// Dead 表示 Death stage 已提交 entity death。
    static constexpr std::uint32_t Dead = 0x80000000U;
    /// KnownMask 是 producer 与 consumer 唯一允许的 bit set。
    static constexpr std::uint32_t KnownMask =
        PhaseMask | Grounded | Dead;
};

/// EventProjectionToken 是权威 gameplay event 的低敏规范值。
struct EventProjectionToken final {
    /// tick 是 event 产生的已提交 SimulationTick。
    std::uint64_t tick;
    /// kind 是 content/protocol 映射前的项目内闭合 token。
    std::uint16_t kind;
    /// source_actor_id 是非零 source，system event 可为零。
    std::uint64_t source_actor_id;
    /// target_actor_id 是 target，未绑定时为零。
    std::uint64_t target_actor_id;
    /// activation_id 是 Ability 相关性，非 Ability event 为零。
    std::uint64_t activation_id;
    /// value_scaled 是 event 的 signed 64-bit scaled 数值。
    std::int64_t value_scaled;
};

/// RejectionProjectionToken 是 command/gameplay 拒绝的稳定规范值。
struct RejectionProjectionToken final {
    /// tick 是拒绝发生的 SimulationTick。
    std::uint64_t tick;
    /// actor_id 是 command binding actor。
    std::uint64_t actor_id;
    /// input_tick 是被拒绝 command 的 InputTick。
    std::uint64_t input_tick;
    /// sequence 是同 actor/input 的稳定序列。
    std::uint64_t sequence;
    /// reason 是闭合稳定 rejection enum 的数值。
    std::uint16_t reason;
};

/// CapacityProjectionToken 是 hard limit 的规范 accounting 结果。
struct CapacityProjectionToken final {
    /// kind 是 queue/entity/query/history 等闭合容量分类。
    std::uint16_t kind;
    /// limit 是启动时固定硬上限。
    std::uint64_t limit;
    /// observed 是当前运行观察到的最大使用量。
    std::uint64_t observed;
    /// rejected 是超过 hard limit 的累计拒绝数。
    std::uint64_t rejected;
};

/// CanonicalGameplayProjection 是 Replication/evidence 共用的不可变结果。
struct CanonicalGameplayProjection final {
    /// text 是 locale-independent、无空白歧义的规范 token stream。
    std::string text;
    /// sha256 是 text 原始 bytes 的 lowercase SHA-256。
    std::string sha256;
};

/// ProjectCanonicalGameplay 只读复制并规范排序全部 projection tokens。
[[nodiscard]] CanonicalGameplayProjection ProjectCanonicalGameplay(
    std::span<const StateProjectionToken> states,
    std::span<const EventProjectionToken> events,
    std::span<const RejectionProjectionToken> rejections,
    std::span<const CapacityProjectionToken> capacities);

}  // namespace ihomeland::sim
