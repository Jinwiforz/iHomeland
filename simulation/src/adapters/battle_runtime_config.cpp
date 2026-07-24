#include "ihomeland/sim/config/battle_runtime_config.hpp"

#include "ihomeland/sim/core/sha256.hpp"

#include <nlohmann/json.hpp>

#include <algorithm>
#include <fstream>
#include <limits>
#include <map>
#include <set>
#include <string_view>
#include <vector>

namespace ihomeland::sim {
namespace {

using Json = nlohmann::json;

/// Fail 统一产生不回显 fixture 内容与本机绝对路径的低敏配置错误。
[[noreturn]] void Fail(const BattleConfigErrorCode code, const std::string& message) {
    throw BattleConfigError(code, message);
}

/// IsSafeRelativePath 拒绝绝对路径、空路径与任何父目录逃逸。
[[nodiscard]] bool IsSafeRelativePath(const std::filesystem::path& path) {
    if (path.empty() || path.is_absolute()) {
        return false;
    }
    const auto normalized = path.lexically_normal();
    return normalized.begin() != normalized.end() && *normalized.begin() != "..";
}

/// ResolveCorpusPath 只允许 manifest 在指定 corpus root 内引用文件。
[[nodiscard]] std::filesystem::path ResolveCorpusPath(
    const std::filesystem::path& root,
    const std::string& relative) {
    const std::filesystem::path path(relative);
    if (!IsSafeRelativePath(path)) {
        Fail(BattleConfigErrorCode::Schema, "manifest path escaped corpus root");
    }
    return root / path;
}

/// ReadJson 以严格 UTF-8 JSON parser 读取单个 corpus 文件。
[[nodiscard]] Json ReadJson(const std::filesystem::path& path) {
    std::ifstream stream(path, std::ios::binary);
    if (!stream) {
        Fail(BattleConfigErrorCode::Io, "required corpus file is missing");
    }
    try {
        return Json::parse(stream, nullptr, true, true);
    } catch (const Json::exception&) {
        Fail(BattleConfigErrorCode::Json, "corpus JSON parsing failed");
    }
}

/// RequireObjectKeys 对消费的每个 object 执行闭合字段检查。
void RequireObjectKeys(
    const Json& value,
    const std::initializer_list<std::string_view> expected,
    const std::string_view context) {
    if (!value.is_object()) {
        Fail(BattleConfigErrorCode::Schema, std::string(context) + " must be an object");
    }
    std::set<std::string, std::less<>> actual;
    for (const auto& [key, unused] : value.items()) {
        static_cast<void>(unused);
        actual.insert(key);
    }
    const std::set<std::string, std::less<>> required(expected.begin(), expected.end());
    if (actual != required) {
        Fail(BattleConfigErrorCode::Schema, std::string(context) + " has unknown or missing fields");
    }
}

/// RequireString 读取必填 string，并拒绝空值和类型转换。
[[nodiscard]] std::string RequireString(const Json& object, const std::string_view key) {
    const auto iterator = object.find(key);
    if (iterator == object.end() || !iterator->is_string()) {
        Fail(BattleConfigErrorCode::Schema, "required string field is invalid");
    }
    const auto result = iterator->get<std::string>();
    if (result.empty()) {
        Fail(BattleConfigErrorCode::Schema, "required string field is empty");
    }
    return result;
}

/// RequireUnsigned 读取无符号整数并执行调用方给出的闭区间约束。
[[nodiscard]] std::uint64_t RequireUnsigned(
    const Json& object,
    const std::string_view key,
    const std::uint64_t minimum,
    const std::uint64_t maximum) {
    const auto iterator = object.find(key);
    if (iterator == object.end() || !iterator->is_number_unsigned()) {
        Fail(BattleConfigErrorCode::Schema, "required unsigned field is invalid");
    }
    const auto result = iterator->get<std::uint64_t>();
    if (result < minimum || result > maximum) {
        Fail(BattleConfigErrorCode::Schema, "required unsigned field is outside its checked range");
    }
    return result;
}

/// RequireDigest 验证 lowercase SHA-256 文本的闭合格式。
[[nodiscard]] std::string RequireDigest(const Json& object, const std::string_view key) {
    const auto digest = RequireString(object, key);
    if (digest.size() != 64 ||
        !std::all_of(digest.begin(), digest.end(), [](const char value) {
            return (value >= '0' && value <= '9') || (value >= 'a' && value <= 'f');
        })) {
        Fail(BattleConfigErrorCode::Schema, "SHA-256 field is not canonical lowercase hex");
    }
    return digest;
}

/// VerifyDigest 把 manifest 声明与文件原始 bytes 绑定。
void VerifyDigest(const std::filesystem::path& path, const std::string& expected) {
    try {
        if (Sha256File(path) != expected) {
            Fail(BattleConfigErrorCode::Digest, "corpus digest mismatch");
        }
    } catch (const BattleConfigError&) {
        throw;
    } catch (const std::exception&) {
        Fail(BattleConfigErrorCode::Io, "corpus digest source cannot be read");
    }
}

/// ValidateProfileManifest 校验双向登记文件摘要并返回 qualification report 摘要。
[[nodiscard]] std::string ValidateProfileManifest(
    const std::filesystem::path& profile_root,
    const Json& manifest) {
    RequireObjectKeys(
        manifest,
        {"format_version", "profile_version", "document_kind", "schema_path", "schema_sha256",
         "required_requirements", "files", "cases"},
        "profile manifest");
    if (RequireString(manifest, "format_version") != "1" ||
        RequireString(manifest, "profile_version") != "battle-network-profile-v1" ||
        RequireString(manifest, "document_kind") != "manifest") {
        Fail(BattleConfigErrorCode::Schema, "profile manifest identity is unsupported");
    }
    VerifyDigest(
        ResolveCorpusPath(profile_root, RequireString(manifest, "schema_path")),
        RequireDigest(manifest, "schema_sha256"));
    const auto& files = manifest.at("files");
    if (!files.is_array() || files.empty()) {
        Fail(BattleConfigErrorCode::Schema, "profile manifest files are empty");
    }
    std::set<std::string, std::less<>> paths;
    std::string report_digest;
    for (const auto& file : files) {
        RequireObjectKeys(file, {"path", "document_kind", "file_sha256"}, "profile manifest file");
        const auto relative = RequireString(file, "path");
        const auto digest = RequireDigest(file, "file_sha256");
        if (!paths.insert(relative).second) {
            Fail(BattleConfigErrorCode::Schema, "profile manifest file path is duplicated");
        }
        VerifyDigest(ResolveCorpusPath(profile_root, relative), digest);
        if (relative == "reports/qualification.json") {
            report_digest = digest;
        }
    }
    if (!paths.contains("profile.json") || !paths.contains("model-binding.json") ||
        report_digest.empty()) {
        Fail(BattleConfigErrorCode::Schema, "profile manifest required files are missing");
    }
    return report_digest;
}

/// ValidateModelBinding 校验 model manifest、assumptions 与全部登记 case 的 source identity。
[[nodiscard]] std::string ValidateModelBinding(
    const std::filesystem::path& model_root,
    const Json& binding) {
    RequireObjectKeys(
        binding,
        {"format_version", "profile_version", "document_kind", "model_format_version",
         "model_version", "manifest_path", "manifest_sha256", "assumptions_path",
         "assumptions_sha256", "required_requirements", "cases"},
        "model binding");
    if (RequireString(binding, "format_version") != "1" ||
        RequireString(binding, "profile_version") != "battle-network-profile-v1" ||
        RequireString(binding, "model_format_version") != "1" ||
        RequireString(binding, "model_version") != "battle-model-v1") {
        Fail(BattleConfigErrorCode::Schema, "model binding identity is unsupported");
    }
    const auto manifest_digest = RequireDigest(binding, "manifest_sha256");
    VerifyDigest(model_root / "manifest.json", manifest_digest);
    VerifyDigest(model_root / "assumptions.json", RequireDigest(binding, "assumptions_sha256"));
    const auto& cases = binding.at("cases");
    if (!cases.is_array() || cases.size() != 10) {
        Fail(BattleConfigErrorCode::Schema, "model binding case set is incomplete");
    }
    std::set<std::string, std::less<>> case_ids;
    for (const auto& item : cases) {
        RequireObjectKeys(item, {"case_id", "path", "file_sha256"}, "model binding case");
        if (!case_ids.insert(RequireString(item, "case_id")).second) {
            Fail(BattleConfigErrorCode::Schema, "model binding case id is duplicated");
        }
        VerifyDigest(
            ResolveCorpusPath(model_root, RequireString(item, "path")),
            RequireDigest(item, "file_sha256"));
    }
    return manifest_digest;
}

/// ParameterValue 是 profile parameter 的已验证整数值与显式单位。
struct ParameterValue final {
    /// value 保存无符号 checked value。
    std::uint64_t value;
    /// unit 保存 schema 登记的单位。
    std::string unit;
    /// classification 保存 qualification 状态。
    std::string classification;
};

/// ReadParameters 拒绝未知/重复 parameter，并要求当前 consumer contract 的完整集合。
[[nodiscard]] std::map<std::string, ParameterValue, std::less<>> ReadParameters(const Json& profile) {
    const std::set<std::string, std::less<>> known{
        "bandwidth-down-per-instance-target", "bandwidth-down-per-player-target",
        "bandwidth-up-per-player-target", "continuous-hold-ticks",
        "correction-angle-millidegrees", "correction-position-millimeters",
        "cpu-per-tick-measurement", "cpu-per-tick-target",
        "full-baseline-interval-snapshots", "history-memory-measurement",
        "history-memory-target", "history-window-ticks", "input-bundle-depth",
        "input-bundle-redundancy", "input-early-window-ticks", "input-gap-expiry-ticks",
        "input-late-window-ticks", "input-step-ns", "interpolation-delay-us",
        "kcp-adapter-parity", "mapping-drift-reset-us", "maximum-baseline-age-ticks",
        "maximum-baseline-fanout", "maximum-extrapolation-us",
        "memory-per-instance-measurement", "memory-per-instance-target",
        "queue-capacity-items", "simulation-step-ns", "snapshot-interval-ticks",
        "wire-encoded-size-parity"};
    const auto& parameters = profile.at("parameters");
    if (!parameters.is_array() || parameters.size() != known.size()) {
        Fail(BattleConfigErrorCode::Schema, "profile parameter set is incomplete");
    }
    std::map<std::string, ParameterValue, std::less<>> result;
    for (const auto& parameter : parameters) {
        RequireObjectKeys(
            parameter,
            {"id", "value", "unit", "classification", "workloads", "evidence"},
            "profile parameter");
        const auto id = RequireString(parameter, "id");
        if (!known.contains(id) || result.contains(id)) {
            Fail(BattleConfigErrorCode::Schema, "profile parameter is unknown or duplicated");
        }
        result.emplace(
            id,
            ParameterValue{
                .value = RequireUnsigned(parameter, "value", 0, std::numeric_limits<std::uint64_t>::max()),
                .unit = RequireString(parameter, "unit"),
                .classification = RequireString(parameter, "classification")});
    }
    return result;
}

/// ReadParameter 强制调用方声明期待单位与 qualification classification。
[[nodiscard]] std::uint64_t ReadParameter(
    const std::map<std::string, ParameterValue, std::less<>>& parameters,
    const std::string_view id,
    const std::string_view unit,
    const std::string_view classification) {
    const auto iterator = parameters.find(id);
    if (iterator == parameters.end() || iterator->second.unit != unit ||
        iterator->second.classification != classification) {
        Fail(BattleConfigErrorCode::Schema, "profile parameter unit or classification drifted");
    }
    return iterator->second.value;
}

/// ToUint32 在窄化前验证范围，避免 profile overflow。
[[nodiscard]] std::uint32_t ToUint32(const std::uint64_t value) {
    if (value > std::numeric_limits<std::uint32_t>::max()) {
        Fail(BattleConfigErrorCode::Schema, "profile value exceeds uint32 range");
    }
    return static_cast<std::uint32_t>(value);
}

}  // namespace

BattleConfigError::BattleConfigError(const BattleConfigErrorCode code, const std::string& message)
    : std::runtime_error(message), code_(code) {}

BattleConfigErrorCode BattleConfigError::Code() const noexcept {
    return code_;
}

BattleRuntimeConfig LoadBattleRuntimeConfig(
    const std::filesystem::path& profile_root,
    const std::filesystem::path& model_root) {
    const auto manifest_path = profile_root / "manifest.json";
    const auto manifest = ReadJson(manifest_path);
    const auto qualification_digest = ValidateProfileManifest(profile_root, manifest);
    const auto model_digest =
        ValidateModelBinding(model_root, ReadJson(profile_root / "model-binding.json"));

    const auto report = ReadJson(profile_root / "reports" / "qualification.json");
    if (RequireString(report, "profile_status") != "qualified" ||
        !report.at("missing").empty() || !report.at("skipped").empty() ||
        !report.at("stale").empty() || !report.at("unclassified").empty()) {
        Fail(BattleConfigErrorCode::Qualification, "battle network profile is not qualified");
    }

    const auto profile = ReadJson(profile_root / "profile.json");
    RequireObjectKeys(
        profile,
        {"format_version", "profile_version", "document_kind", "tool_version",
         "profile_status", "selected_candidate_id", "candidates", "parameters", "mtu_budget",
         "kcp_profile", "capacity", "workloads", "report_path"},
        "profile");
    if (RequireString(profile, "format_version") != "1" ||
        RequireString(profile, "profile_version") != "battle-network-profile-v1" ||
        RequireString(profile, "document_kind") != "profile" ||
        RequireString(profile, "profile_status") != "qualified" ||
        RequireString(profile, "selected_candidate_id") != "candidate-20hz" ||
        RequireString(profile, "report_path") != "reports/qualification.json") {
        Fail(BattleConfigErrorCode::Qualification, "selected profile identity is not qualified");
    }

    const auto parameters = ReadParameters(profile);
    const auto simulation_tick_ns =
        ReadParameter(parameters, "simulation-step-ns", "nanoseconds", "profile_qualified");
    const auto input_tick_ns =
        ReadParameter(parameters, "input-step-ns", "nanoseconds", "profile_qualified");
    constexpr std::uint64_t nanoseconds_per_second = 1'000'000'000ULL;
    constexpr std::uint64_t nanoseconds_per_millisecond = 1'000'000ULL;
    if (simulation_tick_ns == 0 || input_tick_ns == 0 ||
        simulation_tick_ns % nanoseconds_per_millisecond != 0 ||
        nanoseconds_per_second % input_tick_ns != 0 ||
        simulation_tick_ns % input_tick_ns != 0) {
        Fail(BattleConfigErrorCode::Schema, "Tick cadence is not an exact integer mapping");
    }

    const auto& capacity = profile.at("capacity");
    RequireObjectKeys(
        capacity,
        {"qualified_default_players", "qualified_max_players", "evaluated_max_players",
         "visit_configured_max_players", "capacity_gate_required", "capacity_gate_owner",
         "capacity_gate_status"},
        "profile capacity");
    const auto actor_cap = RequireUnsigned(capacity, "qualified_max_players", 1, 8);

    return BattleRuntimeConfig{
        .simulation_tick_ns = simulation_tick_ns,
        .input_tick_ns = input_tick_ns,
        .simulation_tick_ms = ToUint32(simulation_tick_ns / nanoseconds_per_millisecond),
        .input_tick_hz = ToUint32(nanoseconds_per_second / input_tick_ns),
        .history_window_ticks = ToUint32(
            ReadParameter(parameters, "history-window-ticks", "ticks", "profile_qualified")),
        .qualified_actor_cap = ToUint32(actor_cap),
        .queue_capacity_items = ToUint32(
            ReadParameter(parameters, "queue-capacity-items", "items", "target_budget")),
        .cpu_per_tick_target_us = ToUint32(
            ReadParameter(parameters, "cpu-per-tick-target", "microseconds", "target_budget")),
        .memory_per_instance_target_bytes =
            ReadParameter(parameters, "memory-per-instance-target", "bytes", "target_budget"),
        .history_memory_target_bytes =
            ReadParameter(parameters, "history-memory-target", "bytes", "target_budget"),
        .continuous_hold_ticks = ToUint32(
            ReadParameter(parameters, "continuous-hold-ticks", "ticks", "profile_qualified")),
        .input_early_window_ticks = ToUint32(
            ReadParameter(parameters, "input-early-window-ticks", "ticks", "profile_qualified")),
        .input_late_window_ticks = ToUint32(
            ReadParameter(parameters, "input-late-window-ticks", "ticks", "profile_qualified")),
        .input_gap_expiry_ticks = ToUint32(
            ReadParameter(parameters, "input-gap-expiry-ticks", "ticks", "profile_qualified")),
        .model_manifest_sha256 = model_digest,
        .profile_manifest_sha256 = Sha256File(manifest_path),
        .qualification_report_sha256 = qualification_digest};
}

}  // namespace ihomeland::sim
