#include "ihomeland/sim/transport/battle_route.hpp"

#include "ihomeland/battle/v1/battle.pb.h"

#include <algorithm>
#include <array>
#include <limits>
#include <mutex>
#include <ranges>
#include <stdexcept>
#include <utility>

namespace ihomeland::sim {
namespace {

constexpr std::array<BattleRawRoutePolicy, 4> RawRoutes{{
    {
        .message_id = 3000,
        .direction = BattleRouteDirection::ClientToServer,
        .maximum_payload_bytes = 384,
        .maximum_rate_per_second = 40,
        .expiry_microseconds = 300'000,
        .tick_policy = BattleRouteTickPolicy::InputTick,
        .split_policy =
            BattleRawSplitPolicy::InputTickPartitions,
    },
    {
        .message_id = 3001,
        .direction = BattleRouteDirection::ClientToServer,
        .maximum_payload_bytes = 96,
        .maximum_rate_per_second = 4,
        .expiry_microseconds = 250'000,
        .tick_policy = BattleRouteTickPolicy::None,
        .split_policy = BattleRawSplitPolicy::Forbidden,
    },
    {
        .message_id = 3002,
        .direction = BattleRouteDirection::ServerToClient,
        .maximum_payload_bytes = 1040,
        .maximum_rate_per_second = 2,
        .expiry_microseconds = 500'000,
        .tick_policy = BattleRouteTickPolicy::ServerTick,
        .split_policy =
            BattleRawSplitPolicy::StatePartitions,
    },
    {
        .message_id = 3003,
        .direction = BattleRouteDirection::ServerToClient,
        .maximum_payload_bytes = 900,
        .maximum_rate_per_second = 10,
        .expiry_microseconds = 300'000,
        .tick_policy = BattleRouteTickPolicy::ServerTick,
        .split_policy =
            BattleRawSplitPolicy::StatePartitions,
    },
}};

/// ReadUint16BE 读取 canonical raw payload length。
[[nodiscard]] std::uint16_t ReadUint16BE(
    const std::uint8_t* input) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(input[0]) << 8U) |
        static_cast<std::uint16_t>(input[1]));
}

/// ReadUint32BE 读取 canonical message id。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::uint8_t* input) noexcept {
    return
        (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// ReadUint64BE 读取 canonical application sequence。
[[nodiscard]] std::uint64_t ReadUint64BE(
    const std::uint8_t* input) noexcept {
    std::uint64_t value = 0;
    for (std::size_t index = 0; index < 8; ++index) {
        value =
            (value << 8U) |
            static_cast<std::uint64_t>(input[index]);
    }
    return value;
}

/// WriteUint16BE 写入 canonical raw payload length。
void WriteUint16BE(
    std::uint8_t* output,
    const std::uint16_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 8U);
    output[1] = static_cast<std::uint8_t>(value);
}

/// WriteUint32BE 写入 canonical message id。
void WriteUint32BE(
    std::uint8_t* output,
    const std::uint32_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// WriteUint64BE 写入 canonical application sequence。
void WriteUint64BE(
    std::uint8_t* output,
    const std::uint64_t value) noexcept {
    for (std::size_t index = 0; index < 8; ++index) {
        output[7 - index] = static_cast<std::uint8_t>(
            value >> (index * 8U));
    }
}

/// PayloadProjection 是 Protobuf 提取的外层 sequence/tick/partition binding。
struct PayloadProjection final {
    /// valid 表示 payload 是对应 route 的 canonical typed message。
    bool valid;
    /// application_sequence 是 typed payload 自身的非零 sequence identity。
    std::uint64_t application_sequence;
    /// application_tick 按 registry tick policy 提取。
    std::uint64_t application_tick;
    /// partition_index 在 snapshot route 中必须与 raw header一致。
    std::uint32_t partition_index;
    /// partition_count 在 snapshot route 中必须与 raw header一致。
    std::uint32_t partition_count;
    /// has_payload_partition 表示 Protobuf 自身登记了 partition fields。
    bool has_payload_partition;
};

/// ParsePayload 使用 lite generated type验证route-specific outer fields。
[[nodiscard]] PayloadProjection ParsePayload(
    const std::uint32_t message_id,
    const std::span<const std::uint8_t> payload) {
    const auto parse = [&](auto& message) {
        return message.ParseFromArray(
            payload.data(),
            static_cast<int>(payload.size()));
    };
    switch (message_id) {
        case 3000: {
            ihomeland::battle::v1::BattleInputBundle message;
            if (!parse(message) ||
                message.newest_input_tick() == 0) {
                return {};
            }
            return {
                .valid = true,
                .application_sequence =
                    message.newest_input_tick(),
                .application_tick =
                    message.newest_input_tick(),
            };
        }
        case 3001: {
            ihomeland::battle::v1::BattleProbe message;
            if (!parse(message) ||
                message.probe_sequence() == 0) {
                return {};
            }
            return {
                .valid = true,
                .application_sequence =
                    message.probe_sequence(),
            };
        }
        case 3002: {
            ihomeland::battle::v1::BattleFullSnapshot message;
            if (!parse(message) ||
                message.server_tick() == 0 ||
                message.snapshot_sequence() == 0 ||
                message.partition_count() == 0 ||
                message.partition_index() >=
                    message.partition_count()) {
                return {};
            }
            return {
                .valid = true,
                .application_sequence =
                    message.snapshot_sequence(),
                .application_tick = message.server_tick(),
                .partition_index = message.partition_index(),
                .partition_count = message.partition_count(),
                .has_payload_partition = true,
            };
        }
        case 3003: {
            ihomeland::battle::v1::BattleDeltaSnapshot message;
            if (!parse(message) ||
                message.server_tick() == 0 ||
                message.snapshot_sequence() == 0 ||
                message.partition_count() == 0 ||
                message.partition_index() >=
                    message.partition_count()) {
                return {};
            }
            return {
                .valid = true,
                .application_sequence =
                    message.snapshot_sequence(),
                .application_tick = message.server_tick(),
                .partition_index = message.partition_index(),
                .partition_count = message.partition_count(),
                .has_payload_partition = true,
            };
        }
        default:
            return {};
    }
}

/// ValidSplit 执行 outer partition 与 route split policy 的 closed 校验。
[[nodiscard]] bool ValidSplit(
    const BattleRawRoutePolicy& policy,
    const std::uint8_t partition_index,
    const std::uint8_t partition_count) noexcept {
    if (partition_count == 0 ||
        partition_index >= partition_count) {
        return false;
    }
    if (policy.split_policy ==
        BattleRawSplitPolicy::Forbidden) {
        return partition_index == 0 &&
               partition_count == 1;
    }
    return true;
}

}  // namespace

const BattleRawRoutePolicy* FindBattleRawRoutePolicy(
    const std::uint32_t message_id) noexcept {
    const auto found = std::ranges::find(
        RawRoutes,
        message_id,
        &BattleRawRoutePolicy::message_id);
    return found == RawRoutes.end() ?
        nullptr :
        &*found;
}

/// BattleRawDispatcher::Impl 保存四条 raw route 的有界每 session 状态。
struct BattleRawDispatcher::Impl final {
    /// RouteState 只追踪 current sequence partitions 与一秒 rate window。
    struct RouteState final {
        /// application_sequence 是最后接受的新鲜 sequence。
        std::uint64_t application_sequence{};
        /// partition_count 绑定 current sequence 的 immutable count。
        std::uint8_t partition_count{};
        /// received_partitions 是 uint8 partition index 的固定 bitmap。
        std::array<std::uint64_t, 4> received_partitions{};
        /// rate_window_started_us 是当前一秒 window 起点。
        std::uint64_t rate_window_started_us{};
        /// rate_window_count 是当前 window 已接受 datagram 数。
        std::uint16_t rate_window_count{};
    };

    /// 构造函数冻结唯一 registry direction 与 callback。
    Impl(
        const BattleRouteDirection value,
        Handler callback)
        : direction(value),
          handler(std::move(callback)) {}

    /// direction 是当前 session endpoint 的唯一 application 方向。
    BattleRouteDirection direction;
    /// handler 是通过全部 route gate 后的唯一 owner。
    Handler handler;
    /// states 与 RawRoutes 一一对应，不按攻击者输入增长。
    std::array<RouteState, RawRoutes.size()> states{};
    /// mutex 串行化 rate、sequence、partition commit 与 callback。
    std::mutex mutex;
};

BattleRawDispatcher::BattleRawDispatcher(
    const BattleRouteDirection direction,
    Handler handler)
    : impl_(std::make_unique<Impl>(
          direction,
          std::move(handler))) {
    if (!impl_->handler) {
        throw std::invalid_argument(
            "battle raw handler is required");
    }
}

BattleRawDispatcher::~BattleRawDispatcher() = default;

BattleRawDisposition BattleRawDispatcher::Dispatch(
    const std::span<const std::uint8_t> plaintext,
    const BattleRawDispatchContext& context) {
    if (plaintext.size() <= RawHeaderBytes ||
        plaintext.size() >
            RawHeaderBytes + MaximumRawPayloadBytes) {
        return BattleRawDisposition::InvalidEnvelope;
    }
    const auto message_id = ReadUint32BE(plaintext.data());
    const auto payload_bytes =
        ReadUint16BE(plaintext.data() + 4);
    const auto partition_index = plaintext[6];
    const auto partition_count = plaintext[7];
    const auto application_sequence =
        ReadUint64BE(plaintext.data() + 8);
    if (payload_bytes == 0 ||
        static_cast<std::size_t>(payload_bytes) +
                RawHeaderBytes !=
            plaintext.size() ||
        application_sequence == 0) {
        return BattleRawDisposition::InvalidEnvelope;
    }
    const auto* policy =
        FindBattleRawRoutePolicy(message_id);
    if (policy == nullptr) {
        return BattleRawDisposition::UnknownRoute;
    }
    if (policy->direction != impl_->direction) {
        return BattleRawDisposition::DirectionMismatch;
    }
    if (payload_bytes > policy->maximum_payload_bytes) {
        return BattleRawDisposition::SizeExceeded;
    }
    if (!ValidSplit(
            *policy,
            partition_index,
            partition_count)) {
        return BattleRawDisposition::InvalidPartition;
    }
    if (context.now_unix_microseconds == 0 ||
        context.enqueued_unix_microseconds == 0 ||
        context.now_unix_microseconds <
            context.enqueued_unix_microseconds ||
        context.now_unix_microseconds -
                context.enqueued_unix_microseconds >
            policy->expiry_microseconds) {
        return BattleRawDisposition::Expired;
    }

    const auto payload = plaintext.subspan(RawHeaderBytes);
    const auto projection =
        ParsePayload(message_id, payload);
    if (!projection.valid ||
        (projection.has_payload_partition &&
         (projection.partition_index != partition_index ||
          projection.partition_count != partition_count))) {
        return BattleRawDisposition::InvalidPayload;
    }
    if (policy->tick_policy != BattleRouteTickPolicy::None &&
        (context.oldest_accepted_tick == 0 ||
         context.newest_accepted_tick <
             context.oldest_accepted_tick ||
         projection.application_tick <
             context.oldest_accepted_tick ||
         projection.application_tick >
             context.newest_accepted_tick)) {
        return BattleRawDisposition::TickRejected;
    }

    const auto route_index = static_cast<std::size_t>(
        policy - RawRoutes.data());
    std::unique_lock lock(impl_->mutex);
    auto& state = impl_->states[route_index];
    if (state.rate_window_started_us != 0 &&
        context.now_unix_microseconds <
            state.rate_window_started_us) {
        return BattleRawDisposition::Expired;
    }
    if (state.rate_window_started_us == 0 ||
        context.now_unix_microseconds -
                state.rate_window_started_us >=
            1'000'000) {
        state.rate_window_started_us =
            context.now_unix_microseconds;
        state.rate_window_count = 0;
    }
    if (state.rate_window_count >=
        policy->maximum_rate_per_second) {
        return BattleRawDisposition::RateLimited;
    }
    if (application_sequence <
        state.application_sequence) {
        return BattleRawDisposition::StaleSequence;
    }
    if (application_sequence >
        state.application_sequence) {
        state.application_sequence =
            application_sequence;
        state.partition_count = partition_count;
        state.received_partitions.fill(0);
    } else if (state.partition_count !=
               partition_count) {
        return BattleRawDisposition::InvalidPartition;
    }
    const auto word =
        static_cast<std::size_t>(partition_index / 64U);
    const auto bit =
        std::uint64_t{1} << (partition_index % 64U);
    if ((state.received_partitions[word] & bit) != 0) {
        return BattleRawDisposition::InvalidPartition;
    }
    state.received_partitions[word] |= bit;
    ++state.rate_window_count;
    lock.unlock();
    try {
        impl_->handler(BattleRawFrameView{
            .policy = policy,
            .partition_index = partition_index,
            .partition_count = partition_count,
            .application_sequence = application_sequence,
            .application_tick =
                projection.application_tick,
            .payload = payload,
        });
    } catch (...) {
        return BattleRawDisposition::HandlerFailed;
    }
    return BattleRawDisposition::Accepted;
}

std::vector<std::uint8_t> BattleRawDispatcher::Encode(
    const BattleRouteDirection direction,
    const std::uint32_t message_id,
    const std::uint8_t partition_index,
    const std::uint8_t partition_count,
    const std::uint64_t application_sequence,
    const std::span<const std::uint8_t> payload) {
    const auto* policy =
        FindBattleRawRoutePolicy(message_id);
    if (policy == nullptr ||
        policy->direction != direction ||
        application_sequence == 0 ||
        payload.empty() ||
        payload.size() > policy->maximum_payload_bytes ||
        payload.size() > MaximumRawPayloadBytes ||
        payload.size() >
            std::numeric_limits<std::uint16_t>::max() ||
        !ValidSplit(
            *policy,
            partition_index,
            partition_count)) {
        throw std::invalid_argument(
            "battle raw encode policy is invalid");
    }
    const auto projection =
        ParsePayload(message_id, payload);
    if (!projection.valid ||
        (projection.has_payload_partition &&
         (projection.partition_index != partition_index ||
          projection.partition_count != partition_count))) {
        throw std::invalid_argument(
            "battle raw payload binding is invalid");
    }
    std::vector<std::uint8_t> frame(
        RawHeaderBytes + payload.size());
    WriteUint32BE(frame.data(), message_id);
    WriteUint16BE(
        frame.data() + 4,
        static_cast<std::uint16_t>(payload.size()));
    frame[6] = partition_index;
    frame[7] = partition_count;
    WriteUint64BE(
        frame.data() + 8,
        application_sequence);
    std::ranges::copy(
        payload,
        frame.begin() + RawHeaderBytes);
    return frame;
}

}  // namespace ihomeland::sim
