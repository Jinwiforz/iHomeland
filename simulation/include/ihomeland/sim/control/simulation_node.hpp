#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <memory>
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

/// SimulationNode 拥有一个 child 内全部 SimulationInstance 与 result outbox。
class SimulationNode final {
public:
    /// ResultOutboxLimit 是每个 node 的 hard pending result 数量。
    static constexpr std::size_t ResultOutboxLimit = 256;

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
    /// healthy_ 一旦进入 shutdown 就不可恢复。
    bool healthy_{true};
};

}  // namespace ihomeland::sim
