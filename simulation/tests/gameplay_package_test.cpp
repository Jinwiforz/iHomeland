#include "ihomeland/sim/config/gameplay_package.hpp"

#include <filesystem>
#include <iostream>
#include <stdexcept>
#include <string>

namespace {

constexpr const char* PackageId = "personal-world-combat-v1";
constexpr const char* ConfigIdentity =
    "d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b";
constexpr const char* NavigationIdentity =
    "14165efe5b47caf6c7c8e2f9a4794c4d238aa4badeedc3d5badb41a9c2d5d19f";
constexpr const char* PhysicsIdentity =
    "64d53e1803d7cb14f4953ba1978175b162577eeb064716b2630cad0acf4de02e";
constexpr const char* WireIdentity =
    "9a40facbb23aafc556d38b403c8f8b1e264e0f2d9414e32da434b11d07554432";
constexpr const char* ModelManifest =
    "65e136d20dfa244db4ce42007cfe1c0411b7f807b635209704b6ef51e93d08b1";
constexpr const char* ProfileManifest =
    "c7ff3d1f582625d18028c4ce20fb808b2e56e10084c4ccf61790b0c5f486c424";
constexpr const char* MappingIdentity =
    "9e78652d1d05524c2a67668b208f703120a214bfb45f053c94f6c3dc79595e3b";

/// Require 把 package loader 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// Load 使用 production source 与固定 deployment expectations。
[[nodiscard]] ihomeland::sim::GameplayPackageCatalog Load(
    const std::string& config_identity = ConfigIdentity) {
    return ihomeland::sim::LoadGameplayPackageCatalog(
        std::filesystem::path{IHOMELAND_GAMEPLAY_PACKAGE_ROOT},
        PackageId,
        config_identity,
        NavigationIdentity,
        PhysicsIdentity,
        WireIdentity,
        ModelManifest,
        ProfileManifest);
}

/// TestProductionSelection 验证 C++ 独立重算 Go 冻结的同一摘要集合。
void TestProductionSelection() {
    const auto selected = Load();
    Require(
        selected.binding.package_id == PackageId &&
            selected.binding.config_identity == ConfigIdentity &&
            selected.binding.navigation_identity == NavigationIdentity &&
            selected.binding.physics_identity == PhysicsIdentity &&
            selected.binding.wire_identity == WireIdentity &&
            selected.binding.mapping_identity == MappingIdentity &&
            !selected.objects.empty() && selected.mappings.size() == 10,
        "gameplay package selection differs");
}

/// TestExpectedIdentityDriftRejected 验证 child 不接受 Go/C++ source mismatch。
void TestExpectedIdentityDriftRejected() {
    try {
        static_cast<void>(Load(std::string(64, '0')));
    } catch (const std::runtime_error& error) {
        Require(
            std::string(error.what()).find(
                IHOMELAND_GAMEPLAY_PACKAGE_ROOT) == std::string::npos,
            "gameplay package error leaked local path");
        return;
    }
    throw std::runtime_error(
        "gameplay package expected identity drift was accepted");
}

}  // namespace

int main() {
    try {
        TestProductionSelection();
        TestExpectedIdentityDriftRejected();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
