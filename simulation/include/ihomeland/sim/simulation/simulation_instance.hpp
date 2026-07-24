#pragma once

#include "ihomeland/sim/simulation/bounded_inbox.hpp"
#include "ihomeland/sim/simulation/identity.hpp"
#include "ihomeland/sim/simulation/tick_clock.hpp"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <mutex>
#include <span>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

namespace ihomeland::sim {

class CommandIngress;

/// SimulationInstanceState 是实例唯一 owner 的闭合生命周期。
enum class SimulationInstanceState : std::uint8_t {
    /// Created 表示 identity/config 已验证但资源尚未初始化。
    Created,
    /// Starting 表示 startup stack 正按顺序初始化。
    Starting,
    /// Running 表示唯一 worker 可以推进 Tick。
    Running,
    /// Draining 表示 producer 已关闭，worker 只处理现有有界 batch。
    Draining,
    /// Stopped 表示 worker 和 startup resources 已完全释放。
    Stopped,
    /// Failed 表示启动、Tick 或 deadline 失败的终态。
    Failed,
};

/// InstanceErrorCode 是 lifecycle/start/stop 的稳定失败分类。
enum class InstanceErrorCode : std::uint8_t {
    /// InvalidState 表示调用顺序违反生命周期。
    InvalidState,
    /// Startup 表示某个初始化 step 失败且已逆序 rollback。
    Startup,
    /// Tick 表示 worker callback 失败。
    Tick,
    /// Deadline 表示 drain/stop 未在 deadline 内完成。
    Deadline,
};

/// InstanceError 携带稳定 lifecycle 错误码。
class InstanceError final : public std::runtime_error {
public:
    /// 构造函数保存 code 与低敏诊断。
    InstanceError(InstanceErrorCode code, const char* message);

    /// Code 返回机器可判定的 lifecycle failure。
    [[nodiscard]] InstanceErrorCode Code() const noexcept;

private:
    /// code_ 在异常构造后不可变。
    InstanceErrorCode code_;
};

/// StartupStage 是初始化栈的稳定资源分类。
enum class StartupStage : std::uint8_t {
    /// Fixture 表示冻结 corpus/config source。
    Fixture,
    /// Ecs 表示 SimulationWorld 与 component capacity。
    Ecs,
    /// Physics 表示 PhysicsWorld adapter。
    Physics,
    /// Navigation 表示 NavigationWorld adapter。
    Navigation,
    /// History 表示有界 history owner。
    History,
    /// Evidence 表示低敏 evidence accumulator。
    Evidence,
};

/// StartupStep 定义一个可逆初始化动作；rollback 必须幂等且不得抛出。
struct StartupStep final {
    /// stage 用于稳定故障定位，不依赖 callback type。
    StartupStage stage;
    /// initialize 在 simulation worker 启动前执行。
    std::function<void()> initialize;
    /// rollback 在后续失败或正常 stop 时按逆序执行。
    std::function<void()> rollback;
};

/// IngressCommand 是 5.3 worker 边界使用的最小 canonical command envelope。
struct IngressCommand final {
    /// target_tick 是 checked InputTick mapping 的唯一结果。
    std::uint64_t target_tick;
    /// actor_id 是实例绑定的稳定 ActorID。
    std::uint64_t actor_id;
    /// input_tick 是 session generation 内采样 identity。
    std::uint64_t input_tick;
    /// stable_sequence 由 producer 提供并在 Tick batch 内用于确定排序。
    std::uint64_t stable_sequence;
    /// kind 是 GameplayCommandKind 的 canonical 数值。
    std::uint8_t kind;
    /// canonical_payload 是已通过 fixture adapter 安全检查的项目 token。
    std::string canonical_payload;
};

/// TickObservation 是 observer 获得的只读完整 Tick batch。
struct TickObservation final {
    /// tick 是提交中的 SimulationTick，从 1 单调增加。
    std::uint64_t tick;
    /// commands 只在 callback 返回前有效，observer 不得保留 span。
    std::span<const IngressCommand> commands;
};

/// SimulationInstanceConfig 固定 worker cadence、inbox 和 Tick debt hard limit。
struct SimulationInstanceConfig final {
    /// tick_step 必须精确等于冻结的 50 ms。
    std::chrono::nanoseconds tick_step;
    /// inbox_capacity 是异步 producer 的 hard limit。
    std::size_t inbox_capacity;
    /// hard_tick_debt 是允许等待处理的最大显式 clock credits 语义上限。
    std::uint32_t hard_tick_debt;
};

/// SimulationInstance 唯一拥有 lifecycle、worker、Tick 与异步 inbox。
///
/// producer 只能调用 Submit；Tick observer 只在唯一 worker 上执行。Start/Drain/Stop 由外部
/// lifecycle owner 串行调用，Submit 可并发，其他方法不承诺并发重入。
class SimulationInstance final {
public:
    /// TickObserver 在唯一 worker 上处理已冻结完整 batch；抛异常使实例进入 Failed。
    using TickObserver = std::function<void(const TickObservation&)>;

    /// 构造函数验证 50 ms、非零容量/debt，并借用共享 TickClock。
    SimulationInstance(
        SimulationInstanceIdentity identity,
        SimulationInstanceConfig config,
        std::shared_ptr<TickClock> clock,
        TickObserver observer);

    /// 析构函数请求 worker 停止并逆序释放已成功 startup resources。
    ~SimulationInstance();

    SimulationInstance(const SimulationInstance&) = delete;
    SimulationInstance& operator=(const SimulationInstance&) = delete;

    /// Start 按给定顺序执行 startup stack；任一步失败都会逆序 rollback 并进入 Failed。
    void Start(std::span<const StartupStep> steps);

    /// BeginDrain 关闭 producer，并让 worker 在下一个 Tick 处理完现有 batch 后停止。
    void BeginDrain();

    /// Stop 在 deadline 内等待 drain；超时使实例进入 Failed，再强制请求 worker 停止。
    ///
    /// @return true 表示正常 Stopped，false 表示 deadline failure。
    [[nodiscard]] bool Stop(std::chrono::milliseconds deadline);

    /// State 返回当前原子生命周期快照。
    [[nodiscard]] SimulationInstanceState State() const noexcept;

    /// CommittedTick 返回 observer 成功完成的最后一个 Tick。
    [[nodiscard]] std::uint64_t CommittedTick() const noexcept;

    /// Identity 返回不可变实例时间线 identity。
    [[nodiscard]] const SimulationInstanceIdentity& Identity() const noexcept;

    /// InboxHighWatermark 返回不包含 payload 的 queue 使用峰值。
    [[nodiscard]] std::size_t InboxHighWatermark() const;

private:
    friend class CommandIngress;

    /// SubmitValidated 只允许 CommandIngress 调用，并向有界 inbox 转移已验证 command。
    [[nodiscard]] InboxPushResult SubmitValidated(IngressCommand command);

    /// WorkerMain 是唯一推进 Tick 和调用 observer 的执行上下文。
    void WorkerMain(std::stop_token stop_token) noexcept;

    /// RollbackResources 按成功初始化逆序调用 rollback，吞掉异常并保持终态。
    void RollbackResources() noexcept;

    /// identity_ 在实例生命周期内不可变。
    SimulationInstanceIdentity identity_;
    /// config_ 在 worker 启动前验证且不可变。
    SimulationInstanceConfig config_;
    /// clock_ 由 shared owner 保证长于 worker。
    std::shared_ptr<TickClock> clock_;
    /// observer_ 只在唯一 worker 调用。
    TickObserver observer_;
    /// inbox_ 由 inbox_mutex_ 保护。
    BoundedInbox<IngressCommand> inbox_;
    /// queued_total_ 统计 inbox 与 worker future queue 的合计，受 inbox_mutex_ 保护。
    std::size_t queued_total_{0};
    /// pending_commands_ 只由 worker 使用，并预留到 inbox hard capacity。
    std::vector<IngressCommand> pending_commands_;
    /// inbox_mutex_ 线性化 producer push 与 Tick batch drain。
    mutable std::mutex inbox_mutex_;
    /// state_ 允许 producer 和 lifecycle owner 读取当前状态。
    std::atomic<SimulationInstanceState> state_{SimulationInstanceState::Created};
    /// committed_tick_ 只由 worker 写，其他线程读取快照。
    std::atomic<std::uint64_t> committed_tick_{0};
    /// worker_ 是唯一 simulation writer。
    std::jthread worker_;
    /// lifecycle_mutex_ 保护 startup rollback stack 与 stop wait。
    mutable std::mutex lifecycle_mutex_;
    /// lifecycle_condition_ 在 worker 进入 Stopped/Failed 时唤醒 Stop。
    std::condition_variable lifecycle_condition_;
    /// completed_steps_ 只保存成功初始化 step，按逆序释放。
    std::vector<StartupStep> completed_steps_;
};

}  // namespace ihomeland::sim
