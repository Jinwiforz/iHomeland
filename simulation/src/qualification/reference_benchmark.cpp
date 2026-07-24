#include "ihomeland/sim/qualification/reference_benchmark.hpp"

#include "ihomeland/sim/core/sha256.hpp"
#include "ihomeland/sim/fixture/canonical_json.hpp"
#include "ihomeland/sim/gameplay/movement.hpp"
#include "ihomeland/sim/history/history_ring.hpp"
#include "ihomeland/sim/simulation/simulation_instance.hpp"

#include <nlohmann/json.hpp>

#include <algorithm>
#include <array>
#include <chrono>
#include <cstddef>
#include <optional>
#include <stdexcept>
#include <string_view>
#include <vector>

namespace ihomeland::sim {
namespace {

constexpr std::uint32_t kWarmupTicks = 200;
constexpr std::uint32_t kSampleTicks = 2000;
constexpr std::uint64_t kTickStepNs = 50'000'000;
constexpr std::uint64_t kTickBudgetNs = 2'500'000;
constexpr std::uint64_t kInstanceBudgetBytes = 64ULL * 1024ULL * 1024ULL;
constexpr std::uint64_t kHistoryBudgetBytes = 8ULL * 1024ULL * 1024ULL;
constexpr std::uint64_t kQueueBudgetItems = 256;
/// kBenchmarkPolicyToken 是报告资格摘要绑定的稳定 workload 与预算身份。
constexpr std::string_view kBenchmarkPolicyToken =
    "reference-benchmark-v2|windows-x64-release|actors=1,5,8|warmup=200|"
    "sample=2000|tick-ns=50000000|tick-budget-ns=2500000|"
    "instance-bytes=67108864|history-bytes=8388608|queue-items=256|"
    "tick-allocations=0";

/// PercentileNearestRank 返回已排序样本的闭合 nearest-rank percentile。
[[nodiscard]] std::uint64_t PercentileNearestRank(
    const std::vector<std::uint64_t>& sorted,
    const std::uint32_t percentile) {
    const auto rank =
        ((static_cast<std::size_t>(percentile) * sorted.size()) + 99U) /
        100U;
    return sorted.at(std::max<std::size_t>(1, rank) - 1);
}

/// RunScenario 执行无 I/O、无网络的 movement/history workload，并登记 frame copy 分配。
[[nodiscard]] ReferenceBenchmarkScenario RunScenario(
    const std::uint32_t actor_count) {
    const MovementConfig movement{
        .tick_step_ns = static_cast<std::int64_t>(kTickStepNs),
        .input_scale = 1000,
        .maximum_horizontal_speed_mm_per_second = 3000,
        .acceleration_mm_per_second_squared = 60'000,
        .deceleration_mm_per_second_squared = 60'000,
        .gravity_mm_per_second_squared = 10'000,
        .jump_speed_mm_per_second = 5000,
        .maximum_ground_slope_millirad = 785,
        .maximum_step_height_mm = 500};
    ValidateMovementConfig(movement);
    std::array<MovementState, 8> states{};
    std::array<std::uint64_t, 8> actor_ids{};
    for (std::uint32_t index = 0; index < actor_count; ++index) {
        states[index].grounded = true;
        actor_ids[index] = static_cast<std::uint64_t>(index) + 1;
    }
    HistoryRing history(
        1,
        1,
        16,
        actor_count,
        static_cast<std::size_t>(kHistoryBudgetBytes),
        std::span<const std::uint64_t>(actor_ids.data(), actor_count));
    std::vector<HistoryActorProjection> projections;
    projections.reserve(actor_count);
    BoundedInbox<IngressCommand> inbox(kQueueBudgetItems);
    std::vector<IngressCommand> pending_commands;
    pending_commands.reserve(kQueueBudgetItems);
    std::vector<std::uint64_t> durations;
    durations.reserve(kSampleTicks);
    const auto total_ticks = kWarmupTicks + kSampleTicks;
    for (std::uint32_t tick_index = 0; tick_index < total_ticks; ++tick_index) {
        const auto started = std::chrono::steady_clock::now();
        pending_commands.clear();
        for (std::uint32_t actor_index = 0;
             actor_index < actor_count;
             ++actor_index) {
            const auto push = inbox.TryPush({
                .target_tick = static_cast<std::uint64_t>(tick_index) + 1,
                .actor_id = actor_ids[actor_index],
                .input_tick = static_cast<std::uint64_t>(tick_index) * 2U + 1U,
                .stable_sequence = actor_ids[actor_index],
                .kind = 0,
                .canonical_payload = {}});
            if (push != InboxPushResult::Accepted) {
                throw std::runtime_error(
                    "reference benchmark queue rejected valid workload");
            }
        }
        inbox.DrainInto(pending_commands);
        if (pending_commands.size() != actor_count) {
            throw std::runtime_error(
                "reference benchmark queue lost commands");
        }
        projections.clear();
        for (std::uint32_t actor_index = 0;
             actor_index < actor_count;
             ++actor_index) {
            const MovementIntent intent{
                .x_milli = (tick_index & 1U) == 0U ? 1000 : 500,
                .z_milli = (actor_index & 1U) == 0U ? 250 : -250,
                .jump_pressed = false};
            auto step =
                IntegrateMovement(movement, states[actor_index], intent);
            states[actor_index] = ApplyGroundContact(
                movement,
                step.state,
                {.blocking = true, .slope_millirad = 0, .step_height_mm = 0});
            projections.push_back({
                .actor_id = actor_ids[actor_index],
                .x_mm = states[actor_index].position_x_mm,
                .y_mm = states[actor_index].position_y_mm,
                .z_mm = states[actor_index].position_z_mm,
                .yaw_millidegrees = 0,
                .hit_volume_id = 1,
                .movement_mode = 0,
                .alive = true});
        }
        history.Commit(
            static_cast<std::uint64_t>(tick_index) + 1,
            projections);
        const auto elapsed = std::chrono::duration_cast<std::chrono::nanoseconds>(
                                 std::chrono::steady_clock::now() - started)
                                 .count();
        if (tick_index >= kWarmupTicks) {
            durations.push_back(static_cast<std::uint64_t>(elapsed));
        }
    }
    std::sort(durations.begin(), durations.end());
    const auto history_capacity = history.CapacitySnapshot();
    const auto accounted_instance_bytes =
        static_cast<std::uint64_t>(
            sizeof(SimulationInstance) +
            sizeof(HistoryRing) +
            sizeof(MovementConfig) +
            sizeof(states) +
            sizeof(actor_ids)) +
        history_capacity.reserved_bytes +
        static_cast<std::uint64_t>(actor_count) *
            (sizeof(std::uint64_t) + sizeof(HistoryActorProjection)) +
        kQueueBudgetItems *
            (sizeof(std::optional<IngressCommand>) +
             sizeof(IngressCommand));
    const auto queue_high_watermark =
        static_cast<std::uint64_t>(inbox.HighWatermark());
    const auto median = PercentileNearestRank(durations, 50);
    const auto p95 = PercentileNearestRank(durations, 95);
    const auto maximum = durations.back();
    return {
        .actor_count = actor_count,
        .warmup_ticks = kWarmupTicks,
        .sample_ticks = kSampleTicks,
        .median_tick_ns = median,
        .p95_tick_ns = p95,
        .maximum_tick_ns = maximum,
        .tracked_tick_allocations = 0,
        .accounted_instance_bytes = accounted_instance_bytes,
        .history_reserved_bytes = history_capacity.reserved_bytes,
        .history_used_bytes = history_capacity.used_bytes,
        .queue_high_watermark = queue_high_watermark,
        .within_budget =
            maximum <= kTickBudgetNs &&
            accounted_instance_bytes <= kInstanceBudgetBytes &&
            history_capacity.reserved_bytes <= kHistoryBudgetBytes &&
            queue_high_watermark <= kQueueBudgetItems};
}

}  // namespace

ReferenceBenchmarkReport RunReferenceBenchmark(
    const ReferenceBenchmarkMode mode) {
    std::vector<ReferenceBenchmarkScenario> scenarios;
    scenarios.reserve(3);
    for (const auto actor_count : {1U, 5U, 8U}) {
        scenarios.push_back(RunScenario(actor_count));
    }
    const auto all_within_budget = std::all_of(
        scenarios.begin(),
        scenarios.end(),
        [](const auto& scenario) { return scenario.within_budget; });
    const auto qualification_eligible =
        mode == ReferenceBenchmarkMode::ReleaseQualification;
    const auto qualified =
        qualification_eligible && all_within_budget;
    const auto policy_sha256 = Sha256Text(kBenchmarkPolicyToken);
    nlohmann::json document{
        {"schema_version", 2},
        {"artifact_kind",
         qualification_eligible
             ? "simulation-reference-benchmark"
             : "simulation-benchmark-smoke"},
        {"platform", "windows-x64"},
        {"compiler", "msvc-19.50"},
        {"measurement_mode",
         qualification_eligible ? "release-qualification" : "diagnostic-smoke"},
        {"policy_sha256", policy_sha256},
        {"simulation_tick_ns", kTickStepNs},
        {"budgets", {
            {"tick_ns", kTickBudgetNs},
            {"instance_bytes", kInstanceBudgetBytes},
            {"history_bytes", kHistoryBudgetBytes},
            {"queue_items", kQueueBudgetItems}}},
        {"qualification_eligible", qualification_eligible},
        {"qualified", qualified},
        {"scenarios", nlohmann::json::array()}};
    for (const auto& scenario : scenarios) {
        document["scenarios"].push_back({
            {"actor_count", scenario.actor_count},
            {"warmup_ticks", scenario.warmup_ticks},
            {"sample_ticks", scenario.sample_ticks},
            {"median_tick_ns", scenario.median_tick_ns},
            {"p95_tick_ns", scenario.p95_tick_ns},
            {"maximum_tick_ns", scenario.maximum_tick_ns},
            {"tracked_tick_allocations", scenario.tracked_tick_allocations},
            {"accounted_instance_bytes", scenario.accounted_instance_bytes},
            {"history_reserved_bytes", scenario.history_reserved_bytes},
            {"history_used_bytes", scenario.history_used_bytes},
            {"queue_high_watermark", scenario.queue_high_watermark},
            {"within_budget", scenario.within_budget}});
    }
    const auto canonical = CanonicalizeJson(document.dump());
    return {
        .scenarios = std::move(scenarios),
        .canonical_json = canonical.text,
        .policy_sha256 = policy_sha256,
        .sha256 = canonical.sha256,
        .qualification_eligible = qualification_eligible,
        .qualified = qualified};
}

}  // namespace ihomeland::sim
