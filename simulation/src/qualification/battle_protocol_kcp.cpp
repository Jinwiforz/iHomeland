#include "ihomeland/qualification/battle/protocol_client.hpp"

#include "ihomeland/battle/v1/battle.pb.h"

#include <ikcp.h>

#include <algorithm>
#include <deque>
#include <limits>
#include <mutex>
#include <ranges>
#include <stdexcept>
#include <utility>
#include <vector>

namespace ihomeland::qualification::battle {
namespace {

constexpr std::size_t KcpHeaderBytes = 24;
constexpr std::size_t RouteHeaderBytes = 16;
constexpr std::size_t MaximumMessageBytes = 1'000;
constexpr std::size_t ResyncRequestMaximumBytes = 128;
constexpr std::size_t AbilityEventMaximumBytes = 512;
constexpr std::size_t EntityLifecycleMaximumBytes = 512;
constexpr std::size_t ResyncResponseMaximumBytes = 768;
constexpr std::uint32_t AbilityEventMessageID = 3'004;
constexpr std::uint32_t EntityLifecycleMessageID = 3'005;
constexpr std::uint32_t ResyncRequestMessageID = 3'006;
constexpr std::uint32_t ResyncResponseMessageID = 3'007;
constexpr std::uint8_t KcpPush = 81;
constexpr std::uint8_t KcpAck = 82;
constexpr std::uint8_t KcpWindowAsk = 83;
constexpr std::uint8_t KcpWindowTell = 84;
constexpr std::uint32_t DeadLinkRetransmits = 10;

/// WriteUint16BE 编码 route payload length。
void WriteUint16BE(
    std::uint8_t* output,
    const std::uint16_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 8U);
    output[1] = static_cast<std::uint8_t>(value);
}

/// WriteUint32BE 编码 route message id。
void WriteUint32BE(
    std::uint8_t* output,
    const std::uint32_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// WriteUint64BE 编码 route application sequence。
void WriteUint64BE(
    std::uint8_t* output,
    const std::uint64_t value) noexcept {
    for (std::size_t index = 0; index < 8; ++index) {
        output[index] = static_cast<std::uint8_t>(
            value >> ((7U - index) * 8U));
    }
}

/// ReadUint16BE 解码 route payload length。
[[nodiscard]] std::uint16_t ReadUint16BE(
    const std::uint8_t* input) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(input[0]) << 8U) |
        static_cast<std::uint16_t>(input[1]));
}

/// ReadUint32BE 解码 route message id。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::uint8_t* input) noexcept {
    return
        (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// ReadUint64BE 解码 route application sequence。
[[nodiscard]] std::uint64_t ReadUint64BE(
    const std::uint8_t* input) noexcept {
    std::uint64_t output = 0;
    for (std::size_t index = 0; index < 8; ++index) {
        output = (output << 8U) | input[index];
    }
    return output;
}

/// ReadUint16LE 解码 KCP window。
[[nodiscard]] std::uint16_t ReadUint16LE(
    const std::uint8_t* input) noexcept {
    return static_cast<std::uint16_t>(
        static_cast<std::uint16_t>(input[0]) |
        (static_cast<std::uint16_t>(input[1]) << 8U));
}

/// ReadUint32LE 解码 KCP conv/len。
[[nodiscard]] std::uint32_t ReadUint32LE(
    const std::uint8_t* input) noexcept {
    return
        static_cast<std::uint32_t>(input[0]) |
        (static_cast<std::uint32_t>(input[1]) << 8U) |
        (static_cast<std::uint32_t>(input[2]) << 16U) |
        (static_cast<std::uint32_t>(input[3]) << 24U);
}

/// ValidateSegment 拒绝错误 conv、command、window、length 与 trailing bytes。
[[nodiscard]] bool ValidateSegment(
    const std::span<const std::uint8_t> segment,
    const std::uint32_t conversation) noexcept {
    if (segment.empty() ||
        segment.size() >
            KcpHeaderBytes +
                ProtocolKcpLane::MaximumSegmentPayloadBytes) {
        return false;
    }
    std::size_t offset = 0;
    while (offset < segment.size()) {
        if (segment.size() - offset <
            KcpHeaderBytes) {
            return false;
        }
        const auto* header =
            segment.data() + offset;
        const auto command = header[4];
        const auto payload_bytes =
            ReadUint32LE(header + 20);
        if (ReadUint32LE(header) != conversation ||
            (command != KcpPush &&
             command != KcpAck &&
             command != KcpWindowAsk &&
             command != KcpWindowTell) ||
            ReadUint16LE(header + 6) >
                ProtocolKcpLane::WindowSegments ||
            payload_bytes >
                ProtocolKcpLane::
                    MaximumSegmentPayloadBytes ||
            payload_bytes >
                segment.size() -
                    offset -
                    KcpHeaderBytes) {
            return false;
        }
        offset += KcpHeaderBytes + payload_bytes;
    }
    return offset == segment.size();
}

/// KcpCommandCounts 是一个已验证 datagram 内 ACK/PUSH command 的低敏计数。
struct KcpCommandCounts final {
    /// acknowledgements 是 ACK command 数。
    std::uint64_t acknowledgements{};
    /// pushes 是 PUSH command 数。
    std::uint64_t pushes{};
};

/// CountCommands 只读取已通过 ValidateSegment 的公开 KCP header。
[[nodiscard]] KcpCommandCounts CountCommands(
    const std::span<const std::uint8_t> segment) noexcept {
    KcpCommandCounts counts;
    std::size_t offset = 0;
    while (offset < segment.size()) {
        const auto* header = segment.data() + offset;
        if (header[4] == KcpAck) {
            ++counts.acknowledgements;
        } else if (header[4] == KcpPush) {
            ++counts.pushes;
        }
        offset += KcpHeaderBytes + ReadUint32LE(header + 20);
    }
    return counts;
}

/// ParsedS2C 是 closed route 解析出的权威 tick。
struct ParsedS2C final {
    /// server_tick 是 payload 携带的权威 simulation tick。
    std::uint64_t server_tick;
};

/// ParseS2C 验证可靠服务端 payload 并投影 immutable route policy。
[[nodiscard]] std::optional<ParsedS2C> ParseS2C(
    const std::uint32_t message_id,
    const std::span<const std::uint8_t> payload) {
    switch (message_id) {
        case AbilityEventMessageID: {
            ihomeland::battle::v1::
                BattleAbilityReliableEvent message;
            if (payload.size() >
                    AbilityEventMaximumBytes ||
                !message.ParseFromArray(
                    payload.data(),
                    static_cast<int>(payload.size())) ||
                message.event_id() == 0 ||
                message.server_tick() == 0 ||
                message.source_entity_id() == 0 ||
                message.source_entity_generation() == 0 ||
                message.ability_id() == 0 ||
                message.phase() ==
                    ihomeland::battle::v1::
                        BATTLE_ABILITY_PHASE_UNSPECIFIED) {
                return std::nullopt;
            }
            return ParsedS2C{
                .server_tick = message.server_tick(),
            };
        }
        case EntityLifecycleMessageID: {
            ihomeland::battle::v1::
                BattleEntityLifecycle message;
            if (payload.size() >
                    EntityLifecycleMaximumBytes ||
                !message.ParseFromArray(
                    payload.data(),
                    static_cast<int>(payload.size())) ||
                message.event_id() == 0 ||
                message.server_tick() == 0 ||
                message.entity_id() == 0 ||
                message.entity_generation() == 0 ||
                message.kind() ==
                    ihomeland::battle::v1::
                        BATTLE_ENTITY_LIFECYCLE_KIND_UNSPECIFIED) {
                return std::nullopt;
            }
            return ParsedS2C{
                .server_tick = message.server_tick(),
            };
        }
        case ResyncResponseMessageID: {
            ihomeland::battle::v1::
                BattleResyncResponse message;
            if (payload.size() >
                    ResyncResponseMaximumBytes ||
                !message.ParseFromArray(
                    payload.data(),
                    static_cast<int>(payload.size())) ||
                message.request_sequence() == 0 ||
                message.server_tick() == 0 ||
                message.disposition() ==
                    ihomeland::battle::v1::
                        BATTLE_RESYNC_DISPOSITION_UNSPECIFIED) {
                return std::nullopt;
            }
            return ParsedS2C{
                .server_tick = message.server_tick(),
            };
        }
        default:
            return std::nullopt;
    }
}

}  // namespace

/// ProtocolKcpLane::Impl 隔离 KCP handle 与有界 client queue。
struct ProtocolKcpLane::Impl final {
    /// PendingMessage 是尚未提交给 KCP 的 immutable route frame。
    struct PendingMessage final {
        /// frame 是 16-byte route header 与 protobuf。
        std::vector<std::uint8_t> frame;
        /// deadline_unix_ms 等于时终结且不降级。
        std::uint64_t deadline_unix_ms;
    };

    /// 构造函数锁定 exact KCP 2.1.1 profile。
    explicit Impl(
        const std::uint32_t value)
        : conversation(value) {
        kcp = ikcp_create(conversation, this);
        if (kcp == nullptr ||
            ikcp_setmtu(
                kcp,
                static_cast<int>(
                    KcpHeaderBytes +
                    MaximumSegmentPayloadBytes)) != 0 ||
            ikcp_wndsize(
                kcp,
                static_cast<int>(WindowSegments),
                static_cast<int>(WindowSegments)) != 0 ||
            ikcp_nodelay(
                kcp,
                1,
                static_cast<int>(
                    UpdateIntervalMilliseconds),
                static_cast<int>(FastResend),
                1) != 0) {
            if (kcp != nullptr) {
                ikcp_release(kcp);
                kcp = nullptr;
            }
            throw std::runtime_error(
                "qualification KCP initialization failed");
        }
        kcp->rx_minrto =
            static_cast<IINT32>(
                MinimumRtoMilliseconds);
        kcp->rx_rto =
            static_cast<IINT32>(
                MaximumRtoMilliseconds);
        kcp->dead_link = DeadLinkRetransmits;
        kcp->snd_wnd = WindowSegments;
        kcp->rcv_wnd = WindowSegments;
        kcp->stream = 0;
        ikcp_setoutput(kcp, &OutputBridge);
    }

    /// 析构函数释放第三方 handle。
    ~Impl() {
        if (kcp != nullptr) {
            ikcp_release(kcp);
        }
    }

    /// OutputBridge 只复制单个有界 KCP output，不允许异常穿越 C ABI。
    static int OutputBridge(
        const char* buffer,
        const int length,
        ikcpcb*,
        void* user) noexcept {
        auto* self = static_cast<Impl*>(user);
        if (self == nullptr ||
            buffer == nullptr ||
            length <= 0 ||
            static_cast<std::size_t>(length) >
                KcpHeaderBytes +
                    MaximumSegmentPayloadBytes) {
            if (self != nullptr) {
                self->closed = true;
                self->close_reason =
                    KcpCloseReason::Output;
            }
            return -1;
        }
        try {
            const auto* first =
                reinterpret_cast<const std::uint8_t*>(
                    buffer);
            self->outputs.emplace_back(
                first,
                first + length);
            ++self->output_datagrams;
            return 0;
        } catch (...) {
            self->closed = true;
            self->close_reason =
                KcpCloseReason::Output;
            return -1;
        }
    }

    /// ClampRto 防止第三方 adaptive 状态越过冻结边界。
    void ClampRto() noexcept {
        kcp->rx_minrto = static_cast<IINT32>(
            MinimumRtoMilliseconds);
        kcp->rx_rto = std::clamp(
            kcp->rx_rto,
            static_cast<IINT32>(
                MinimumRtoMilliseconds),
            static_cast<IINT32>(
                MaximumRtoMilliseconds));
        auto* cursor = kcp->snd_buf.next;
        while (cursor != &kcp->snd_buf) {
            auto* segment =
                iqueue_entry(cursor, IKCPSEG, node);
            segment->rto = std::clamp(
                segment->rto,
                static_cast<IUINT32>(
                    MinimumRtoMilliseconds),
                static_cast<IUINT32>(
                    MaximumRtoMilliseconds));
            cursor = cursor->next;
        }
    }

    /// ReconcileInflight 按one-message-one-segment profile回收已ACK deadline。
    void ReconcileInflight() {
        const auto waiting = static_cast<std::size_t>(
            std::max(0, ikcp_waitsnd(kcp)));
        while (inflight_deadlines.size() > waiting) {
            inflight_deadlines.pop_front();
            ++reconciled_messages;
        }
    }

    /// conversation 是 session-derived nonzero conv。
    std::uint32_t conversation;
    /// kcp 是 client-owned third-party handle。
    ikcpcb* kcp{};
    /// queued 是尚未交给 KCP 的 application messages。
    std::deque<PendingMessage> queued;
    /// inflight_deadlines 对应尚在 KCP send buffer 的 message deadlines。
    std::deque<std::uint64_t> inflight_deadlines;
    /// outputs 等待进入 secure KCP lane。
    std::vector<std::vector<std::uint8_t>> outputs;
    /// messages 等待交给 qualification state owner。
    std::vector<KcpMessage> messages;
    /// input_datagrams 是 secure KCP lane 接受并交给 primitive 的累计 datagram 数。
    std::uint64_t input_datagrams{};
    /// input_ack_commands 是已验证入站 ACK command 累计数。
    std::uint64_t input_ack_commands{};
    /// input_push_commands 是已验证入站 PUSH command 累计数。
    std::uint64_t input_push_commands{};
    /// output_datagrams 是 primitive 产生的 KCP datagram 累计数。
    std::uint64_t output_datagrams{};
    /// reconciled_messages 是 peer ACK 已回收的 inflight message 累计数。
    std::uint64_t reconciled_messages{};
    /// last_received_sequence 拒绝可靠 route replay/rollback。
    std::uint64_t last_received_sequence{};
    /// clock_origin_unix_ms 将 absolute clock 映射为 KCP uint32 runtime。
    std::uint64_t clock_origin_unix_ms{};
    /// last_update_unix_ms 拒绝 clock rollback。
    std::uint64_t last_update_unix_ms{};
    /// closed 是 parse、primitive 或 expiry failure 后的稳定终态。
    bool closed{false};
    /// close_reason 是不含动态数据的终态分类。
    KcpCloseReason close_reason{KcpCloseReason::None};
    /// mutex 串行化 KCP C handle 和所有 queues。
    mutable std::mutex mutex;
};

ProtocolKcpLane::ProtocolKcpLane(
    const std::uint32_t conversation)
    : impl_(std::make_unique<Impl>(conversation)) {
    if (conversation == 0) {
        throw std::invalid_argument(
            "qualification KCP conversation is zero");
    }
}

ProtocolKcpLane::~ProtocolKcpLane() = default;

bool ProtocolKcpLane::QueueResync(
    const std::uint64_t application_sequence,
    const std::span<const std::uint8_t> protobuf_payload,
    const std::uint64_t now_unix_ms) {
    if (application_sequence == 0 ||
        protobuf_payload.empty() ||
        protobuf_payload.size() >
            ResyncRequestMaximumBytes ||
        now_unix_ms == 0 ||
        now_unix_ms >
            std::numeric_limits<std::uint64_t>::max() -
                ResyncExpiryMilliseconds) {
        return false;
    }
    ihomeland::battle::v1::BattleResyncRequest message;
    if (!message.ParseFromArray(
            protobuf_payload.data(),
            static_cast<int>(protobuf_payload.size())) ||
        message.request_sequence() !=
            application_sequence ||
        message.latest_server_tick() == 0 ||
        message.reason() ==
            ihomeland::battle::v1::
                BATTLE_RESYNC_REASON_UNSPECIFIED) {
        return false;
    }
    std::scoped_lock lock(impl_->mutex);
    if (impl_->closed ||
        impl_->queued.size() +
                impl_->inflight_deadlines.size() >=
            QueueItems) {
        return false;
    }
    std::vector<std::uint8_t> frame(
        RouteHeaderBytes +
        protobuf_payload.size());
    WriteUint32BE(
        frame.data(),
        ResyncRequestMessageID);
    WriteUint16BE(
        frame.data() + 4,
        static_cast<std::uint16_t>(
            protobuf_payload.size()));
    WriteUint64BE(
        frame.data() + 8,
        application_sequence);
    std::ranges::copy(
        protobuf_payload,
        frame.begin() + RouteHeaderBytes);
    impl_->queued.push_back({
        .frame = std::move(frame),
        .deadline_unix_ms =
            now_unix_ms +
            ResyncExpiryMilliseconds,
    });
    return true;
}

bool ProtocolKcpLane::Input(
    const std::span<const std::uint8_t> segment,
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(impl_->mutex);
    if (impl_->closed) {
        return false;
    }
    if (now_unix_ms == 0 ||
        (impl_->last_update_unix_ms != 0 &&
         now_unix_ms < impl_->last_update_unix_ms)) {
        impl_->closed = true;
        impl_->close_reason =
            KcpCloseReason::Clock;
        return false;
    }
    if (!ValidateSegment(
            segment,
            impl_->conversation)) {
        impl_->closed = true;
        impl_->close_reason =
            KcpCloseReason::Protocol;
        return false;
    }
    const auto commands = CountCommands(segment);
    ++impl_->input_datagrams;
    impl_->input_ack_commands +=
        commands.acknowledgements;
    impl_->input_push_commands += commands.pushes;
    if (ikcp_input(
            impl_->kcp,
            reinterpret_cast<const char*>(
                segment.data()),
            static_cast<long>(segment.size())) < 0) {
        impl_->closed = true;
        impl_->close_reason =
            KcpCloseReason::Primitive;
        return false;
    }
    impl_->ClampRto();
    impl_->ReconcileInflight();
    return true;
}

void ProtocolKcpLane::Update(
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(impl_->mutex);
    if (impl_->closed) {
        return;
    }
    if (now_unix_ms == 0 ||
        (impl_->last_update_unix_ms != 0 &&
         now_unix_ms < impl_->last_update_unix_ms)) {
        impl_->closed = true;
        impl_->close_reason =
            KcpCloseReason::Clock;
        return;
    }
    if (impl_->clock_origin_unix_ms == 0) {
        impl_->clock_origin_unix_ms = now_unix_ms;
    }
    const auto relative_now =
        now_unix_ms -
        impl_->clock_origin_unix_ms;
    if (relative_now >
        std::numeric_limits<std::uint32_t>::max()) {
        impl_->closed = true;
        impl_->close_reason =
            KcpCloseReason::Clock;
        return;
    }
    while (!impl_->queued.empty()) {
        if (now_unix_ms >=
            impl_->queued.front().deadline_unix_ms) {
            impl_->queued.pop_front();
            continue;
        }
        if (impl_->inflight_deadlines.size() >=
            QueueItems) {
            break;
        }
        auto& item = impl_->queued.front();
        if (ikcp_send(
                impl_->kcp,
                reinterpret_cast<const char*>(
                    item.frame.data()),
                static_cast<int>(
                    item.frame.size())) < 0) {
                impl_->closed = true;
                impl_->close_reason =
                    KcpCloseReason::Primitive;
                return;
        }
        impl_->inflight_deadlines.push_back(
            item.deadline_unix_ms);
        impl_->queued.pop_front();
    }
    if (std::ranges::any_of(
            impl_->inflight_deadlines,
            [&](const std::uint64_t deadline) {
                return now_unix_ms >= deadline;
            })) {
        impl_->closed = true;
        impl_->close_reason =
            KcpCloseReason::InflightExpiry;
        return;
    }
    if (impl_->last_update_unix_ms == 0 ||
        now_unix_ms -
                impl_->last_update_unix_ms >=
            UpdateIntervalMilliseconds) {
        impl_->ClampRto();
        ikcp_update(
            impl_->kcp,
            static_cast<IUINT32>(
                relative_now));
        impl_->ClampRto();
        impl_->last_update_unix_ms =
            now_unix_ms;
    }
    impl_->ReconcileInflight();

    while (!impl_->closed) {
        const auto bytes = ikcp_peeksize(impl_->kcp);
        if (bytes < 0) {
            break;
        }
        if (bytes <= static_cast<int>(
                         RouteHeaderBytes) ||
            bytes > static_cast<int>(
                        MaximumMessageBytes)) {
            impl_->closed = true;
            impl_->close_reason =
                KcpCloseReason::Protocol;
            break;
        }
        std::vector<std::uint8_t> frame(
            static_cast<std::size_t>(bytes));
        if (ikcp_recv(
                impl_->kcp,
                reinterpret_cast<char*>(
                    frame.data()),
                bytes) != bytes) {
            impl_->closed = true;
            impl_->close_reason =
                KcpCloseReason::Primitive;
            break;
        }
        const auto message_id =
            ReadUint32BE(frame.data());
        const auto payload_bytes =
            ReadUint16BE(frame.data() + 4);
        const auto application_sequence =
            ReadUint64BE(frame.data() + 8);
        const auto parsed = ParseS2C(
            message_id,
            std::span(frame).subspan(
                RouteHeaderBytes));
        if (frame[6] != 0 ||
            frame[7] != 0 ||
            payload_bytes == 0 ||
            static_cast<std::size_t>(
                payload_bytes) +
                    RouteHeaderBytes !=
                frame.size() ||
            application_sequence == 0 ||
            application_sequence <=
                impl_->last_received_sequence ||
            !parsed.has_value()) {
            impl_->closed = true;
            impl_->close_reason =
                KcpCloseReason::Protocol;
            break;
        }
        impl_->last_received_sequence =
            application_sequence;
        impl_->messages.push_back({
            .message_id = message_id,
            .application_sequence =
                application_sequence,
            .server_tick = parsed->server_tick,
        });
    }
}

std::vector<std::vector<std::uint8_t>>
ProtocolKcpLane::TakeSegments() {
    std::scoped_lock lock(impl_->mutex);
    return std::exchange(impl_->outputs, {});
}

std::vector<KcpMessage>
ProtocolKcpLane::TakeMessages() {
    std::scoped_lock lock(impl_->mutex);
    return std::exchange(impl_->messages, {});
}

bool ProtocolKcpLane::Closed() const noexcept {
    std::scoped_lock lock(impl_->mutex);
    return impl_->closed;
}

KcpCloseReason
ProtocolKcpLane::CloseReason() const noexcept {
    std::scoped_lock lock(impl_->mutex);
    return impl_->close_reason;
}

ProtocolKcpStatus
ProtocolKcpLane::Status() const noexcept {
    std::scoped_lock lock(impl_->mutex);
    return {
        .close_reason = impl_->close_reason,
        .queued_messages = impl_->queued.size(),
        .inflight_messages =
            impl_->inflight_deadlines.size(),
        .waiting_segments =
            static_cast<std::size_t>(
                std::max(
                    0,
                    ikcp_waitsnd(impl_->kcp))),
        .input_datagrams = impl_->input_datagrams,
        .input_ack_commands =
            impl_->input_ack_commands,
        .input_push_commands =
            impl_->input_push_commands,
        .output_datagrams =
            impl_->output_datagrams,
        .reconciled_messages =
            impl_->reconciled_messages,
    };
}

}  // namespace ihomeland::qualification::battle
