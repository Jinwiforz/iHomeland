#include "ihomeland/sim/config/gameplay_package.hpp"

#include "ihomeland/sim/core/sha256.hpp"

#include <nlohmann/json.hpp>

#include <algorithm>
#include <array>
#include <charconv>
#include <fstream>
#include <iterator>
#include <limits>
#include <map>
#include <set>
#include <stdexcept>
#include <string_view>

namespace ihomeland::sim {
namespace {

using Json = nlohmann::json;

constexpr std::string_view FormatVersion =
    "gameplay-config-format-v1";
constexpr std::string_view ProductionState = "production";
constexpr std::uintmax_t MaximumDocumentBytes = 1U << 20U;

const std::map<std::string, std::string, std::less<>> RequiredDocuments{
    {"authority.json", "authority-catalog"},
    {"bindings.json", "external-bindings"},
    {"package.json", "gameplay-package"},
    {"presentation.json", "presentation-catalog"},
    {"wire-mapping.json", "wire-mapping"},
};

/// RequireKeys 验证 deployment-owned document 的 closed 顶层字段。
void RequireKeys(
    const Json& value,
    const std::set<std::string, std::less<>>& expected,
    const char* context) {
    if (!value.is_object() || value.size() != expected.size()) {
        throw std::runtime_error(
            std::string("gameplay package ") + context +
            " fields are invalid");
    }
    for (const auto& [name, ignored] : value.items()) {
        static_cast<void>(ignored);
        if (!expected.contains(name)) {
            throw std::runtime_error(
                std::string("gameplay package ") + context +
                " contains an unknown field");
        }
    }
}

/// ReadDocument 读取有界 regular file 并将 filesystem 失败收敛为低敏分类。
[[nodiscard]] std::string ReadDocument(
    const std::filesystem::path& path) {
    std::error_code error;
    const auto status = std::filesystem::symlink_status(path, error);
    const auto size = std::filesystem::file_size(path, error);
    if (error || !std::filesystem::is_regular_file(status) ||
        size == 0 || size > MaximumDocumentBytes) {
        throw std::runtime_error(
            "gameplay package document source is invalid");
    }
    std::ifstream stream(path, std::ios::binary);
    if (!stream) {
        throw std::runtime_error(
            "gameplay package document cannot be read");
    }
    std::string source{
        std::istreambuf_iterator<char>{stream},
        std::istreambuf_iterator<char>{}};
    if (stream.bad()) {
        throw std::runtime_error(
            "gameplay package document cannot be read");
    }
    return source;
}

/// ParseDocument 使用 strict UTF-8 JSON parser 且不在异常中保留 source。
[[nodiscard]] Json ParseDocument(const std::string& source) {
    try {
        return Json::parse(source, nullptr, true, true);
    } catch (const Json::exception&) {
        throw std::runtime_error("gameplay package JSON is invalid");
    }
}

/// RequireString 返回非空 string 字段。
[[nodiscard]] const std::string& RequireString(
    const Json& value,
    const char* field) {
    if (!value.contains(field) || !value.at(field).is_string() ||
        value.at(field).get_ref<const std::string&>().empty()) {
        throw std::runtime_error(
            "gameplay package string binding is invalid");
    }
    return value.at(field).get_ref<const std::string&>();
}

/// RequireBinding 验证 version/digest closed binding。
[[nodiscard]] const std::string& RequireBinding(
    const Json& value,
    const char* field,
    const std::string_view expected_version) {
    const auto& binding = value.at(field);
    RequireKeys(
        binding,
        {"manifest_sha256", "version"},
        "manifest binding");
    if (RequireString(binding, "version") != expected_version) {
        throw std::runtime_error(
            "gameplay package manifest version differs");
    }
    const auto& digest = RequireString(binding, "manifest_sha256");
    if (digest.size() != 64 || !std::ranges::all_of(
            digest,
            [](const char character) {
                return (character >= '0' && character <= '9') ||
                       (character >= 'a' && character <= 'f');
            })) {
        throw std::runtime_error(
            "gameplay package manifest digest is invalid");
    }
    return digest;
}

/// ValidateEnvelope 隔离 fixture/classification 并绑定 document kind/package。
void ValidateEnvelope(
    const Json& document,
    const std::string_view kind,
    const std::string& package_id) {
    if (RequireString(document, "format_version") != FormatVersion ||
        RequireString(document, "document_kind") != kind ||
        RequireString(document, "package_id") != package_id ||
        RequireString(document, "qualification_state") !=
            ProductionState) {
        throw std::runtime_error(
            "gameplay package document envelope is invalid");
    }
}

}  // namespace

GameplayPackageCatalog LoadGameplayPackageCatalog(
    const std::filesystem::path& root,
    const std::string& expected_package_id,
    const std::string& expected_config_identity,
    const std::string& expected_navigation_identity,
    const std::string& expected_physics_identity,
    const std::string& expected_wire_identity,
    const std::string& expected_model_manifest,
    const std::string& expected_profile_manifest) {
    try {
        if (!root.is_absolute() || expected_package_id.empty()) {
            throw std::runtime_error(
                "gameplay package selection input is invalid");
        }
        std::error_code error;
        std::set<std::string, std::less<>> entries;
        for (const auto& entry :
             std::filesystem::directory_iterator(root, error)) {
            if (error || !entry.is_regular_file(error)) {
                throw std::runtime_error(
                    "gameplay package root is invalid");
            }
            entries.insert(entry.path().filename().string());
        }
        std::set<std::string, std::less<>> required;
        for (const auto& [path, ignored] : RequiredDocuments) {
            static_cast<void>(ignored);
            required.insert(path);
        }
        if (error || entries != required) {
            throw std::runtime_error(
                "gameplay package root is not closed");
        }

        std::map<std::string, Json, std::less<>> parsed;
        std::map<std::string, std::string, std::less<>> digests;
        for (const auto& [relative, kind] : RequiredDocuments) {
            const auto source = ReadDocument(root / relative);
            auto document = ParseDocument(source);
            ValidateEnvelope(document, kind, expected_package_id);
            digests.emplace(relative, Sha256Text(source));
            parsed.emplace(relative, std::move(document));
        }

        const auto& package = parsed.at("package.json");
        RequireKeys(
            package,
            {"authority_path", "bindings_path", "document_kind",
             "format_version", "governance_binding", "model_binding",
             "package_id", "package_version", "presentation_path",
             "profile_binding", "qualification_state", "required_roles",
             "wire_binding", "wire_mapping_path"},
            "manifest");
        if (RequireString(package, "package_version") !=
                expected_package_id ||
            RequireString(package, "authority_path") != "authority.json" ||
            RequireString(package, "bindings_path") != "bindings.json" ||
            RequireString(package, "presentation_path") !=
                "presentation.json" ||
            RequireString(package, "wire_mapping_path") !=
                "wire-mapping.json" ||
            !package.at("required_roles").is_array() ||
            package.at("required_roles").empty()) {
            throw std::runtime_error(
                "gameplay package manifest selection is invalid");
        }
        const auto& governance = RequireBinding(
            package, "governance_binding", FormatVersion);
        if (RequireBinding(
                package, "model_binding", "battle-model-v1") !=
                expected_model_manifest ||
            RequireBinding(
                package,
                "profile_binding",
                "battle-network-profile-v2") !=
                expected_profile_manifest ||
            RequireBinding(
                package, "wire_binding", "battle-wire-v1") !=
                expected_wire_identity) {
            throw std::runtime_error(
                "gameplay package upstream binding differs");
        }

        const auto& bindings = parsed.at("bindings.json");
        RequireKeys(
            bindings,
            {"collision_layer_ref", "document_kind", "format_version",
             "map_content_identity", "map_id", "navigation_identity",
             "navigation_policy_ref", "package_id", "physics_identity",
             "qualification_state"},
            "external binding");
        const auto& navigation =
            RequireString(bindings, "navigation_identity");
        const auto& physics =
            RequireString(bindings, "physics_identity");
        const auto& map_id = RequireString(bindings, "map_id");
        const auto& map_content_identity =
            RequireString(bindings, "map_content_identity");
        if (navigation != expected_navigation_identity ||
            physics != expected_physics_identity) {
            throw std::runtime_error(
                "gameplay package external binding differs");
        }

        std::string identity_material =
            "ihomeland-gameplay-production-v1\n" + governance + "\n";
        for (const auto& [relative, kind] : RequiredDocuments) {
            identity_material += relative;
            identity_material.push_back('\0');
            identity_material += kind;
            identity_material.push_back('\0');
            identity_material += digests.at(relative);
            identity_material.push_back('\n');
        }
        const auto config_identity = Sha256Text(identity_material);
        if (config_identity != expected_config_identity) {
            throw std::runtime_error(
                "gameplay package source identity differs");
        }
        const auto& authority = parsed.at("authority.json");
        RequireKeys(
            authority,
            {"document_kind", "format_version", "objects", "package_id",
             "qualification_state"},
            "authority");
        if (!authority.at("objects").is_array() ||
            authority.at("objects").empty()) {
            throw std::runtime_error(
                "gameplay package authority objects are invalid");
        }
        std::vector<GameplayAuthorityObject> objects;
        std::map<std::string, std::string, std::less<>> object_kinds;
        for (const auto& value : authority.at("objects")) {
            RequireKeys(
                value,
                {"id", "kind", "numeric_values", "references", "role"},
                "authority object");
            GameplayAuthorityObject object{
                .id = RequireString(value, "id"),
                .kind = RequireString(value, "kind"),
                .role = RequireString(value, "role"),
            };
            if (object.id.starts_with("fixture/") ||
                !value.at("references").is_array() ||
                !value.at("numeric_values").is_array() ||
                !object_kinds.emplace(object.id, object.kind).second) {
                throw std::runtime_error(
                    "gameplay package authority identity is invalid");
            }
            std::set<std::string, std::less<>> numeric_fields;
            for (const auto& numeric : value.at("numeric_values")) {
                RequireKeys(
                    numeric,
                    {"field", "unit", "value"},
                    "numeric value");
                const auto& text = RequireString(numeric, "value");
                std::int64_t parsed_value = 0;
                const auto result = std::from_chars(
                    text.data(), text.data() + text.size(), parsed_value);
                const auto& field = RequireString(numeric, "field");
                if (result.ec != std::errc{} ||
                    result.ptr != text.data() + text.size() ||
                    (text.size() > 1 && text.front() == '0') ||
                    text == "-0" || !numeric_fields.insert(field).second) {
                    throw std::runtime_error(
                        "gameplay package numeric value is invalid");
                }
                object.numeric_values.push_back(GameplayNumericValue{
                    .field = field,
                    .value = parsed_value,
                    .unit = RequireString(numeric, "unit"),
                });
            }
            std::set<std::string, std::less<>> reference_edges;
            for (const auto& reference : value.at("references")) {
                RequireKeys(
                    reference,
                    {"field", "target_id", "target_kind"},
                    "authority reference");
                GameplayAuthorityReference edge{
                    .field = RequireString(reference, "field"),
                    .target_id = RequireString(reference, "target_id"),
                    .target_kind = RequireString(reference, "target_kind"),
                };
                const auto edge_key = edge.field + std::string(1, '\0') +
                                      edge.target_id;
                if (edge.target_id.starts_with("fixture/") ||
                    !reference_edges.insert(edge_key).second) {
                    throw std::runtime_error(
                        "gameplay package authority reference is invalid");
                }
                object.references.push_back(std::move(edge));
            }
            objects.push_back(std::move(object));
        }
        for (const auto& object : objects) {
            for (const auto& reference : object.references) {
                const auto target = object_kinds.find(reference.target_id);
                if (target == object_kinds.end() ||
                    target->second != reference.target_kind) {
                    throw std::runtime_error(
                        "gameplay package authority reference differs");
                }
            }
        }

        const auto& mapping_document = parsed.at("wire-mapping.json");
        RequireKeys(
            mapping_document,
            {"document_kind", "format_version", "mappings", "package_id",
             "qualification_state", "retired_numeric_ids",
             "retired_semantic_ids"},
            "wire mapping");
        if (!mapping_document.at("mappings").is_array() ||
            mapping_document.at("mappings").empty() ||
            !mapping_document.at("retired_numeric_ids").is_array() ||
            !mapping_document.at("retired_semantic_ids").is_array()) {
            throw std::runtime_error(
                "gameplay package wire mapping arrays are invalid");
        }
        std::set<std::uint32_t> numeric_ids;
        std::set<std::string, std::less<>> semantic_ids;
        std::vector<GameplayWireMapping> mappings;
        std::uint32_t previous_numeric_id = 0;
        for (const auto& mapping : mapping_document.at("mappings")) {
            RequireKeys(
                mapping,
                {"kind", "numeric_id", "semantic_id"},
                "wire mapping entry");
            if (!mapping.at("numeric_id").is_number_unsigned()) {
                throw std::runtime_error(
                    "gameplay package wire numeric identity is invalid");
            }
            const auto numeric64 = mapping.at("numeric_id").get<std::uint64_t>();
            if (numeric64 == 0 ||
                numeric64 > std::numeric_limits<std::uint32_t>::max()) {
                throw std::runtime_error(
                    "gameplay package wire numeric identity is invalid");
            }
            GameplayWireMapping typed{
                .kind = RequireString(mapping, "kind"),
                .semantic_id = RequireString(mapping, "semantic_id"),
                .numeric_id = static_cast<std::uint32_t>(numeric64),
            };
            const auto object = object_kinds.find(typed.semantic_id);
            if (object == object_kinds.end() || object->second != typed.kind ||
                typed.semantic_id.starts_with("fixture/") ||
                typed.numeric_id <= previous_numeric_id ||
                !numeric_ids.insert(typed.numeric_id).second ||
                !semantic_ids.insert(typed.semantic_id).second) {
                throw std::runtime_error(
                    "gameplay package wire mapping identity differs");
            }
            previous_numeric_id = typed.numeric_id;
            mappings.push_back(std::move(typed));
        }
        for (const auto& [id, kind] : object_kinds) {
            if ((kind == "actor" || kind == "weapon" || kind == "ability" ||
                 kind == "projectile") && !semantic_ids.contains(id)) {
                throw std::runtime_error(
                    "gameplay package wire mapping coverage differs");
            }
        }
        for (const auto& retired : mapping_document.at("retired_numeric_ids")) {
            if (!retired.is_number_unsigned() ||
                retired.get<std::uint64_t>() == 0 ||
                retired.get<std::uint64_t>() >
                    std::numeric_limits<std::uint32_t>::max() ||
                numeric_ids.contains(
                    static_cast<std::uint32_t>(
                        retired.get<std::uint64_t>()))) {
                throw std::runtime_error(
                    "gameplay package retired numeric identity is invalid");
            }
        }
        for (const auto& retired : mapping_document.at("retired_semantic_ids")) {
            if (!retired.is_string() || retired.get_ref<const std::string&>().empty() ||
                semantic_ids.contains(retired.get_ref<const std::string&>())) {
                throw std::runtime_error(
                    "gameplay package retired semantic identity is invalid");
            }
        }
        return GameplayPackageCatalog{
            .binding = GameplayPackageBinding{
                .package_id = expected_package_id,
                .config_identity = config_identity,
                .navigation_identity = navigation,
                .physics_identity = physics,
                .wire_identity = expected_wire_identity,
                .mapping_identity = digests.at("wire-mapping.json"),
                .map_id = map_id,
                .map_content_identity = map_content_identity,
            },
            .objects = std::move(objects),
            .mappings = std::move(mappings),
        };
    } catch (const std::filesystem::filesystem_error&) {
        throw std::runtime_error("gameplay package filesystem failure");
    }
}

GameplayPackageBinding LoadGameplayPackageBinding(
    const std::filesystem::path& root,
    const std::string& expected_package_id,
    const std::string& expected_config_identity,
    const std::string& expected_navigation_identity,
    const std::string& expected_physics_identity,
    const std::string& expected_wire_identity,
    const std::string& expected_model_manifest,
    const std::string& expected_profile_manifest) {
    return LoadGameplayPackageCatalog(
               root,
               expected_package_id,
               expected_config_identity,
               expected_navigation_identity,
               expected_physics_identity,
               expected_wire_identity,
               expected_model_manifest,
               expected_profile_manifest)
        .binding;
}

}  // namespace ihomeland::sim
