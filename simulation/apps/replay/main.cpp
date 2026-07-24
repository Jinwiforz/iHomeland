#include "ihomeland/sim/core/sha256.hpp"
#include "ihomeland/sim/evidence/replay_evidence.hpp"

#include <filesystem>
#include <cstdint>
#include <iostream>
#include <stdexcept>
#include <string>
#include <string_view>

namespace {

/// Input 返回 replay tool self-test 使用的完整低敏 identity。
[[nodiscard]] ihomeland::sim::ReplayEvidenceInput Input() {
    const std::string digest(64, 'a');
    ihomeland::sim::ReplayEvidenceInput input{
        .case_id = "kinematic-jump-and-collision",
        .build = {
            .compiler_identity = "msvc-19.50.35737",
            .build_sha256 = digest,
            .dependency_sha256 = digest,
            .model_sha256 = digest,
            .profile_sha256 = digest,
            .config_sha256 = digest,
            .navigation_sha256 = digest,
            .physics_sha256 = digest},
        .run = {
            .assignment_sha256 = digest,
            .instance_sha256 = digest,
            .first_tick = 1,
            .last_tick = 2,
            .first_input_tick = 1,
            .last_input_tick = 2,
            .seed = 7,
            .result_sha256 = digest},
        .budget = {
            .maximum_tick_ns = 1'000'000,
            .instance_bytes = 1024,
            .history_bytes = 512,
            .queue_high_watermark = 2},
        .diagnostics = {}};
    for (std::uint32_t index = 0; index < 20; ++index) {
        input.diagnostics.push_back({
            .name = "metric." + std::to_string(index),
            .value = std::string(128, 'x'),
            .sensitivity =
                ihomeland::sim::EvidenceFieldSensitivity::OperationalLow});
    }
    return input;
}

/// RunSelfTest 连续生成同一 replay，并证明 digest、截断和 source 只读性。
int RunSelfTest() {
    const auto source_manifest =
        std::filesystem::path(IHOMELAND_MODEL_ROOT) / "manifest.json";
    const auto before = ihomeland::sim::Sha256File(source_manifest);
    const ihomeland::sim::ReplayEvidencePolicy policy{
        .maximum_report_bytes = 1800,
        .maximum_retained_reports = 2,
        .maximum_retention_days = 1};
    const auto input = Input();
    const auto first = ihomeland::sim::BuildReplayEvidence(input, policy);
    const auto second = ihomeland::sim::BuildReplayEvidence(input, policy);
    const auto after = ihomeland::sim::Sha256File(source_manifest);
    if (first.sha256 != second.sha256 ||
        first.canonical_json != second.canonical_json ||
        first.truncated_diagnostics == 0 ||
        first.truncation_reason != "diagnostic-size-limit" ||
        before != after) {
        throw std::runtime_error(
            "replay tool determinism, truncation or source read-only gate failed");
    }
    std::cout << first.canonical_json << '\n';
    return 0;
}

}  // namespace

/// main 提供无网络 replay evidence 路径；首版只开放可回归的 self-test action。
int main(const int argument_count, const char* const arguments[]) {
    try {
        if (argument_count != 2 ||
            std::string_view(arguments[1]) != "--self-test") {
            throw std::invalid_argument(
                "usage: ihomeland-sim-replay --self-test");
        }
        return RunSelfTest();
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
