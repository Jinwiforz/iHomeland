#pragma once

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <stdexcept>
#include <utility>
#include <vector>

namespace ihomeland::sim {

/// InboxPushResult 区分成功、hard capacity 与关闭状态。
enum class InboxPushResult : std::uint8_t {
    /// Accepted 表示 value 所有权已转入 ring。
    Accepted,
    /// Capacity 表示 ring 已满且原 value 未被保存。
    Capacity,
    /// Closed 表示 drain 已开始且不再接收新 value。
    Closed,
};

/// BoundedInbox 是由外部 mutex 保护的预分配 FIFO ring。
///
/// @tparam Value 必须可移动；ring 构造后不扩容。该类型本身不加锁，owner 必须在所有方法
/// 外围使用同一 mutex，使异步 producer 只能入队、不能直接访问 simulation state。
template <typename Value>
class BoundedInbox final {
public:
    /// 构造函数一次性分配 capacity 个 optional slot；零容量拒绝。
    explicit BoundedInbox(const std::size_t capacity)
        : slots_(capacity) {
        if (capacity == 0) {
            throw std::invalid_argument("inbox capacity must be positive");
        }
    }

    /// TryPush 在 ring 未满且开放时转移 value。
    [[nodiscard]] InboxPushResult TryPush(Value value) {
        if (closed_) {
            return InboxPushResult::Closed;
        }
        if (size_ == slots_.size()) {
            return InboxPushResult::Capacity;
        }
        const auto tail = (head_ + size_) % slots_.size();
        slots_[tail].emplace(std::move(value));
        ++size_;
        high_watermark_ = std::max(high_watermark_, size_);
        return InboxPushResult::Accepted;
    }

    /// DrainInto 按 FIFO 顺序追加当前完整 batch，不在 Tick 热路径分配。
    ///
    /// @param destination 必须预留足够剩余 capacity；已有 future command 会保留在前部。
    /// @throws std::length_error 当 destination 剩余 capacity 不足时抛出，ring 保持不变。
    void DrainInto(std::vector<Value>& destination) {
        if (destination.capacity() - destination.size() < size_) {
            throw std::length_error(
                "inbox drain destination capacity is insufficient");
        }
        while (size_ != 0) {
            destination.push_back(std::move(*slots_[head_]));
            slots_[head_].reset();
            head_ = (head_ + 1) % slots_.size();
            --size_;
        }
    }

    /// Close 幂等关闭 producer 入口，已排队 value 仍可由 worker Drain。
    void Close() noexcept { closed_ = true; }

    /// Empty 返回当前 ring 是否无排队 value。
    [[nodiscard]] bool Empty() const noexcept { return size_ == 0; }

    /// Size 返回当前排队数。
    [[nodiscard]] std::size_t Size() const noexcept { return size_; }

    /// HighWatermark 返回生命周期内最大同时排队数。
    [[nodiscard]] std::size_t HighWatermark() const noexcept { return high_watermark_; }

private:
    /// slots_ 在构造后长度固定，optional 控制 value 生命周期。
    std::vector<std::optional<Value>> slots_;
    /// head_ 指向 FIFO 下一读取 slot。
    std::size_t head_{0};
    /// size_ 是当前已构造 value 数。
    std::size_t size_{0};
    /// high_watermark_ 只在成功 push 后单调增加。
    std::size_t high_watermark_{0};
    /// closed_ 阻止 drain 开始后的新 producer 写入。
    bool closed_{false};
};

}  // namespace ihomeland::sim
