#include "ihomeland/sim/evidence/replay_evidence.hpp"

#include "ihomeland/sim/fixture/canonical_json.hpp"

#include <nlohmann/json.hpp>

#include <algorithm>
#include <array>
#include <cctype>
#include <string_view>
#include <tuple>

namespace ihomeland::sim {
namespace {

/// IsLowerHexDigest 验证 64-char lowercase SHA-256。
[[nodiscard]] bool IsLowerHexDigest(const std::string_view value) {
    return value.size() == 64 &&
           std::all_of(value.begin(), value.end(), [](const char character) {
               return (character >= '0' && character <= '9') ||
                      (character >= 'a' && character <= 'f');
           });
}

/// IsIdentifier 验证不含 path/quote/whitespace 的 lowercase evidence token。
[[nodiscard]] bool IsIdentifier(const std::string_view value) {
    return !value.empty() && value.size() <= 64 &&
           std::all_of(value.begin(), value.end(), [](const char character) {
               return (character >= 'a' && character <= 'z') ||
                      (character >= '0' && character <= '9') ||
                      character == '-' || character == '_' || character == '.';
           });
}

/// ContainsSensitiveName 拒绝即使错误标为低敏的 credential/personal 语义。
[[nodiscard]] bool ContainsSensitiveName(std::string name) {
    std::transform(name.begin(), name.end(), name.begin(), [](const unsigned char value) {
        return static_cast<char>(std::tolower(value));
    });
    constexpr std::array forbidden{
        std::string_view("token"),
        std::string_view("ticket"),
        std::string_view("password"),
        std::string_view("credential"),
        std::string_view("secret"),
        std::string_view("aead"),
        std::string_view("private_key"),
        std::string_view("account"),
        std::string_view("email"),
    };
    return std::any_of(forbidden.begin(), forbidden.end(), [&](const auto keyword) {
        return name.find(keyword) != std::string::npos;
    });
}

/// ValidateInput 在生成任何 report bytes 前执行 schema/security gate。
void ValidateInput(
    const ReplayEvidenceInput& input,
    const ReplayEvidencePolicy& policy) {
    if (!IsIdentifier(input.case_id) ||
        !IsIdentifier(input.build.compiler_identity) ||
        input.run.first_tick == 0 || input.run.last_tick < input.run.first_tick ||
        input.run.first_input_tick == 0 ||
        input.run.last_input_tick < input.run.first_input_tick ||
        policy.maximum_report_bytes == 0 ||
        policy.maximum_retained_reports == 0 ||
        policy.maximum_retention_days == 0) {
        throw EvidenceError(EvidenceErrorCode::Schema, "replay evidence identity or policy is invalid");
    }
    const std::array digests{
        std::string_view(input.build.build_sha256),
        std::string_view(input.build.dependency_sha256),
        std::string_view(input.build.model_sha256),
        std::string_view(input.build.profile_sha256),
        std::string_view(input.build.config_sha256),
        std::string_view(input.build.navigation_sha256),
        std::string_view(input.build.physics_sha256),
        std::string_view(input.run.assignment_sha256),
        std::string_view(input.run.instance_sha256),
        std::string_view(input.run.result_sha256),
    };
    if (!std::all_of(digests.begin(), digests.end(), IsLowerHexDigest)) {
        throw EvidenceError(EvidenceErrorCode::Schema, "replay evidence digest is invalid");
    }
    for (const auto& field : input.diagnostics) {
        if (field.sensitivity == EvidenceFieldSensitivity::Secret ||
            field.sensitivity == EvidenceFieldSensitivity::Personal ||
            ContainsSensitiveName(field.name)) {
            throw EvidenceError(
                EvidenceErrorCode::Security,
                "sensitive diagnostic field is forbidden in replay evidence");
        }
        if (!IsIdentifier(field.name) || field.value.size() > 256 ||
            !std::all_of(field.value.begin(), field.value.end(), [](const unsigned char character) {
                return character >= 0x20U && character <= 0x7eU;
            })) {
            throw EvidenceError(EvidenceErrorCode::Schema, "evidence diagnostic field is invalid");
        }
    }
}

/// BuildDocument 创建固定字段、非 settlement、非持久事实的 JSON value。
[[nodiscard]] nlohmann::json BuildDocument(
    const ReplayEvidenceInput& input,
    const ReplayEvidencePolicy& policy,
    const std::vector<EvidenceDiagnosticField>& diagnostics,
    const std::size_t truncated) {
    nlohmann::json document{
        {"schema_version", 1},
        {"artifact_kind", "simulation-replay-evidence"},
        {"case_id", input.case_id},
        {"settlement_authority", false},
        {"persistent_storage", "none"},
        {"build", {
            {"compiler_identity", input.build.compiler_identity},
            {"build_sha256", input.build.build_sha256},
            {"dependency_sha256", input.build.dependency_sha256},
            {"model_sha256", input.build.model_sha256},
            {"profile_sha256", input.build.profile_sha256},
            {"config_sha256", input.build.config_sha256},
            {"navigation_sha256", input.build.navigation_sha256},
            {"physics_sha256", input.build.physics_sha256}}},
        {"run", {
            {"assignment_sha256", input.run.assignment_sha256},
            {"instance_sha256", input.run.instance_sha256},
            {"first_tick", input.run.first_tick},
            {"last_tick", input.run.last_tick},
            {"first_input_tick", input.run.first_input_tick},
            {"last_input_tick", input.run.last_input_tick},
            {"seed", input.run.seed},
            {"result_sha256", input.run.result_sha256}}},
        {"budget", {
            {"maximum_tick_ns", input.budget.maximum_tick_ns},
            {"instance_bytes", input.budget.instance_bytes},
            {"history_bytes", input.budget.history_bytes},
            {"queue_high_watermark", input.budget.queue_high_watermark}}},
        {"retention", {
            {"maximum_reports", policy.maximum_retained_reports},
            {"maximum_days", policy.maximum_retention_days}}},
        {"truncation", {
            {"truncated_diagnostics", truncated},
            {"reason", truncated == 0 ? "" : "diagnostic-size-limit"}}},
        {"diagnostics", nlohmann::json::array()}};
    for (const auto& field : diagnostics) {
        document["diagnostics"].push_back({
            {"name", field.name},
            {"value", field.value},
            {"sensitivity",
             field.sensitivity == EvidenceFieldSensitivity::Public
                 ? "public"
                 : "operational-low"}});
    }
    return document;
}

}  // namespace

EvidenceError::EvidenceError(const EvidenceErrorCode code, const char* message)
    : std::runtime_error(message), code_(code) {}

EvidenceErrorCode EvidenceError::Code() const noexcept {
    return code_;
}

ReplayEvidenceReport BuildReplayEvidence(
    const ReplayEvidenceInput& input,
    const ReplayEvidencePolicy& policy) {
    ValidateInput(input, policy);
    auto diagnostics = input.diagnostics;
    std::stable_sort(diagnostics.begin(), diagnostics.end(), [](const auto& left, const auto& right) {
        return std::tie(left.name, left.value, left.sensitivity) <
               std::tie(right.name, right.value, right.sensitivity);
    });
    std::size_t truncated = 0;
    while (true) {
        const auto canonical =
            CanonicalizeJson(BuildDocument(input, policy, diagnostics, truncated).dump());
        if (canonical.text.size() <= policy.maximum_report_bytes) {
            ValidateReplayEvidenceReport(canonical.text);
            return {
                .canonical_json = canonical.text,
                .sha256 = canonical.sha256,
                .truncated_diagnostics = truncated,
                .truncation_reason =
                    truncated == 0 ? "" : "diagnostic-size-limit"};
        }
        if (diagnostics.empty()) {
            throw EvidenceError(
                EvidenceErrorCode::Capacity,
                "mandatory replay evidence exceeds report byte limit");
        }
        diagnostics.pop_back();
        ++truncated;
    }
}

void ValidateReplayEvidenceReport(const std::string_view canonical_json) {
    const auto document = nlohmann::json::parse(canonical_json);
    constexpr std::array required{
        std::string_view("schema_version"),
        std::string_view("artifact_kind"),
        std::string_view("case_id"),
        std::string_view("settlement_authority"),
        std::string_view("persistent_storage"),
        std::string_view("build"),
        std::string_view("run"),
        std::string_view("budget"),
        std::string_view("retention"),
        std::string_view("truncation"),
        std::string_view("diagnostics"),
    };
    if (!document.is_object() || document.size() != required.size() ||
        !std::all_of(required.begin(), required.end(), [&](const auto name) {
            return document.contains(name);
        }) ||
        document.at("schema_version") != 1 ||
        document.at("artifact_kind") != "simulation-replay-evidence" ||
        document.at("settlement_authority") != false ||
        document.at("persistent_storage") != "none" ||
        !document.at("build").is_object() || !document.at("run").is_object() ||
        !document.at("budget").is_object() || !document.at("retention").is_object() ||
        !document.at("truncation").is_object() || !document.at("diagnostics").is_array()) {
        throw EvidenceError(EvidenceErrorCode::Schema, "replay evidence report schema is invalid");
    }
}

}  // namespace ihomeland::sim
