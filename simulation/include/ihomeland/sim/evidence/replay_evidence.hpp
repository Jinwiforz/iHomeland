#pragma once

#include <cstddef>
#include <cstdint>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace ihomeland::sim {

/// EvidenceErrorCode 是 replay evidence schema/security/capacity 的稳定失败分类。
enum class EvidenceErrorCode : std::uint8_t {
    /// Schema 表示 identity、range、field 或 report shape 不合法。
    Schema,
    /// Security 表示 secret、credential 或个人数据试图进入 evidence。
    Security,
    /// Capacity 表示 mandatory report 本身超过 byte hard limit。
    Capacity,
};

/// EvidenceError 保留可机器判断的 evidence gate 失败原因。
class EvidenceError final : public std::runtime_error {
public:
    /// 构造函数绑定稳定错误码与低敏诊断文本。
    EvidenceError(EvidenceErrorCode code, const char* message);

    /// Code 返回不依赖文本的稳定错误码。
    [[nodiscard]] EvidenceErrorCode Code() const noexcept;

private:
    /// code_ 是当前 evidence 失败分类。
    EvidenceErrorCode code_;
};

/// EvidenceFieldSensitivity 是 diagnostic field 的闭合数据分类。
enum class EvidenceFieldSensitivity : std::uint8_t {
    /// Public 表示版本或公开 content identity。
    Public,
    /// OperationalLow 表示不含账号/网络凭据的低敏运行统计。
    OperationalLow,
    /// Personal 表示个人数据，evidence writer 必须拒绝。
    Personal,
    /// Secret 表示 credential/key/token，evidence writer 必须拒绝。
    Secret,
};

/// ReplayBuildIdentity 绑定可复现 build 与全部 source-of-truth digests。
struct ReplayBuildIdentity final {
    /// compiler_identity 是固定 compiler/toolset token，不含本机用户名或路径。
    std::string compiler_identity;
    /// build_sha256 是 executable/build manifest digest。
    std::string build_sha256;
    /// dependency_sha256 是 exact dependency manifest digest。
    std::string dependency_sha256;
    /// model_sha256 是 battle model manifest/binding digest。
    std::string model_sha256;
    /// profile_sha256 是 battle network profile binding digest。
    std::string profile_sha256;
    /// config_sha256 是 runtime/content config digest。
    std::string config_sha256;
    /// navigation_sha256 是 nav asset identity digest。
    std::string navigation_sha256;
    /// physics_sha256 是 physics config/scene identity digest。
    std::string physics_sha256;
};

/// ReplayRunIdentity 绑定 assignment/instance/Tick/input/seed 与规范结果。
struct ReplayRunIdentity final {
    /// assignment_sha256 是完整 AssignmentStamp fingerprint。
    std::string assignment_sha256;
    /// instance_sha256 是 SimulationInstance identity fingerprint。
    std::string instance_sha256;
    /// first_tick 是 replay 覆盖的首个非零 Tick。
    std::uint64_t first_tick;
    /// last_tick 是 replay 覆盖的末尾 Tick。
    std::uint64_t last_tick;
    /// first_input_tick 是 replay 覆盖的首个 InputTick。
    std::uint64_t first_input_tick;
    /// last_input_tick 是 replay 覆盖的末尾 InputTick。
    std::uint64_t last_input_tick;
    /// seed 是 versioned deterministic root seed。
    std::uint64_t seed;
    /// result_sha256 是 canonical state/event/rejection/capacity digest。
    std::string result_sha256;
};

/// ReplayBudgetEvidence 保存本次 run 的低敏 hard-target accounting。
struct ReplayBudgetEvidence final {
    /// maximum_tick_ns 是该 run 的最大 Tick wall duration。
    std::uint64_t maximum_tick_ns;
    /// instance_bytes 是当前实例 allocator/memory accounting。
    std::uint64_t instance_bytes;
    /// history_bytes 是 history ring accounting。
    std::uint64_t history_bytes;
    /// queue_high_watermark 是 command queue 最大使用量。
    std::uint64_t queue_high_watermark;
};

/// EvidenceDiagnosticField 是可截断的附加低敏诊断项。
struct EvidenceDiagnosticField final {
    /// name 只允许 lowercase ASCII identifier，敏感语义名称会被拒绝。
    std::string name;
    /// value 是长度受限的可打印 ASCII 低敏值。
    std::string value;
    /// sensitivity 必须是 Public 或 OperationalLow。
    EvidenceFieldSensitivity sensitivity;
};

/// ReplayEvidencePolicy 固定 size/retention/truncation hard rules。
struct ReplayEvidencePolicy final {
    /// maximum_report_bytes 是 canonical report 的正整数 hard limit。
    std::size_t maximum_report_bytes;
    /// maximum_retained_reports 是外部 sink 允许保留的最大 report 数。
    std::uint32_t maximum_retained_reports;
    /// maximum_retention_days 是外部 sink 允许保留的最大天数。
    std::uint32_t maximum_retention_days;
};

/// ReplayEvidenceInput 是 writer 的闭合低敏输入。
struct ReplayEvidenceInput final {
    /// case_id 是 source corpus 中的稳定 lowercase identifier。
    std::string case_id;
    /// build 绑定 exact toolchain/dependencies/source identities。
    ReplayBuildIdentity build;
    /// run 绑定 assignment/instance/ranges/seed/result。
    ReplayRunIdentity run;
    /// budget 保存当前 run 的测量值。
    ReplayBudgetEvidence budget;
    /// diagnostics 是唯一允许截断的附加字段。
    std::vector<EvidenceDiagnosticField> diagnostics;
};

/// ReplayEvidenceReport 是 in-memory canonical evidence artifact。
struct ReplayEvidenceReport final {
    /// canonical_json 是 schema-validated、键顺序稳定的 UTF-8 JSON。
    std::string canonical_json;
    /// sha256 是 canonical_json 原始 bytes 的 lowercase SHA-256。
    std::string sha256;
    /// truncated_diagnostics 是因 size policy 丢弃的尾部字段数。
    std::size_t truncated_diagnostics;
    /// truncation_reason 为空或固定 token diagnostic-size-limit。
    std::string truncation_reason;
};

/// BuildReplayEvidence 验证低敏输入并按 size/retention policy 生成 report。
[[nodiscard]] ReplayEvidenceReport BuildReplayEvidence(
    const ReplayEvidenceInput& input,
    const ReplayEvidencePolicy& policy);

/// ValidateReplayEvidenceReport 严格验证 canonical report schema 与非 settlement 边界。
void ValidateReplayEvidenceReport(std::string_view canonical_json);

}  // namespace ihomeland::sim
