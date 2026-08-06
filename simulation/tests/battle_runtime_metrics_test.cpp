#include "ihomeland/sim/observability/battle_runtime_metrics.hpp"

#include <array>
#include <cstdint>
#include <iostream>
#include <limits>
#include <stdexcept>
#include <thread>

namespace {

using ihomeland::sim::BattleCloseReasonCategory;
using ihomeland::sim::BattleMetricDirection;
using ihomeland::sim::BattleMetricLane;
using ihomeland::sim::BattleRuntimeMetrics;

/// Require 在资格 metrics 不变量失败时终止当前 test executable。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// TestConcurrentMonotonicSnapshot 验证累计、高水位、饱和与不清零读取。
void TestConcurrentMonotonicSnapshot() {
    BattleRuntimeMetrics metrics;
    constexpr std::size_t worker_count = 8;
    constexpr std::uint64_t samples_per_worker = 1'000;
    constexpr std::size_t raw_packet_bytes = 128;
    constexpr std::size_t kcp_packet_bytes = 256;
    std::array<std::jthread, worker_count> workers;
    for (auto& worker : workers) {
        worker = std::jthread([&metrics] {
            for (std::uint64_t sample = 0;
                 sample < samples_per_worker;
                 ++sample) {
                metrics.RecordDatagram(
                    BattleMetricDirection::Ingress,
                    BattleMetricLane::Raw,
                    raw_packet_bytes);
                metrics.RecordDatagram(
                    BattleMetricDirection::Egress,
                    BattleMetricLane::Kcp,
                    kcp_packet_bytes);
                metrics.RecordReject();
                metrics.RecordExpiry();
                metrics.RecordKcpRetransmits(2);
                metrics.ObserveIngressQueue(31);
                metrics.ObserveEgressQueue(47);
                metrics.ObserveKcpQueue(53);
                metrics.ObserveTick(2'500'000, 4);
                metrics.ObserveMemory(65'536, 8'192);
                metrics.ObserveCombat(12, 7, 3, 2, false);
            }
        });
    }
    for (auto& worker : workers) {
        worker.join();
    }
    metrics.RecordRebind();
    metrics.RecordRekey();
    metrics.RecordClose(BattleCloseReasonCategory::Lifecycle);
    metrics.ObserveCombat(10, 4, 0, 0, true);

    const auto expected_samples =
        worker_count * samples_per_worker;
    const auto first = metrics.Snapshot();
    const auto second = metrics.Snapshot();
    Require(
        first.raw_ingress_packets == expected_samples &&
            first.raw_ingress_bytes ==
                expected_samples * raw_packet_bytes &&
            first.kcp_egress_packets == expected_samples &&
            first.kcp_egress_bytes ==
                expected_samples * kcp_packet_bytes,
        "runtime metric lane accounting drifted");
    Require(
        first.rejected_packets == expected_samples &&
            first.expired_messages == expected_samples &&
            first.kcp_retransmits == expected_samples * 2,
        "runtime metric disposition accounting drifted");
    Require(
        first.ingress_queue_high_watermark == 31 &&
            first.egress_queue_high_watermark == 47 &&
            first.kcp_queue_high_watermark == 53 &&
            first.maximum_tick_duration_ns == 2'500'000 &&
            first.tick_debt_high_watermark == 4 &&
            first.instance_memory_bytes == 65'536 &&
            first.history_memory_bytes == 8'192,
        "runtime metric high-watermark drifted");
    Require(
        first.combat_actor_high_watermark == 12 &&
            first.combat_projectile_high_watermark == 7 &&
            first.combat_ability_events == expected_samples * 3 &&
            first.combat_lifecycle_events == expected_samples * 2 &&
            first.encounter_completions == 1,
        "runtime combat metric accounting drifted");
    Require(
        first.rebinds == 1 && first.rekeys == 1 &&
            first.close_lifecycle == 1,
        "runtime metric lifecycle accounting drifted");
    Require(
        first.raw_ingress_packets ==
                second.raw_ingress_packets &&
            first.kcp_retransmits == second.kcp_retransmits &&
            first.maximum_tick_duration_ns ==
                second.maximum_tick_duration_ns,
        "runtime metric snapshot cleared or mutated counters");

    BattleRuntimeMetrics saturated;
    saturated.RecordDrop(
        std::numeric_limits<std::uint64_t>::max());
    saturated.RecordDrop();
    Require(
        saturated.Snapshot().dropped_packets ==
            std::numeric_limits<std::uint64_t>::max(),
        "runtime metric counter wrapped after saturation");
}

}  // namespace

/// main 执行低敏 runtime metrics 的并发与只读 contract。
int main() {
    try {
        TestConcurrentMonotonicSnapshot();
        std::cout << "battle_runtime_metrics_test: PASS\n";
        return 0;
    } catch (const std::exception& error) {
        std::cerr
            << "battle_runtime_metrics_test: FAIL: "
            << error.what() << '\n';
        return 1;
    }
}
