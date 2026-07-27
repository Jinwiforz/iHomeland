#pragma once

#include "ihomeland/sim/gameplay/projection.hpp"
#include "ihomeland/sim/simulation/command_ingress.hpp"
#include "ihomeland/sim/transport/battle_route.hpp"
#include "ihomeland/sim/transport/resource_governor.hpp"

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <span>
#include <string>
#include <vector>

namespace ihomeland::sim {

struct InputAcknowledgementProjection;

/// BattleActorRole 是Go install允许投影的closed role。
enum class BattleActorRole : std::uint8_t {
    /// Owner 表示PersonalWorld owner。
    Owner = 1,
    /// Visitor 表示受控VisitSession actor。
    Visitor = 2,
};

/// BattleActorBinding 是Go control在exact instance预留的不可变actor事实。
struct BattleActorBinding final {
    /// player_id 来自account Session owner。
    std::string player_id;
    /// role 来自PersonalWorld或VisitSession policy。
    BattleActorRole role;
    /// actor_id 是SimulationInstance内非零identity。
    std::uint64_t actor_id;
    /// actor_slot 是0..7的installed+active slot。
    std::uint8_t actor_slot;
};

/// BattleSessionInvalidationReason 是停止新gameplay的低基数权威原因。
enum class BattleSessionInvalidationReason : std::uint8_t {
    /// None 表示session仍current。
    None = 0,
    /// AccountSession 表示SessionID/epoch lineage失效。
    AccountSession = 1,
    /// Membership 表示Owner/Visitor membership或grace失效。
    Membership = 2,
    /// Assignment 表示AssignmentStamp被successor替换。
    Assignment = 3,
    /// Target 表示Go current SimulationTarget revision改变。
    Target = 4,
    /// Instance 表示exact SimulationInstance停止或失败。
    Instance = 5,
    /// Node 表示child/node失效。
    Node = 6,
    /// Listener 表示node-global UDP listener失败。
    Listener = 7,
    /// Protocol 表示认证协议、crypto或rollover终结。
    Protocol = 8,
    /// Backpressure 表示hard queue budget要求关闭session。
    Backpressure = 9,
};

/// BattleSessionAuthority 是本地与Go invalidator共享的单向终结屏障。
class BattleSessionAuthority final {
public:
    /// Active 返回是否仍允许新gameplay。
    [[nodiscard]] bool Active() const noexcept;

    /// Invalidate 首次提交稳定reason，后续调用不得恢复或覆盖。
    void Invalidate(
        BattleSessionInvalidationReason reason) noexcept;

    /// Reason 返回首次终结原因。
    [[nodiscard]] BattleSessionInvalidationReason
    Reason() const noexcept;

private:
    /// reason_ 为None时active，其他值均terminal。
    std::atomic<BattleSessionInvalidationReason> reason_{
        BattleSessionInvalidationReason::None};
};

/// BattleSessionContext 是握手成功后唯一不可变authority binding。
class BattleSessionContext final {
public:
    /// 构造函数验证account/session/assignment/instance/actor的exact非零binding。
    BattleSessionContext(
        std::string account_session_id,
        std::uint64_t account_session_epoch,
        std::uint64_t battle_session_handle,
        std::uint32_t battle_session_generation,
        std::uint32_t endpoint_generation,
        std::string assignment_fingerprint,
        std::string simulation_instance_id,
        std::uint64_t mapping_generation,
        std::uint64_t target_revision,
        BattleActorBinding actor,
        std::shared_ptr<BattleSessionAuthority> authority);

    /// AccountSessionId 返回Go Session owner lineage。
    [[nodiscard]] const std::string&
    AccountSessionId() const noexcept;
    /// AccountSessionEpoch 返回不可回退epoch。
    [[nodiscard]] std::uint64_t
    AccountSessionEpoch() const noexcept;
    /// BattleSessionHandle 返回低敏runtime routing handle。
    [[nodiscard]] std::uint64_t
    BattleSessionHandle() const noexcept;
    /// BattleSessionGeneration 返回session incarnation。
    [[nodiscard]] std::uint32_t
    BattleSessionGeneration() const noexcept;
    /// EndpointGeneration 返回握手时或已commit rebind generation。
    [[nodiscard]] std::uint32_t
    EndpointGeneration() const noexcept;
    /// AssignmentFingerprint 返回exact current AssignmentStamp。
    [[nodiscard]] const std::string&
    AssignmentFingerprint() const noexcept;
    /// SimulationInstanceId 返回不可复活worker identity。
    [[nodiscard]] const std::string&
    SimulationInstanceId() const noexcept;
    /// MappingGeneration 返回InputTick timeline generation。
    [[nodiscard]] std::uint64_t
    MappingGeneration() const noexcept;
    /// TargetRevision 返回Go current target revision。
    [[nodiscard]] std::uint64_t
    TargetRevision() const noexcept;
    /// Actor 返回只来自Go install的actor binding。
    [[nodiscard]] const BattleActorBinding&
    Actor() const noexcept;
    /// Authority 返回共享terminal barrier。
    [[nodiscard]] const std::shared_ptr<
        BattleSessionAuthority>&
    Authority() const noexcept;

private:
    /// account_session_id_ 是Go Session owner lineage。
    std::string account_session_id_;
    /// account_session_epoch_ 是不可回退撤销屏障。
    std::uint64_t account_session_epoch_;
    /// battle_session_handle_ 是低敏runtime routing handle。
    std::uint64_t battle_session_handle_;
    /// battle_session_generation_ 防止旧session incarnation复活。
    std::uint32_t battle_session_generation_;
    /// endpoint_generation_ 是握手冻结的初始AAD generation。
    std::uint32_t endpoint_generation_;
    /// assignment_fingerprint_ 是exact current AssignmentStamp。
    std::string assignment_fingerprint_;
    /// simulation_instance_id_ 是不可复活worker identity。
    std::string simulation_instance_id_;
    /// mapping_generation_ 是InputTick timeline generation。
    std::uint64_t mapping_generation_;
    /// target_revision_ 是Go current target revision。
    std::uint64_t target_revision_;
    /// actor_ 是Go install唯一提供的actor binding。
    BattleActorBinding actor_;
    /// authority_ 是本地与Go invalidator共享终结屏障。
    std::shared_ptr<BattleSessionAuthority> authority_;
};

/// BattleIngressDisposition 是input bundle到SimulationInstance边界的稳定结果。
enum class BattleIngressDisposition : std::uint8_t {
    /// Accepted 表示全部command进入有界inbox。
    Accepted = 1,
    /// Partial 表示部分合法command进入，其余按closed reason拒绝。
    Partial = 2,
    /// AuthorityRejected 表示session/target/assignment已失效。
    AuthorityRejected = 3,
    /// InvalidPayload 表示protobuf、unknown field、顺序或allowlist错误。
    InvalidPayload = 4,
    /// RateLimited 表示session/message/instance token bucket耗尽。
    RateLimited = 5,
    /// Backpressure 表示node/session/inbox hard budget耗尽。
    Backpressure = 6,
    /// SimulationRejected 表示CommandIngress拒绝全部command。
    SimulationRejected = 7,
};

/// BattleIngressResult 汇总bundle结果且不包含payload/identity。
struct BattleIngressResult final {
    /// disposition 是bundle闭合结果。
    BattleIngressDisposition disposition;
    /// accepted_commands 是进入inbox的数量。
    std::size_t accepted_commands;
    /// rejected_commands 是被allowlist/inbox拒绝的数量。
    std::size_t rejected_commands;
    /// last_rejection 是最后一个CommandIngress低基数原因。
    CommandRejection last_rejection;
};

/// BattleInputIngress 把validated 3000 raw payload映射为绑定actor的intent commands。
class BattleInputIngress final {
public:
    /// 构造函数借用immutable context、唯一CommandIngress与session governor。
    BattleInputIngress(
        const BattleSessionContext& context,
        CommandIngress& command_ingress,
        BattleSessionResourceGovernor& resources);

    /// Handle 只接受3000 c2s frame并禁止payload identity/unsafe state。
    [[nodiscard]] BattleIngressResult Handle(
        const BattleRawFrameView& frame,
        std::uint64_t now_unix_ms);

private:
    /// context_ 是immutable actor/session/assignment binding。
    const BattleSessionContext* context_;
    /// command_ingress_ 是SimulationInstance唯一输入边界。
    CommandIngress* command_ingress_;
    /// resources_ 是当前session拥有的node hard budget视图。
    BattleSessionResourceGovernor* resources_;
};

/// BattleReplicationLane 是每个logical message唯一允许的传输lane。
enum class BattleReplicationLane : std::uint8_t {
    /// Raw 承载可替换full/delta snapshot。
    Raw = 1,
    /// Kcp 承载可靠event/lifecycle/resync response。
    Kcp = 2,
};

/// BattleReplicationItem 是ready egress queue的owned immutable item。
struct BattleReplicationItem final {
    /// lane 是registry唯一lane。
    BattleReplicationLane lane;
    /// message_id 是3002..3005或3007。
    std::uint32_t message_id;
    /// application_sequence 是session generation内非零序列。
    std::uint64_t application_sequence;
    /// application_tick 是权威server tick。
    std::uint64_t application_tick;
    /// partition_index 是 raw snapshot 的零基分区；KCP 固定为 0。
    std::uint8_t partition_index{};
    /// partition_count 是同一 logical snapshot 的分区数；KCP 固定为 1。
    std::uint8_t partition_count{1};
    /// expires_at_unix_ms 等于即终结。
    std::uint64_t expires_at_unix_ms;
    /// payload 是typed Protobuf exact bytes。
    std::vector<std::uint8_t> payload;
};

/// BattleReplicationMetrics 是低敏queue/replacement/expiry accounting。
struct BattleReplicationMetrics final {
    /// queued 是当前总items。
    std::size_t queued;
    /// kcp_queued 是当前reliable items。
    std::size_t kcp_queued;
    /// replaced_snapshots 是latest-wins替换累计。
    std::uint64_t replaced_snapshots;
    /// expired 是deadline终结累计。
    std::uint64_t expired;
    /// rejected 是authority/capacity/invalid投影累计。
    std::uint64_t rejected;
};

/// BattleReplicationQueue 从只读simulation projection/event构造唯一lane egress。
class BattleReplicationQueue final {
public:
    /// QueueItems 是session/node profile hard budget。
    static constexpr std::size_t QueueItems = 256;
    /// KcpItems 与KCP adapter application queue一致。
    static constexpr std::size_t KcpItems = 64;
    /// SnapshotMaximumPartitions 与 raw receiver 的固定 32-bit bitmap 一致。
    static constexpr std::size_t SnapshotMaximumPartitions = 32;

    /// 构造函数借用immutable context与session resource governor。
    BattleReplicationQueue(
        const BattleSessionContext& context,
        BattleSessionResourceGovernor& resources);

    /// 析构函数释放尚未发送item占用的egress budget。
    ~BattleReplicationQueue();

    BattleReplicationQueue(
        const BattleReplicationQueue&) = delete;
    BattleReplicationQueue& operator=(
        const BattleReplicationQueue&) = delete;

    /// QueueFullSnapshot 生成一个有界full baseline raw partition。
    [[nodiscard]] bool QueueFullSnapshot(
        std::uint64_t server_tick,
        std::uint64_t snapshot_sequence,
        std::uint64_t baseline_id,
        const InputAcknowledgementProjection&
            acknowledgement,
        std::span<const StateProjectionToken> states,
        std::uint64_t now_unix_ms);

    /// QueueDeltaSnapshot 生成latest-wins delta raw partition。
    [[nodiscard]] bool QueueDeltaSnapshot(
        std::uint64_t server_tick,
        std::uint64_t snapshot_sequence,
        std::uint64_t baseline_id,
        const InputAcknowledgementProjection&
            acknowledgement,
        std::span<const StateProjectionToken> states,
        std::uint64_t now_unix_ms);

    /// QueueAbilityEvent 从只读event投影生成3004可靠消息。
    [[nodiscard]] bool QueueAbilityEvent(
        const EventProjectionToken& event,
        std::uint32_t source_generation,
        std::uint32_t ability_id,
        std::uint32_t phase,
        std::uint64_t now_unix_ms);

    /// QueueEntityLifecycle 从simulation event生成3005可靠消息。
    [[nodiscard]] bool QueueEntityLifecycle(
        const EventProjectionToken& event,
        std::uint32_t entity_generation,
        std::uint32_t kind,
        std::uint32_t archetype_id,
        std::uint64_t now_unix_ms);

    /// QueueResyncResponse 确认恢复策略但不在KCP内嵌snapshot。
    [[nodiscard]] bool QueueResyncResponse(
        std::uint64_t request_sequence,
        std::uint64_t server_tick,
        std::uint32_t disposition,
        std::uint64_t scheduled_baseline_id,
        std::uint64_t now_unix_ms);

    /// Pop 丢弃expired item并返回下一owned egress。
    [[nodiscard]] std::optional<BattleReplicationItem>
    Pop(std::uint64_t now_unix_ms);

    /// Metrics 返回低敏queue状态。
    [[nodiscard]] BattleReplicationMetrics
    Metrics() const;

private:
    struct Impl;
    /// impl_ 隐藏generated adapter、有界queue与mutex。
    std::unique_ptr<Impl> impl_;
};

}  // namespace ihomeland::sim
