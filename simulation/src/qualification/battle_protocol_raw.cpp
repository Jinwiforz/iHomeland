#include "ihomeland/qualification/battle/protocol_client.hpp"

#include "ihomeland/battle/v1/battle.pb.h"

#include <algorithm>
#include <limits>
#include <ranges>
#include <stdexcept>

namespace ihomeland::qualification::battle {
namespace {

constexpr std::size_t RawHeaderBytes = 16;
constexpr std::size_t InputMaximumPayloadBytes = 384;
constexpr std::size_t ProbeMaximumPayloadBytes = 96;
constexpr std::size_t FullMaximumPayloadBytes = 1'040;
constexpr std::size_t DeltaMaximumPayloadBytes = 900;
constexpr std::uint32_t InputMessageID = 3'000;
constexpr std::uint32_t ProbeMessageID = 3'001;
constexpr std::uint32_t FullSnapshotMessageID = 3'002;
constexpr std::uint32_t DeltaSnapshotMessageID = 3'003;

/// WriteUint16BE 编码 raw payload length。
void WriteUint16BE(
    std::uint8_t* output,
    const std::uint16_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 8U);
    output[1] = static_cast<std::uint8_t>(value);
}

/// WriteUint32BE 编码 raw message id。
void WriteUint32BE(
    std::uint8_t* output,
    const std::uint32_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// WriteUint64BE 编码 raw application sequence。
void WriteUint64BE(
    std::uint8_t* output,
    const std::uint64_t value) noexcept {
    for (std::size_t index = 0; index < 8; ++index) {
        output[index] = static_cast<std::uint8_t>(
            value >> ((7U - index) * 8U));
    }
}

/// ReadUint16BE 解码 raw payload length。
[[nodiscard]] std::uint16_t ReadUint16BE(
    const std::uint8_t* input) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(input[0]) << 8U) |
        static_cast<std::uint16_t>(input[1]));
}

/// ReadUint32BE 解码 raw message id。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::uint8_t* input) noexcept {
    return
        (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// ReadUint64BE 解码 raw application sequence。
[[nodiscard]] std::uint64_t ReadUint64BE(
    const std::uint8_t* input) noexcept {
    std::uint64_t value = 0;
    for (std::size_t index = 0; index < 8; ++index) {
        value = (value << 8U) | input[index];
    }
    return value;
}

/// SnapshotProjection 是 full/delta typed outer fields 的统一投影。
struct SnapshotProjection final {
    /// valid 表示 Protobuf 与 route-specific 不变量合法。
    bool valid{};
    /// server_tick 是权威 simulation tick。
    std::uint64_t server_tick{};
    /// snapshot_sequence 是 raw state 序列。
    std::uint64_t snapshot_sequence{};
    /// baseline_id 是建立或引用的 baseline。
    std::uint64_t baseline_id{};
    /// partition_index 是本分片序号。
    std::uint32_t partition_index{};
    /// partition_count 是本 snapshot 的总分片数。
    std::uint32_t partition_count{};
    /// last_processed_input_tick 是显式存在的当前 actor 确认。
    std::uint64_t last_processed_input_tick{};
};

/// ParseSnapshot 只接受 full/delta closed outer contract。
[[nodiscard]] SnapshotProjection ParseSnapshot(
    const std::uint32_t message_id,
    const std::span<const std::uint8_t> payload) {
    if (message_id == FullSnapshotMessageID) {
        ihomeland::battle::v1::BattleFullSnapshot message;
        if (!message.ParseFromArray(
                payload.data(),
                static_cast<int>(payload.size())) ||
            message.server_tick() == 0 ||
            message.snapshot_sequence() == 0 ||
            message.baseline_id() == 0 ||
            !message
                 .has_last_processed_input_tick() ||
            message.partition_count() == 0 ||
            message.partition_count() >
                ProtocolRawLane::MaximumPartitions ||
            message.partition_index() >=
                message.partition_count()) {
            return {};
        }
        return {
            .valid = true,
            .server_tick = message.server_tick(),
            .snapshot_sequence =
                message.snapshot_sequence(),
            .baseline_id = message.baseline_id(),
            .partition_index = message.partition_index(),
            .partition_count = message.partition_count(),
            .last_processed_input_tick =
                message.last_processed_input_tick(),
        };
    }
    if (message_id == DeltaSnapshotMessageID) {
        ihomeland::battle::v1::BattleDeltaSnapshot message;
        if (!message.ParseFromArray(
                payload.data(),
                static_cast<int>(payload.size())) ||
            message.server_tick() == 0 ||
            message.snapshot_sequence() == 0 ||
            message.baseline_id() == 0 ||
            !message
                 .has_last_processed_input_tick() ||
            message.partition_count() == 0 ||
            message.partition_count() >
                ProtocolRawLane::MaximumPartitions ||
            message.partition_index() >=
                message.partition_count()) {
            return {};
        }
        return {
            .valid = true,
            .server_tick = message.server_tick(),
            .snapshot_sequence =
                message.snapshot_sequence(),
            .baseline_id = message.baseline_id(),
            .partition_index = message.partition_index(),
            .partition_count = message.partition_count(),
            .last_processed_input_tick =
                message.last_processed_input_tick(),
        };
    }
    return {};
}

}  // namespace

ProtocolRawLane::ProtocolRawLane() = default;

std::vector<std::uint8_t>
ProtocolRawLane::EncodeInputBundle(
    const std::uint64_t application_sequence,
    const std::span<const std::uint8_t> protobuf_payload) const {
    if (protobuf_payload.empty() ||
        protobuf_payload.size() >
            InputMaximumPayloadBytes) {
        throw std::invalid_argument(
            "qualification input payload is invalid");
    }
    ihomeland::battle::v1::BattleInputBundle message;
    if (!message.ParseFromArray(
            protobuf_payload.data(),
            static_cast<int>(protobuf_payload.size())) ||
        message.newest_input_tick() == 0 ||
        message.commands_size() == 0) {
        throw std::invalid_argument(
            "qualification input contract is invalid");
    }
    return EncodeC2S(
        InputMessageID,
        application_sequence,
        protobuf_payload);
}

std::vector<std::uint8_t>
ProtocolRawLane::EncodeProbe(
    const std::uint64_t application_sequence,
    const std::span<const std::uint8_t> protobuf_payload) const {
    if (protobuf_payload.empty() ||
        protobuf_payload.size() >
            ProbeMaximumPayloadBytes) {
        throw std::invalid_argument(
            "qualification probe payload is invalid");
    }
    ihomeland::battle::v1::BattleProbe message;
    if (!message.ParseFromArray(
            protobuf_payload.data(),
            static_cast<int>(protobuf_payload.size())) ||
        message.probe_sequence() == 0) {
        throw std::invalid_argument(
            "qualification probe contract is invalid");
    }
    return EncodeC2S(
        ProbeMessageID,
        application_sequence,
        protobuf_payload);
}

RawDecodeResult
ProtocolRawLane::DecodeSnapshot(
    const std::span<const std::uint8_t> plaintext) {
    if (plaintext.size() <= RawHeaderBytes ||
        plaintext.size() > MaximumPayloadBytes) {
        return {
            .disposition = RawDecodeDisposition::Rejected,
            .message = std::nullopt,
        };
    }
    const auto message_id =
        ReadUint32BE(plaintext.data());
    const auto payload_bytes =
        ReadUint16BE(plaintext.data() + 4);
    const auto partition_index = plaintext[6];
    const auto partition_count = plaintext[7];
    const auto application_sequence =
        ReadUint64BE(plaintext.data() + 8);
    const auto maximum_payload =
        message_id == FullSnapshotMessageID
        ? FullMaximumPayloadBytes
        : message_id == DeltaSnapshotMessageID
        ? DeltaMaximumPayloadBytes
        : 0;
    if (maximum_payload == 0 ||
        payload_bytes == 0 ||
        payload_bytes > maximum_payload ||
        static_cast<std::size_t>(payload_bytes) +
                RawHeaderBytes !=
            plaintext.size() ||
        partition_count == 0 ||
        partition_count > MaximumPartitions ||
        partition_index >= partition_count ||
        application_sequence == 0) {
        return {
            .disposition = RawDecodeDisposition::Rejected,
            .message = std::nullopt,
        };
    }
    const auto projection = ParseSnapshot(
        message_id,
        plaintext.subspan(RawHeaderBytes));
    if (!projection.valid ||
        projection.partition_index !=
            partition_index ||
        projection.partition_count !=
            partition_count) {
        if (pending_snapshot_sequence_ != 0 &&
            (!projection.valid ||
             projection.snapshot_sequence ==
                 pending_snapshot_sequence_)) {
            ResetPendingSnapshot();
        }
        return {
            .disposition = RawDecodeDisposition::Rejected,
            .message = std::nullopt,
        };
    }
    if (projection.snapshot_sequence <=
            latest_snapshot_sequence_ ||
        (pending_snapshot_sequence_ != 0 &&
         projection.snapshot_sequence <
             pending_snapshot_sequence_)) {
        return {
            .disposition = RawDecodeDisposition::Discarded,
            .message = std::nullopt,
        };
    }
    if (projection.server_tick <
            latest_server_tick_ ||
        projection.last_processed_input_tick <
            last_processed_input_tick_) {
        if (projection.snapshot_sequence ==
            pending_snapshot_sequence_) {
            ResetPendingSnapshot();
        }
        return {
            .disposition = RawDecodeDisposition::Rejected,
            .message = std::nullopt,
        };
    }
    if (message_id == DeltaSnapshotMessageID &&
        projection.baseline_id != baseline_id_) {
        if (projection.snapshot_sequence ==
            pending_snapshot_sequence_) {
            ResetPendingSnapshot();
        }
        return {
            .disposition = RawDecodeDisposition::BaselineGap,
            .message = std::nullopt,
        };
    }

    if (pending_snapshot_sequence_ != 0 &&
        projection.snapshot_sequence >
            pending_snapshot_sequence_) {
        ResetPendingSnapshot();
    }
    const auto begins_snapshot =
        pending_snapshot_sequence_ == 0;
    if (begins_snapshot) {
        pending_message_id_ = message_id;
        pending_baseline_id_ = projection.baseline_id;
        pending_snapshot_sequence_ =
            projection.snapshot_sequence;
        pending_server_tick_ = projection.server_tick;
        pending_partition_count_ =
            projection.partition_count;
        pending_last_processed_input_tick_ =
            projection.last_processed_input_tick;
        pending_partition_bitmap_ = 0;
    } else if (
        pending_message_id_ != message_id ||
        pending_baseline_id_ != projection.baseline_id ||
        pending_snapshot_sequence_ !=
            projection.snapshot_sequence ||
        pending_server_tick_ != projection.server_tick ||
        pending_partition_count_ !=
            projection.partition_count ||
        pending_last_processed_input_tick_ !=
            projection.last_processed_input_tick) {
        ResetPendingSnapshot();
        return {
            .disposition = RawDecodeDisposition::Rejected,
            .message = std::nullopt,
        };
    }

    const auto partition_bit =
        std::uint32_t{1} << projection.partition_index;
    if ((pending_partition_bitmap_ &
         partition_bit) != 0) {
        return {
            .disposition = RawDecodeDisposition::Discarded,
            .message = std::nullopt,
        };
    }
    pending_partition_bitmap_ |= partition_bit;
    const auto expected_bitmap =
        pending_partition_count_ == MaximumPartitions
        ? std::numeric_limits<std::uint32_t>::max()
        : (std::uint32_t{1} <<
               pending_partition_count_) -
              1U;
    if (pending_partition_bitmap_ !=
        expected_bitmap) {
        return {
            .disposition = RawDecodeDisposition::Pending,
            .message = std::nullopt,
        };
    }
    latest_server_tick_ =
        pending_server_tick_;
    latest_snapshot_sequence_ =
        pending_snapshot_sequence_;
    last_processed_input_tick_ =
        pending_last_processed_input_tick_;
    if (pending_message_id_ ==
        FullSnapshotMessageID) {
        baseline_id_ = pending_baseline_id_;
    }
    pending_message_id_ = 0;
    pending_baseline_id_ = 0;
    pending_snapshot_sequence_ = 0;
    pending_server_tick_ = 0;
    pending_partition_count_ = 0;
    pending_last_processed_input_tick_ = 0;
    pending_partition_bitmap_ = 0;
    return {
        .disposition = RawDecodeDisposition::Accepted,
        .message = RawMessage{
            .message_id = message_id,
            .application_sequence =
                application_sequence,
            .server_tick = projection.server_tick,
            .snapshot_sequence =
                projection.snapshot_sequence,
            .baseline_id = projection.baseline_id,
            .partition_index =
                projection.partition_index,
            .partition_count =
                projection.partition_count,
            .last_processed_input_tick =
                projection.last_processed_input_tick,
        },
    };
}

std::uint64_t
ProtocolRawLane::LatestServerTick() const noexcept {
    return latest_server_tick_;
}

std::uint64_t
ProtocolRawLane::LatestSnapshotSequence() const noexcept {
    return latest_snapshot_sequence_;
}

std::uint64_t
ProtocolRawLane::LastProcessedInputTick() const noexcept {
    return last_processed_input_tick_;
}

std::uint64_t
ProtocolRawLane::BaselineID() const noexcept {
    return baseline_id_;
}

std::uint64_t
ProtocolRawLane::PendingSnapshotSequence() const noexcept {
    return pending_snapshot_sequence_;
}

void ProtocolRawLane::ResetPendingSnapshot() noexcept {
    pending_message_id_ = 0;
    pending_baseline_id_ = 0;
    pending_snapshot_sequence_ = 0;
    pending_server_tick_ = 0;
    pending_partition_count_ = 0;
    pending_last_processed_input_tick_ = 0;
    pending_partition_bitmap_ = 0;
}

std::vector<std::uint8_t>
ProtocolRawLane::EncodeC2S(
    const std::uint32_t message_id,
    const std::uint64_t application_sequence,
    const std::span<const std::uint8_t> protobuf_payload) {
    if ((message_id != InputMessageID &&
         message_id != ProbeMessageID) ||
        application_sequence == 0 ||
        protobuf_payload.empty() ||
        protobuf_payload.size() >
            std::numeric_limits<std::uint16_t>::max() ||
        protobuf_payload.size() +
                RawHeaderBytes >
            MaximumPayloadBytes) {
        throw std::invalid_argument(
            "qualification raw route input is invalid");
    }
    std::vector<std::uint8_t> output(
        RawHeaderBytes + protobuf_payload.size());
    WriteUint32BE(output.data(), message_id);
    WriteUint16BE(
        output.data() + 4,
        static_cast<std::uint16_t>(
            protobuf_payload.size()));
    output[6] = 0;
    output[7] = 1;
    WriteUint64BE(
        output.data() + 8,
        application_sequence);
    std::ranges::copy(
        protobuf_payload,
        output.begin() + RawHeaderBytes);
    return output;
}

}  // namespace ihomeland::qualification::battle
