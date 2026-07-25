#pragma once

#include "ihomeland/sim/transport/udp_listener.hpp"

#include <chrono>
#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <mutex>
#include <optional>
#include <span>
#include <string>
#include <vector>

namespace ihomeland::sim {

/// SimulationNodeConfig 绑定 child incarnation、资格 digest 与容量。
struct SimulationNodeConfig final {
    /// simulation_node_id 是 Go 为本 child 生成的不可复活 identity。
    std::string simulation_node_id;
    /// runtime_node_id 是 placement 使用的受信 node identity。
    std::string runtime_node_id;
    /// build_identity 是 B0.3 Release build identity SHA-256。
    std::string build_identity;
    /// model_manifest 是冻结 battle model manifest SHA-256。
    std::string model_manifest;
    /// profile_manifest 是冻结 network profile manifest SHA-256。
    std::string profile_manifest;
    /// instance_capacity 是当前 child 的 hard instance slots。
    std::size_t instance_capacity;
    /// actor_capacity 是每个 instance 已资格 actor hard cap。
    std::size_t actor_capacity;
};

/// ControlAssignment 是 Go placement AssignmentStamp 的私有 control 投影。
struct ControlAssignment final {
    /// personal_world_id 只用于完整 binding，不由 C++ 解释 owner。
    std::string personal_world_id;
    /// world_instance_id 是不可复活 runtime identity。
    std::string world_instance_id;
    /// runtime_node_id 必须与当前 node registration 一致。
    std::string runtime_node_id;
    /// generation 是 placement 单调 generation。
    std::uint64_t generation;
    /// fencing_token 是 placement 单调写 fence。
    std::uint64_t fencing_token;
    /// fingerprint 由 Go 对完整 stamp 计算并用于跨进程比较。
    std::string fingerprint;
};

/// InstanceStartCommand 固定一次幂等 SimulationInstance 启动。
struct InstanceStartCommand final {
    /// start_request_id 是响应丢失时必须复用的稳定 identity。
    std::string start_request_id;
    /// assignment 是完整 placement binding。
    ControlAssignment assignment;
    /// mapping_generation 变化时旧 InputTick timeline 失效。
    std::uint64_t mapping_generation;
    /// seed 是 exact assignment/config 派生的非零确定性根种子。
    std::uint64_t seed;
    /// config_identity 绑定 checked runtime config。
    std::string config_identity;
    /// navigation_identity 绑定 nav asset/config。
    std::string navigation_identity;
    /// physics_identity 绑定 physics adapter/config。
    std::string physics_identity;
    /// actor_capacity 不能超过 node 已资格 cap。
    std::size_t actor_capacity;
};

/// InstanceReadyReceipt 是 C++ 完成 startup 后返回的 exact binding。
struct InstanceReadyReceipt final {
    /// start_request_id 回显幂等 request identity。
    std::string start_request_id;
    /// assignment_fingerprint 回显完整 Go stamp。
    std::string assignment_fingerprint;
    /// simulation_instance_id 是 C++ 生成的不可复活 identity。
    std::string simulation_instance_id;
    /// mapping_generation 绑定本 instance input timeline。
    std::uint64_t mapping_generation;
    /// seed 回显本 instance 的确定性根种子。
    std::uint64_t seed;
    /// replayed 表示返回既有 ready receipt。
    bool replayed;
};

/// InstanceStatusReceipt 是 control status query 的只读投影。
struct InstanceStatusReceipt final {
    /// assignment_fingerprint 绑定查询的完整 stamp。
    std::string assignment_fingerprint;
    /// simulation_instance_id 标识被观察实例。
    std::string simulation_instance_id;
    /// state 是 running、drained 或 stopped。
    std::string state;
    /// committed_tick 是已完整提交的最后 Tick。
    std::uint64_t committed_tick;
};

/// ResultProposal 是 C++ 等待 Go terminal ack 的有界低敏结果。
struct ResultProposal final {
    /// result_id 在本 assignment timeline 内不可复用。
    std::string result_id;
    /// result_kind 当前只允许 lifecycle summary。
    std::string result_kind;
    /// assignment_fingerprint 绑定完整 placement stamp。
    std::string assignment_fingerprint;
    /// simulation_instance_id 绑定 C++ runtime incarnation。
    std::string simulation_instance_id;
    /// tick_start 是摘要覆盖的首 Tick。
    std::uint64_t tick_start;
    /// tick_end 是摘要覆盖的末 Tick。
    std::uint64_t tick_end;
    /// payload_digest 绑定低敏 canonical payload。
    std::string payload_digest;
    /// evidence_digest 绑定 B0.3 replay evidence 摘要。
    std::string evidence_digest;
    /// proposal_fingerprint 绑定以上全部 identity。
    std::string proposal_fingerprint;
};

/// BattleTicketState 是 exact child 内不可逆 ticket lifecycle。
enum class BattleTicketState : std::uint8_t {
    /// Installed 已预留 actor slot，尚未被握手消费。
    Installed = 1,
    /// Consumed 已由一次成功握手转换为 active actor。
    Consumed = 2,
    /// Revoked 已显式撤销且不可恢复。
    Revoked = 3,
    /// Expired 已到绝对 deadline 且不可恢复。
    Expired = 4,
};

/// BattleTicketBinding 是 Go authority facts 在 exact child 的不可变投影。
struct BattleTicketBinding final {
    /// player_id 来自认证 Session，不接受 UDP payload 覆盖。
    std::string player_id;
    /// session_id 绑定账号 session lineage。
    std::string session_id;
    /// session_epoch 是撤销屏障。
    std::uint64_t session_epoch;
    /// role 只允许 owner 或 visitor。
    std::string role;
    /// personal_world_id 绑定持久世界。
    std::string personal_world_id;
    /// visit_session_id 仅 Visitor 非空。
    std::string visit_session_id;
    /// world_instance_id 绑定 placement runtime。
    std::string world_instance_id;
    /// runtime_node_id 必须匹配本 node registration。
    std::string runtime_node_id;
    /// assignment_generation 是 current placement generation。
    std::uint64_t assignment_generation;
    /// fencing_token 绑定完整 AssignmentStamp。
    std::uint64_t fencing_token;
    /// assignment_fingerprint 必须匹配运行 instance。
    std::string assignment_fingerprint;
    /// simulation_node_id 必须匹配本 child incarnation。
    std::string simulation_node_id;
    /// simulation_instance_id 绑定不可复活 worker。
    std::string simulation_instance_id;
    /// mapping_generation 绑定 InputTick timeline。
    std::uint64_t mapping_generation;
    /// target_revision 绑定 Go current target revision。
    std::uint64_t target_revision;
    /// model_identity 绑定冻结 model。
    std::string model_identity;
    /// profile_identity 绑定冻结 network profile。
    std::string profile_identity;
    /// config_identity 绑定 checked runtime config。
    std::string config_identity;
    /// wire_identity 绑定 battle wire corpus。
    std::string wire_identity;
    /// actor_slot 是 0..7 的 exact instance slot。
    std::uint8_t actor_slot;
    /// advertised_host 是 ticket 唯一允许的 UDP host。
    std::string advertised_host;
    /// advertised_port 是 ticket 唯一允许的 UDP port。
    std::uint16_t advertised_port;
    /// issue_id 绑定 HTTP response-loss identity。
    std::string issue_id;
    /// issued_at_unix_ms 是首次冻结签发时刻。
    std::uint64_t issued_at_unix_ms;
    /// expires_at_unix_ms 是等于即失效的绝对 deadline。
    std::uint64_t expires_at_unix_ms;
};

/// BattleTicketInstallCommand 是 private control 唯一可安装的 secret-bearing 输入。
struct BattleTicketInstallCommand final {
    /// install_request_id 绑定 control request replay。
    std::string install_request_id;
    /// ticket_id 是 UDP 只可查找的 opaque identity。
    std::string ticket_id;
    /// binding_fingerprint 绑定全部 immutable fields。
    std::string binding_fingerprint;
    /// binding 是 Go 收集的完整 authority facts。
    BattleTicketBinding binding;
    /// proof_key 只允许进入 locked memory，不得日志或持久化。
    std::array<std::uint8_t, 32> proof_key;
};

/// BattleTicketReceipt 是 install/status/revoke 的低敏结果。
struct BattleTicketReceipt final {
    /// ticket_id 回显 exact lookup identity。
    std::string ticket_id;
    /// binding_fingerprint 回显完整 binding。
    std::string binding_fingerprint;
    /// simulation_instance_id 回显 exact worker。
    std::string simulation_instance_id;
    /// actor_slot 是首次安装冻结的位置。
    std::uint8_t actor_slot;
    /// state 是不可逆 lifecycle 状态。
    BattleTicketState state;
    /// replayed 表示相同 request/binding 的幂等结果。
    bool replayed;
};

/// SimulationNode 拥有一个 child 内全部 SimulationInstance 与 result outbox。
class SimulationNode final {
public:
    /// ResultOutboxLimit 是每个 node 的 hard pending result 数量。
    static constexpr std::size_t ResultOutboxLimit = 256;
    /// BattleTicketRegistryLimit 限制含 terminal tombstone 的 ticket 总量。
    static constexpr std::size_t BattleTicketRegistryLimit = 256;

    /// 构造函数验证资格 identity 与 1..8 actor capacity。
    explicit SimulationNode(SimulationNodeConfig config);

    /// 析构函数停止全部实例并释放 worker。
    ~SimulationNode();

    SimulationNode(const SimulationNode&) = delete;
    SimulationNode& operator=(const SimulationNode&) = delete;

    /// Start 幂等启动 exact assignment；冲突 identity fail closed。
    [[nodiscard]] InstanceReadyReceipt Start(const InstanceStartCommand& command);

    /// Status 返回 exact assignment 的当前 runtime 状态。
    [[nodiscard]] InstanceStatusReceipt Status(
        const std::string& world_instance_id,
        const std::string& assignment_fingerprint) const;

    /// Drain 在 deadline 内停止输入、完成有限 Tick 并生成 lifecycle result。
    [[nodiscard]] InstanceStatusReceipt Drain(
        const std::string& world_instance_id,
        const std::string& assignment_fingerprint,
        std::chrono::milliseconds deadline);

    /// Stop 只清理 exact assignment；已停止 replay 幂等成功。
    void Stop(
        const std::string& world_instance_id,
        const std::string& assignment_fingerprint,
        std::chrono::milliseconds deadline);

    /// PendingResults 返回 immutable outbox 副本。
    [[nodiscard]] std::vector<ResultProposal> PendingResults() const;

    /// AckResult 只删除 ResultID + fingerprint 完整匹配的 proposal；exact ack replay 幂等。
    void AckResult(
        const std::string& result_id,
        const std::string& proposal_fingerprint);

    /// InstallBattleTicket 原子验证 exact target 并预留 installed+active actor slot。
    [[nodiscard]] BattleTicketReceipt InstallBattleTicket(
        const BattleTicketInstallCommand& command,
        std::uint64_t observed_unix_ms);

    /// BattleTicketStatus 返回 exact ticket/binding 状态并惰性提交 expiry。
    [[nodiscard]] BattleTicketReceipt BattleTicketStatus(
        const std::string& ticket_id,
        const std::string& binding_fingerprint,
        std::uint64_t observed_unix_ms);

    /// ConsumeBattleTicket 至多一次把 Installed 转为 Consumed。
    [[nodiscard]] BattleTicketReceipt ConsumeBattleTicket(
        const std::string& ticket_id,
        const std::string& binding_fingerprint,
        std::uint64_t observed_unix_ms);

    /// AuthenticateAndConsumeBattleTicket 在同一 registry lock 内验证 proof 并一次消费。
    ///
    /// authenticator 只获得有界 proof key view，禁止保存、记录或跨调用使用。
    void AuthenticateAndConsumeBattleTicket(
        const std::string& ticket_id,
        const std::string& expected_simulation_node_id,
        const std::string& expected_advertised_host,
        std::uint16_t expected_advertised_port,
        std::uint64_t observed_unix_ms,
        const std::function<bool(
            std::span<const std::uint8_t, 32>,
            const BattleTicketBinding&,
            const std::string&)>& authenticator);

    /// RevokeBattleTicket 以 request identity 幂等终结 exact ticket。
    [[nodiscard]] BattleTicketReceipt RevokeBattleTicket(
        const std::string& revoke_request_id,
        const std::string& ticket_id,
        const std::string& binding_fingerprint,
        const std::string& simulation_instance_id,
        std::uint8_t actor_slot);

    /// InstalledOrActiveActors 返回 exact instance 仍占用的 slot 数。
    [[nodiscard]] std::size_t InstalledOrActiveActors(
        const std::string& simulation_instance_id,
        std::uint64_t observed_unix_ms);

    /// StartBattleUdpListener 创建并启动本 node 生命周期内唯一 UDP listener。
    ///
    /// 任一 bind/start 失败会使 node fail closed，禁止改端口重试。
    void StartBattleUdpListener(
        BattleUdpListenerConfig config,
        BattleUdpListener::DatagramHandler handler);

    /// StopBattleUdpListener 幂等停止 node-global UDP ingress。
    void StopBattleUdpListener() noexcept;

    /// UdpListenerStatus 返回已创建 listener 的低敏状态；未创建时为空。
    [[nodiscard]] std::optional<BattleUdpListenerStatus>
    UdpListenerStatus() const;

    /// BeginShutdown 阻止新 start，并有界停止全部实例。
    void BeginShutdown(std::chrono::milliseconds deadline);

    /// Healthy 报告 node 是否仍接受新 instance。
    [[nodiscard]] bool Healthy() const noexcept;

    /// RunningInstances 返回当前占用 slots。
    [[nodiscard]] std::size_t RunningInstances() const noexcept;

    /// Config 返回不可变 node registration。
    [[nodiscard]] const SimulationNodeConfig& Config() const noexcept;

private:
    struct Entry;
    struct TicketEntry;
    struct RevokeTombstone;
    /// RetiredBinding 保存同一 node incarnation 内不可复活的 exact runtime tombstone。
    struct RetiredBinding {
        /// world_instance_id 绑定已经停止的 Go-owned runtime identity。
        std::string world_instance_id;
        /// assignment_fingerprint 绑定 exact stop replay fence。
        std::string assignment_fingerprint;
    };

    /// config_ 是 hello 验证后的不可变 registration。
    SimulationNodeConfig config_;
    /// entries_ 的 concrete type 隐藏 core worker ownership。
    std::vector<std::unique_ptr<Entry>> entries_;
    /// retired_bindings_ 支持 exact stop replay并拒绝同 node 内复活 WorldInstanceID。
    std::vector<RetiredBinding> retired_bindings_;
    /// outbox_ 保存等待 Go ack 的 immutable proposals。
    std::vector<ResultProposal> outbox_;
    /// acked_results_ 有界保存 exact ack replay identity，避免响应重放误杀 control session。
    std::vector<ResultProposal> acked_results_;
    /// tickets_ 保存 locked proof key 与 terminal tombstone。
    std::vector<std::unique_ptr<TicketEntry>> tickets_;
    /// revoke_tombstones_ 阻止先到 cancel 后的迟到 install 恢复资格。
    std::vector<std::unique_ptr<RevokeTombstone>> revoke_tombstones_;
    /// ticket_mutex_ 保护 install/consume/revoke/status 的原子 hard cap。
    mutable std::mutex ticket_mutex_;
    /// battle_udp_listener_ 是 raw、KCP 与 transport-control 唯一 socket owner。
    std::unique_ptr<BattleUdpListener> battle_udp_listener_;
    /// battle_udp_listener_created_ 阻止失败后改端口或停止后创建第二 listener。
    bool battle_udp_listener_created_{false};
    /// healthy_ 一旦进入 shutdown 就不可恢复。
    bool healthy_{true};
};

}  // namespace ihomeland::sim
