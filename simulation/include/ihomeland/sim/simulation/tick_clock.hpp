#pragma once

#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <mutex>
#include <stop_token>

namespace ihomeland::sim {

/// TickClock 是 SimulationInstance 唯一读取时间的显式等待 port。
class TickClock {
public:
    /// 析构函数允许通过基类 owner 正确释放实现。
    virtual ~TickClock() = default;

    /// WaitNext 等待一个固定 step；stop 请求返回 false 且不产生 Tick。
    ///
    /// @param stop_token 由唯一 simulation worker 的 jthread 提供。
    /// @param step 必须等于实例冻结的 50 ms；实现不得修改 gameplay dt。
    /// @return true 表示获得一个 Tick credit，false 表示停止。
    [[nodiscard]] virtual bool WaitNext(
        std::stop_token stop_token,
        std::chrono::nanoseconds step) = 0;

    /// PendingCredits 返回已到期但尚未消费的 Tick 数；实时 clock 没有显式 backlog 时返回零。
    [[nodiscard]] virtual std::uint64_t PendingCredits() const noexcept = 0;
};

/// SteadyTickClock 使用 monotonic steady_clock 调度生产环境离线 worker。
class SteadyTickClock final : public TickClock {
public:
    /// WaitNext 等待到上次 deadline 加固定 step，不用 wall-clock 改变 dt。
    [[nodiscard]] bool WaitNext(
        std::stop_token stop_token,
        std::chrono::nanoseconds step) override;

    /// PendingCredits 对 steady deadline 调度返回零；真实耗时 debt 由后续 benchmark 观测。
    [[nodiscard]] std::uint64_t PendingCredits() const noexcept override;

private:
    /// mutex_ 只保护 stop-aware condition variable wait。
    std::mutex mutex_;
    /// condition_ 使 jthread stop_token 可以提前终止等待。
    std::condition_variable_any condition_;
};

/// ManualTickClock 由测试显式发放 Tick credit，不读取 wall clock 或 sleep。
class ManualTickClock final : public TickClock {
public:
    /// WaitNext 消费一个 credit；stop 请求时立即返回 false。
    [[nodiscard]] bool WaitNext(
        std::stop_token stop_token,
        std::chrono::nanoseconds step) override;

    /// PendingCredits 返回测试尚未消费的显式 credits。
    [[nodiscard]] std::uint64_t PendingCredits() const noexcept override;

    /// Advance 增加 count 个 credits 并唤醒唯一 worker。
    void Advance(std::uint32_t count = 1);

private:
    /// mutex_ 保护 credits_。
    mutable std::mutex mutex_;
    /// condition_ 支持 stop-aware wait。
    std::condition_variable_any condition_;
    /// credits_ 是尚未消费的显式 Tick 数。
    std::uint64_t credits_{0};
};

}  // namespace ihomeland::sim
