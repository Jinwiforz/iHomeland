#pragma once

#include "ihomeland/sim/transport/battle_route.hpp"
#include "ihomeland/sim/transport/secure_datagram.hpp"

#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <span>

namespace ihomeland::sim {

class BattleRuntimeMetrics;

/// BattleKcpMessageView 是 callback 期间有效的已重组可靠消息。
struct BattleKcpMessageView final {
    /// policy 是 registry 中 immutable KCP route。
    const BattleKcpRoutePolicy* policy;
    /// application_sequence 是 route envelope 的非零有序序列。
    std::uint64_t application_sequence;
    /// application_tick 是 typed Protobuf 投影的 input/server tick。
    std::uint64_t application_tick;
    /// payload 是 exact Protobuf bytes，仅在 callback 期间有效。
    std::span<const std::uint8_t> payload;
};

/// BattleKcpDisposition 是 adapter 的稳定有界结果。
enum class BattleKcpDisposition : std::uint8_t {
    /// Accepted 表示 segment/update 已成功处理。
    Accepted = 1,
    /// Queued 表示 application message 已进入有界 KCP queue。
    Queued = 2,
    /// InvalidSegment 表示 conv/header/window/length/ceiling 不合法。
    InvalidSegment = 3,
    /// RouteRejected 表示 direction/message/payload/sequence 不符合 registry。
    RouteRejected = 4,
    /// QueueFull 表示64-message hard cap已达到。
    QueueFull = 5,
    /// Expired 表示 route deadline 已终结消息或 session KCP state。
    Expired = 6,
    /// ClockInvalid 表示 absolute clock回退或session relative clock越过uint32范围。
    ClockInvalid = 7,
    /// Closed 表示 dead-link、callback failure或既有终态。
    Closed = 8,
};

/// BattleKcpStatus 是低敏、无 payload 的 adapter 状态快照。
struct BattleKcpStatus final {
    /// closed 表示 adapter 不再收发。
    bool closed;
    /// queued_messages 是尚未交给 KCP 的 application queue。
    std::size_t queued_messages;
    /// inflight_messages 是已交给 KCP 尚未确认的 messages。
    std::size_t inflight_messages;
    /// waiting_segments 是 KCP send queue/buffer 数。
    std::size_t waiting_segments;
    /// expired_messages 是已稳定终结的累计数。
    std::uint64_t expired_messages;
    /// emitted_segments 是交给 secure KCP lane 的累计 datagram 数。
    std::uint64_t emitted_segments;
    /// received_messages 是通过 reassembly route gate 的累计数。
    std::uint64_t received_messages;
};

/// BattleKcpAdapter 隔离 exact KCP 2.1.1 handle与项目可靠route契约。
class BattleKcpAdapter final {
public:
    /// UpdateIntervalMilliseconds 固定 KCP update cadence。
    static constexpr std::uint32_t UpdateIntervalMilliseconds = 10;
    /// WindowSegments 固定 send/receive window。
    static constexpr std::uint32_t WindowSegments = 64;
    /// FastResend 固定 fast retransmit threshold。
    static constexpr std::uint32_t FastResend = 2;
    /// MinimumRtoMilliseconds 固定 RTO lower bound。
    static constexpr std::uint32_t MinimumRtoMilliseconds = 30;
    /// MaximumRtoMilliseconds 固定 RTO upper bound。
    static constexpr std::uint32_t MaximumRtoMilliseconds = 200;
    /// DeadLinkRetransmits 固定 terminal retransmit count。
    static constexpr std::uint32_t DeadLinkRetransmits = 10;
    /// MaximumSegmentPayloadBytes 固定 KCP mss ceiling。
    static constexpr std::size_t MaximumSegmentPayloadBytes = 1000;
    /// MaximumMessageBytes 固定 route envelope + payload ceiling。
    static constexpr std::size_t MaximumMessageBytes = 1000;
    /// QueueItems 固定 application + inflight message hard cap。
    static constexpr std::size_t QueueItems = 64;
    /// ReliableEventExpiryMilliseconds 保留 adapter API，并引用 route owner。
    static constexpr std::uint32_t ReliableEventExpiryMilliseconds =
        BattleKcpRoutePolicy::ReliableEventExpiryMilliseconds;
    /// ResyncExpiryMilliseconds 保留 adapter API，并引用 route owner。
    static constexpr std::uint32_t ResyncExpiryMilliseconds =
        BattleKcpRoutePolicy::ResyncExpiryMilliseconds;

    /// SegmentOutput 把 KCP bytes交给同一session的secure KCP packet owner。
    using SegmentOutput =
        std::function<void(std::span<const std::uint8_t>)>;
    /// MessageHandler 接收reassembly后再次验证的application message。
    using MessageHandler =
        std::function<void(const BattleKcpMessageView&)>;

    /// 构造函数冻结conversation、local role与两个唯一callback。
    BattleKcpAdapter(
        std::uint32_t conversation,
        BattleTransportRole local_role,
        SegmentOutput segment_output,
        MessageHandler message_handler,
        BattleRuntimeMetrics* runtime_metrics = nullptr);

    /// 析构函数释放KCP handle并丢弃未确认的非持久transport数据。
    ~BattleKcpAdapter();

    BattleKcpAdapter(const BattleKcpAdapter&) = delete;
    BattleKcpAdapter& operator=(const BattleKcpAdapter&) = delete;
    BattleKcpAdapter(BattleKcpAdapter&&) = delete;
    BattleKcpAdapter& operator=(BattleKcpAdapter&&) = delete;

    /// Queue 编码并加入有界application queue，不直接调用socket。
    [[nodiscard]] BattleKcpDisposition Queue(
        std::uint32_t message_id,
        std::uint64_t application_sequence,
        std::span<const std::uint8_t> payload,
        std::uint64_t now_unix_ms);

    /// Input 验证authenticated KCP bytes并分发完整、未过期message。
    [[nodiscard]] BattleKcpDisposition Input(
        std::span<const std::uint8_t> segment,
        std::uint64_t now_unix_ms);

    /// Update 按10ms cadence推进发送、重传、ACK与deadline。
    [[nodiscard]] BattleKcpDisposition Update(
        std::uint64_t now_unix_ms);

    /// Status 返回低敏queue与lifecycle快照。
    [[nodiscard]] BattleKcpStatus Status() const;

    /// ValidateSegment 验证一个或多个canonical KCP segments，不改变handle。
    [[nodiscard]] static bool ValidateSegment(
        std::span<const std::uint8_t> segment,
        std::uint32_t expected_conversation) noexcept;

private:
    struct Impl;
    /// impl_ 隐藏第三方 KCP handle、queue和callback bridge。
    std::unique_ptr<Impl> impl_;
};

}  // namespace ihomeland::sim
