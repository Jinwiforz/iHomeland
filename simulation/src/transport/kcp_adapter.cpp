#include "ihomeland/sim/transport/kcp_adapter.hpp"

#include "ihomeland/sim/observability/battle_runtime_metrics.hpp"
#include "ihomeland/battle/v1/battle.pb.h"

#include <ikcp.h>

#include <algorithm>
#include <array>
#include <deque>
#include <limits>
#include <mutex>
#include <optional>
#include <ranges>
#include <stdexcept>
#include <utility>
#include <vector>

namespace ihomeland::sim {
namespace {

constexpr std::size_t KcpHeaderBytes = 24;
constexpr std::size_t RouteEnvelopeBytes = 16;
constexpr std::uint8_t KcpPush = 81;
constexpr std::uint8_t KcpAck = 82;
constexpr std::uint8_t KcpWindowAsk = 83;
constexpr std::uint8_t KcpWindowTell = 84;

/// ReadUint16LE 读取 KCP little-endian window。
[[nodiscard]] std::uint16_t ReadUint16LE(
    const std::uint8_t* input) noexcept {
    return static_cast<std::uint16_t>(
        static_cast<std::uint16_t>(input[0]) |
        (static_cast<std::uint16_t>(input[1]) << 8U));
}

/// ReadUint32LE 读取 KCP little-endian conv/len。
[[nodiscard]] std::uint32_t ReadUint32LE(
    const std::uint8_t* input) noexcept {
    return
        static_cast<std::uint32_t>(input[0]) |
        (static_cast<std::uint32_t>(input[1]) << 8U) |
        (static_cast<std::uint32_t>(input[2]) << 16U) |
        (static_cast<std::uint32_t>(input[3]) << 24U);
}

/// ReadUint16BE 读取 route payload length/flags。
[[nodiscard]] std::uint16_t ReadUint16BE(
    const std::uint8_t* input) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(input[0]) << 8U) |
        static_cast<std::uint16_t>(input[1]));
}

/// ReadUint32BE 读取 route message id。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::uint8_t* input) noexcept {
    return
        (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// ReadUint64BE 读取 route application sequence。
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

/// WriteUint16BE 写入 route payload length。
void WriteUint16BE(
    std::uint8_t* output,
    const std::uint16_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 8U);
    output[1] = static_cast<std::uint8_t>(value);
}

/// WriteUint32BE 写入 route message id。
void WriteUint32BE(
    std::uint8_t* output,
    const std::uint32_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// WriteUint64BE 写入 route application sequence。
void WriteUint64BE(
    std::uint8_t* output,
    const std::uint64_t value) noexcept {
    for (std::size_t index = 0; index < 8; ++index) {
        output[7 - index] = static_cast<std::uint8_t>(
            value >> (index * 8U));
    }
}

/// PayloadTick 保存typed KCP payload的closed parse结果。
struct PayloadTick final {
    /// valid 表示message type与必要identity均合法。
    bool valid;
    /// tick 是input/server tick；无独立tick时使用相关server tick。
    std::uint64_t tick;
};

/// ParsePayload 验证KCP route对应的lite Protobuf outer contract。
[[nodiscard]] PayloadTick ParsePayload(
    const std::uint32_t message_id,
    const std::span<const std::uint8_t> payload) {
    const auto parse = [&](auto& message) {
        return message.ParseFromArray(
            payload.data(),
            static_cast<int>(payload.size()));
    };
    switch (message_id) {
        case 3004: {
            ihomeland::battle::v1::
                BattleAbilityReliableEvent message;
            return {
                .valid =
                    parse(message) &&
                    message.event_id() != 0 &&
                    message.server_tick() != 0 &&
                    message.source_entity_id() != 0 &&
                    message.source_entity_generation() != 0 &&
                    message.ability_id() != 0 &&
                    message.phase() !=
                        ihomeland::battle::v1::
                            BATTLE_ABILITY_PHASE_UNSPECIFIED,
                .tick = message.server_tick(),
            };
        }
        case 3005: {
            ihomeland::battle::v1::
                BattleEntityLifecycle message;
            return {
                .valid =
                    parse(message) &&
                    message.event_id() != 0 &&
                    message.server_tick() != 0 &&
                    message.entity_id() != 0 &&
                    message.entity_generation() != 0 &&
                    message.kind() !=
                        ihomeland::battle::v1::
                            BATTLE_ENTITY_LIFECYCLE_KIND_UNSPECIFIED,
                .tick = message.server_tick(),
            };
        }
        case 3006: {
            ihomeland::battle::v1::
                BattleResyncRequest message;
            return {
                .valid =
                    parse(message) &&
                    message.request_sequence() != 0 &&
                    message.latest_server_tick() != 0 &&
                    message.reason() !=
                        ihomeland::battle::v1::
                            BATTLE_RESYNC_REASON_UNSPECIFIED,
                .tick = message.latest_server_tick(),
            };
        }
        case 3007: {
            ihomeland::battle::v1::
                BattleResyncResponse message;
            return {
                .valid =
                    parse(message) &&
                    message.request_sequence() != 0 &&
                    message.server_tick() != 0 &&
                    message.disposition() !=
                        ihomeland::battle::v1::
                            BATTLE_RESYNC_DISPOSITION_UNSPECIFIED,
                .tick = message.server_tick(),
            };
        }
        default:
            return {};
    }
}

/// OutboundDirection 将local transport role映射到application发送方向。
[[nodiscard]] BattleRouteDirection OutboundDirection(
    const BattleTransportRole role) noexcept {
    return role == BattleTransportRole::Server ?
        BattleRouteDirection::ServerToClient :
        BattleRouteDirection::ClientToServer;
}

/// InboundDirection 将local transport role映射到application接收方向。
[[nodiscard]] BattleRouteDirection InboundDirection(
    const BattleTransportRole role) noexcept {
    return role == BattleTransportRole::Server ?
        BattleRouteDirection::ClientToServer :
        BattleRouteDirection::ServerToClient;
}

}  // namespace

/// BattleKcpAdapter::Impl 隔离KCP 2.1.1 handle与有界application状态。
struct BattleKcpAdapter::Impl final {
    /// PendingMessage 是尚未交给KCP的immutable route frame。
    struct PendingMessage final {
        /// frame 是16-byte route envelope加Protobuf。
        std::vector<std::uint8_t> frame;
        /// deadline_unix_ms 是 immutable route policy 推导的绝对终点。
        std::uint64_t deadline_unix_ms;
    };

    /// ReceivedMessage 是解锁后交给application handler的owned frame。
    struct ReceivedMessage final {
        /// policy 是immutable static registry projection。
        const BattleKcpRoutePolicy* policy;
        /// application_sequence 是validated route sequence。
        std::uint64_t application_sequence;
        /// application_tick 是typed payload tick。
        std::uint64_t application_tick;
        /// payload 是owned Protobuf bytes。
        std::vector<std::uint8_t> payload;
    };

    /// SeenSegment 保存固定 window 内已输出 PUSH sequence。
    struct SeenSegment final {
        /// sequence 是 KCP segment sequence。
        std::uint32_t sequence{};
        /// used 表示 slot 已观察至少一次。
        bool used{};
    };

    /// 构造函数创建exact conv handle并锁定profile参数。
    Impl(
        const std::uint32_t value,
        const BattleTransportRole role,
        SegmentOutput output,
        MessageHandler handler,
        BattleRuntimeMetrics* metrics)
        : conversation(value),
          local_role(role),
          segment_output(std::move(output)),
          message_handler(std::move(handler)),
          runtime_metrics(metrics) {
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
                "battle KCP profile initialization failed");
        }
        kcp->rx_minrto =
            static_cast<IINT32>(MinimumRtoMilliseconds);
        kcp->rx_rto =
            static_cast<IINT32>(MaximumRtoMilliseconds);
        kcp->dead_link = DeadLinkRetransmits;
        kcp->snd_wnd = WindowSegments;
        kcp->rcv_wnd = WindowSegments;
        kcp->stream = 0;
        ikcp_setoutput(kcp, &OutputBridge);
    }

    /// 析构函数释放第三方handle。
    ~Impl() {
        if (kcp != nullptr) {
            ikcp_release(kcp);
        }
    }

    /// OutputBridge 只复制有界KCP bytes，禁止异常跨越C ABI。
    static int OutputBridge(
        const char* buffer,
        const int length,
        ikcpcb*,
        void* user) noexcept {
        auto* self = static_cast<Impl*>(user);
        if (self == nullptr || buffer == nullptr ||
            length <= 0 ||
            static_cast<std::size_t>(length) >
                KcpHeaderBytes +
                    MaximumSegmentPayloadBytes) {
            if (self != nullptr) {
                self->closed = true;
            }
            return -1;
        }
        try {
            const auto* first =
                reinterpret_cast<const std::uint8_t*>(
                    buffer);
            std::size_t offset = 0;
            while (offset + KcpHeaderBytes <=
                   static_cast<std::size_t>(length)) {
                const auto* header = first + offset;
                const auto payload_bytes =
                    ReadUint32LE(header + 20);
                const auto remaining_bytes =
                    static_cast<std::size_t>(length) -
                    offset -
                    KcpHeaderBytes;
                if (payload_bytes > remaining_bytes) {
                    self->closed = true;
                    return -1;
                }
                if (header[4] == KcpPush) {
                    const auto sequence =
                        ReadUint32LE(header + 12);
                    auto& slot = self->seen_segments.at(
                        sequence % WindowSegments);
                    if (slot.used &&
                        slot.sequence == sequence &&
                        self->runtime_metrics != nullptr) {
                        self->runtime_metrics->
                            RecordKcpRetransmits(1);
                    }
                    slot = SeenSegment{
                        .sequence = sequence,
                        .used = true,
                    };
                }
                offset += KcpHeaderBytes +
                          payload_bytes;
            }
            if (offset != static_cast<std::size_t>(length)) {
                self->closed = true;
                return -1;
            }
            self->pending_outputs.emplace_back(
                first,
                first + length);
            return 0;
        } catch (...) {
            self->closed = true;
            return -1;
        }
    }

    /// ClampRto 强制第三方adaptive与segment timer保持30..200ms。
    void ClampRto() noexcept {
        kcp->rx_minrto =
            static_cast<IINT32>(MinimumRtoMilliseconds);
        kcp->rx_rto = std::clamp(
            kcp->rx_rto,
            static_cast<IINT32>(MinimumRtoMilliseconds),
            static_cast<IINT32>(MaximumRtoMilliseconds));
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
        }
    }

    /// ParseReceived 在reassembly后重新验证route、direction、size与sequence。
    [[nodiscard]] std::optional<ReceivedMessage>
    ParseReceived(
        const std::span<const std::uint8_t> frame) {
        if (frame.size() <= RouteEnvelopeBytes ||
            frame.size() > MaximumMessageBytes) {
            return std::nullopt;
        }
        const auto message_id =
            ReadUint32BE(frame.data());
        const auto payload_length =
            ReadUint16BE(frame.data() + 4);
        const auto route_flags =
            ReadUint16BE(frame.data() + 6);
        const auto sequence =
            ReadUint64BE(frame.data() + 8);
        const auto* policy =
            FindBattleKcpRoutePolicy(message_id);
        if (policy == nullptr ||
            policy->direction !=
                InboundDirection(local_role) ||
            payload_length == 0 ||
            payload_length >
                policy->maximum_payload_bytes ||
            static_cast<std::size_t>(payload_length) +
                    RouteEnvelopeBytes !=
                frame.size() ||
            route_flags != 0 ||
            sequence == 0 ||
            sequence <= last_received_sequence) {
            return std::nullopt;
        }
        const auto payload =
            frame.subspan(RouteEnvelopeBytes);
        const auto typed =
            ParsePayload(message_id, payload);
        if (!typed.valid) {
            return std::nullopt;
        }
        last_received_sequence = sequence;
        return ReceivedMessage{
            .policy = policy,
            .application_sequence = sequence,
            .application_tick = typed.tick,
            .payload = std::vector<std::uint8_t>(
                payload.begin(),
                payload.end()),
        };
    }

    /// conversation 是session-derived nonzero KCP conv。
    std::uint32_t conversation;
    /// local_role 冻结application方向。
    BattleTransportRole local_role;
    /// segment_output 是secure KCP lane唯一出口。
    SegmentOutput segment_output;
    /// message_handler 是validated reliable route唯一入口。
    MessageHandler message_handler;
    /// runtime_metrics 可选借用 node 生命周期内的低敏累计 owner。
    BattleRuntimeMetrics* runtime_metrics;
    /// kcp 是私有第三方handle。
    ikcpcb* kcp{};
    /// queued_messages 在固定64项预算内等待send。
    std::deque<PendingMessage> queued_messages;
    /// inflight_deadlines 对应one-message-one-segment send顺序。
    std::deque<std::uint64_t> inflight_deadlines;
    /// pending_outputs 暂存当前update生成的有界segments。
    std::vector<std::vector<std::uint8_t>>
        pending_outputs;
    /// last_update_unix_ms 检测clock rollback并执行10ms cadence。
    std::uint64_t last_update_unix_ms{};
    /// clock_origin_unix_ms 把64-bit absolute deadline clock映射为KCP 32-bit relative clock。
    std::uint64_t clock_origin_unix_ms{};
    /// last_received_sequence 阻止可靠route replay/回退。
    std::uint64_t last_received_sequence{};
    /// expired_messages 是低敏累计终结计数。
    std::uint64_t expired_messages{};
    /// emitted_segments 是低敏累计输出计数。
    std::uint64_t emitted_segments{};
    /// received_messages 是低敏累计application计数。
    std::uint64_t received_messages{};
    /// seen_segments 以 KCP window 固定内存识别首次输出与重传。
    std::array<SeenSegment, WindowSegments> seen_segments{};
    /// closed 是dead-link/deadline/callback failure终态。
    bool closed{false};
    /// mutex 串行化third-party handle与全部queue。
    mutable std::mutex mutex;
};

BattleKcpAdapter::BattleKcpAdapter(
    const std::uint32_t conversation,
    const BattleTransportRole local_role,
    SegmentOutput segment_output,
    MessageHandler message_handler,
    BattleRuntimeMetrics* runtime_metrics)
    : impl_(std::make_unique<Impl>(
          conversation,
          local_role,
          std::move(segment_output),
          std::move(message_handler),
          runtime_metrics)) {
    if (conversation == 0 ||
        !impl_->segment_output ||
        !impl_->message_handler) {
        throw std::invalid_argument(
            "battle KCP binding is invalid");
    }
}

BattleKcpAdapter::~BattleKcpAdapter() = default;

BattleKcpDisposition BattleKcpAdapter::Queue(
    const std::uint32_t message_id,
    const std::uint64_t application_sequence,
    const std::span<const std::uint8_t> payload,
    const std::uint64_t now_unix_ms) {
    const auto* policy =
        FindBattleKcpRoutePolicy(message_id);
    if (policy == nullptr ||
        policy->direction !=
            OutboundDirection(impl_->local_role) ||
        application_sequence == 0 ||
        payload.empty() ||
        payload.size() > policy->maximum_payload_bytes ||
        payload.size() + RouteEnvelopeBytes >
            MaximumMessageBytes ||
        now_unix_ms == 0 ||
        now_unix_ms >
            std::numeric_limits<std::uint64_t>::max() -
                policy->expiry_milliseconds ||
        !ParsePayload(message_id, payload).valid) {
        if (impl_->runtime_metrics != nullptr) {
            impl_->runtime_metrics->RecordReject();
        }
        return BattleKcpDisposition::RouteRejected;
    }
    std::scoped_lock lock(impl_->mutex);
    if (impl_->closed) {
        return BattleKcpDisposition::Closed;
    }
    if (impl_->queued_messages.size() +
            impl_->inflight_deadlines.size() >=
        QueueItems) {
        if (impl_->runtime_metrics != nullptr) {
            impl_->runtime_metrics->RecordReject();
        }
        return BattleKcpDisposition::QueueFull;
    }
    std::vector<std::uint8_t> frame(
        RouteEnvelopeBytes + payload.size());
    WriteUint32BE(frame.data(), message_id);
    WriteUint16BE(
        frame.data() + 4,
        static_cast<std::uint16_t>(payload.size()));
    frame[6] = 0;
    frame[7] = 0;
    WriteUint64BE(
        frame.data() + 8,
        application_sequence);
    std::ranges::copy(
        payload,
        frame.begin() + RouteEnvelopeBytes);
    impl_->queued_messages.push_back(
        Impl::PendingMessage{
            .frame = std::move(frame),
            .deadline_unix_ms =
                now_unix_ms +
                policy->expiry_milliseconds,
        });
    if (impl_->runtime_metrics != nullptr) {
        impl_->runtime_metrics->ObserveKcpQueue(
            impl_->queued_messages.size() +
            impl_->inflight_deadlines.size());
    }
    return BattleKcpDisposition::Queued;
}

bool BattleKcpAdapter::ValidateSegment(
    const std::span<const std::uint8_t> segment,
    const std::uint32_t expected_conversation) noexcept {
    if (expected_conversation == 0 ||
        segment.empty() ||
        segment.size() >
            KcpHeaderBytes +
                MaximumSegmentPayloadBytes) {
        return false;
    }
    std::size_t offset = 0;
    while (offset < segment.size()) {
        if (segment.size() - offset < KcpHeaderBytes) {
            return false;
        }
        const auto* header = segment.data() + offset;
        const auto command = header[4];
        const auto payload_length =
            ReadUint32LE(header + 20);
        if (ReadUint32LE(header) !=
                expected_conversation ||
            (command != KcpPush &&
             command != KcpAck &&
             command != KcpWindowAsk &&
             command != KcpWindowTell) ||
            ReadUint16LE(header + 6) >
                WindowSegments ||
            payload_length >
                MaximumSegmentPayloadBytes ||
            static_cast<std::size_t>(payload_length) >
                segment.size() - offset -
                    KcpHeaderBytes) {
            return false;
        }
        offset +=
            KcpHeaderBytes +
            static_cast<std::size_t>(payload_length);
    }
    return offset == segment.size();
}

BattleKcpDisposition BattleKcpAdapter::Input(
    const std::span<const std::uint8_t> segment,
    const std::uint64_t now_unix_ms) {
    std::vector<Impl::ReceivedMessage> messages;
    BattleKcpDisposition disposition =
        BattleKcpDisposition::Accepted;
    {
        std::unique_lock lock(impl_->mutex);
        if (impl_->closed) {
            return BattleKcpDisposition::Closed;
        }
        if (now_unix_ms == 0 ||
            (impl_->last_update_unix_ms != 0 &&
             now_unix_ms < impl_->last_update_unix_ms)) {
            return BattleKcpDisposition::ClockInvalid;
        }
        if (!ValidateSegment(
                segment,
                impl_->conversation)) {
            if (impl_->runtime_metrics != nullptr) {
                impl_->runtime_metrics->RecordReject();
            }
            return BattleKcpDisposition::InvalidSegment;
        }
        if (ikcp_input(
                impl_->kcp,
                reinterpret_cast<const char*>(
                    segment.data()),
                static_cast<long>(segment.size())) < 0) {
            if (impl_->runtime_metrics != nullptr) {
                impl_->runtime_metrics->RecordReject();
            }
            return BattleKcpDisposition::InvalidSegment;
        }
        impl_->ClampRto();
        impl_->ReconcileInflight();
        for (;;) {
            const auto message_bytes =
                ikcp_peeksize(impl_->kcp);
            if (message_bytes < 0) {
                break;
            }
            if (message_bytes == 0 ||
                static_cast<std::size_t>(message_bytes) >
                    MaximumMessageBytes) {
                impl_->closed = true;
                return BattleKcpDisposition::RouteRejected;
            }
            std::vector<std::uint8_t> frame(
                static_cast<std::size_t>(
                    message_bytes));
            if (ikcp_recv(
                    impl_->kcp,
                    reinterpret_cast<char*>(
                        frame.data()),
                    message_bytes) != message_bytes) {
                impl_->closed = true;
                return BattleKcpDisposition::Closed;
            }
            auto parsed = impl_->ParseReceived(frame);
            if (!parsed.has_value()) {
                impl_->closed = true;
                if (impl_->runtime_metrics != nullptr) {
                    impl_->runtime_metrics->RecordReject();
                }
                return BattleKcpDisposition::RouteRejected;
            }
            messages.push_back(std::move(*parsed));
        }
    }
    for (const auto& message : messages) {
        try {
            impl_->message_handler(
                BattleKcpMessageView{
                    .policy = message.policy,
                    .application_sequence =
                        message.application_sequence,
                    .application_tick =
                        message.application_tick,
                    .payload = message.payload,
                });
            std::scoped_lock lock(impl_->mutex);
            ++impl_->received_messages;
            if (impl_->runtime_metrics != nullptr) {
                impl_->runtime_metrics->ObserveKcpQueue(
                    impl_->queued_messages.size() +
                    impl_->inflight_deadlines.size() +
                    static_cast<std::size_t>(std::max(
                        0,
                        ikcp_waitsnd(impl_->kcp))));
            }
        } catch (...) {
            std::scoped_lock lock(impl_->mutex);
            impl_->closed = true;
            return BattleKcpDisposition::Closed;
        }
    }
    return disposition;
}

BattleKcpDisposition BattleKcpAdapter::Update(
    const std::uint64_t now_unix_ms) {
    std::vector<std::vector<std::uint8_t>> outputs;
    BattleKcpDisposition disposition =
        BattleKcpDisposition::Accepted;
    {
        std::unique_lock lock(impl_->mutex);
        if (impl_->closed) {
            return BattleKcpDisposition::Closed;
        }
        if (now_unix_ms == 0 ||
            (impl_->last_update_unix_ms != 0 &&
             now_unix_ms < impl_->last_update_unix_ms)) {
            return BattleKcpDisposition::ClockInvalid;
        }
        if (impl_->clock_origin_unix_ms == 0) {
            impl_->clock_origin_unix_ms =
                now_unix_ms;
        }
        const auto relative_now =
            now_unix_ms -
            impl_->clock_origin_unix_ms;
        if (relative_now >
            std::numeric_limits<std::uint32_t>::max()) {
            impl_->closed = true;
            return BattleKcpDisposition::ClockInvalid;
        }
        if (std::ranges::any_of(
                impl_->inflight_deadlines,
                [now_unix_ms](const std::uint64_t deadline) {
                    return now_unix_ms >= deadline;
                }) &&
            ikcp_waitsnd(impl_->kcp) > 0) {
            ++impl_->expired_messages;
            if (impl_->runtime_metrics != nullptr) {
                impl_->runtime_metrics->RecordExpiry();
            }
            impl_->closed = true;
            return BattleKcpDisposition::Expired;
        }
        const auto queued_before_expiry =
            impl_->queued_messages.size();
        std::erase_if(
            impl_->queued_messages,
            [now_unix_ms](const Impl::PendingMessage& message) {
                return now_unix_ms >=
                    message.deadline_unix_ms;
            });
        const auto expired_queued =
            queued_before_expiry -
            impl_->queued_messages.size();
        if (expired_queued != 0) {
            impl_->expired_messages += expired_queued;
            if (impl_->runtime_metrics != nullptr) {
                impl_->runtime_metrics->RecordExpiry(
                    expired_queued);
            }
            disposition = BattleKcpDisposition::Expired;
        }
        while (!impl_->queued_messages.empty()) {
            auto& message =
                impl_->queued_messages.front();
            if (ikcp_send(
                    impl_->kcp,
                    reinterpret_cast<const char*>(
                        message.frame.data()),
                    static_cast<int>(
                        message.frame.size())) < 0) {
                impl_->closed = true;
                return BattleKcpDisposition::Closed;
            }
            impl_->inflight_deadlines.push_back(
                message.deadline_unix_ms);
            impl_->queued_messages.pop_front();
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
        if (impl_->kcp->state ==
            std::numeric_limits<IUINT32>::max()) {
            impl_->closed = true;
            return BattleKcpDisposition::Closed;
        }
        outputs = std::move(impl_->pending_outputs);
        impl_->pending_outputs.clear();
        if (impl_->runtime_metrics != nullptr) {
            impl_->runtime_metrics->ObserveKcpQueue(
                impl_->queued_messages.size() +
                impl_->inflight_deadlines.size() +
                static_cast<std::size_t>(std::max(
                    0,
                    ikcp_waitsnd(impl_->kcp))));
        }
    }
    for (const auto& output : outputs) {
        try {
            impl_->segment_output(output);
            std::scoped_lock lock(impl_->mutex);
            ++impl_->emitted_segments;
        } catch (...) {
            std::scoped_lock lock(impl_->mutex);
            impl_->closed = true;
            return BattleKcpDisposition::Closed;
        }
    }
    return disposition;
}

BattleKcpStatus BattleKcpAdapter::Status() const {
    std::scoped_lock lock(impl_->mutex);
    return {
        .closed = impl_->closed,
        .queued_messages =
            impl_->queued_messages.size(),
        .inflight_messages =
            impl_->inflight_deadlines.size(),
        .waiting_segments =
            static_cast<std::size_t>(
                std::max(
                    0,
                    ikcp_waitsnd(impl_->kcp))),
        .expired_messages =
            impl_->expired_messages,
        .emitted_segments =
            impl_->emitted_segments,
        .received_messages =
            impl_->received_messages,
    };
}

}  // namespace ihomeland::sim
