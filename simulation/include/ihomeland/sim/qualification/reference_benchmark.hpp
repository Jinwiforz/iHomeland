#pragma once

#include <cstdint>
#include <string>
#include <vector>

namespace ihomeland::sim {

/// ReferenceBenchmarkMode 区分非资格 smoke 与固定 Release 预算裁决。
enum class ReferenceBenchmarkMode : std::uint8_t {
    /// DiagnosticSmoke 执行同一 workload，但不产生 qualified 结论。
    DiagnosticSmoke,
    /// ReleaseQualification 允许依据固定 Release measurement 裁决预算。
    ReleaseQualification,
};

/// ReferenceBenchmarkScenario 保存单个 actor workload 的稳定测量统计。
struct ReferenceBenchmarkScenario final {
    /// actor_count 是本场景固定并发 actor 数。
    std::uint32_t actor_count;
    /// warmup_ticks 是不进入统计的预热 Tick 数。
    std::uint32_t warmup_ticks;
    /// sample_ticks 是进入统计的 Tick 数。
    std::uint32_t sample_ticks;
    /// median_tick_ns 是排序样本的 50th percentile。
    std::uint64_t median_tick_ns;
    /// p95_tick_ns 是 nearest-rank 95th percentile。
    std::uint64_t p95_tick_ns;
    /// maximum_tick_ns 是全部样本最大值。
    std::uint64_t maximum_tick_ns;
    /// tracked_tick_allocations 是测量循环显式动态分配次数。
    std::uint64_t tracked_tick_allocations;
    /// accounted_instance_bytes 是当前 workload 的显式内存 accounting。
    std::uint64_t accounted_instance_bytes;
    /// history_reserved_bytes 是 HistoryRing 构造时 hard accounting。
    std::uint64_t history_reserved_bytes;
    /// history_used_bytes 是最终有效窗口的 hard accounting。
    std::uint64_t history_used_bytes;
    /// queue_high_watermark 是 actor command workload 的有界峰值。
    std::uint64_t queue_high_watermark;
    /// within_budget 表示本次观测未超过 hard/target budget，不单独构成资格。
    bool within_budget;
};

/// ReferenceBenchmarkReport 是 1/5/8 actor 当前实现预算 evidence。
struct ReferenceBenchmarkReport final {
    /// scenarios 必须按 actor_count 1、5、8 排列。
    std::vector<ReferenceBenchmarkScenario> scenarios;
    /// canonical_json 是无用户名和绝对路径的稳定 schema。
    std::string canonical_json;
    /// policy_sha256 只绑定固定 workload、统计口径与预算，不包含 wall-clock 测量值。
    std::string policy_sha256;
    /// sha256 绑定 canonical_json 原始 bytes。
    std::string sha256;
    /// qualification_eligible 只对固定 Release measurement 为 true。
    bool qualification_eligible;
    /// qualified 仅在具备资格口径且全部 scenarios 通过时为 true。
    bool qualified;
};

/// RunReferenceBenchmark 执行固定 warmup/sample 的 Windows x64 1/5/8 actor workload。
///
/// @param mode 决定本次运行只做 diagnostic smoke，还是允许 Release 预算裁决。
[[nodiscard]] ReferenceBenchmarkReport RunReferenceBenchmark(
    ReferenceBenchmarkMode mode);

}  // namespace ihomeland::sim
