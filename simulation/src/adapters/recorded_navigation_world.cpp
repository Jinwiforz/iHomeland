#include "ihomeland/sim/fixture/recorded_navigation_world.hpp"

#include <algorithm>
#include <utility>

namespace ihomeland::sim {
namespace {

/// ValidateAsset 拒绝空 map/version/digest/scale identity。
void ValidateAsset(const NavigationAssetIdentity& identity) {
    const auto lowercase_hex = std::all_of(
        identity.sha256.begin(),
        identity.sha256.end(),
        [](const char value) {
            return (value >= '0' && value <= '9') ||
                   (value >= 'a' && value <= 'f');
        });
    if (identity.map_id == 0 || identity.version == 0 ||
        identity.sha256.size() != 64 || !lowercase_hex ||
        identity.coordinate_scale_mm == 0) {
        throw RecordedNavigationError(
            RecordedNavigationErrorCode::InvalidRecord,
            "recorded navigation asset identity is invalid");
    }
}

/// ValidateQuery 拒绝无法稳定关联或没有容量的 NavigationQuery。
void ValidateQuery(const NavigationQuery& query) {
    if (query.query_id == 0 || query.tick == 0 ||
        query.actor_id == 0 || query.maximum_points == 0) {
        throw RecordedNavigationError(
            RecordedNavigationErrorCode::InvalidRecord,
            "recorded NavigationQuery identity or capacity is invalid");
    }
}

}  // namespace

RecordedNavigationError::RecordedNavigationError(
    const RecordedNavigationErrorCode code,
    const char* message)
    : std::runtime_error(message), code_(code) {}

RecordedNavigationErrorCode RecordedNavigationError::Code() const noexcept {
    return code_;
}

RecordedNavigationWorld::RecordedNavigationWorld(
    NavigationAssetIdentity expected_asset,
    std::vector<RecordedNavigationExchange> exchanges,
    const std::size_t maximum_exchanges,
    const std::size_t maximum_points_per_query)
    : expected_asset_(std::move(expected_asset)),
      exchanges_(std::move(exchanges)),
      maximum_points_per_query_(maximum_points_per_query) {
    ValidateAsset(expected_asset_);
    if (maximum_exchanges == 0 || maximum_points_per_query == 0 ||
        exchanges_.size() > maximum_exchanges) {
        throw RecordedNavigationError(
            RecordedNavigationErrorCode::Capacity,
            "recorded navigation trace exceeds hard capacity");
    }
    for (const auto& exchange : exchanges_) {
        ValidateQuery(exchange.query);
        const auto invalid_shape =
            (exchange.query.kind == NavigationQueryKind::NearestPoly &&
             exchange.points_mm.size() != 1) ||
            (exchange.query.kind == NavigationQueryKind::FindPath &&
             exchange.points_mm.size() < 2);
        if (invalid_shape ||
            exchange.points_mm.size() > maximum_points_per_query_) {
            throw RecordedNavigationError(
                RecordedNavigationErrorCode::InvalidRecord,
                "recorded navigation path shape is invalid");
        }
    }
}

void RecordedNavigationWorld::LoadAsset(const NavigationAssetIdentity& identity) {
    ValidateAsset(identity);
    if (!(identity == expected_asset_)) {
        throw RecordedNavigationError(
            RecordedNavigationErrorCode::AssetDrift,
            "navigation asset identity differs from recorded trace");
    }
    loaded_ = true;
}

NavigationQueryResult RecordedNavigationWorld::Query(const NavigationQuery& query) {
    ValidateQuery(query);
    if (!loaded_) {
        throw RecordedNavigationError(
            RecordedNavigationErrorCode::NotLoaded,
            "navigation query requires a validated asset");
    }
    if (next_ == exchanges_.size()) {
        throw RecordedNavigationError(
            RecordedNavigationErrorCode::Exhausted,
            "recorded navigation trace is exhausted");
    }
    const auto& exchange = exchanges_[next_];
    if (!(exchange.query == query)) {
        throw RecordedNavigationError(
            RecordedNavigationErrorCode::QueryOrder,
            "runtime NavigationQuery differs from the next recorded query");
    }
    if (exchange.points_mm.size() > query.maximum_points ||
        exchange.points_mm.size() > maximum_points_per_query_) {
        throw RecordedNavigationError(
            RecordedNavigationErrorCode::Capacity,
            "recorded navigation path exceeds runtime query capacity");
    }
    ++next_;
    return {
        .query_id = query.query_id,
        .points_mm = exchange.points_mm,
        .path_identity =
            CanonicalNavigationPathIdentity(expected_asset_, query, exchange.points_mm)};
}

void RecordedNavigationWorld::Reset() noexcept {
    next_ = 0;
    loaded_ = false;
}

std::size_t RecordedNavigationWorld::Consumed() const noexcept {
    return next_;
}

}  // namespace ihomeland::sim
