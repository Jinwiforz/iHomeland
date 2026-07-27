#include "ihomeland/sim/transport/battle_transport_runtime.hpp"

#include "ihomeland/sim/observability/battle_runtime_metrics.hpp"
#include "ihomeland/sim/transport/authenticated_multiplexer.hpp"
#include "ihomeland/sim/transport/battle_transport_control.hpp"
#include "ihomeland/sim/transport/resource_governor.hpp"
#include "ihomeland/sim/transport/secure_datagram.hpp"

#include <algorithm>
#include <array>
#include <chrono>
#include <condition_variable>
#include <deque>
#include <limits>
#include <mutex>
#include <optional>
#include <ranges>
#include <stdexcept>
#include <thread>
#include <utility>
#include <vector>

namespace ihomeland::sim {
namespace {

constexpr std::array<std::uint8_t, 4> ClientHelloMagic{
    'I', 'H', 'B', 'H'};
constexpr std::array<std::uint8_t, 4> ClientAuthMagic{
    'I', 'H', 'B', 'A'};
constexpr std::array<std::uint8_t, 4> SecureMagic{
    'I', 'H', 'B', 'T'};
constexpr std::size_t SessionDigestBytes = 8;
constexpr std::size_t BindingDiscriminatorBytes = 8;
constexpr std::size_t SecureSessionDigestOffset = 8;
constexpr std::size_t SecureSessionGenerationOffset = 16;
constexpr std::size_t MaximumPendingRuntimeDatagrams = 256;
constexpr auto KcpUpdateInterval =
    std::chrono::milliseconds(10);

/// ObservedUnixMilliseconds 返回 periodic KCP worker 使用的 UTC 毫秒。
[[nodiscard]] std::uint64_t
ObservedUnixMilliseconds() {
    const auto observed =
        std::chrono::duration_cast<
            std::chrono::milliseconds>(
            std::chrono::system_clock::now()
                .time_since_epoch())
            .count();
    if (observed <= 0) {
        throw std::runtime_error(
            "battle transport runtime clock is invalid");
    }
    return static_cast<std::uint64_t>(
        observed);
}

/// IsAllZero 拒绝未配置 identity 与 digest。
template <std::size_t Size>
[[nodiscard]] bool IsAllZero(
    const std::array<std::uint8_t, Size>& value) noexcept {
    return std::ranges::all_of(
        value,
        [](const std::uint8_t item) {
            return item == 0;
        });
}

/// ReadUint32BE 解码 secure header generation。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::uint8_t* input) noexcept {
    return
        (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// ReadUint64BE 把 8-byte digest 映射为非零低敏 runtime handle。
[[nodiscard]] std::uint64_t ReadUint64BE(
    const std::array<std::uint8_t, 8>& input) noexcept {
    std::uint64_t output = 0;
    for (const auto value : input) {
        output = (output << 8U) | value;
    }
    return output;
}

/// DatagramKind 对 listener 已 fast-gated 的 magic 做 closed 分类。
enum class DatagramKind : std::uint8_t {
    /// ClientHello 是 cookie-first pre-auth request。
    ClientHello = 1,
    /// ClientAuth 是 cookie 后 authenticated handshake。
    ClientAuth = 2,
    /// Secure 是 active session raw/KCP/control packet。
    Secure = 3,
    /// Invalid 表示 magic 或最小 header 漂移。
    Invalid = 4,
};

/// ClassifyDatagram 不读取 payload 或执行动态 lookup。
[[nodiscard]] DatagramKind ClassifyDatagram(
    const std::span<const std::uint8_t> datagram) noexcept {
    if (datagram.size() < 8) {
        return DatagramKind::Invalid;
    }
    const auto magic = datagram.first<4>();
    if (std::ranges::equal(ClientHelloMagic, magic)) {
        return DatagramKind::ClientHello;
    }
    if (std::ranges::equal(ClientAuthMagic, magic)) {
        return DatagramKind::ClientAuth;
    }
    if (std::ranges::equal(SecureMagic, magic)) {
        return DatagramKind::Secure;
    }
    return DatagramKind::Invalid;
}

/// SessionRouteKey 是 secure header 的 node-local lookup identity。
struct SessionRouteKey final {
    /// session_id_digest 是 ServerAccept session ID 的 SHA-256 前 8 bytes。
    std::array<std::uint8_t, SessionDigestBytes>
        session_id_digest{};
    /// generation 防止 predecessor session lookup 命中 successor。
    std::uint32_t generation{};

    /// operator== 执行完整 route identity 比较。
    [[nodiscard]] bool operator==(
        const SessionRouteKey&) const noexcept = default;
};

/// ParseSessionRouteKey 只投影 secure header 的公开 routing fields。
[[nodiscard]] std::optional<SessionRouteKey>
ParseSessionRouteKey(
    const std::span<const std::uint8_t> datagram) {
    if (datagram.size() <
        BattleSecureChannel::SecureHeaderBytes +
            BattleSecureChannel::AeadTagBytes +
            1) {
        return std::nullopt;
    }
    SessionRouteKey key{};
    std::ranges::copy_n(
        datagram.begin() + SecureSessionDigestOffset,
        key.session_id_digest.size(),
        key.session_id_digest.begin());
    key.generation = ReadUint32BE(
        datagram.data() +
        SecureSessionGenerationOffset);
    if (IsAllZero(key.session_id_digest) ||
        key.generation == 0) {
        return std::nullopt;
    }
    return key;
}

/// CloseReasonCategory 将 session authority 原因归入 closed 低敏指标维度。
[[nodiscard]] BattleCloseReasonCategory
CloseReasonCategory(
    const BattleSessionInvalidationReason reason) noexcept {
    switch (reason) {
        case BattleSessionInvalidationReason::Protocol:
            return BattleCloseReasonCategory::
                Authentication;
        case BattleSessionInvalidationReason::Backpressure:
            return BattleCloseReasonCategory::Resource;
        case BattleSessionInvalidationReason::Listener:
            return BattleCloseReasonCategory::Transport;
        case BattleSessionInvalidationReason::AccountSession:
        case BattleSessionInvalidationReason::Membership:
        case BattleSessionInvalidationReason::Assignment:
        case BattleSessionInvalidationReason::Target:
        case BattleSessionInvalidationReason::Instance:
        case BattleSessionInvalidationReason::Node:
            return BattleCloseReasonCategory::Lifecycle;
        case BattleSessionInvalidationReason::None:
        default:
            return BattleCloseReasonCategory::Internal;
    }
}

}  // namespace

std::optional<BattleSnapshotPublication>
BattleSnapshotCadence::Next(
    const std::uint64_t server_tick,
    const bool force_full) noexcept {
    if (terminal_) {
        return std::nullopt;
    }
    if (server_tick == 0 ||
        server_tick < last_server_tick_ ||
        snapshot_sequence_ ==
        std::numeric_limits<std::uint64_t>::max()) {
        terminal_ = true;
        return std::nullopt;
    }
    if (recovery_deadline_tick_ != 0 &&
        server_tick > recovery_deadline_tick_) {
        next_recovery_full_tick_ = 0;
        recovery_deadline_tick_ = 0;
    }
    const bool recovery_full =
        next_recovery_full_tick_ != 0 &&
        server_tick >= next_recovery_full_tick_;
    if (!force_full &&
        last_server_tick_ != 0 &&
        server_tick - last_server_tick_ <
            SnapshotIntervalTicks) {
        return std::nullopt;
    }
    ++snapshot_sequence_;
    last_server_tick_ = server_tick;
    const bool full =
        force_full ||
        recovery_full ||
        baseline_id_ == 0 ||
        snapshot_sequence_ - baseline_id_ >=
            FullBaselineIntervalSnapshots;
    if (full) {
        baseline_id_ = snapshot_sequence_;
        if (recovery_deadline_tick_ != 0) {
            if (server_tick <=
                    std::numeric_limits<
                        std::uint64_t>::max() -
                        RecoveryFullIntervalTicks &&
                server_tick +
                        RecoveryFullIntervalTicks <=
                    recovery_deadline_tick_) {
                next_recovery_full_tick_ =
                    server_tick +
                    RecoveryFullIntervalTicks;
            } else {
                next_recovery_full_tick_ = 0;
            }
        }
    }
    return BattleSnapshotPublication{
        .snapshot_sequence = snapshot_sequence_,
        .baseline_id = baseline_id_,
        .full = full,
    };
}

bool BattleSnapshotCadence::BeginRecovery(
    const std::uint64_t server_tick) noexcept {
    if (terminal_ ||
        server_tick == 0 ||
        server_tick != last_server_tick_ ||
        server_tick >
            std::numeric_limits<std::uint64_t>::max() -
                RecoveryWindowTicks) {
        terminal_ = true;
        return false;
    }
    if (recovery_deadline_tick_ == 0 ||
        server_tick > recovery_deadline_tick_) {
        recovery_deadline_tick_ =
            server_tick + RecoveryWindowTicks;
    }
    if (server_tick <=
            std::numeric_limits<std::uint64_t>::max() -
                RecoveryFullIntervalTicks &&
        server_tick + RecoveryFullIntervalTicks <=
            recovery_deadline_tick_) {
        next_recovery_full_tick_ =
            server_tick + RecoveryFullIntervalTicks;
    } else {
        next_recovery_full_tick_ = 0;
    }
    return true;
}

bool BattleSnapshotCadence::Terminal() const noexcept {
    return terminal_;
}

/// BattleTransportRuntime::Impl 隔离 node-global crypto 与固定 active session registry。
struct BattleTransportRuntime::Impl final {
    /// QueuedDatagram 保存 listener callback 之后的固定 MTU owned copy。
    struct QueuedDatagram final {
        /// bytes 保存 exact UDP datagram，不保留 listener span。
        std::array<
            std::uint8_t,
            BattleUdpListener::MaximumDatagramBytes> bytes{};
        /// size 是 bytes 中有效的前缀长度。
        std::size_t size{};
        /// remote 是 listener canonical 化后的来源。
        BattleRemoteEndpoint remote{};
        /// observed_unix_ms 是 receive callback 的绝对时间快照。
        std::uint64_t observed_unix_ms{};
    };

    /// Session 组合一个 authenticated actor 的全部 transport owner。
    struct Session final {
        /// 构造函数一次性消费 bootstrap seed 并按依赖顺序建立所有 adapter。
        Session(
            Impl& runtime,
            std::unique_ptr<
                BattleAuthenticatedHandshake::
                    SessionBootstrap> bootstrap_value,
            const std::uint64_t now_unix_ms)
            : owner(runtime),
              bootstrap_metadata(
                  std::move(bootstrap_value)) {
            if (bootstrap_metadata == nullptr) {
                throw std::invalid_argument(
                    "battle session bootstrap is missing");
            }
            operation_now_unix_ms = now_unix_ms;
            const auto session_digest =
                owner.crypto.Sha256(
                    bootstrap_metadata->SessionId());
            std::ranges::copy_n(
                session_digest.begin(),
                route_key.session_id_digest.size(),
                route_key.session_id_digest.begin());
            route_key.generation =
                bootstrap_metadata->
                    BattleSessionGeneration();
            std::ranges::copy_n(
                bootstrap_metadata->
                    BindingFingerprint().begin(),
                binding_fingerprint.size(),
                binding_fingerprint.begin());
            const auto session_handle =
                ReadUint64BE(
                    route_key.session_id_digest);
            const auto& binding =
                bootstrap_metadata->Binding();
            const auto instance_digest =
                owner.crypto.Sha256(
                    std::span(
                        reinterpret_cast<
                            const std::uint8_t*>(
                            binding
                                .simulation_instance_id
                                .data()),
                        binding
                            .simulation_instance_id
                            .size()));
            std::array<std::uint8_t, 8>
                instance_handle_bytes{};
            std::ranges::copy_n(
                instance_digest.begin(),
                instance_handle_bytes.size(),
                instance_handle_bytes.begin());
            const auto instance_handle =
                ReadUint64BE(
                    instance_handle_bytes);
            authority =
                std::make_shared<
                    BattleSessionAuthority>();
            context =
                std::make_unique<BattleSessionContext>(
                    binding.session_id,
                    binding.session_epoch,
                    session_handle,
                    route_key.generation,
                    bootstrap_metadata->
                        EndpointGeneration(),
                    binding.assignment_fingerprint,
                    binding.simulation_instance_id,
                    binding.mapping_generation,
                    binding.target_revision,
                    BattleActorBinding{
                        .player_id = binding.player_id,
                        .role =
                            bootstrap_metadata->Role() == 1
                            ? BattleActorRole::Owner
                            : BattleActorRole::Visitor,
                        .actor_id =
                            static_cast<std::uint64_t>(
                                binding.actor_slot) +
                            1,
                        .actor_slot =
                            binding.actor_slot,
                    },
                    authority);
            auto* command_ingress =
                owner.command_ingress_provider(
                    *context);
            if (command_ingress == nullptr ||
                instance_handle == 0) {
                throw std::runtime_error(
                    "battle session instance ingress is stale");
            }
            session_resources =
                std::make_unique<
                    BattleSessionResourceGovernor>(
                    owner.resources,
                    session_handle,
                    instance_handle);
            input_ingress =
                std::make_unique<BattleInputIngress>(
                    *context,
                    *command_ingress,
                    *session_resources);
            bootstrap_metadata->ConsumeSessionSeed(
                [&](const auto& session_seed) {
                    channel =
                        std::make_unique<
                            BattleSecureChannel>(
                            owner.crypto,
                            session_seed,
                            BattleTransportRole::Server,
                            BattleSecureIdentity{
                                .session_id_digest =
                                    route_key
                                        .session_id_digest,
                                .battle_session_generation =
                                    route_key.generation,
                                .endpoint_generation =
                                    bootstrap_metadata->
                                        EndpointGeneration(),
                                .binding_discriminator =
                                    BindingDiscriminator(),
                            },
                            bootstrap_metadata->KeyEpoch(),
                            1,
                            now_unix_ms,
                            0,
                            owner.runtime_metrics);
                });
            control =
                std::make_unique<
                    BattleTransportControl>(
                    owner.crypto,
                    *channel,
                    bootstrap_metadata->Remote(),
                    bootstrap_metadata->
                        EndpointGeneration(),
                    owner.sender,
                    owner.runtime_metrics);
            raw_dispatcher =
                std::make_unique<BattleRawDispatcher>(
                    BattleRouteDirection::
                        ClientToServer,
                    [this](
                        const BattleRawFrameView& frame) {
                        const auto result =
                            input_ingress->Handle(
                                frame,
                                operation_now_unix_ms);
                        if (result.disposition ==
                                BattleIngressDisposition::
                                    Backpressure ||
                            result.disposition ==
                                BattleIngressDisposition::
                                    AuthorityRejected) {
                            authority->Invalidate(
                                result.disposition ==
                                        BattleIngressDisposition::
                                            Backpressure
                                    ? BattleSessionInvalidationReason::
                                          Backpressure
                                    : BattleSessionInvalidationReason::
                                          Assignment);
                        }
                    });
            const auto conversation =
                KcpConversation();
            kcp =
                std::make_unique<BattleKcpAdapter>(
                    conversation,
                    BattleTransportRole::Server,
                    [this](
                        const std::span<const std::uint8_t>
                            segment) {
                        const auto sealed =
                            channel->Seal(
                                BattlePacketKind::Kcp,
                                segment,
                                operation_now_unix_ms);
                        if (sealed.disposition !=
                            BattleSealDisposition::Sealed ||
                            owner.sender(
                                sealed.datagram,
                                control->
                                    ActiveEndpoint()) !=
                                BattleUdpSendDisposition::
                                    Queued) {
                            authority->Invalidate(
                                BattleSessionInvalidationReason::
                                    Protocol);
                        }
                    },
                    [this](
                        const BattleKcpMessageView& message) {
                        QueueResyncResponse(message);
                    },
                    owner.runtime_metrics);
            replication =
                std::make_unique<
                    BattleReplicationQueue>(
                    *context,
                    *session_resources);
            multiplexer =
                std::make_unique<
                    BattleAuthenticatedMultiplexer>(
                    *channel,
                    [this] {
                        return control->
                            ActiveEndpoint();
                    },
                    *raw_dispatcher,
                    [this] {
                        return authority->Active() &&
                            owner.authority_validator(
                                *context);
                    },
                    [this](
                        const std::span<
                            const std::uint8_t>
                            plaintext,
                        const BattleRemoteEndpoint&
                            remote,
                        const std::uint64_t
                            now_unix_ms) {
                        const auto result =
                            control->Handle(
                                plaintext,
                                remote,
                                now_unix_ms);
                        if (result.close_session) {
                            if (result.disposition ==
                                BattleTransportControlDisposition::
                                    CloseRequested) {
                                close_reason =
                                    BattleCloseReasonCategory::
                                        Normal;
                            }
                            authority->Invalidate(
                                BattleSessionInvalidationReason::
                                    Protocol);
                            return BattleControlDispatchDisposition::
                                CloseRequested;
                        }
                        return BattleControlDispatchDisposition::
                            Accepted;
                    },
                    kcp.get(),
                    owner.runtime_metrics);
            bootstrap_metadata.reset();
        }

        /// BindingDiscriminator 截取完整 fingerprint 的前 8 bytes。
        [[nodiscard]] std::array<
            std::uint8_t,
            BindingDiscriminatorBytes>
        BindingDiscriminator() const noexcept {
            std::array<
                std::uint8_t,
                BindingDiscriminatorBytes> output{};
            std::ranges::copy_n(
                binding_fingerprint.begin(),
                output.size(),
                output.begin());
            return output;
        }

        /// KcpConversation 从非零 64-bit route handle 选择稳定非零 32-bit conv。
        [[nodiscard]] std::uint32_t
        KcpConversation() const noexcept {
            const auto handle =
                ReadUint64BE(
                    route_key.session_id_digest);
            const auto lower =
                static_cast<std::uint32_t>(handle);
            return lower != 0
                ? lower
                : static_cast<std::uint32_t>(
                      handle >> 32U);
        }

        /// HandleSecure 验证 authority/AEAD/route 并同步推进 KCP output。
        [[nodiscard]] BattleMultiplexerResult
        HandleSecure(
            const std::span<const std::uint8_t> datagram,
            const BattleRemoteEndpoint& remote,
            const std::uint64_t now_unix_ms) {
            AdvanceOperationClock(now_unix_ms);
            auto raw_context =
                owner.raw_context_provider(
                    *context,
                    operation_now_unix_ms);
            const auto result = multiplexer->Handle(
                datagram,
                remote,
                raw_context,
                operation_now_unix_ms);
            FlushReplication();
            return result;
        }

        /// AdvanceOperationClock 把排队接收时刻归一为 session 单调 high-watermark。
        ///
        /// listener receive timestamp 可能早于 worker 已执行的 periodic KCP update；
        /// 该排队关系不是系统时钟回退，不得把合法 secure packet 误判为 ClockInvalid。
        void AdvanceOperationClock(
            const std::uint64_t candidate_unix_ms) noexcept {
            operation_now_unix_ms = std::max(
                operation_now_unix_ms,
                candidate_unix_ms);
        }

        /// QueueCurrentProjection 从 simulation owner 冻结 snapshot，不在 transport 生成状态。
        void QueueCurrentProjection(
            const bool force_full) {
            const auto projection =
                owner.replication_projection_provider(
                    *context);
            if (!projection.has_value()) {
                return;
            }
            const auto publication =
                snapshot_cadence.Next(
                    projection->server_tick,
                    force_full);
            if (!publication.has_value()) {
                if (snapshot_cadence.Terminal()) {
                    authority->Invalidate(
                        BattleSessionInvalidationReason::
                            Protocol);
                }
                return;
            }
            QueueProjection(*projection, *publication);
            if (authority->Active() &&
                !snapshot_cadence.BeginRecovery(
                    projection->server_tick)) {
                authority->Invalidate(
                    BattleSessionInvalidationReason::
                        Protocol);
            }
        }

        /// QueueProjection 把单次 cadence 决议映射为 full 或 delta queue item。
        void QueueProjection(
            const BattleReplicationProjection& projection,
            const BattleSnapshotPublication& publication) {
            bool queued = false;
            if (publication.full) {
                queued =
                    replication->QueueFullSnapshot(
                        projection.server_tick,
                        publication.snapshot_sequence,
                        publication.baseline_id,
                        projection.acknowledgement,
                        projection.states,
                        operation_now_unix_ms);
            } else {
                queued =
                    replication->QueueDeltaSnapshot(
                        projection.server_tick,
                        publication.snapshot_sequence,
                        publication.baseline_id,
                        projection.acknowledgement,
                        projection.states,
                        operation_now_unix_ms);
            }
            if (!queued) {
                authority->Invalidate(
                    BattleSessionInvalidationReason::
                        Backpressure);
            }
        }

        /// QueueResyncResponse 安排 KCP receipt 与下一 raw full baseline。
        void QueueResyncResponse(
            const BattleKcpMessageView& message) {
            owner.kcp_handler(*context, message);
            const auto projection =
                owner.replication_projection_provider(
                    *context);
            if (!projection.has_value()) {
                authority->Invalidate(
                    BattleSessionInvalidationReason::
                        Instance);
                return;
            }
            const auto publication =
                snapshot_cadence.Next(
                    projection->server_tick,
                    true);
            if (!publication.has_value()) {
                authority->Invalidate(
                    BattleSessionInvalidationReason::
                        Protocol);
                return;
            }
            if (!replication->QueueResyncResponse(
                    message.application_sequence,
                    projection->server_tick,
                    1,
                    publication->baseline_id,
                    operation_now_unix_ms)) {
                authority->Invalidate(
                    BattleSessionInvalidationReason::
                        Backpressure);
                return;
            }
            QueueProjection(*projection, *publication);
        }

        /// FlushReplication 把 owned queue 映射到唯一 raw/KCP lane 与 listener。
        void FlushReplication() {
            while (authority->Active()) {
                auto item = replication->Pop(
                    operation_now_unix_ms);
                if (!item.has_value()) {
                    break;
                }
                if (item->lane ==
                    BattleReplicationLane::Raw) {
                    const auto plaintext =
                        BattleRawDispatcher::Encode(
                            BattleRouteDirection::
                                ServerToClient,
                            item->message_id,
                            item->partition_index,
                            item->partition_count,
                            item->application_sequence,
                            item->payload);
                    const auto sealed = channel->Seal(
                        BattlePacketKind::Raw,
                        plaintext,
                        operation_now_unix_ms);
                    if (sealed.disposition !=
                            BattleSealDisposition::
                                Sealed ||
                        owner.sender(
                            sealed.datagram,
                            control->ActiveEndpoint()) !=
                            BattleUdpSendDisposition::
                                Queued) {
                        authority->Invalidate(
                            BattleSessionInvalidationReason::
                                Protocol);
                    }
                } else if (
                    kcp->Queue(
                        item->message_id,
                        item->application_sequence,
                        item->payload,
                        operation_now_unix_ms) !=
                    BattleKcpDisposition::Queued) {
                    authority->Invalidate(
                        BattleSessionInvalidationReason::
                            Backpressure);
                }
            }
            if (authority->Active()) {
                const auto updated =
                    kcp->Update(
                        operation_now_unix_ms);
                if (updated !=
                        BattleKcpDisposition::Accepted &&
                    updated !=
                        BattleKcpDisposition::Queued) {
                    authority->Invalidate(
                        BattleSessionInvalidationReason::
                            Protocol);
                }
            }
        }

        /// Tick 在无新 datagram 时仍按 profile cadence 推进 KCP 重传与 ACK。
        void Tick(
            const std::uint64_t now_unix_ms) {
            AdvanceOperationClock(now_unix_ms);
            QueueCurrentProjection(false);
            FlushReplication();
        }

        /// BootstrapSnapshot 在 ServerAccept 已排队后发送首个 full snapshot。
        [[nodiscard]] bool BootstrapSnapshot() {
            QueueCurrentProjection(true);
            FlushReplication();
            return authority->Active();
        }

        /// CloseReason 返回受控 close override 或 authority 终结分类。
        [[nodiscard]] BattleCloseReasonCategory
        CloseReason() const noexcept {
            if (close_reason.has_value()) {
                return *close_reason;
            }
            return CloseReasonCategory(
                authority->Reason());
        }

        /// owner 是 node-global callbacks、crypto 与 metrics owner。
        Impl& owner;
        /// bootstrap_metadata 只在构造期间持有 locked seed。
        std::unique_ptr<
            BattleAuthenticatedHandshake::
                SessionBootstrap> bootstrap_metadata;
        /// route_key 是 active registry 的低敏 lookup identity。
        SessionRouteKey route_key;
        /// binding_fingerprint 支持 exact revoke 与 secure discriminator。
        CryptoProvider::Key32 binding_fingerprint{};
        /// authority 是 control/runtime 共享的不可恢复终结屏障。
        std::shared_ptr<BattleSessionAuthority> authority;
        /// context 是 payload 不得覆盖的 immutable actor binding。
        std::unique_ptr<BattleSessionContext> context;
        /// session_resources 独占该session rate/budget并在析构时回收registry。
        std::unique_ptr<BattleSessionResourceGovernor>
            session_resources;
        /// channel 独占 traffic/rekey secret、nonce 与 replay window。
        std::unique_ptr<BattleSecureChannel> channel;
        /// control 独占 endpoint rebind、rekey、close 与control output。
        std::unique_ptr<BattleTransportControl> control;
        /// input_ingress 把 authenticated actor input 提交给唯一 instance producer。
        std::unique_ptr<BattleInputIngress> input_ingress;
        /// raw_dispatcher 是 3000/3001 ingress 的唯一 route owner。
        std::unique_ptr<BattleRawDispatcher> raw_dispatcher;
        /// kcp 是 3004..3007 reliable lane 的唯一 owner。
        std::unique_ptr<BattleKcpAdapter> kcp;
        /// replication 是当前actor唯一有界egress queue。
        std::unique_ptr<BattleReplicationQueue> replication;
        /// multiplexer 组合 endpoint、authority、secure 与 lane dispatch。
        std::unique_ptr<BattleAuthenticatedMultiplexer>
            multiplexer;
        /// operation_now_unix_ms 是不因 listener queue 顺序回退的 session 时钟。
        std::uint64_t operation_now_unix_ms{};
        /// snapshot_cadence 独占 full 周期与 raw application identity。
        BattleSnapshotCadence snapshot_cadence;
        /// close_reason 只为已确认的 authenticated client close 覆盖 Protocol 分类。
        std::optional<BattleCloseReasonCategory>
            close_reason;
    };

    /// 构造函数验证 callbacks 并预留固定 active registry。
    Impl(
        BattleTransportRuntimeConfig config_value,
        BattleTicketAuthenticator& authenticator,
        DatagramSender sender_value,
        RawContextProvider context_provider,
        CommandIngressProvider
            command_ingress_provider_value,
        ReplicationProjectionProvider
            replication_projection_provider_value,
        KcpMessageHandler kcp_handler_value,
        AuthorityValidator authority_validator_value,
        const std::uint64_t initial_unix_ms,
        BattleRuntimeMetrics* metrics)
        : config(std::move(config_value)),
          sender(std::move(sender_value)),
          raw_context_provider(
              std::move(context_provider)),
          command_ingress_provider(
              std::move(
                  command_ingress_provider_value)),
          replication_projection_provider(
              std::move(
                  replication_projection_provider_value)),
          kcp_handler(std::move(kcp_handler_value)),
          authority_validator(
              std::move(authority_validator_value)),
          runtime_metrics(metrics),
          cookie_keys(crypto, initial_unix_ms),
          cookie_gate(
              cookie_keys,
              config.listener_identity),
          handshake(
              crypto,
              cookie_gate,
              authenticator,
              config.simulation_node_id,
              config.advertised_host,
              config.advertised_port),
          resources(metrics) {
        if (config.simulation_node_id.empty() ||
            config.advertised_host.empty() ||
            config.advertised_port == 0 ||
            IsAllZero(config.listener_identity) ||
            config.maximum_sessions == 0 ||
            config.maximum_sessions > 8 ||
            !sender ||
            !raw_context_provider ||
            !command_ingress_provider ||
            !replication_projection_provider ||
            !kcp_handler ||
            !authority_validator ||
            initial_unix_ms == 0) {
            throw std::invalid_argument(
                "battle transport runtime config is invalid");
        }
        sessions.reserve(config.maximum_sessions);
    }

    /// FindSession 按公开 secure route identity执行 bounded linear lookup。
    [[nodiscard]] auto FindSession(
        const SessionRouteKey& key) {
        return std::find_if(
            sessions.begin(),
            sessions.end(),
            [&](const auto& session) {
                return session->route_key == key;
            });
    }

    /// RecordSessionClose 在唯一 erase owner 处累计一次低敏终结原因。
    void RecordSessionClose(
        const Session& session) noexcept {
        if (runtime_metrics != nullptr) {
            runtime_metrics->RecordClose(
                session.CloseReason());
        }
    }

    /// config 是 node-global immutable composition identity。
    BattleTransportRuntimeConfig config;
    /// sender 是同一 listener 的唯一 output port。
    DatagramSender sender;
    /// raw_context_provider 提供 current instance Tick fence。
    RawContextProvider raw_context_provider;
    /// command_ingress_provider 解析 exact active instance producer。
    CommandIngressProvider command_ingress_provider;
    /// replication_projection_provider 只向 transport 提供 committed owned snapshot。
    ReplicationProjectionProvider
        replication_projection_provider;
    /// kcp_handler 是 reliable application ingress port。
    KcpMessageHandler kcp_handler;
    /// authority_validator 查询 current control facts。
    AuthorityValidator authority_validator;
    /// runtime_metrics 是可选 node-global低敏 metrics owner。
    BattleRuntimeMetrics* runtime_metrics;
    /// crypto 是 runtime 内全部 handshake/session primitive owner。
    CryptoProvider crypto;
    /// cookie_keys 是 process-local current/previous cookie secret。
    BattleCookieKeyRing cookie_keys;
    /// cookie_gate 在任何 ticket lookup 前运行。
    BattleHandshakeCookieGate cookie_gate;
    /// handshake 原子消费 ticket 并转移一次性 bootstrap。
    BattleAuthenticatedHandshake handshake;
    /// resources 提供 pre-auth 与 node/session hard budget。
    BattleResourceGovernor resources;
    /// sessions 是预留到 actor cap 的唯一 active registry。
    std::vector<std::unique_ptr<Session>> sessions;
    /// mutex 串行化 listener dispatch 与 control revoke/stop。
    mutable std::mutex mutex;
    /// stopped 一旦设置不可恢复。
    bool stopped{false};
    /// pending_datagrams 是 listener 与 runtime worker 之间的固定 hard queue。
    std::deque<QueuedDatagram> pending_datagrams;
    /// queue_mutex 只保护 enqueue/stop/worker handoff，不包围 crypto。
    std::mutex queue_mutex;
    /// queue_condition 唤醒唯一 runtime worker。
    std::condition_variable queue_condition;
    /// worker 是 expensive crypto/session dispatch 的唯一执行者。
    std::thread worker;
    /// queue_stopping 阻止 Stop 之后的新 owned copy。
    bool queue_stopping{false};
};

BattleTransportRuntime::BattleTransportRuntime(
    BattleTransportRuntimeConfig config,
    BattleTicketAuthenticator& ticket_authenticator,
    DatagramSender sender,
    RawContextProvider raw_context_provider,
    CommandIngressProvider command_ingress_provider,
    ReplicationProjectionProvider
        replication_projection_provider,
    KcpMessageHandler kcp_handler,
    AuthorityValidator authority_validator,
    const std::uint64_t initial_unix_ms,
    BattleRuntimeMetrics* runtime_metrics)
    : impl_(std::make_unique<Impl>(
          std::move(config),
          ticket_authenticator,
          std::move(sender),
          std::move(raw_context_provider),
          std::move(command_ingress_provider),
          std::move(
              replication_projection_provider),
          std::move(kcp_handler),
          std::move(authority_validator),
          initial_unix_ms,
          runtime_metrics)) {
    impl_->worker = std::thread([this] {
        auto next_update =
            std::chrono::steady_clock::now() +
            KcpUpdateInterval;
        while (true) {
            Impl::QueuedDatagram pending;
            bool has_datagram = false;
            {
                std::unique_lock lock(
                    impl_->queue_mutex);
                static_cast<void>(
                    impl_->queue_condition.wait_until(
                    lock,
                    next_update,
                    [this] {
                        return
                            impl_->queue_stopping ||
                            !impl_->pending_datagrams
                                 .empty();
                    }));
                if (impl_->queue_stopping &&
                    impl_->pending_datagrams.empty()) {
                    return;
                }
                if (!impl_->pending_datagrams.empty()) {
                    pending =
                        std::move(
                            impl_->pending_datagrams
                                .front());
                    impl_->pending_datagrams.pop_front();
                    has_datagram = true;
                }
            }
            if (has_datagram) {
                static_cast<void>(
                    HandleDatagram(
                        std::span(pending.bytes)
                            .first(pending.size),
                        pending.remote,
                        pending.observed_unix_ms));
            }
            const auto steady_now =
                std::chrono::steady_clock::now();
            if (steady_now < next_update) {
                continue;
            }
            // 处理积压 datagram 后仍执行 periodic update；延迟时从当前时刻重新排期，
            // 避免 ingress 持续繁忙造成饥饿，也避免暂停恢复后产生 catch-up burst。
            next_update =
                steady_now + KcpUpdateInterval;
            std::scoped_lock lock(impl_->mutex);
            if (impl_->stopped) {
                continue;
            }
            const auto now_unix_ms =
                ObservedUnixMilliseconds();
            for (const auto& session :
                 impl_->sessions) {
                session->Tick(now_unix_ms);
            }
            std::erase_if(
                impl_->sessions,
                [](const auto& session) {
                    return !session->authority->
                        Active();
                });
        }
    });
}

BattleTransportRuntime::~BattleTransportRuntime() {
    Stop();
}

BattleTransportRuntimeDisposition
BattleTransportRuntime::HandleDatagram(
    const std::span<const std::uint8_t> datagram,
    const BattleRemoteEndpoint& remote,
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(impl_->mutex);
    if (impl_->stopped) {
        return BattleTransportRuntimeDisposition::Stopped;
    }
    const auto kind = ClassifyDatagram(datagram);
    if (kind == DatagramKind::ClientHello) {
        if (!impl_->resources
                 .AllowPreAuthIp(
                     remote,
                     now_unix_ms)
                 .allowed) {
            return BattleTransportRuntimeDisposition::Dropped;
        }
        const auto decision =
            impl_->cookie_gate.HandleClientHello(
                datagram,
                remote,
                now_unix_ms);
        if (decision.response_bytes == 0 ||
            impl_->sender(
                std::span(decision.response)
                    .first(
                        decision.response_bytes),
                remote) !=
                BattleUdpSendDisposition::Queued) {
            return BattleTransportRuntimeDisposition::Dropped;
        }
        return BattleTransportRuntimeDisposition::
            RetryQueued;
    }
    if (kind == DatagramKind::ClientAuth) {
        if (impl_->sessions.size() >=
            impl_->config.maximum_sessions) {
            return BattleTransportRuntimeDisposition::Dropped;
        }
        std::array<std::uint8_t, 16> ticket_id{};
        if (datagram.size() <
            BattleAuthenticatedHandshake::
                ClientAuthBytes) {
            return BattleTransportRuntimeDisposition::Dropped;
        }
        std::ranges::copy_n(
            datagram.begin() + 8,
            ticket_id.size(),
            ticket_id.begin());
        const auto ticket_digest =
            impl_->crypto.Sha256(ticket_id);
        std::array<std::uint8_t, 8>
            ticket_handle_bytes{};
        std::ranges::copy_n(
            ticket_digest.begin(),
            ticket_handle_bytes.size(),
            ticket_handle_bytes.begin());
        if (!impl_->resources
                 .AllowTicket(
                     ReadUint64BE(
                         ticket_handle_bytes),
                     now_unix_ms)
                 .allowed) {
            return BattleTransportRuntimeDisposition::Dropped;
        }
        auto result =
            impl_->handshake.HandleClientAuth(
                datagram,
                remote,
                now_unix_ms);
        if (result.outcome ==
            BattleAuthenticatedHandshake::Outcome::
                Replayed) {
            const auto active = std::find_if(
                impl_->sessions.begin(),
                impl_->sessions.end(),
                [&](const auto& session) {
                    return session->route_key.generation ==
                        result.battle_session_generation;
                });
            if (active == impl_->sessions.end() ||
                impl_->sender(
                    std::span(result.response)
                        .first(result.response_bytes),
                    remote) !=
                    BattleUdpSendDisposition::Queued) {
                return BattleTransportRuntimeDisposition::Dropped;
            }
            return BattleTransportRuntimeDisposition::
                AcceptReplayed;
        }
        if (result.outcome !=
                BattleAuthenticatedHandshake::Outcome::
                    Accepted ||
            result.bootstrap == nullptr) {
            if (impl_->runtime_metrics != nullptr) {
                impl_->runtime_metrics->RecordReject();
            }
            return BattleTransportRuntimeDisposition::Dropped;
        }
        std::unique_ptr<Impl::Session> session;
        try {
            session = std::make_unique<Impl::Session>(
                *impl_,
                std::move(result.bootstrap),
                now_unix_ms);
        } catch (...) {
            return BattleTransportRuntimeDisposition::Dropped;
        }
        if (impl_->FindSession(session->route_key) !=
                impl_->sessions.end() ||
            impl_->sender(
                std::span(result.response)
                    .first(result.response_bytes),
                remote) !=
                BattleUdpSendDisposition::Queued) {
            return BattleTransportRuntimeDisposition::Dropped;
        }
        if (!session->BootstrapSnapshot()) {
            return BattleTransportRuntimeDisposition::Dropped;
        }
        impl_->sessions.push_back(
            std::move(session));
        return BattleTransportRuntimeDisposition::
            SessionAccepted;
    }
    if (kind != DatagramKind::Secure) {
        return BattleTransportRuntimeDisposition::Dropped;
    }
    const auto route_key =
        ParseSessionRouteKey(datagram);
    if (!route_key.has_value()) {
        return BattleTransportRuntimeDisposition::Dropped;
    }
    const auto found =
        impl_->FindSession(*route_key);
    if (found == impl_->sessions.end()) {
        return BattleTransportRuntimeDisposition::Dropped;
    }
    const auto outcome =
        (*found)->HandleSecure(
            datagram,
            remote,
            now_unix_ms);
    if (!(*found)->authority->Active() ||
        outcome.disposition ==
            BattleMultiplexerDisposition::
                AuthorityRejected ||
        outcome.disposition ==
            BattleMultiplexerDisposition::
                LaneUnavailable ||
        outcome.disposition ==
            BattleMultiplexerDisposition::
                ControlRejected ||
        outcome.disposition ==
            BattleMultiplexerDisposition::
                CloseRequested) {
        (*found)->authority->Invalidate(
            BattleSessionInvalidationReason::
                Protocol);
        impl_->RecordSessionClose(*(*found));
        impl_->sessions.erase(found);
        return BattleTransportRuntimeDisposition::
            SessionClosed;
    }
    if (outcome.disposition ==
            BattleMultiplexerDisposition::
                RawAccepted ||
        outcome.disposition ==
            BattleMultiplexerDisposition::
                KcpAccepted ||
        outcome.disposition ==
            BattleMultiplexerDisposition::
                ControlAccepted) {
        return BattleTransportRuntimeDisposition::
            SecureDispatched;
    }
    return BattleTransportRuntimeDisposition::Dropped;
}

BattleTransportEnqueueDisposition
BattleTransportRuntime::EnqueueDatagram(
    const std::span<const std::uint8_t> datagram,
    const BattleRemoteEndpoint& remote,
    const std::uint64_t now_unix_ms) {
    if (datagram.empty() ||
        datagram.size() >
            BattleUdpListener::MaximumDatagramBytes ||
        remote.port == 0 ||
        IsAllZero(remote.address) ||
        now_unix_ms == 0) {
        if (impl_->runtime_metrics != nullptr) {
            impl_->runtime_metrics->RecordReject();
        }
        return BattleTransportEnqueueDisposition::
            Invalid;
    }
    std::scoped_lock lock(impl_->queue_mutex);
    if (impl_->queue_stopping) {
        if (impl_->runtime_metrics != nullptr) {
            impl_->runtime_metrics->RecordDrop();
        }
        return BattleTransportEnqueueDisposition::
            Stopped;
    }
    if (impl_->pending_datagrams.size() >=
        MaximumPendingRuntimeDatagrams) {
        if (impl_->runtime_metrics != nullptr) {
            impl_->runtime_metrics->RecordReject();
        }
        return BattleTransportEnqueueDisposition::
            QueueFull;
    }
    auto pending = Impl::QueuedDatagram{
        .size = datagram.size(),
        .remote = remote,
        .observed_unix_ms = now_unix_ms,
    };
    std::ranges::copy(
        datagram,
        pending.bytes.begin());
    impl_->pending_datagrams.push_back(
        std::move(pending));
    if (impl_->runtime_metrics != nullptr) {
        impl_->runtime_metrics->ObserveIngressQueue(
            impl_->pending_datagrams.size());
    }
    impl_->queue_condition.notify_one();
    return BattleTransportEnqueueDisposition::
        Queued;
}

bool BattleTransportRuntime::RevokeBinding(
    const std::span<const std::uint8_t, 32>
        binding_fingerprint) noexcept {
    std::scoped_lock lock(impl_->mutex);
    const auto found = std::find_if(
        impl_->sessions.begin(),
        impl_->sessions.end(),
        [&](const auto& session) {
            return impl_->crypto.ConstantTimeEqual(
                session->binding_fingerprint,
                binding_fingerprint);
        });
    if (found == impl_->sessions.end()) {
        return false;
    }
    (*found)->authority->Invalidate(
        BattleSessionInvalidationReason::Target);
    impl_->RecordSessionClose(*(*found));
    impl_->sessions.erase(found);
    return true;
}

std::size_t BattleTransportRuntime::RevokeInstance(
    const std::string& simulation_instance_id,
    const BattleSessionInvalidationReason reason) noexcept {
    if (simulation_instance_id.empty() ||
        reason ==
            BattleSessionInvalidationReason::None) {
        return 0;
    }
    std::scoped_lock lock(impl_->mutex);
    std::size_t revoked = 0;
    std::erase_if(
        impl_->sessions,
        [&](const auto& session) {
            if (session->context->
                    SimulationInstanceId() !=
                simulation_instance_id) {
                return false;
            }
            session->authority->Invalidate(reason);
            impl_->RecordSessionClose(*session);
            ++revoked;
            return true;
        });
    return revoked;
}

void BattleTransportRuntime::Stop() noexcept {
    {
        std::scoped_lock lock(
            impl_->queue_mutex);
        if (!impl_->queue_stopping) {
            impl_->queue_stopping = true;
            impl_->pending_datagrams.clear();
        }
    }
    impl_->queue_condition.notify_all();
    if (impl_->worker.joinable() &&
        impl_->worker.get_id() !=
            std::this_thread::get_id()) {
        impl_->worker.join();
    }
    std::scoped_lock lock(impl_->mutex);
    if (!impl_->stopped) {
        impl_->stopped = true;
        for (auto& session : impl_->sessions) {
            session->authority->Invalidate(
                BattleSessionInvalidationReason::Node);
            impl_->RecordSessionClose(*session);
        }
        impl_->sessions.clear();
    }
}

std::size_t
BattleTransportRuntime::ActiveSessionCount() const noexcept {
    std::scoped_lock lock(impl_->mutex);
    return impl_->sessions.size();
}

}  // namespace ihomeland::sim
