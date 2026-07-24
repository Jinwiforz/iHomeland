#pragma once

#include "ihomeland/sim/navigation/navigation_world.hpp"

#include <cstddef>
#include <cstdint>
#include <memory>
#include <span>
#include <stdexcept>

namespace ihomeland::sim {

/// DetourNavigationErrorCode 是 live navigation adapter 的稳定失败分类。
enum class DetourNavigationErrorCode : std::uint8_t {
    /// InvalidConfig 表示单位、预算或 identity 结构不合法。
    InvalidConfig,
    /// AssetDrift 表示 map/version/digest/scale 或 nav bytes 漂移。
    AssetDrift,
    /// NotLoaded 表示 query 绕过启动阶段的 asset gate。
    NotLoaded,
    /// Capacity 表示 nodes、polygons 或 points 有界预算不足。
    Capacity,
    /// Status 表示 Detour 返回不可接受的失败或 partial status。
    Status,
    /// NonFinite 表示坐标转换产生 NaN、Inf 或整数越界。
    NonFinite,
};

/// DetourNavigationError 保留不依赖第三方 status 数值的项目失败原因。
class DetourNavigationError final : public std::runtime_error {
public:
    /// 构造函数绑定稳定错误码与低敏诊断文本。
    DetourNavigationError(DetourNavigationErrorCode code, const char* message);

    /// Code 返回供测试和 evidence 使用的稳定错误码。
    [[nodiscard]] DetourNavigationErrorCode Code() const noexcept;

private:
    /// code_ 是当前 adapter 失败分类。
    DetourNavigationErrorCode code_;
};

/// DetourNavigationConfig 冻结 asset identity、坐标范围与全部 query hard limits。
struct DetourNavigationConfig final {
    /// expected_asset 是该实例唯一允许加载的 nav identity。
    NavigationAssetIdentity expected_asset;
    /// maximum_nodes 是 Detour search node pool hard limit。
    std::uint32_t maximum_nodes;
    /// maximum_polygons 是 nearest/path corridor hard limit。
    std::uint32_t maximum_polygons;
    /// maximum_points 是规范 path point hard limit。
    std::uint32_t maximum_points;
    /// nearest_half_extent_mm 是 nearest-poly 三轴固定搜索半径。
    std::uint32_t nearest_half_extent_mm;
    /// maximum_coordinate_mm 是进入 float 边界前允许的坐标绝对值。
    std::int64_t maximum_coordinate_mm;
};

/// DetourNavigationWorld 只消费已版本化 nav bytes，并以 PIMPL 隔离 Detour types。
class DetourNavigationWorld final : public NavigationWorld {
public:
    /// 构造函数复制有界 nav bytes；实际加载由 LoadAsset identity gate 触发。
    DetourNavigationWorld(
        DetourNavigationConfig config,
        std::span<const std::byte> nav_data);

    /// 析构函数按 query、nav mesh、owned tile data 的顺序释放第三方资源。
    ~DetourNavigationWorld() override;

    DetourNavigationWorld(const DetourNavigationWorld&) = delete;
    DetourNavigationWorld& operator=(const DetourNavigationWorld&) = delete;
    DetourNavigationWorld(DetourNavigationWorld&&) noexcept;
    DetourNavigationWorld& operator=(DetourNavigationWorld&&) noexcept;

    /// LoadAsset 验证 map/version/digest/scale 和原始 bytes digest 后初始化 runtime。
    void LoadAsset(const NavigationAssetIdentity& identity) override;

    /// Query 执行有界 nearest/path 查询并返回量化、规范的项目值。
    [[nodiscard]] NavigationQueryResult Query(const NavigationQuery& query) override;

private:
    /// Impl 是唯一允许出现 Detour ref、status、allocator 与 nav handle 的 owner。
    class Impl;
    /// impl_ 唯一拥有 copied bytes 与 Detour runtime。
    std::unique_ptr<Impl> impl_;
};

}  // namespace ihomeland::sim
