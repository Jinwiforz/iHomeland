#pragma once

#include "ihomeland/sim/transport/authenticated_handshake.hpp"
#include "ihomeland/sim/transport/battle_session.hpp"
#include "ihomeland/sim/transport/kcp_adapter.hpp"
#include "ihomeland/sim/transport/udp_listener.hpp"
#include "ihomeland/sim/gameplay/projection.hpp"
#include "ihomeland/sim/simulation/input_timeline.hpp"

#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <span>
#include <string>
#include <vector>

namespace ihomeland::sim {

class BattleRuntimeMetrics;

/// BattleTransportRuntimeConfig 冻结 node-global authenticated data-plane composition。
struct BattleTransportRuntimeConfig final {
    /// simulation_node_id 必须等于 ticket install 的 exact child identity。
    std::string simulation_node_id;
    /// advertised_host 必须等于 HTTPS BattleTicket response 的 numeric host。
    std::string advertised_host;
    /// advertised_port 必须等于唯一 listener 的 public UDP port。
    std::uint16_t advertised_port;
    /// listener_identity 绑定 cookie，禁止跨 listener 重放。
    std::array<std::uint8_t, 16> listener_identity;
    /// maximum_sessions 必须位于当前 1..8 actor hard cap。
    std::size_t maximum_sessions;
};

/// BattleReplicationProjection 是 simulation owner 在一个 committed tick 冻结的只读视图。
struct BattleReplicationProjection final {
    /// server_tick 是 projection 所属的非零 committed SimulationTick。
    std::uint64_t server_tick;
    /// acknowledgement 只属于当前 BattleSession actor 与 mapping generation。
    InputAcknowledgementProjection acknowledgement;
    /// states 是同一 tick 的全部公开 entity state owned copy。
    std::vector<StateProjectionToken> states;
    /// ability_events 是 instance journal 内可供每个 session 独立追赶的可靠事件。
    std::vector<CombatAbilityEvent> ability_events;
    /// lifecycle_events 是 instance journal 内可供每个 session 独立追赶的可靠事件。
    std::vector<CombatLifecycleEvent> lifecycle_events;
    /// player_actor_ids 区分预留空 player slot 与始终公开的 encounter entity。
    std::vector<std::uint64_t> player_actor_ids;
    /// encounter_complete 是不可结算的暂态 Boss defeat projection。
    bool encounter_complete;
};

/// BattleSnapshotPublication 是一次不可变 snapshot cadence 决议。
struct BattleSnapshotPublication final {
    /// snapshot_sequence 是 session 内严格递增的 raw application identity。
    std::uint64_t snapshot_sequence;
    /// baseline_id 是本次 full identity 或 delta 引用的 current full identity。
    std::uint64_t baseline_id;
    /// full 表示本次必须发布完整 baseline。
    bool full;
};

/// BattleSnapshotCadence 独占 session snapshot sequence 与 baseline 周期。
class BattleSnapshotCadence final {
public:
    /// SnapshotIntervalTicks 来自冻结 profile 的 10 Hz snapshot cadence。
    static constexpr std::uint64_t
        SnapshotIntervalTicks = 2;
    /// FullBaselineIntervalSnapshots 来自冻结 profile 的 full baseline cadence。
    static constexpr std::uint64_t
        FullBaselineIntervalSnapshots = 10;
    /// RecoveryFullIntervalTicks 在 recovery window 内保持冻结的 2/s full 上限。
    static constexpr std::uint64_t
        RecoveryFullIntervalTicks = 10;
    /// RecoveryWindowTicks 把冗余 baseline 限定在一次 resync 后的 1.5 秒。
    static constexpr std::uint64_t
        RecoveryWindowTicks = 30;

    /// Next 按 committed Tick 提交到期 publication；未到期或耗尽时返回空。
    [[nodiscard]] std::optional<
        BattleSnapshotPublication>
    Next(
        std::uint64_t server_tick,
        bool force_full) noexcept;

    /// BeginRecovery 在已提交 resync full 后启动有界冗余，不延长活跃窗口。
    [[nodiscard]] bool BeginRecovery(
        std::uint64_t server_tick) noexcept;

    /// Terminal 表示 Tick 回退、零值或 sequence 耗尽后的不可逆终态。
    [[nodiscard]] bool Terminal() const noexcept;

private:
    /// snapshot_sequence_ 是最近一次已提交的 publication identity。
    std::uint64_t snapshot_sequence_{};
    /// baseline_id_ 是最近一次 full publication identity。
    std::uint64_t baseline_id_{};
    /// last_server_tick_ 是最近一次已发布 projection 的 committed Tick。
    std::uint64_t last_server_tick_{};
    /// next_recovery_full_tick_ 是 recovery window 内下一次 full 的最早 Tick。
    std::uint64_t next_recovery_full_tick_{};
    /// recovery_deadline_tick_ 固定一次 recovery window，重复请求不得无限续期。
    std::uint64_t recovery_deadline_tick_{};
    /// terminal_ 阻止无效 cadence state 被静默解释为“尚未到期”。
    bool terminal_{};
};

/// BattleTransportRuntimeDisposition 是 datagram 在 node-global owner 内的闭合结果。
enum class BattleTransportRuntimeDisposition : std::uint8_t {
    /// RetryQueued 表示合法 ClientHello 已从同一 listener 排队返回 Retry。
    RetryQueued = 1,
    /// SessionAccepted 表示首次握手已发布唯一 active session。
    SessionAccepted = 2,
    /// AcceptReplayed 表示 exact ClientAuth 已排队重放 byte-identical accept。
    AcceptReplayed = 3,
    /// SecureDispatched 表示 authenticated raw 或 KCP 已进入唯一 lane owner。
    SecureDispatched = 4,
    /// Dropped 表示 closed validation、authority、capacity 或 output gate 拒绝。
    Dropped = 5,
    /// SessionClosed 表示 hard protocol/lane failure 已终结 active session。
    SessionClosed = 6,
    /// Stopped 表示 runtime 已开始 shutdown，不再处理 datagram。
    Stopped = 7,
};

/// BattleTransportEnqueueDisposition 是 listener callback 的有界提交结果。
enum class BattleTransportEnqueueDisposition : std::uint8_t {
    /// Queued 表示 owned datagram 已提交到 runtime worker。
    Queued = 1,
    /// Invalid 表示 datagram、remote 或时间不符合固定边界。
    Invalid = 2,
    /// QueueFull 表示 pending ingress 已达到 node hard cap。
    QueueFull = 3,
    /// Stopped 表示 runtime 已开始 shutdown。
    Stopped = 4,
};

/// BattleTransportRuntime 是一个 SimulationNode 的唯一 handshake/session owner。
class BattleTransportRuntime final {
public:
    /// DatagramSender 必须把 owned copy 排队到同一个 BattleUdpListener。
    using DatagramSender = std::function<BattleUdpSendDisposition(
        std::span<const std::uint8_t>,
        const BattleRemoteEndpoint&)>;

    /// RawContextProvider 从 current instance 提供 InputTick/expiry fence。
    using RawContextProvider = std::function<BattleRawDispatchContext(
        const BattleSessionContext&,
        std::uint64_t)>;

    /// CommandIngressProvider 解析 exact active instance 的唯一 command port。
    ///
    /// provider 不得创建 fallback ingress；返回 nullptr 会使 handshake fail closed。
    using CommandIngressProvider =
        std::function<CommandIngress*(
            const BattleSessionContext&)>;

    /// ReplicationProjectionProvider 从 simulation owner 取得同一 committed tick 快照。
    using ReplicationProjectionProvider =
        std::function<std::optional<
            BattleReplicationProjection>(
            const BattleSessionContext&)>;

    /// KcpMessageHandler 只接收 authenticated reassembly 后的登记消息。
    using KcpMessageHandler = std::function<void(
        const BattleSessionContext&,
        const BattleKcpMessageView&)>;

    /// AuthorityValidator 查询 current session/assignment/target，不信任 payload。
    using AuthorityValidator = std::function<bool(
        const BattleSessionContext&)>;

    /// SessionLifecycleObserver 把 authenticated session generation 的唯一生灭通知给 simulation owner。
    using SessionLifecycleObserver = std::function<bool(
        const BattleSessionContext&,
        bool)>;

    /// 构造函数创建 cookie、handshake、resource 与固定 session registry。
    BattleTransportRuntime(
        BattleTransportRuntimeConfig config,
        BattleTicketAuthenticator& ticket_authenticator,
        DatagramSender sender,
        RawContextProvider raw_context_provider,
        CommandIngressProvider command_ingress_provider,
        ReplicationProjectionProvider
            replication_projection_provider,
        KcpMessageHandler kcp_handler,
        AuthorityValidator authority_validator,
        std::uint64_t initial_unix_ms,
        BattleRuntimeMetrics* runtime_metrics = nullptr,
        SessionLifecycleObserver session_lifecycle_observer = {});

    /// 析构函数 fail closed 清理全部 session、KCP 与 traffic secret。
    ~BattleTransportRuntime();

    BattleTransportRuntime(
        const BattleTransportRuntime&) = delete;
    BattleTransportRuntime& operator=(
        const BattleTransportRuntime&) = delete;

    /// HandleDatagram 在唯一 listener callback 上执行 closed protocol dispatch。
    [[nodiscard]] BattleTransportRuntimeDisposition
    HandleDatagram(
        std::span<const std::uint8_t> datagram,
        const BattleRemoteEndpoint& remote,
        std::uint64_t now_unix_ms);

    /// EnqueueDatagram 复制 listener span 并交给唯一 runtime worker。
    [[nodiscard]] BattleTransportEnqueueDisposition
    EnqueueDatagram(
        std::span<const std::uint8_t> datagram,
        const BattleRemoteEndpoint& remote,
        std::uint64_t now_unix_ms);

    /// RevokeBinding 终结 exact binding 的 active session，未找到时幂等返回 false。
    [[nodiscard]] bool RevokeBinding(
        std::span<const std::uint8_t, 32>
            binding_fingerprint) noexcept;

    /// RevokeInstance 终结 exact instance 的全部 active session。
    [[nodiscard]] std::size_t RevokeInstance(
        const std::string& simulation_instance_id,
        BattleSessionInvalidationReason reason) noexcept;

    /// Stop 阻止新 dispatch 并逆序清理所有 active session。
    void Stop() noexcept;

    /// ActiveSessionCount 返回不含 identity 的当前 session 数。
    [[nodiscard]] std::size_t
    ActiveSessionCount() const noexcept;

private:
    struct Impl;
    /// impl_ 隔离固定 registry、crypto state 与 adapter composition。
    std::unique_ptr<Impl> impl_;
};

}  // namespace ihomeland::sim
