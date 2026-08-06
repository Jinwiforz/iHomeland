#pragma once

#include "ihomeland/sim/navigation/navigation_world.hpp"
#include "ihomeland/sim/physics/physics_world.hpp"

#include <cstddef>
#include <cstdint>
#include <filesystem>
#include <memory>
#include <string>
#include <vector>

namespace ihomeland::sim {

/// ArenaSpawnPoint 是 versioned server arena source 中的确定出生锚点。
struct ArenaSpawnPoint final {
    /// id 是 encounter role 使用的稳定名称。
    std::string id;
    /// position_mm 是服务器权威整数坐标。
    Vector3Mm position_mm;
    /// yaw_millidegrees 是服务器权威朝向。
    std::int32_t yaw_millidegrees;
};

/// PersonalWorldArenaCatalog 是 Jolt/Detour adapter 共用的不可变 arena snapshot。
struct PersonalWorldArenaCatalog final {
    /// map_id 必须与 production bindings 完全一致。
    std::string map_id;
    /// map_content_identity 绑定 arena.json 原始 bytes。
    std::string map_content_identity;
    /// navigation_identity 绑定 navigation.json 原始 bytes。
    std::string navigation_identity;
    /// physics_identity 绑定 physics.json 原始 bytes。
    std::string physics_identity;
    /// minimum_mm 定义闭合查询范围的最小坐标。
    Vector3Mm minimum_mm;
    /// maximum_mm 定义闭合查询范围的最大坐标。
    Vector3Mm maximum_mm;
    /// spawn_points 按 source 顺序冻结，id 唯一。
    std::vector<ArenaSpawnPoint> spawn_points;
    /// implementation 保存已验证 collider/nav data，不暴露第三方类型。
    std::shared_ptr<const void> implementation;
};

/// LoadPersonalWorldArenaCatalog 读取 exact closed source 并重算 map/nav/physics identity。
///
/// root 仅用于本机启动；异常不得包含路径或 source 全文。
[[nodiscard]] PersonalWorldArenaCatalog LoadPersonalWorldArenaCatalog(
    const std::filesystem::path& root,
    const std::string& expected_map_id,
    const std::string& expected_map_content_identity,
    const std::string& expected_navigation_identity,
    const std::string& expected_physics_identity);

/// CreatePersonalWorldPhysicsWorld 为一个 instance 创建独占 Jolt query world。
[[nodiscard]] std::shared_ptr<PhysicsWorld> CreatePersonalWorldPhysicsWorld(
    const PersonalWorldArenaCatalog& catalog);

/// CreatePersonalWorldNavigationWorld 为一个 instance 创建已完成 identity gate 的 Detour world。
[[nodiscard]] std::shared_ptr<NavigationWorld> CreatePersonalWorldNavigationWorld(
    const PersonalWorldArenaCatalog& catalog);

}  // namespace ihomeland::sim
