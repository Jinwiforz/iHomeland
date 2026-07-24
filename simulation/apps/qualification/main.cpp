#include "ihomeland/sim/core/sha256.hpp"
#include "ihomeland/sim/fixture/canonical_json.hpp"
#include "ihomeland/sim/qualification/reference_benchmark.hpp"

#include <nlohmann/json.hpp>

#include <array>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <regex>
#include <stdexcept>
#include <string>
#include <string_view>

namespace {

/// ReadJsonFile 读取本地资格输入，并把 open/parse failure 收敛为低敏诊断。
[[nodiscard]] nlohmann::json ReadJsonFile(
    const std::filesystem::path& path,
    const std::string_view label) {
    std::ifstream stream(path, std::ios::binary);
    if (!stream) {
        throw std::runtime_error(
            std::string(label) + " is missing");
    }
    try {
        nlohmann::json document;
        stream >> document;
        return document;
    } catch (const nlohmann::json::exception&) {
        throw std::runtime_error(
            std::string(label) + " is not valid JSON");
    }
}

/// WriteAtomic 只在完整写入成功后替换本地 ignored qualification artifact。
void WriteAtomic(
    const std::filesystem::path& destination,
    const std::string_view content) {
    std::filesystem::create_directories(destination.parent_path());
    auto temporary = destination;
    temporary += ".partial";
    {
        std::ofstream stream(
            temporary,
            std::ios::binary | std::ios::trunc);
        if (!stream ||
            !stream.write(
                content.data(),
                static_cast<std::streamsize>(content.size()))) {
            throw std::runtime_error(
                "qualification temporary report write failed");
        }
    }
    std::error_code error;
    std::filesystem::remove(destination, error);
    error.clear();
    std::filesystem::rename(temporary, destination, error);
    if (error) {
        std::filesystem::remove(temporary, error);
        throw std::runtime_error("qualification report commit failed");
    }
}

/// EmitQualification 在 CI/ASan gates 成功后生成唯一 report 与 manifest。
int EmitQualification() {
    if (IHOMELAND_ASAN_BUILD != 0) {
        throw std::runtime_error(
            "qualification report requires windows-msvc-ci Release build");
    }
    const auto model_manifest =
        std::filesystem::path(IHOMELAND_MODEL_ROOT) / "manifest.json";
    const auto profile_manifest =
        std::filesystem::path(IHOMELAND_PROFILE_ROOT) / "manifest.json";
    const auto model_before = ihomeland::sim::Sha256File(model_manifest);
    const auto profile_before = ihomeland::sim::Sha256File(profile_manifest);
    const auto build_sha256 =
        ihomeland::sim::Sha256File(IHOMELAND_BUILD_MANIFEST_PATH);
    const auto build_identity_sha256 =
        ihomeland::sim::Sha256File(IHOMELAND_BUILD_IDENTITY_PATH);
    const auto asan_build_identity_sha256 =
        ihomeland::sim::Sha256File(IHOMELAND_ASAN_BUILD_IDENTITY_PATH);
    const auto gate_receipt_sha256 =
        ihomeland::sim::Sha256File(IHOMELAND_QUALIFICATION_GATE_PATH);
    const auto build_identity = ReadJsonFile(
        IHOMELAND_BUILD_IDENTITY_PATH,
        "qualification build identity");
    const auto build_manifest = ReadJsonFile(
        IHOMELAND_BUILD_MANIFEST_PATH,
        "qualification build manifest");
    const auto asan_build_identity = ReadJsonFile(
        IHOMELAND_ASAN_BUILD_IDENTITY_PATH,
        "qualification ASan build identity");
    const auto gate_receipt = ReadJsonFile(
        IHOMELAND_QUALIFICATION_GATE_PATH,
        "qualification gate receipt");
    const std::regex lowercase_sha256("^[0-9a-f]{64}$");
    if (build_identity.value("schema_version", 0) != 1 ||
        build_identity.value("preset", "") != "windows-msvc-ci" ||
        !std::regex_match(
            build_identity.value("source_sha256", ""),
            lowercase_sha256) ||
        !std::regex_match(
            build_identity.value("target_identity", ""),
            lowercase_sha256) ||
        !build_identity.contains("manifest") ||
        build_identity["manifest"] != build_manifest ||
        build_manifest.value("build_type", "") != "Release" ||
        build_manifest.value("asan", true)) {
        throw std::runtime_error(
            "qualification requires current windows-msvc-ci build identity");
    }
    if (asan_build_identity.value("schema_version", 0) != 1 ||
        asan_build_identity.value("preset", "") != "windows-msvc-asan" ||
        asan_build_identity.value("source_sha256", "") !=
            build_identity.value("source_sha256", "") ||
        !std::regex_match(
            asan_build_identity.value("target_identity", ""),
            lowercase_sha256) ||
        !asan_build_identity.contains("manifest") ||
        !asan_build_identity["manifest"].value("asan", false)) {
        throw std::runtime_error(
            "qualification requires current windows-msvc-asan build identity");
    }
    if (gate_receipt.value("schema_version", 0) != 1 ||
        gate_receipt.value("source_sha256", "") !=
            build_identity.value("source_sha256", "") ||
        gate_receipt.value("ci_target_identity", "") !=
            build_identity.value("target_identity", "") ||
        gate_receipt.value("asan_target_identity", "") !=
            asan_build_identity.value("target_identity", "") ||
        !gate_receipt.value("ci_ctest_passed", false) ||
        !gate_receipt.value("asan_ctest_passed", false)) {
        throw std::runtime_error(
            "qualification gate receipt does not match current source");
    }
    const auto benchmark = ihomeland::sim::RunReferenceBenchmark(
        ihomeland::sim::ReferenceBenchmarkMode::ReleaseQualification);
    if (!benchmark.qualified) {
        throw std::runtime_error(
            "reference benchmark is not qualified");
    }
    constexpr std::array gates{
        "unit",
        "contract",
        "integration",
        "negative",
        "determinism",
        "jolt-parity",
        "detour-parity",
        "replay",
        "benchmark",
        "comprehensive",
        "architecture",
        "asan",
    };
    nlohmann::json report{
        {"schema_version", 1},
        {"artifact_kind", "b0.3-implementation-qualification"},
        {"conclusion", "implementation-qualified-windows-x64"},
        {"qualified", true},
        {"platform", "windows-x64"},
        {"compiler", "msvc-19.50.35737"},
        {"cxx_standard", 20},
        {"simulation_tick_ns", 50'000'000},
        {"maximum_actors", 8},
        {"build_manifest_sha256", build_sha256},
        {"build_identity_sha256", build_identity_sha256},
        {"asan_build_identity_sha256", asan_build_identity_sha256},
        {"gate_receipt_sha256", gate_receipt_sha256},
        {"model_manifest_sha256", model_before},
        {"profile_manifest_sha256", profile_before},
        {"benchmark_policy_sha256", benchmark.policy_sha256},
        {"benchmark_measurement_path", "reference-benchmark.json"},
        {"gates", nlohmann::json::array()},
        {"not_qualified", {
            "linux",
            "go-control",
            "socket-kcp-aead",
            "production-network",
            "unity-runtime"}}};
    for (const auto gate : gates) {
        report["gates"].push_back({{"name", gate}, {"passed", true}});
    }
    const auto canonical_report =
        ihomeland::sim::CanonicalizeJson(report.dump());
    nlohmann::json manifest{
        {"schema_version", 1},
        {"artifact_kind", "b0.3-qualification-manifest"},
        {"conclusion", "implementation-qualified-windows-x64"},
        {"report_path", "qualification.json"},
        {"report_sha256", canonical_report.sha256},
        {"benchmark_policy_sha256", benchmark.policy_sha256},
        {"build_manifest_sha256", build_sha256},
        {"build_identity_sha256", build_identity_sha256},
        {"asan_build_identity_sha256", asan_build_identity_sha256},
        {"gate_receipt_sha256", gate_receipt_sha256},
        {"model_manifest_sha256", model_before},
        {"profile_manifest_sha256", profile_before}};
    const auto canonical_manifest =
        ihomeland::sim::CanonicalizeJson(manifest.dump());
    const auto output_root = std::filesystem::path(IHOMELAND_REPORT_ROOT);
    WriteAtomic(
        output_root / "reference-benchmark.json",
        benchmark.canonical_json + "\n");
    WriteAtomic(
        output_root / "qualification.json",
        canonical_report.text + "\n");
    WriteAtomic(
        output_root / "qualification-manifest.json",
        canonical_manifest.text + "\n");
    if (ihomeland::sim::Sha256File(model_manifest) != model_before ||
        ihomeland::sim::Sha256File(profile_manifest) != profile_before) {
        throw std::runtime_error(
            "qualification generation modified source corpus");
    }
    std::cout << canonical_report.sha256 << '\n';
    return 0;
}

}  // namespace

/// main 只开放 verify 完成后生成本地 qualification evidence 的闭合 action。
int main(const int argument_count, const char* const arguments[]) {
    try {
        if (argument_count != 2 ||
            std::string_view(arguments[1]) != "--emit-qualified") {
            throw std::invalid_argument(
                "usage: ihomeland-sim-qualification --emit-qualified");
        }
        return EmitQualification();
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
