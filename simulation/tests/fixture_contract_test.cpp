#include "ihomeland/sim/config/battle_runtime_config.hpp"
#include "ihomeland/sim/core/sha256.hpp"
#include "ihomeland/sim/fixture/canonical_json.hpp"
#include "ihomeland/sim/fixture/offline_harness.hpp"

#include <nlohmann/json.hpp>

#include <filesystem>
#include <fstream>
#include <iostream>
#include <locale>
#include <sstream>
#include <stdexcept>
#include <string>
#include <string_view>

namespace {

using Json = nlohmann::json;

/// Require 把失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// RequireConfigError 验证配置负例产生期待的稳定错误码。
template <typename Callback>
void RequireConfigError(
    Callback&& callback,
    const ihomeland::sim::BattleConfigErrorCode expected,
    const char* message) {
    try {
        callback();
    } catch (const ihomeland::sim::BattleConfigError& error) {
        Require(error.Code() == expected, message);
        return;
    }
    throw std::runtime_error(message);
}

/// RequireHarnessError 验证离线 contract 的稳定错误码。
template <typename Callback>
void RequireHarnessError(
    Callback&& callback,
    const ihomeland::sim::HarnessErrorCode expected,
    const char* message) {
    try {
        callback();
    } catch (const ihomeland::sim::HarnessError& error) {
        Require(error.Code() == expected, message);
        return;
    }
    throw std::runtime_error(message);
}

/// ReadJson 读取测试隔离副本中的 JSON。
[[nodiscard]] Json ReadJson(const std::filesystem::path& path) {
    std::ifstream stream(path, std::ios::binary);
    if (!stream) {
        throw std::runtime_error("test JSON open failed");
    }
    return Json::parse(stream);
}

/// WriteJson 以 UTF-8/LF 和单一末尾换行更新隔离副本。
void WriteJson(const std::filesystem::path& path, const Json& value) {
    std::ofstream stream(path, std::ios::binary | std::ios::trunc);
    const auto text = value.dump(2) + "\n";
    if (!stream || !stream.write(text.data(), static_cast<std::streamsize>(text.size()))) {
        throw std::runtime_error("test JSON write failed");
    }
}

/// RefreshProfileManifest 更新隔离副本中单个 profile file 的登记摘要。
void RefreshProfileManifest(
    const std::filesystem::path& profile_root,
    const std::string_view relative_path) {
    const auto manifest_path = profile_root / "manifest.json";
    auto manifest = ReadJson(manifest_path);
    bool found = false;
    for (auto& file : manifest.at("files")) {
        if (file.at("path").get<std::string>() == relative_path) {
            file["file_sha256"] =
                ihomeland::sim::Sha256File(profile_root / std::filesystem::path(relative_path));
            found = true;
        }
    }
    Require(found, "profile manifest entry not found");
    WriteJson(manifest_path, manifest);
}

/// IsolatedCorpus 为每个负例复制 source corpus，并在析构时只删除系统临时目录内的副本。
class IsolatedCorpus final {
public:
    /// 构造函数创建固定测试根，串行 CTest 下不会与生产 source 重叠。
    IsolatedCorpus() {
        root_ = std::filesystem::temp_directory_path() / "ihomeland-simulation-fixture-negative";
        std::error_code error;
        std::filesystem::remove_all(root_, error);
        std::filesystem::create_directories(root_);
        std::filesystem::copy(
            std::filesystem::path(IHOMELAND_PROFILE_ROOT),
            root_ / "profile",
            std::filesystem::copy_options::recursive);
        std::filesystem::copy(
            std::filesystem::path(IHOMELAND_MODEL_ROOT),
            root_ / "model",
            std::filesystem::copy_options::recursive);
    }

    /// 析构函数清理隔离副本，不触碰仓库 source corpus。
    ~IsolatedCorpus() {
        std::error_code error;
        std::filesystem::remove_all(root_, error);
    }

    IsolatedCorpus(const IsolatedCorpus&) = delete;
    IsolatedCorpus& operator=(const IsolatedCorpus&) = delete;

    /// ProfileRoot 返回可变 profile 隔离根。
    [[nodiscard]] std::filesystem::path ProfileRoot() const {
        return root_ / "profile";
    }

    /// ModelRoot 返回可变 model 隔离根。
    [[nodiscard]] std::filesystem::path ModelRoot() const {
        return root_ / "model";
    }

    /// OutputRoot 返回与 corpus 分离的 evidence 目录。
    [[nodiscard]] std::filesystem::path OutputRoot() const {
        return root_ / "evidence";
    }

private:
    /// root_ 始终位于系统临时目录的固定子目录。
    std::filesystem::path root_;
};

/// CommaNumpunct 为 locale 独立性测试提供逗号小数点。
class CommaNumpunct final : public std::numpunct<char> {
protected:
    /// do_decimal_point 返回与 JSON 语法冲突的 locale 小数点。
    [[nodiscard]] char do_decimal_point() const override { return ','; }
};

/// TestCanonicalJson 验证键序、空白、换行、locale 与重复运行不影响 token/digest。
void TestCanonicalJson() {
    const auto first = ihomeland::sim::CanonicalizeJson(
        "{\r\n  \"z\": [3, 2, 1], \"a\": {\"n\": 1.5, \"v\": true}\r\n}");
    const auto second = ihomeland::sim::CanonicalizeJson(
        "{\"a\":{\"v\":true,\"n\":1.5},\"z\":[3,2,1]}");
    Require(first.text == "{\"a\":{\"n\":1.5,\"v\":true},\"z\":[3,2,1]}", "canonical token order drifted");
    Require(first.text == second.text && first.sha256 == second.sha256, "canonical digest depends on input layout");

    const auto previous = std::locale();
    std::locale::global(std::locale(previous, new CommaNumpunct()));
    const auto locale_result = ihomeland::sim::CanonicalizeJson("{\"n\":1.5}");
    std::locale::global(previous);
    Require(locale_result.text == "{\"n\":1.5}", "canonical number depends on locale");
    for (int iteration = 0; iteration < 32; ++iteration) {
        Require(
            ihomeland::sim::CanonicalizeJson(second.text).sha256 == second.sha256,
            "canonical digest drifted across repeated runs");
    }
}

/// TestOfflineContracts 覆盖 stdin/file source、recorded source、sink 容量和关闭语义。
void TestOfflineContracts() {
    std::istringstream input("{\"b\":2,\"a\":1}\n{\"kind\":\"jump\"}\n{\"kind\":\"extra\"}\n");
    ihomeland::sim::JsonLineCommandSource stdin_source(input, 2);
    const auto first = stdin_source.Next();
    Require(first.has_value() && first->ordinal == 1 && first->canonical_json == "{\"a\":1,\"b\":2}", "stdin command normalization failed");
    Require(stdin_source.Next().has_value(), "stdin second command missing");
    RequireHarnessError(
        [&] { static_cast<void>(stdin_source.Next()); },
        ihomeland::sim::HarnessErrorCode::Capacity,
        "stdin capacity did not fail closed");
    stdin_source.Close();
    RequireHarnessError(
        [&] { static_cast<void>(stdin_source.Next()); },
        ihomeland::sim::HarnessErrorCode::Closed,
        "closed stdin source remained readable");

    ihomeland::sim::RecordedResultSource recorded(
        {{"physics-1", "{\"hits\":[]}"}, {"navigation-1", "{\"path\":[1,2]}"}},
        2);
    Require(recorded.Take("physics-1").query_id == "physics-1", "recorded query was not consumed");
    RequireHarnessError(
        [&] { static_cast<void>(recorded.Take("wrong")); },
        ihomeland::sim::HarnessErrorCode::Sequence,
        "recorded query reorder was accepted");
    Require(recorded.Take("navigation-1").canonical_result == "{\"path\":[1,2]}", "recorded result normalization failed");
    RequireHarnessError(
        [&] { static_cast<void>(recorded.Take("overflow")); },
        ihomeland::sim::HarnessErrorCode::Capacity,
        "recorded capacity did not fail closed");

    IsolatedCorpus corpus;
    ihomeland::sim::EvidenceSink sink(
        corpus.OutputRoot(),
        {corpus.ProfileRoot(), corpus.ModelRoot()},
        16);
    sink.Write("run/result.json", "{}");
    Require(sink.BytesWritten() == 2, "evidence byte accounting drifted");
    RequireHarnessError(
        [&] { sink.Write("../escape.json", "{}"); },
        ihomeland::sim::HarnessErrorCode::ForbiddenPath,
        "evidence path escape was accepted");
    RequireHarnessError(
        [&] { sink.Write("run/result.json:hidden", "{}"); },
        ihomeland::sim::HarnessErrorCode::ForbiddenPath,
        "evidence alternate data stream was accepted");
    RequireHarnessError(
        [&] { sink.Write("too-large.json", std::string(15, 'x')); },
        ihomeland::sim::HarnessErrorCode::Capacity,
        "evidence capacity overflow was accepted");
    sink.Close();
    RequireHarnessError(
        [&] { sink.Write("closed.json", "{}"); },
        ihomeland::sim::HarnessErrorCode::Closed,
        "closed evidence sink remained writable");
    RequireHarnessError(
        [&] {
            ihomeland::sim::EvidenceSink forbidden(
                corpus.ProfileRoot() / "reports",
                {corpus.ProfileRoot(), corpus.ModelRoot()},
                32);
            static_cast<void>(forbidden);
        },
        ihomeland::sim::HarnessErrorCode::ForbiddenPath,
        "source corpus write root was accepted");
}

/// TestConfigNegatives 覆盖 drift、case、unsafe field、单位、overflow、旧报告和 source 只读性。
void TestConfigNegatives() {
    {
        IsolatedCorpus corpus;
        auto manifest = ReadJson(corpus.ProfileRoot() / "manifest.json");
        manifest["profile_version"] =
            "battle-network-profile-v1";
        WriteJson(corpus.ProfileRoot() / "manifest.json", manifest);
        RequireConfigError(
            [&] { static_cast<void>(ihomeland::sim::LoadBattleRuntimeConfig(corpus.ProfileRoot(), corpus.ModelRoot())); },
            ihomeland::sim::BattleConfigErrorCode::Schema,
            "profile v1 manifest was accepted");
    }
    {
        IsolatedCorpus corpus;
        auto profile = ReadJson(corpus.ProfileRoot() / "profile.json");
        profile["profile_status"] = "drifted";
        WriteJson(corpus.ProfileRoot() / "profile.json", profile);
        RequireConfigError(
            [&] { static_cast<void>(ihomeland::sim::LoadBattleRuntimeConfig(corpus.ProfileRoot(), corpus.ModelRoot())); },
            ihomeland::sim::BattleConfigErrorCode::Digest,
            "manifest drift did not fail with digest error");
    }
    {
        IsolatedCorpus corpus;
        auto binding = ReadJson(corpus.ProfileRoot() / "model-binding.json");
        binding["cases"].erase(binding["cases"].begin());
        WriteJson(corpus.ProfileRoot() / "model-binding.json", binding);
        RefreshProfileManifest(corpus.ProfileRoot(), "model-binding.json");
        RequireConfigError(
            [&] { static_cast<void>(ihomeland::sim::LoadBattleRuntimeConfig(corpus.ProfileRoot(), corpus.ModelRoot())); },
            ihomeland::sim::BattleConfigErrorCode::Schema,
            "unregistered case set was accepted");
    }
    {
        IsolatedCorpus corpus;
        std::filesystem::remove(
            corpus.ModelRoot() / "cases" / "movement" / "kinematic-jump-and-collision.json");
        RequireConfigError(
            [&] { static_cast<void>(ihomeland::sim::LoadBattleRuntimeConfig(corpus.ProfileRoot(), corpus.ModelRoot())); },
            ihomeland::sim::BattleConfigErrorCode::Io,
            "missing model case was accepted");
    }
    {
        IsolatedCorpus corpus;
        auto profile = ReadJson(corpus.ProfileRoot() / "profile.json");
        profile["password"] = "forbidden";
        WriteJson(corpus.ProfileRoot() / "profile.json", profile);
        RefreshProfileManifest(corpus.ProfileRoot(), "profile.json");
        RequireConfigError(
            [&] { static_cast<void>(ihomeland::sim::LoadBattleRuntimeConfig(corpus.ProfileRoot(), corpus.ModelRoot())); },
            ihomeland::sim::BattleConfigErrorCode::Schema,
            "unsafe unknown field was accepted");
    }
    {
        IsolatedCorpus corpus;
        auto profile = ReadJson(corpus.ProfileRoot() / "profile.json");
        for (auto& parameter : profile["parameters"]) {
            if (parameter["id"] == "queue-capacity-items") {
                parameter["unit"] = "bytes";
            }
        }
        WriteJson(corpus.ProfileRoot() / "profile.json", profile);
        RefreshProfileManifest(corpus.ProfileRoot(), "profile.json");
        RequireConfigError(
            [&] { static_cast<void>(ihomeland::sim::LoadBattleRuntimeConfig(corpus.ProfileRoot(), corpus.ModelRoot())); },
            ihomeland::sim::BattleConfigErrorCode::Schema,
            "illegal parameter unit was accepted");
    }
    {
        IsolatedCorpus corpus;
        auto profile = ReadJson(corpus.ProfileRoot() / "profile.json");
        for (auto& parameter : profile["parameters"]) {
            if (parameter["id"] == "queue-capacity-items") {
                parameter["value"] = 4'294'967'296ULL;
            }
        }
        WriteJson(corpus.ProfileRoot() / "profile.json", profile);
        RefreshProfileManifest(corpus.ProfileRoot(), "profile.json");
        RequireConfigError(
            [&] { static_cast<void>(ihomeland::sim::LoadBattleRuntimeConfig(corpus.ProfileRoot(), corpus.ModelRoot())); },
            ihomeland::sim::BattleConfigErrorCode::Schema,
            "budget overflow was accepted");
    }
    {
        IsolatedCorpus corpus;
        auto report = ReadJson(corpus.ProfileRoot() / "reports" / "qualification.json");
        report["stale"] = Json::array({"old-result"});
        WriteJson(corpus.ProfileRoot() / "reports" / "qualification.json", report);
        RefreshProfileManifest(corpus.ProfileRoot(), "reports/qualification.json");
        RequireConfigError(
            [&] { static_cast<void>(ihomeland::sim::LoadBattleRuntimeConfig(corpus.ProfileRoot(), corpus.ModelRoot())); },
            ihomeland::sim::BattleConfigErrorCode::Qualification,
            "stale qualification report was accepted");
    }

    const auto profile_digest =
        ihomeland::sim::Sha256File(std::filesystem::path(IHOMELAND_PROFILE_ROOT) / "manifest.json");
    static_cast<void>(ihomeland::sim::LoadBattleRuntimeConfig(
        std::filesystem::path(IHOMELAND_PROFILE_ROOT),
        std::filesystem::path(IHOMELAND_MODEL_ROOT)));
    Require(
        ihomeland::sim::Sha256File(std::filesystem::path(IHOMELAND_PROFILE_ROOT) / "manifest.json") ==
            profile_digest,
        "runtime config reader modified source corpus");
}

}  // namespace

/// main 执行 canonical、离线 contract 与配置负例集合。
int main() {
    try {
        TestCanonicalJson();
        TestOfflineContracts();
        TestConfigNegatives();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
