#pragma once

#include <cstdint>

namespace ihomeland::sim {

/// FixedTickConfig 保存已验证的固定步长参数，不承担墙钟调度。
struct FixedTickConfig final {
    /// simulation_tick_ms 是每次权威模拟步长，单位毫秒。
    std::uint32_t simulation_tick_ms;
    /// input_tick_hz 是输入采样频率，单位 Hz。
    std::uint32_t input_tick_hz;
};

/// FixedTickState 是 smoke runtime 的最小可观测状态。
struct FixedTickState final {
    /// committed_tick 是最后完成提交的权威 Tick。
    std::uint64_t committed_tick;
    /// elapsed_ms 是由固定步长推导的逻辑时间，不读取系统墙钟。
    std::uint64_t elapsed_ms;
};

/// FixedTick 证明 core 可以用显式配置推进确定性的逻辑时间。
class FixedTick final {
public:
    /// 构造函数拒绝零步长或不能整除一秒的输入频率。
    explicit FixedTick(FixedTickConfig config);

    /// Advance 提交一个完整 Tick，并返回新的只读状态。
    [[nodiscard]] FixedTickState Advance();

    /// State 返回当前状态的副本，不暴露内部可变所有权。
    [[nodiscard]] FixedTickState State() const noexcept;

private:
    /// config_ 在构造成功后保持不可变。
    const FixedTickConfig config_;
    /// state_ 只由 Advance 在单 worker 边界内修改。
    FixedTickState state_;
};

}  // namespace ihomeland::sim
