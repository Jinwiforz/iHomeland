#pragma once

#include <filesystem>
#include <cstdint>
#include <memory>
#include <string>
#include <vector>

namespace ihomeland::sim {

struct PersonalWorldArenaCatalog;

/// GameplayPackageBinding 是 C++ 从本机 production source 独立重算的部署身份。
struct GameplayPackageBinding final {
    /// package_id 是 operator 选择且 source 声明一致的 semantic identity。
    std::string package_id;
    /// config_identity 绑定五份 closed document 与 governance manifest。
    std::string config_identity;
    /// navigation_identity 绑定 external Detour source。
    std::string navigation_identity;
    /// physics_identity 绑定 external Jolt source。
    std::string physics_identity;
    /// wire_identity 绑定 battle wire manifest。
    std::string wire_identity;
    /// mapping_identity 绑定 semantic/numeric mapping bytes。
    std::string mapping_identity;
    /// map_id 是 server arena 与 Unity presentation 共用的 semantic identity。
    std::string map_id;
    /// map_content_identity 绑定 versioned arena geometry source。
    std::string map_content_identity;
};

/// GameplayAuthorityReference 是解析并验证 target kind 后的 immutable edge。
struct GameplayAuthorityReference final {
    /// field 是 authority object 内的 typed edge name。
    std::string field;
    /// target_id 是已存在的 production semantic identity。
    std::string target_id;
    /// target_kind 必须与目标 object kind 完全一致。
    std::string target_kind;
};

/// GameplayNumericValue 保存 checked int64 与显式单位，禁止 runtime 重解文本。
struct GameplayNumericValue final {
    /// field 是 object 内唯一数值字段名。
    std::string field;
    /// value 是从规范十进制文本 checked 解析的 int64。
    std::int64_t value;
    /// unit 禁止调用方以字段名猜测量纲。
    std::string unit;
};

/// GameplayAuthorityObject 是 production authority graph 的 immutable typed node。
struct GameplayAuthorityObject final {
    /// id 是非 fixture production semantic identity。
    std::string id;
    /// kind 是治理 registry 登记的 semantic kind。
    std::string kind;
    /// role 是 package required role 或 none。
    std::string role;
    /// references 是已验证 target kind 的有序边集合。
    std::vector<GameplayAuthorityReference> references;
    /// numeric_values 是 checked int64 与单位的有序集合。
    std::vector<GameplayNumericValue> numeric_values;
};

/// GameplayWireMapping 是 semantic identity 到非零 uint32 的唯一映射。
struct GameplayWireMapping final {
    /// kind 保持 actor/weapon/ability/projectile namespace 隔离。
    std::string kind;
    /// semantic_id 必须引用同 kind authority object。
    std::string semantic_id;
    /// numeric_id 是 nonzero wire uint32 identity。
    std::uint32_t numeric_id;
};

/// GameplayPackageCatalog 保存 node/instance 只读共享的 production authority snapshot。
struct GameplayPackageCatalog final {
    /// binding 保存跨 Go/C++/Unity 相同的 source identities。
    GameplayPackageBinding binding;
    /// objects 是 immutable authority graph snapshot。
    std::vector<GameplayAuthorityObject> objects;
    /// mappings 是完整 wire-eligible object 映射。
    std::vector<GameplayWireMapping> mappings;
    /// arena 是由独立 source identity gate 加载的 server authority snapshot。
    std::shared_ptr<const PersonalWorldArenaCatalog> arena;
};

/// LoadGameplayPackageCatalog 完整解析 authority 与 mapping，并验证 typed graph/overflow/coverage。
[[nodiscard]] GameplayPackageCatalog LoadGameplayPackageCatalog(
    const std::filesystem::path& root,
    const std::string& expected_package_id,
    const std::string& expected_config_identity,
    const std::string& expected_navigation_identity,
    const std::string& expected_physics_identity,
    const std::string& expected_wire_identity,
    const std::string& expected_model_manifest,
    const std::string& expected_profile_manifest);

/// LoadGameplayPackageBinding 在 node ready 前验证 closed root 与全部跨边界 identity。
///
/// root 只用于本机读取；异常不得携带绝对路径或 document 全文。
[[nodiscard]] GameplayPackageBinding LoadGameplayPackageBinding(
    const std::filesystem::path& root,
    const std::string& expected_package_id,
    const std::string& expected_config_identity,
    const std::string& expected_navigation_identity,
    const std::string& expected_physics_identity,
    const std::string& expected_wire_identity,
    const std::string& expected_model_manifest,
    const std::string& expected_profile_manifest);

}  // namespace ihomeland::sim
