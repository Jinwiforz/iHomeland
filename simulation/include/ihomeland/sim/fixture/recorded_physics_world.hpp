#pragma once

#include "ihomeland/sim/physics/physics_world.hpp"

#include <cstddef>
#include <cstdint>
#include <stdexcept>
#include <vector>

namespace ihomeland::sim {

/// RecordedPhysicsErrorCode 是 fixture query trace 的稳定失败分类。
enum class RecordedPhysicsErrorCode : std::uint8_t {
    /// InvalidRecord 表示记录自身缺少 identity 或违反 hit contract。
    InvalidRecord,
    /// QueryOrder 表示 runtime query 与下一条登记 query 不一致。
    QueryOrder,
    /// Exhausted 表示 runtime 消费超过登记 trace。
    Exhausted,
    /// Capacity 表示 result 超出 query/adapter hard limit。
    Capacity,
};

/// RecordedPhysicsError 保留可机器判断的 trace mismatch。
class RecordedPhysicsError final : public std::runtime_error {
public:
    /// 构造函数绑定稳定错误码与低敏诊断文本。
    RecordedPhysicsError(RecordedPhysicsErrorCode code, const char* message);

    /// Code 返回不依赖文本的稳定错误码。
    [[nodiscard]] RecordedPhysicsErrorCode Code() const noexcept;

private:
    /// code_ 是当前 recorded adapter 失败分类。
    RecordedPhysicsErrorCode code_;
};

/// RecordedPhysicsExchange 是一个严格 request/result fixture pair。
struct RecordedPhysicsExchange final {
    /// query 是 runtime 必须按顺序提交的完整项目 value。
    PhysicsQuery query;
    /// hits 可以按第三方 callback arrival order 登记，构造时会规范排序。
    std::vector<PhysicsHitValue> hits;
};

/// RecordedPhysicsWorld 按只读 trace 提供确定 PhysicsWorld 实现。
class RecordedPhysicsWorld final : public PhysicsWorld {
public:
    /// 构造函数验证完整 trace 并预先规范化 hit，拒绝超过 hard capacity。
    RecordedPhysicsWorld(
        std::vector<RecordedPhysicsExchange> exchanges,
        std::size_t maximum_exchanges,
        std::size_t maximum_hits_per_query);

    /// Query 只允许消费下一条完全相同的 query，不搜索或跳过 trace。
    [[nodiscard]] PhysicsQueryResult Query(const PhysicsQuery& query) override;

    /// Reset 回到第一条 exchange，供同 build/config 重复 determinism run。
    void Reset() noexcept;

    /// Consumed 返回当前已严格消费的 exchange 数量。
    [[nodiscard]] std::size_t Consumed() const noexcept;

private:
    /// exchanges_ 保存构造时复制并规范化的只读 trace。
    std::vector<RecordedPhysicsExchange> exchanges_;
    /// maximum_hits_per_query_ 是 adapter 自身的 result hard limit。
    std::size_t maximum_hits_per_query_;
    /// next_ 指向下一条必须匹配的 exchange。
    std::size_t next_{0};
};

}  // namespace ihomeland::sim
