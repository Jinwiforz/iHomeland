#include "ihomeland/sim/navigation/detour_navigation_world.hpp"

#include "ihomeland/sim/core/sha256.hpp"

#include <DetourAlloc.h>
#include <DetourNavMesh.h>
#include <DetourNavMeshQuery.h>
#include <DetourStatus.h>

#include <algorithm>
#include <array>
#include <cmath>
#include <cstring>
#include <limits>
#include <tuple>
#include <utility>
#include <vector>

namespace ihomeland::sim {
namespace {

/// IsLowercaseSha256 检查闭合 lowercase hex digest。
[[nodiscard]] bool IsLowercaseSha256(const std::string& value) {
    return value.size() == 64 &&
           std::all_of(value.begin(), value.end(), [](const char item) {
               return (item >= '0' && item <= '9') ||
                      (item >= 'a' && item <= 'f');
           });
}

/// ValidateConfig 在任何第三方分配前拒绝漂移配置和不可表达预算。
void ValidateConfig(const DetourNavigationConfig& config) {
    if (config.expected_asset.map_id == 0 ||
        config.expected_asset.version == 0 ||
        !IsLowercaseSha256(config.expected_asset.sha256) ||
        config.expected_asset.coordinate_scale_mm == 0 ||
        config.maximum_nodes == 0 || config.maximum_nodes > 65'535 ||
        config.maximum_polygons == 0 ||
        config.maximum_polygons >
            static_cast<std::uint32_t>(std::numeric_limits<int>::max()) ||
        config.maximum_points < 2 ||
        config.maximum_points >
            static_cast<std::uint32_t>(std::numeric_limits<int>::max() / 3) ||
        config.nearest_half_extent_mm == 0 ||
        config.maximum_coordinate_mm <= 0) {
        throw DetourNavigationError(
            DetourNavigationErrorCode::InvalidConfig,
            "Detour navigation configuration is invalid");
    }
}

/// RejectStatus 把 Detour status 映射为闭合项目错误，不泄漏 numeric bits。
void RejectStatus(const dtStatus status, const char* message) {
    if (dtStatusDetail(status, DT_BUFFER_TOO_SMALL) ||
        dtStatusDetail(status, DT_OUT_OF_NODES) ||
        dtStatusDetail(status, DT_OUT_OF_MEMORY)) {
        throw DetourNavigationError(
            DetourNavigationErrorCode::Capacity,
            "Detour navigation query exhausted a fixed budget");
    }
    if (!dtStatusSucceed(status) ||
        dtStatusDetail(status, DT_PARTIAL_RESULT)) {
        throw DetourNavigationError(DetourNavigationErrorCode::Status, message);
    }
}

}  // namespace

/// DetourNavigationWorld::Impl 是 nav bytes、mesh、query 与转换策略的唯一 owner。
class DetourNavigationWorld::Impl final {
public:
    /// NearestValue 保留内部 Detour ref 与已量化项目 point。
    struct NearestValue final {
        /// ref 仅在本次 adapter query 内使用。
        dtPolyRef ref;
        /// point 是候选 polygon 上的规范整数点。
        NavigationPointMm point;
    };

    /// 构造函数复制 nav bytes，并在复制前验证配置和输入大小。
    Impl(
        DetourNavigationConfig config,
        const std::span<const std::byte> nav_data)
        : config_(std::move(config)), nav_data_(nav_data.begin(), nav_data.end()) {
        ValidateConfig(config_);
        if (nav_data_.empty() ||
            nav_data_.size() >
                static_cast<std::size_t>(std::numeric_limits<int>::max())) {
            throw DetourNavigationError(
                DetourNavigationErrorCode::InvalidConfig,
                "Detour nav data size is invalid");
        }
    }

    /// 析构函数先释放 query，再由 nav mesh 释放 DT_TILE_FREE_DATA bytes。
    ~Impl() {
        if (query_ != nullptr) {
            dtFreeNavMeshQuery(query_);
        }
        if (nav_mesh_ != nullptr) {
            dtFreeNavMesh(nav_mesh_);
        }
    }

    Impl(const Impl&) = delete;
    Impl& operator=(const Impl&) = delete;

    /// LoadAsset 执行完整 identity/digest gate 并一次性初始化 runtime。
    void LoadAsset(const NavigationAssetIdentity& identity) {
        if (!(identity == config_.expected_asset) ||
            Sha256Bytes(nav_data_) != identity.sha256) {
            throw DetourNavigationError(
                DetourNavigationErrorCode::AssetDrift,
                "Detour nav asset identity or bytes digest drifted");
        }
        if (loaded_) {
            return;
        }
        auto* const owned_data = static_cast<unsigned char*>(
            dtAlloc(nav_data_.size(), DT_ALLOC_PERM));
        if (owned_data == nullptr) {
            throw DetourNavigationError(
                DetourNavigationErrorCode::Capacity,
                "Detour nav data allocation exhausted fixed memory");
        }
        std::memcpy(owned_data, nav_data_.data(), nav_data_.size());
        nav_mesh_ = dtAllocNavMesh();
        if (nav_mesh_ == nullptr) {
            dtFree(owned_data);
            throw DetourNavigationError(
                DetourNavigationErrorCode::Capacity,
                "Detour nav mesh allocation failed");
        }
        const auto nav_status = nav_mesh_->init(
            owned_data,
            static_cast<int>(nav_data_.size()),
            DT_TILE_FREE_DATA);
        if (!dtStatusSucceed(nav_status)) {
            dtFree(owned_data);
            dtFreeNavMesh(nav_mesh_);
            nav_mesh_ = nullptr;
            throw DetourNavigationError(
                DetourNavigationErrorCode::Status,
                "Detour rejected registered nav data");
        }
        query_ = dtAllocNavMeshQuery();
        if (query_ == nullptr) {
            dtFreeNavMesh(nav_mesh_);
            nav_mesh_ = nullptr;
            throw DetourNavigationError(
                DetourNavigationErrorCode::Capacity,
                "Detour nav query allocation failed");
        }
        const auto query_status =
            query_->init(nav_mesh_, static_cast<int>(config_.maximum_nodes));
        if (!dtStatusSucceed(query_status)) {
            dtFreeNavMeshQuery(query_);
            query_ = nullptr;
            dtFreeNavMesh(nav_mesh_);
            nav_mesh_ = nullptr;
            throw DetourNavigationError(
                DetourNavigationErrorCode::Status,
                "Detour nav query initialization failed");
        }
        loaded_ = true;
    }

    /// Query 执行闭合 nearest/path policy，并在 adapter 内完成 status mapping。
    [[nodiscard]] NavigationQueryResult Query(const NavigationQuery& query) const {
        ValidateQuery(query);
        if (!loaded_) {
            throw DetourNavigationError(
                DetourNavigationErrorCode::NotLoaded,
                "Detour navigation query requires a validated asset");
        }
        std::vector<NavigationPointMm> points;
        if (query.kind == NavigationQueryKind::NearestPoly) {
            points.push_back(FindNearest(query.start_mm).point);
        } else if (query.kind == NavigationQueryKind::FindPath) {
            points = FindPath(query);
        } else {
            throw DetourNavigationError(
                DetourNavigationErrorCode::InvalidConfig,
                "NavigationQuery kind is outside the fixed registry");
        }
        return {
            .query_id = query.query_id,
            .points_mm = points,
            .path_identity = CanonicalNavigationPathIdentity(
                config_.expected_asset,
                query,
                points)};
    }

private:
    /// ValidateQuery 拒绝无 identity、无容量或超出坐标 contract 的请求。
    void ValidateQuery(const NavigationQuery& query) const {
        const auto within = [this](const NavigationPointMm& point) {
            const auto valid_axis = [this](const std::int64_t value) {
                return value >= -config_.maximum_coordinate_mm &&
                       value <= config_.maximum_coordinate_mm;
            };
            return valid_axis(point.x) && valid_axis(point.y) &&
                   valid_axis(point.z);
        };
        if (query.query_id == 0 || query.tick == 0 || query.actor_id == 0 ||
            query.maximum_points == 0 ||
            query.maximum_points > config_.maximum_points ||
            !within(query.start_mm) || !within(query.end_mm)) {
            throw DetourNavigationError(
                DetourNavigationErrorCode::Capacity,
                "NavigationQuery identity, coordinate or point budget is invalid");
        }
    }

    /// ToDetour 把 millimeters 按已登记 scale 转成有限 float。
    [[nodiscard]] std::array<float, 3> ToDetour(
        const NavigationPointMm& point) const {
        const auto scale =
            static_cast<double>(config_.expected_asset.coordinate_scale_mm);
        const std::array<float, 3> converted{
            static_cast<float>(static_cast<double>(point.x) / scale),
            static_cast<float>(static_cast<double>(point.y) / scale),
            static_cast<float>(static_cast<double>(point.z) / scale)};
        if (!std::all_of(converted.begin(), converted.end(), [](const float value) {
                return std::isfinite(value);
            })) {
            throw DetourNavigationError(
                DetourNavigationErrorCode::NonFinite,
                "project coordinate cannot be represented by Detour");
        }
        return converted;
    }

    /// FromDetour 量化第三方 float point，并拒绝非有限值和配置范围外坐标。
    [[nodiscard]] NavigationPointMm FromDetour(const float* point) const {
        const auto scale =
            static_cast<double>(config_.expected_asset.coordinate_scale_mm);
        std::array<std::int64_t, 3> converted{};
        for (std::size_t index = 0; index < converted.size(); ++index) {
            const auto scaled = static_cast<double>(point[index]) * scale;
            if (!std::isfinite(scaled) ||
                scaled < -static_cast<double>(config_.maximum_coordinate_mm) ||
                scaled > static_cast<double>(config_.maximum_coordinate_mm)) {
                throw DetourNavigationError(
                    DetourNavigationErrorCode::NonFinite,
                    "Detour result cannot be quantized to project coordinates");
            }
            converted[index] = std::llround(scaled);
        }
        return {.x = converted[0], .y = converted[1], .z = converted[2]};
    }

    /// FindNearest 规范化 callback/container 顺序，并以 point/ref tie-break 选唯一候选。
    [[nodiscard]] NearestValue FindNearest(
        const NavigationPointMm& requested) const {
        const auto center = ToDetour(requested);
        const auto extent_value =
            static_cast<float>(config_.nearest_half_extent_mm) /
            static_cast<float>(config_.expected_asset.coordinate_scale_mm);
        const std::array<float, 3> extents{
            extent_value, extent_value, extent_value};
        std::vector<dtPolyRef> candidates(config_.maximum_polygons);
        int count = 0;
        const auto status = query_->queryPolygons(
            center.data(),
            extents.data(),
            &filter_,
            candidates.data(),
            &count,
            static_cast<int>(candidates.size()));
        RejectStatus(status, "Detour nearest polygon query failed");
        if (count <= 0) {
            throw DetourNavigationError(
                DetourNavigationErrorCode::Status,
                "Detour nearest polygon query found no candidate");
        }
        std::vector<NearestValue> values;
        values.reserve(static_cast<std::size_t>(count));
        for (int index = 0; index < count; ++index) {
            std::array<float, 3> closest{};
            bool over = false;
            const auto closest_status = query_->closestPointOnPoly(
                candidates[static_cast<std::size_t>(index)],
                center.data(),
                closest.data(),
                &over);
            static_cast<void>(over);
            RejectStatus(closest_status, "Detour closest point query failed");
            values.push_back({
                .ref = candidates[static_cast<std::size_t>(index)],
                .point = FromDetour(closest.data())});
        }
        const auto distance = [&requested](const NavigationPointMm& point) {
            const auto dx = static_cast<long double>(point.x) - requested.x;
            const auto dy = static_cast<long double>(point.y) - requested.y;
            const auto dz = static_cast<long double>(point.z) - requested.z;
            return dx * dx + dy * dy + dz * dz;
        };
        std::sort(values.begin(), values.end(), [&distance](
                                                  const NearestValue& left,
                                                  const NearestValue& right) {
            return std::tuple{
                       distance(left.point),
                       left.point.x,
                       left.point.y,
                       left.point.z,
                       left.ref} <
                   std::tuple{
                       distance(right.point),
                       right.point.x,
                       right.point.y,
                       right.point.z,
                       right.ref};
        });
        return values.front();
    }

    /// FindPath 执行有界 corridor/straight path，并拒绝 partial 或截断结果。
    [[nodiscard]] std::vector<NavigationPointMm> FindPath(
        const NavigationQuery& query) const {
        if (query.maximum_points < 2) {
            throw DetourNavigationError(
                DetourNavigationErrorCode::Capacity,
                "FindPath requires capacity for start and end points");
        }
        const auto start = FindNearest(query.start_mm);
        const auto end = FindNearest(query.end_mm);
        const auto start_position = ToDetour(query.start_mm);
        const auto end_position = ToDetour(query.end_mm);
        std::vector<dtPolyRef> corridor(config_.maximum_polygons);
        int corridor_count = 0;
        const auto path_status = query_->findPath(
            start.ref,
            end.ref,
            start_position.data(),
            end_position.data(),
            &filter_,
            corridor.data(),
            &corridor_count,
            static_cast<int>(corridor.size()));
        RejectStatus(path_status, "Detour path corridor query failed");
        if (corridor_count <= 0) {
            throw DetourNavigationError(
                DetourNavigationErrorCode::Status,
                "Detour path corridor is empty");
        }
        const auto point_capacity = static_cast<int>(query.maximum_points);
        std::vector<float> straight_points(
            static_cast<std::size_t>(point_capacity) * 3);
        std::vector<unsigned char> straight_flags(
            static_cast<std::size_t>(point_capacity));
        std::vector<dtPolyRef> straight_refs(
            static_cast<std::size_t>(point_capacity));
        int point_count = 0;
        const auto straight_status = query_->findStraightPath(
            start_position.data(),
            end_position.data(),
            corridor.data(),
            corridor_count,
            straight_points.data(),
            straight_flags.data(),
            straight_refs.data(),
            &point_count,
            point_capacity);
        RejectStatus(straight_status, "Detour straight path query failed");
        if (point_count < 2) {
            throw DetourNavigationError(
                DetourNavigationErrorCode::Status,
                "Detour straight path has fewer than two points");
        }
        std::vector<NavigationPointMm> result;
        result.reserve(static_cast<std::size_t>(point_count));
        for (int index = 0; index < point_count; ++index) {
            result.push_back(
                FromDetour(straight_points.data() + (index * 3)));
        }
        return result;
    }

    /// config_ 是启动后不可变的 identity、scale 与 query budget。
    DetourNavigationConfig config_;
    /// nav_data_ 是构造时复制的只读版本化 tile bytes。
    std::vector<std::byte> nav_data_;
    /// nav_mesh_ 在 LoadAsset 成功后拥有 DT_TILE_FREE_DATA bytes。
    dtNavMesh* nav_mesh_{nullptr};
    /// query_ 绑定 nav_mesh_ 并拥有固定 node pool。
    dtNavMeshQuery* query_{nullptr};
    /// filter_ 是首版固定 all-walkable polygon filter。
    dtQueryFilter filter_;
    /// loaded_ 表示完整 identity 与 bytes digest gate 已通过。
    bool loaded_{false};
};

DetourNavigationError::DetourNavigationError(
    const DetourNavigationErrorCode code,
    const char* message)
    : std::runtime_error(message), code_(code) {}

DetourNavigationErrorCode DetourNavigationError::Code() const noexcept {
    return code_;
}

DetourNavigationWorld::DetourNavigationWorld(
    DetourNavigationConfig config,
    const std::span<const std::byte> nav_data)
    : impl_(std::make_unique<Impl>(std::move(config), nav_data)) {}

DetourNavigationWorld::~DetourNavigationWorld() = default;
DetourNavigationWorld::DetourNavigationWorld(
    DetourNavigationWorld&&) noexcept = default;
DetourNavigationWorld& DetourNavigationWorld::operator=(
    DetourNavigationWorld&&) noexcept = default;

void DetourNavigationWorld::LoadAsset(
    const NavigationAssetIdentity& identity) {
    if (impl_ == nullptr) {
        throw DetourNavigationError(
            DetourNavigationErrorCode::Status,
            "Detour NavigationWorld was moved from");
    }
    impl_->LoadAsset(identity);
}

NavigationQueryResult DetourNavigationWorld::Query(
    const NavigationQuery& query) {
    if (impl_ == nullptr) {
        throw DetourNavigationError(
            DetourNavigationErrorCode::Status,
            "Detour NavigationWorld was moved from");
    }
    return impl_->Query(query);
}

}  // namespace ihomeland::sim
