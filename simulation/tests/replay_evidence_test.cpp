#include "ihomeland/sim/evidence/replay_evidence.hpp"

#include <nlohmann/json.hpp>

#include <algorithm>
#include <iostream>
#include <stdexcept>
#include <string>

namespace {

/// Require 把 replay evidence 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// Input 返回绑定全部 mandatory identity 的低敏 evidence input。
[[nodiscard]] ihomeland::sim::ReplayEvidenceInput Input() {
    const std::string digest(64, 'a');
    return {
        .case_id = "effect-damage-death",
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
        .diagnostics = {
            {.name = "adapter.mode", .value = "recorded", .sensitivity = ihomeland::sim::EvidenceFieldSensitivity::OperationalLow},
            {.name = "run.kind", .value = "determinism", .sensitivity = ihomeland::sim::EvidenceFieldSensitivity::Public}}};
}

/// TestCanonicalAndTruncation 验证 diagnostic reorder 与 size truncation 稳定。
void TestCanonicalAndTruncation() {
    const ihomeland::sim::ReplayEvidencePolicy generous{
        .maximum_report_bytes = 16'384,
        .maximum_retained_reports = 16,
        .maximum_retention_days = 7};
    auto first_input = Input();
    auto reversed_input = Input();
    std::reverse(reversed_input.diagnostics.begin(), reversed_input.diagnostics.end());
    const auto first = ihomeland::sim::BuildReplayEvidence(first_input, generous);
    const auto second = ihomeland::sim::BuildReplayEvidence(reversed_input, generous);
    Require(
        first.canonical_json == second.canonical_json &&
            first.sha256 == second.sha256 && first.sha256.size() == 64,
        "replay evidence depends on diagnostic arrival order");

    auto many = Input();
    for (std::uint32_t index = 0; index < 20; ++index) {
        many.diagnostics.push_back({
            .name = "metric." + std::to_string(index),
            .value = std::string(128, 'x'),
            .sensitivity = ihomeland::sim::EvidenceFieldSensitivity::OperationalLow});
    }
    const ihomeland::sim::ReplayEvidencePolicy limited{
        .maximum_report_bytes = 1800,
        .maximum_retained_reports = 2,
        .maximum_retention_days = 1};
    const auto truncated = ihomeland::sim::BuildReplayEvidence(many, limited);
    Require(
        truncated.canonical_json.size() <= limited.maximum_report_bytes &&
            truncated.truncated_diagnostics > 0 &&
            truncated.truncation_reason == "diagnostic-size-limit",
        "replay evidence truncation policy drifted");
}

/// TestSecurityAndSchema 验证 secret/personal 字段与 settlement tamper 被拒绝。
void TestSecurityAndSchema() {
    const ihomeland::sim::ReplayEvidencePolicy policy{
        .maximum_report_bytes = 16'384,
        .maximum_retained_reports = 16,
        .maximum_retention_days = 7};
    auto secret = Input();
    secret.diagnostics.push_back({
        .name = "battle.ticket",
        .value = "forbidden",
        .sensitivity = ihomeland::sim::EvidenceFieldSensitivity::OperationalLow});
    bool secret_rejected = false;
    try {
        static_cast<void>(ihomeland::sim::BuildReplayEvidence(secret, policy));
    } catch (const ihomeland::sim::EvidenceError& error) {
        Require(
            error.Code() == ihomeland::sim::EvidenceErrorCode::Security,
            "secret evidence field returned wrong code");
        secret_rejected = true;
    }
    Require(secret_rejected, "secret evidence field was accepted");
    const auto valid = ihomeland::sim::BuildReplayEvidence(Input(), policy);
    auto tampered = nlohmann::json::parse(valid.canonical_json);
    tampered["settlement_authority"] = true;
    try {
        ihomeland::sim::ValidateReplayEvidenceReport(tampered.dump());
    } catch (const ihomeland::sim::EvidenceError& error) {
        Require(
            error.Code() == ihomeland::sim::EvidenceErrorCode::Schema,
            "settlement evidence tamper returned wrong code");
        return;
    }
    throw std::runtime_error("settlement-authority evidence was accepted");
}

}  // namespace

/// main 执行 evidence binding/security/size/schema 的正负向回归。
int main() {
    try {
        TestCanonicalAndTruncation();
        TestSecurityAndSchema();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
