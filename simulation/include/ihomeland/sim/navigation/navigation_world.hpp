#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace ihomeland::sim {

/// NavigationPointMm 是 NavigationWorld 边界唯一允许的整数坐标。
struct NavigationPointMm final {
    /// x 是 world X，单位 millimeters。
    std::int64_t x;
    /// y 是 world Y，单位 millimeters。
    std::int64_t y;
    /// z 是 world Z，单位 millimeters。
    std::int64_t z;

    /// operator== 比较规范整数坐标。
    bool operator==(const NavigationPointMm&) const = default;
};

/// NavigationAssetIdentity 绑定 map/version/digest/scale，禁止静默复用旧 nav data。
struct NavigationAssetIdentity final {
    /// map_id 是非零稳定 map identity。
    std::uint64_t map_id;
    /// version 是非零 nav data format/content version。
    std::uint32_t version;
    /// sha256 是 nav data 原始 bytes 的 lowercase SHA-256。
    std::string sha256;
    /// coordinate_scale_mm 是 Detour unit 到项目 millimeters 的正整数比例。
    std::uint32_t coordinate_scale_mm;

    /// operator== 用于 recorded/live adapter 启动 identity gate。
    bool operator==(const NavigationAssetIdentity&) const = default;
};

/// NavigationQueryKind 是首版 AI 允许的闭合 navigation query 集。
enum class NavigationQueryKind : std::uint8_t {
    /// NearestPoly 返回 start 附近唯一规范点。
    NearestPoly,
    /// FindPath 返回 start 到 end 的有序路径点。
    FindPath,
};

/// NavigationQuery 是 AIIntent 提交给 adapter 的完整项目 value。
struct NavigationQuery final {
    /// query_id 是实例/Tick 内非零稳定 identity。
    std::uint64_t query_id;
    /// tick 是当前非零 SimulationTick。
    std::uint64_t tick;
    /// kind 决定 nearest-poly 或 path policy。
    NavigationQueryKind kind;
    /// actor_id 是 query authority owner。
    std::uint64_t actor_id;
    /// start_mm 是 query 起点。
    NavigationPointMm start_mm;
    /// end_mm 是 path 终点；NearestPoly 可等于 start。
    NavigationPointMm end_mm;
    /// maximum_points 是预先分配的正整数 result hard limit。
    std::size_t maximum_points;

    /// operator== 用于 recorded adapter 严格 trace 比较。
    bool operator==(const NavigationQuery&) const = default;
};

/// NavigationQueryResult 是 adapter 的规范 path value。
struct NavigationQueryResult final {
    /// query_id 必须与输入完全一致。
    std::uint64_t query_id;
    /// points_mm 对 NearestPoly 恰有一个点，对 FindPath 保持路径顺序。
    std::vector<NavigationPointMm> points_mm;
    /// path_identity 是 asset/query/points 的 lowercase SHA-256。
    std::string path_identity;
};

/// CanonicalNavigationPathIdentity 绑定 asset/query/有序 points 并返回 lowercase SHA-256。
[[nodiscard]] std::string CanonicalNavigationPathIdentity(
    const NavigationAssetIdentity& asset,
    const NavigationQuery& query,
    const std::vector<NavigationPointMm>& points);

/// NavigationWorld 是 AI systems 可依赖的唯一 navigation port。
class NavigationWorld {
public:
    /// 虚析构函数允许实例 owner 经窄接口释放 adapter。
    virtual ~NavigationWorld() = default;

    /// LoadAsset 在实例启动阶段验证并绑定唯一 nav asset。
    virtual void LoadAsset(const NavigationAssetIdentity& identity) = 0;

    /// Query 同步返回规范 result，不允许运行时烘焙或返回第三方 status。
    [[nodiscard]] virtual NavigationQueryResult Query(const NavigationQuery& query) = 0;
};

}  // namespace ihomeland::sim
