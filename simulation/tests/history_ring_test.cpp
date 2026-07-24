#include "ihomeland/sim/history/history_ring.hpp"

#include <array>
#include <iostream>
#include <stdexcept>
#include <vector>

namespace {

/// Require 把 history ring 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// Actor 构造指定 Tick 位置的最小历史投影。
[[nodiscard]] ihomeland::sim::HistoryActorProjection Actor(
    const std::uint64_t actor_id,
    const std::int64_t x_mm) {
    return {
        .actor_id = actor_id,
        .x_mm = x_mm,
        .y_mm = 0,
        .z_mm = 0,
        .yaw_millidegrees = 0,
        .hit_volume_id = 1,
        .movement_mode = 1,
        .alive = true};
}

/// Query 构造当前 generation 下的 melee 历史查询。
[[nodiscard]] ihomeland::sim::HistoryQuery Query(
    const std::uint64_t requested_tick,
    const std::uint64_t current_tick) {
    return {
        .assignment_generation = 1,
        .mapping_generation = 2,
        .requester_actor_id = 42,
        .target_actor_id = 99,
        .kind = ihomeland::sim::HistoryQueryKind::MeleeSweep,
        .requested_tick = requested_tick,
        .current_tick = current_tick,
        .maximum_lookback_ticks = 16};
}

/// TestRingAndClamp 验证 future clamp、只读 copy、overwrite/缺帧与 accounting。
void TestRingAndClamp() {
    const std::vector<std::uint64_t> requesters{42};
    ihomeland::sim::HistoryRing ring(1, 2, 16, 2, 8U * 1024U * 1024U, requesters);
    for (std::uint64_t tick = 1; tick <= 17; ++tick) {
        const std::array actors{
            Actor(99, static_cast<std::int64_t>(tick)),
            Actor(42, 0)};
        ring.Commit(tick, actors);
    }
    const auto clamped = ring.Query(Query(100, 17));
    Require(
        clamped.effective_tick == 17 && clamped.future_clamped &&
            clamped.actor.x_mm == 17,
        "history future clamp drifted");
    auto copy = clamped.actor;
    copy.x_mm = 999;
    Require(
        ring.Query(Query(17, 17)).actor.x_mm == 17,
        "history query result wrote back into ring");
    bool expired_rejected = false;
    try {
        static_cast<void>(ring.Query(Query(1, 17)));
    } catch (const ihomeland::sim::HistoryError& error) {
        Require(
            error.Code() == ihomeland::sim::HistoryErrorCode::Expired,
            "evicted history Tick returned wrong code");
        expired_rejected = true;
    }
    Require(expired_rejected, "evicted history Tick remained readable");
    const auto capacity = ring.CapacitySnapshot();
    Require(
        capacity.committed_frames == 16 &&
            capacity.reserved_bytes <= capacity.byte_limit &&
            capacity.used_bytes <= capacity.reserved_bytes,
        "history hard byte accounting drifted");
}

/// TestGapResetAndPolicy 验证缺帧、stale generation、actor policy 与 reset。
void TestGapResetAndPolicy() {
    const std::vector<std::uint64_t> requesters{42};
    ihomeland::sim::HistoryRing ring(1, 2, 16, 2, 1024 * 1024, requesters);
    const std::array tick_one{Actor(99, 1)};
    const std::array tick_three{Actor(99, 3)};
    ring.Commit(1, tick_one);
    ring.Commit(3, tick_three);
    bool missing_rejected = false;
    try {
        static_cast<void>(ring.Query(Query(2, 3)));
    } catch (const ihomeland::sim::HistoryError& error) {
        Require(
            error.Code() == ihomeland::sim::HistoryErrorCode::Missing,
            "missing history frame returned wrong code");
        missing_rejected = true;
    }
    Require(missing_rejected, "missing history frame was synthesized");
    auto unauthorized = Query(3, 3);
    unauthorized.requester_actor_id = 7;
    bool policy_rejected = false;
    try {
        static_cast<void>(ring.Query(unauthorized));
    } catch (const ihomeland::sim::HistoryError& error) {
        Require(
            error.Code() == ihomeland::sim::HistoryErrorCode::ActorPolicy,
            "history actor policy returned wrong code");
        policy_rejected = true;
    }
    Require(policy_rejected, "unauthorized history requester was accepted");
    ring.Reset(2, 3);
    auto stale = Query(1, 1);
    try {
        static_cast<void>(ring.Query(stale));
    } catch (const ihomeland::sim::HistoryError& error) {
        Require(
            error.Code() == ihomeland::sim::HistoryErrorCode::StaleGeneration &&
                ring.LatestTick() == 0,
            "history reset retained stale generation state");
        return;
    }
    throw std::runtime_error("stale history generation was accepted");
}

}  // namespace

/// main 执行 16-Tick ring、query policy、reset 与 hard accounting 回归。
int main() {
    try {
        TestRingAndClamp();
        TestGapResetAndPolicy();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
