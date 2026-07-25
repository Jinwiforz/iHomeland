#pragma once

#include "ihomeland/sim/transport/secure_datagram.hpp"

#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <span>
#include <vector>

namespace ihomeland::sim {

/// BattleRouteDirection 是 numeric registry 冻结的唯一 application 方向。
enum class BattleRouteDirection : std::uint8_t {
    /// ClientToServer 只允许客户端认证 session 发往 simulation。
    ClientToServer = 1,
    /// ServerToClient 只允许 simulation replication 发往客户端。
    ServerToClient = 2,
};

/// BattleRouteTickPolicy 描述 raw payload 必须投影的权威 tick 类型。
enum class BattleRouteTickPolicy : std::uint8_t {
    /// None 表示 route 不携带可与 simulation tick 比较的字段。
    None = 0,
    /// InputTick 使用 BattleInputBundle.newest_input_tick。
    InputTick = 1,
    /// ServerTick 使用 snapshot.server_tick。
    ServerTick = 2,
};

/// BattleRawSplitPolicy 冻结 raw route 是否允许 bounded partitions。
enum class BattleRawSplitPolicy : std::uint8_t {
    /// Forbidden 要求 partition_index=0 且 partition_count=1。
    Forbidden = 0,
    /// InputTickPartitions 允许同一 input sequence 的 uint8 bounded partitions。
    InputTickPartitions = 1,
    /// StatePartitions 允许同一 snapshot sequence 的 uint8 bounded partitions。
    StatePartitions = 2,
};

/// BattleRawRoutePolicy 是 C++ adapter 对 tracked registry 的 closed projection。
struct BattleRawRoutePolicy final {
    /// message_id 是 battle owner range 内的唯一 numeric identity。
    std::uint32_t message_id;
    /// direction 是 route 唯一允许方向。
    BattleRouteDirection direction;
    /// maximum_payload_bytes 是 protobuf encoded hard ceiling。
    std::uint16_t maximum_payload_bytes;
    /// maximum_rate_per_second 是 per-session/message 的基础 hard rate。
    std::uint16_t maximum_rate_per_second;
    /// expiry_microseconds 是进入 dispatcher 前的最大 application age。
    std::uint32_t expiry_microseconds;
    /// tick_policy 决定 payload 中必须验证的 tick。
    BattleRouteTickPolicy tick_policy;
    /// split_policy 决定 raw partition header 的合法形式。
    BattleRawSplitPolicy split_policy;
};

/// FindBattleRawRoutePolicy 返回3000..3003 raw registry projection。
[[nodiscard]] const BattleRawRoutePolicy*
FindBattleRawRoutePolicy(std::uint32_t message_id) noexcept;

/// BattleRawFrameView 是 callback 期间有效的已验证 raw route view。
struct BattleRawFrameView final {
    /// policy 是 registry 中的 immutable route policy。
    const BattleRawRoutePolicy* policy;
    /// partition_index 是零基 datagram partition。
    std::uint8_t partition_index;
    /// partition_count 是当前 application sequence 的总 partitions。
    std::uint8_t partition_count;
    /// application_sequence 是 route policy 定义的非零业务序列。
    std::uint64_t application_sequence;
    /// application_tick 是从 protobuf 提取的 input/server tick；无 tick route 为零。
    std::uint64_t application_tick;
    /// payload 是 exact protobuf bytes，仅在 callback 期间有效。
    std::span<const std::uint8_t> payload;
};

/// BattleRawDispatchContext 是 trusted composition 提供的时间与 tick fence。
struct BattleRawDispatchContext final {
    /// now_unix_microseconds 是当前单调映射后的绝对观测时间。
    std::uint64_t now_unix_microseconds;
    /// enqueued_unix_microseconds 是 ingress/egress owner 首次接收该消息的时间。
    std::uint64_t enqueued_unix_microseconds;
    /// oldest_accepted_tick 是 current assignment 允许的最旧 tick。
    std::uint64_t oldest_accepted_tick;
    /// newest_accepted_tick 是 current assignment 允许的最新 tick。
    std::uint64_t newest_accepted_tick;
};

/// BattleRawDisposition 是 application callback 前的 stable closed decision。
enum class BattleRawDisposition : std::uint8_t {
    /// Accepted 表示 route、payload、时间与顺序均已提交。
    Accepted = 1,
    /// InvalidEnvelope 表示 fixed raw header 或长度不合法。
    InvalidEnvelope = 2,
    /// UnknownRoute 表示 message id 未登记。
    UnknownRoute = 3,
    /// DirectionMismatch 表示 route 不允许当前 dispatcher 方向。
    DirectionMismatch = 4,
    /// SizeExceeded 表示 payload 超过 route 或 raw MTU ceiling。
    SizeExceeded = 5,
    /// InvalidPayload 表示 Protobuf、tick 或 route sequence projection 不合法。
    InvalidPayload = 6,
    /// InvalidPartition 表示 split policy、index/count 或重复 partition 不合法。
    InvalidPartition = 7,
    /// StaleSequence 表示 application sequence 已被更新数据取代。
    StaleSequence = 8,
    /// Expired 表示 queue/application age 已超过 registry。
    Expired = 9,
    /// TickRejected 表示 payload tick 越过 current assignment fence。
    TickRejected = 10,
    /// RateLimited 表示 per-session/message 基础 hard rate 已耗尽。
    RateLimited = 11,
    /// HandlerFailed 表示 application callback 抛错并要求 session fail closed。
    HandlerFailed = 12,
};

/// BattleRawDispatcher 为一个 authenticated BattleSession 验证并分发 raw lane。
class BattleRawDispatcher final {
public:
    /// RawHeaderBytes 是 tracked wire-layout 的固定长度。
    static constexpr std::size_t RawHeaderBytes = 16;
    /// MaximumRawPayloadBytes 是 secure/AEAD 后的 raw payload MTU ceiling。
    static constexpr std::size_t MaximumRawPayloadBytes = 1120;

    /// Handler 只接收通过完整 policy gate 的 raw frame。
    using Handler = std::function<void(const BattleRawFrameView&)>;

    /// 构造函数冻结当前 session 的 inbound 或 outbound direction。
    BattleRawDispatcher(
        BattleRouteDirection direction,
        Handler handler);

    /// 析构函数释放有界 route sequence/rate 状态。
    ~BattleRawDispatcher();

    BattleRawDispatcher(const BattleRawDispatcher&) = delete;
    BattleRawDispatcher& operator=(const BattleRawDispatcher&) = delete;
    BattleRawDispatcher(BattleRawDispatcher&&) = delete;
    BattleRawDispatcher& operator=(BattleRawDispatcher&&) = delete;

    /// Dispatch 解析、验证并原子提交 route rate/sequence/partition 状态。
    [[nodiscard]] BattleRawDisposition Dispatch(
        std::span<const std::uint8_t> plaintext,
        const BattleRawDispatchContext& context);

    /// Encode 仅为已登记方向、合法 payload 与 split policy 生成 canonical raw frame。
    [[nodiscard]] static std::vector<std::uint8_t> Encode(
        BattleRouteDirection direction,
        std::uint32_t message_id,
        std::uint8_t partition_index,
        std::uint8_t partition_count,
        std::uint64_t application_sequence,
        std::span<const std::uint8_t> payload);

private:
    struct Impl;
    /// impl_ 隐藏 Protobuf adapter 与有界每 route session state。
    std::unique_ptr<Impl> impl_;
};

}  // namespace ihomeland::sim
