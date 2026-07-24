#pragma once

#include "ihomeland/sim/navigation/navigation_world.hpp"

#include <cstddef>
#include <cstdint>
#include <stdexcept>
#include <vector>

namespace ihomeland::sim {

/// RecordedNavigationErrorCode 是 nav asset/trace 的稳定失败分类。
enum class RecordedNavigationErrorCode : std::uint8_t {
    /// InvalidRecord 表示 asset/query/path 本身不符合项目 contract。
    InvalidRecord,
    /// AssetDrift 表示加载 identity 与记录绑定值不一致。
    AssetDrift,
    /// NotLoaded 表示 query 绕过启动 asset load。
    NotLoaded,
    /// QueryOrder 表示 runtime query 与下一条登记 query 不一致。
    QueryOrder,
    /// Exhausted 表示 runtime 消费超过登记 trace。
    Exhausted,
    /// Capacity 表示 trace/result 超过 hard limit。
    Capacity,
};

/// RecordedNavigationError 保留可机器判断的 recorded nav 失败原因。
class RecordedNavigationError final : public std::runtime_error {
public:
    /// 构造函数绑定稳定错误码与低敏诊断文本。
    RecordedNavigationError(RecordedNavigationErrorCode code, const char* message);

    /// Code 返回不依赖文本的稳定错误码。
    [[nodiscard]] RecordedNavigationErrorCode Code() const noexcept;

private:
    /// code_ 是当前 recorded navigation 失败分类。
    RecordedNavigationErrorCode code_;
};

/// RecordedNavigationExchange 是一个严格 request/path fixture pair。
struct RecordedNavigationExchange final {
    /// query 是 runtime 必须按顺序提交的完整 value。
    NavigationQuery query;
    /// points_mm 是已登记的 nearest point 或有序 path。
    std::vector<NavigationPointMm> points_mm;
};

/// RecordedNavigationWorld 按只读 trace 提供确定 NavigationWorld 实现。
class RecordedNavigationWorld final : public NavigationWorld {
public:
    /// 构造函数绑定 expected asset 并拒绝超过 query/path hard capacity。
    RecordedNavigationWorld(
        NavigationAssetIdentity expected_asset,
        std::vector<RecordedNavigationExchange> exchanges,
        std::size_t maximum_exchanges,
        std::size_t maximum_points_per_query);

    /// LoadAsset 只接受与 recorded trace 完全一致的 versioned identity。
    void LoadAsset(const NavigationAssetIdentity& identity) override;

    /// Query 只消费下一条完全相同 query，并计算稳定 path identity。
    [[nodiscard]] NavigationQueryResult Query(const NavigationQuery& query) override;

    /// Reset 清空 loaded/consumed state，供连续 determinism run。
    void Reset() noexcept;

    /// Consumed 返回已严格消费的 exchange 数量。
    [[nodiscard]] std::size_t Consumed() const noexcept;

private:
    /// expected_asset_ 是 trace 唯一允许的 nav asset identity。
    NavigationAssetIdentity expected_asset_;
    /// exchanges_ 保存构造时验证的只读 trace。
    std::vector<RecordedNavigationExchange> exchanges_;
    /// maximum_points_per_query_ 是 adapter 自身 path hard limit。
    std::size_t maximum_points_per_query_;
    /// next_ 指向下一条必须匹配的 exchange。
    std::size_t next_{0};
    /// loaded_ 表示 asset identity gate 已通过。
    bool loaded_{false};
};

}  // namespace ihomeland::sim
