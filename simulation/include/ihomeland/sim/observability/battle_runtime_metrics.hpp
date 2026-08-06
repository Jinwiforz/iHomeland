#pragma once

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <limits>

namespace ihomeland::sim {

/// BattleMetricLane 是资格报告允许区分的闭合 transport lane。
enum class BattleMetricLane : std::uint8_t {
    /// Raw 表示不重传的 snapshot/input lane。
    Raw,
    /// Kcp 表示 KCP 可靠事件 lane。
    Kcp,
};

/// BattleMetricDirection 是以 simulation node 为参照的流量方向。
enum class BattleMetricDirection : std::uint8_t {
    /// Ingress 表示进入 simulation node 的 datagram。
    Ingress,
    /// Egress 表示离开 simulation node 的 datagram。
    Egress,
};

/// BattleCloseReasonCategory 是低基数且不携带业务身份的终结分类。
enum class BattleCloseReasonCategory : std::uint8_t {
    /// Normal 表示双方完成受控关闭。
    Normal,
    /// Authentication 表示认证、AEAD 或 replay gate 失败。
    Authentication,
    /// Timeout 表示 handshake、message 或 inactivity deadline 到期。
    Timeout,
    /// Resource 表示有界资源或速率 gate 终结 session。
    Resource,
    /// Lifecycle 表示 assignment、VisitSession 或 process replacement。
    Lifecycle,
    /// Transport 表示 socket、KCP dead-link 或 endpoint failure。
    Transport,
    /// Internal 表示不可恢复的内部不变量失败。
    Internal,
};

/// BattleRuntimeMetricsSnapshot 是不含身份、endpoint、secret 或 payload 的累计快照。
struct BattleRuntimeMetricsSnapshot final {
    /// raw_ingress_bytes 是已接受 raw 入站 bytes。
    std::uint64_t raw_ingress_bytes;
    /// raw_ingress_packets 是已接受 raw 入站 packets。
    std::uint64_t raw_ingress_packets;
    /// raw_egress_bytes 是已生成 raw 出站 bytes。
    std::uint64_t raw_egress_bytes;
    /// raw_egress_packets 是已生成 raw 出站 packets。
    std::uint64_t raw_egress_packets;
    /// kcp_ingress_bytes 是已接受 KCP 入站 bytes。
    std::uint64_t kcp_ingress_bytes;
    /// kcp_ingress_packets 是已接受 KCP 入站 packets。
    std::uint64_t kcp_ingress_packets;
    /// kcp_egress_bytes 是已生成 KCP 出站 bytes。
    std::uint64_t kcp_egress_bytes;
    /// kcp_egress_packets 是已生成 KCP 出站 packets。
    std::uint64_t kcp_egress_packets;
    /// dropped_packets 是故障注入前 runtime 自身丢弃累计。
    std::uint64_t dropped_packets;
    /// rejected_packets 是 closed protocol/resource gate 拒绝累计。
    std::uint64_t rejected_packets;
    /// expired_messages 是 transport deadline 终结累计。
    std::uint64_t expired_messages;
    /// kcp_retransmits 是 KCP xmit 计数的单调增量累计。
    std::uint64_t kcp_retransmits;
    /// ingress_queue_high_watermark 是 node/session 入站 items 峰值。
    std::uint64_t ingress_queue_high_watermark;
    /// egress_queue_high_watermark 是 node/session 出站 items 峰值。
    std::uint64_t egress_queue_high_watermark;
    /// kcp_queue_high_watermark 是 application、inflight 与 waiting 中的最大项数。
    std::uint64_t kcp_queue_high_watermark;
    /// maximum_tick_duration_ns 是完整 observer Tick 的最大耗时。
    std::uint64_t maximum_tick_duration_ns;
    /// tick_debt_high_watermark 是 worker 观察到的最大待处理 clock credits。
    std::uint64_t tick_debt_high_watermark;
    /// instance_memory_bytes 是 live instance owners 的实际预留 accounting 峰值。
    std::uint64_t instance_memory_bytes;
    /// history_memory_bytes 是 live HistoryRing 的实际预留 accounting 峰值。
    std::uint64_t history_memory_bytes;
    /// rebinds 是成功提交 endpoint generation 的累计次数。
    std::uint64_t rebinds;
    /// rekeys 是成功提交 key epoch 的累计次数。
    std::uint64_t rekeys;
    /// combat_actor_high_watermark 是公开 player/monster/Boss entity 峰值。
    std::uint64_t combat_actor_high_watermark;
    /// combat_projectile_high_watermark 是公开 projectile entity 峰值。
    std::uint64_t combat_projectile_high_watermark;
    /// combat_ability_events 是已提交 ability reliable events 累计。
    std::uint64_t combat_ability_events;
    /// combat_lifecycle_events 是已提交 lifecycle reliable events 累计。
    std::uint64_t combat_lifecycle_events;
    /// encounter_completions 是 Boss defeat 暂态首次提交累计，不代表 settlement。
    std::uint64_t encounter_completions;
    /// close_normal 是 normal 分类终结累计。
    std::uint64_t close_normal;
    /// close_authentication 是 authentication 分类终结累计。
    std::uint64_t close_authentication;
    /// close_timeout 是 timeout 分类终结累计。
    std::uint64_t close_timeout;
    /// close_resource 是 resource 分类终结累计。
    std::uint64_t close_resource;
    /// close_lifecycle 是 lifecycle 分类终结累计。
    std::uint64_t close_lifecycle;
    /// close_transport 是 transport 分类终结累计。
    std::uint64_t close_transport;
    /// close_internal 是 internal 分类终结累计。
    std::uint64_t close_internal;
};

/// BattleRuntimeMetrics 汇总 transport 与 simulation owner 的低敏单调计数。
///
/// 写入仅使用饱和原子操作，snapshot 不清零也不阻塞 runtime。动态 identity、标签和值
/// 均不进入该 owner，避免资格入口形成新的高基数管理面。
class BattleRuntimeMetrics final {
public:
    /// RecordDatagram 累计一个已接受或已生成的 lane datagram。
    void RecordDatagram(
        BattleMetricDirection direction,
        BattleMetricLane lane,
        std::size_t bytes) noexcept {
        auto* byte_counter = &raw_ingress_bytes_;
        auto* packet_counter = &raw_ingress_packets_;
        if (lane == BattleMetricLane::Raw &&
            direction == BattleMetricDirection::Egress) {
            byte_counter = &raw_egress_bytes_;
            packet_counter = &raw_egress_packets_;
        } else if (lane == BattleMetricLane::Kcp &&
                   direction == BattleMetricDirection::Ingress) {
            byte_counter = &kcp_ingress_bytes_;
            packet_counter = &kcp_ingress_packets_;
        } else if (lane == BattleMetricLane::Kcp &&
                   direction == BattleMetricDirection::Egress) {
            byte_counter = &kcp_egress_bytes_;
            packet_counter = &kcp_egress_packets_;
        }
        AddSaturated(*byte_counter, static_cast<std::uint64_t>(bytes));
        AddSaturated(*packet_counter, 1);
    }

    /// RecordDrop 累计 runtime 主动丢弃的 datagram。
    void RecordDrop(const std::uint64_t count = 1) noexcept {
        AddSaturated(dropped_packets_, count);
    }

    /// RecordReject 累计 protocol、authority 或资源拒绝。
    void RecordReject(const std::uint64_t count = 1) noexcept {
        AddSaturated(rejected_packets_, count);
    }

    /// RecordExpiry 累计 deadline 终结的 transport message。
    void RecordExpiry(const std::uint64_t count = 1) noexcept {
        AddSaturated(expired_messages_, count);
    }

    /// RecordKcpRetransmits 累计 KCP xmit 单调增量。
    void RecordKcpRetransmits(const std::uint64_t count) noexcept {
        AddSaturated(kcp_retransmits_, count);
    }

    /// ObserveIngressQueue 更新入站 queue 峰值。
    void ObserveIngressQueue(const std::size_t items) noexcept {
        ObserveMaximum(
            ingress_queue_high_watermark_,
            static_cast<std::uint64_t>(items));
    }

    /// ObserveEgressQueue 更新出站 queue 峰值。
    void ObserveEgressQueue(const std::size_t items) noexcept {
        ObserveMaximum(
            egress_queue_high_watermark_,
            static_cast<std::uint64_t>(items));
    }

    /// ObserveKcpQueue 更新 KCP application/inflight/waiting 峰值。
    void ObserveKcpQueue(const std::size_t items) noexcept {
        ObserveMaximum(
            kcp_queue_high_watermark_,
            static_cast<std::uint64_t>(items));
    }

    /// ObserveTick 更新完整 Tick 耗时与 debt 峰值。
    void ObserveTick(
        const std::uint64_t duration_ns,
        const std::uint64_t debt) noexcept {
        ObserveMaximum(maximum_tick_duration_ns_, duration_ns);
        ObserveMaximum(tick_debt_high_watermark_, debt);
    }

    /// ObserveMemory 更新 live instance/history 实际预留 accounting 峰值。
    void ObserveMemory(
        const std::size_t instance_bytes,
        const std::size_t history_bytes) noexcept {
        ObserveMaximum(
            instance_memory_bytes_,
            static_cast<std::uint64_t>(instance_bytes));
        ObserveMaximum(
            history_memory_bytes_,
            static_cast<std::uint64_t>(history_bytes));
    }

    /// RecordRebind 累计成功 endpoint generation 切换。
    void RecordRebind() noexcept {
        AddSaturated(rebinds_, 1);
    }

    /// RecordRekey 累计成功 key epoch 切换。
    void RecordRekey() noexcept {
        AddSaturated(rekeys_, 1);
    }

    /// ObserveCombat 更新低敏 entity 峰值与本 Tick event outcome。
    void ObserveCombat(
        const std::size_t actor_entities,
        const std::size_t projectile_entities,
        const std::size_t ability_events,
        const std::size_t lifecycle_events,
        const bool encounter_completed_now) noexcept {
        ObserveMaximum(
            combat_actor_high_watermark_,
            static_cast<std::uint64_t>(actor_entities));
        ObserveMaximum(
            combat_projectile_high_watermark_,
            static_cast<std::uint64_t>(projectile_entities));
        AddSaturated(
            combat_ability_events_,
            static_cast<std::uint64_t>(ability_events));
        AddSaturated(
            combat_lifecycle_events_,
            static_cast<std::uint64_t>(lifecycle_events));
        if (encounter_completed_now) {
            AddSaturated(encounter_completions_, 1);
        }
    }

    /// RecordClose 累计一个闭合的低敏终结分类。
    void RecordClose(const BattleCloseReasonCategory reason) noexcept {
        switch (reason) {
            case BattleCloseReasonCategory::Normal:
                AddSaturated(close_normal_, 1);
                break;
            case BattleCloseReasonCategory::Authentication:
                AddSaturated(close_authentication_, 1);
                break;
            case BattleCloseReasonCategory::Timeout:
                AddSaturated(close_timeout_, 1);
                break;
            case BattleCloseReasonCategory::Resource:
                AddSaturated(close_resource_, 1);
                break;
            case BattleCloseReasonCategory::Lifecycle:
                AddSaturated(close_lifecycle_, 1);
                break;
            case BattleCloseReasonCategory::Transport:
                AddSaturated(close_transport_, 1);
                break;
            case BattleCloseReasonCategory::Internal:
                AddSaturated(close_internal_, 1);
                break;
        }
    }

    /// Snapshot 返回不清零的近似原子低敏投影。
    [[nodiscard]] BattleRuntimeMetricsSnapshot Snapshot() const noexcept {
        return BattleRuntimeMetricsSnapshot{
            .raw_ingress_bytes = raw_ingress_bytes_.load(),
            .raw_ingress_packets = raw_ingress_packets_.load(),
            .raw_egress_bytes = raw_egress_bytes_.load(),
            .raw_egress_packets = raw_egress_packets_.load(),
            .kcp_ingress_bytes = kcp_ingress_bytes_.load(),
            .kcp_ingress_packets = kcp_ingress_packets_.load(),
            .kcp_egress_bytes = kcp_egress_bytes_.load(),
            .kcp_egress_packets = kcp_egress_packets_.load(),
            .dropped_packets = dropped_packets_.load(),
            .rejected_packets = rejected_packets_.load(),
            .expired_messages = expired_messages_.load(),
            .kcp_retransmits = kcp_retransmits_.load(),
            .ingress_queue_high_watermark =
                ingress_queue_high_watermark_.load(),
            .egress_queue_high_watermark =
                egress_queue_high_watermark_.load(),
            .kcp_queue_high_watermark = kcp_queue_high_watermark_.load(),
            .maximum_tick_duration_ns = maximum_tick_duration_ns_.load(),
            .tick_debt_high_watermark = tick_debt_high_watermark_.load(),
            .instance_memory_bytes = instance_memory_bytes_.load(),
            .history_memory_bytes = history_memory_bytes_.load(),
            .rebinds = rebinds_.load(),
            .rekeys = rekeys_.load(),
            .combat_actor_high_watermark =
                combat_actor_high_watermark_.load(),
            .combat_projectile_high_watermark =
                combat_projectile_high_watermark_.load(),
            .combat_ability_events = combat_ability_events_.load(),
            .combat_lifecycle_events = combat_lifecycle_events_.load(),
            .encounter_completions = encounter_completions_.load(),
            .close_normal = close_normal_.load(),
            .close_authentication = close_authentication_.load(),
            .close_timeout = close_timeout_.load(),
            .close_resource = close_resource_.load(),
            .close_lifecycle = close_lifecycle_.load(),
            .close_transport = close_transport_.load(),
            .close_internal = close_internal_.load(),
        };
    }

private:
    /// AddSaturated 避免长时运行中无符号计数回绕成更小值。
    static void AddSaturated(
        std::atomic<std::uint64_t>& target,
        const std::uint64_t increment) noexcept {
        auto current = target.load();
        while (current != std::numeric_limits<std::uint64_t>::max()) {
            const auto available =
                std::numeric_limits<std::uint64_t>::max() - current;
            const auto next =
                increment > available
                    ? std::numeric_limits<std::uint64_t>::max()
                    : current + increment;
            if (target.compare_exchange_weak(current, next)) {
                return;
            }
        }
    }

    /// ObserveMaximum 以单调 CAS 更新峰值。
    static void ObserveMaximum(
        std::atomic<std::uint64_t>& target,
        const std::uint64_t candidate) noexcept {
        auto current = target.load();
        while (candidate > current &&
               !target.compare_exchange_weak(current, candidate)) {
        }
    }

    /// raw_ingress_bytes_ 保存已接受 raw 入站 bytes。
    std::atomic<std::uint64_t> raw_ingress_bytes_{};
    /// raw_ingress_packets_ 保存已接受 raw 入站 packets。
    std::atomic<std::uint64_t> raw_ingress_packets_{};
    /// raw_egress_bytes_ 保存已生成 raw 出站 bytes。
    std::atomic<std::uint64_t> raw_egress_bytes_{};
    /// raw_egress_packets_ 保存已生成 raw 出站 packets。
    std::atomic<std::uint64_t> raw_egress_packets_{};
    /// kcp_ingress_bytes_ 保存已接受 KCP 入站 bytes。
    std::atomic<std::uint64_t> kcp_ingress_bytes_{};
    /// kcp_ingress_packets_ 保存已接受 KCP 入站 packets。
    std::atomic<std::uint64_t> kcp_ingress_packets_{};
    /// kcp_egress_bytes_ 保存已生成 KCP 出站 bytes。
    std::atomic<std::uint64_t> kcp_egress_bytes_{};
    /// kcp_egress_packets_ 保存已生成 KCP 出站 packets。
    std::atomic<std::uint64_t> kcp_egress_packets_{};
    /// dropped_packets_ 保存 runtime 主动丢弃累计。
    std::atomic<std::uint64_t> dropped_packets_{};
    /// rejected_packets_ 保存 closed gate 拒绝累计。
    std::atomic<std::uint64_t> rejected_packets_{};
    /// expired_messages_ 保存 deadline 终结累计。
    std::atomic<std::uint64_t> expired_messages_{};
    /// kcp_retransmits_ 保存 KCP xmit 单调增量。
    std::atomic<std::uint64_t> kcp_retransmits_{};
    /// ingress_queue_high_watermark_ 保存入站 items 峰值。
    std::atomic<std::uint64_t> ingress_queue_high_watermark_{};
    /// egress_queue_high_watermark_ 保存出站 items 峰值。
    std::atomic<std::uint64_t> egress_queue_high_watermark_{};
    /// kcp_queue_high_watermark_ 保存 KCP items 峰值。
    std::atomic<std::uint64_t> kcp_queue_high_watermark_{};
    /// maximum_tick_duration_ns_ 保存完整 Tick 最大耗时。
    std::atomic<std::uint64_t> maximum_tick_duration_ns_{};
    /// tick_debt_high_watermark_ 保存最大待处理 clock credits。
    std::atomic<std::uint64_t> tick_debt_high_watermark_{};
    /// instance_memory_bytes_ 保存 live instance 实际预留 accounting 峰值。
    std::atomic<std::uint64_t> instance_memory_bytes_{};
    /// history_memory_bytes_ 保存 live history 实际预留 accounting 峰值。
    std::atomic<std::uint64_t> history_memory_bytes_{};
    /// rebinds_ 保存成功 endpoint generation 切换累计。
    std::atomic<std::uint64_t> rebinds_{};
    /// rekeys_ 保存成功 key epoch 切换累计。
    std::atomic<std::uint64_t> rekeys_{};
    /// combat_actor_high_watermark_ 保存公开 non-projectile entity 峰值。
    std::atomic<std::uint64_t> combat_actor_high_watermark_{};
    /// combat_projectile_high_watermark_ 保存公开 projectile entity 峰值。
    std::atomic<std::uint64_t> combat_projectile_high_watermark_{};
    /// combat_ability_events_ 保存 committed reliable ability event 累计。
    std::atomic<std::uint64_t> combat_ability_events_{};
    /// combat_lifecycle_events_ 保存 committed reliable lifecycle event 累计。
    std::atomic<std::uint64_t> combat_lifecycle_events_{};
    /// encounter_completions_ 保存 encounter-complete 首次 transition 累计。
    std::atomic<std::uint64_t> encounter_completions_{};
    /// close_normal_ 保存 normal 终结累计。
    std::atomic<std::uint64_t> close_normal_{};
    /// close_authentication_ 保存 authentication 终结累计。
    std::atomic<std::uint64_t> close_authentication_{};
    /// close_timeout_ 保存 timeout 终结累计。
    std::atomic<std::uint64_t> close_timeout_{};
    /// close_resource_ 保存 resource 终结累计。
    std::atomic<std::uint64_t> close_resource_{};
    /// close_lifecycle_ 保存 lifecycle 终结累计。
    std::atomic<std::uint64_t> close_lifecycle_{};
    /// close_transport_ 保存 transport 终结累计。
    std::atomic<std::uint64_t> close_transport_{};
    /// close_internal_ 保存 internal 终结累计。
    std::atomic<std::uint64_t> close_internal_{};
};

}  // namespace ihomeland::sim
