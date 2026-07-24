#pragma once

#include "ihomeland/sim/ecs/simulation_world.hpp"
#include "ihomeland/sim/ecs/structural_commands.hpp"

#include <array>
#include <cstdint>
#include <functional>
#include <span>
#include <stdexcept>
#include <string_view>
#include <vector>

namespace ihomeland::sim {

/// GameplayStage 的数值顺序是权威 Tick pipeline 的冻结执行顺序。
enum class GameplayStage : std::uint8_t {
    /// DrainInput 将 worker 的规范 command batch 交给输入时间线。
    DrainInput = 0,
    /// InputIntent 解析 player continuous/discrete intent。
    InputIntent = 1,
    /// AIIntent 产生与玩家输入同构的服务端 intent。
    AIIntent = 2,
    /// AbilityActivation 唯一拥有 grant、cost、cooldown 与 phase mutation。
    AbilityActivation = 3,
    /// Movement 唯一拥有整数运动意图到 velocity 的转换。
    Movement = 4,
    /// Physics 唯一提交规范 collision/ground 结果。
    Physics = 5,
    /// HitDetection 唯一产生权威 hit 与 projectile lifecycle 结果。
    HitDetection = 6,
    /// Effect 唯一拥有 GameplayEffect stack、refresh 与 expiry。
    Effect = 7,
    /// Attribute 唯一提交 scaled Attribute 与 damage mutation。
    Attribute = 8,
    /// Death 唯一提交稳定 death cause。
    Death = 9,
    /// Replication 只读投影当前 Tick 的 state/event/rejection token。
    Replication = 10,
    /// CommitDeferredStructuralChanges 是唯一结构变化 barrier。
    CommitDeferredStructuralChanges = 11,
};

/// kGameplayStageCount 是冻结 pipeline 的阶段数量。
inline constexpr std::size_t kGameplayStageCount = 12;

/// GameplayPipelineErrorCode 是构造阶段的稳定拒绝分类。
enum class GameplayPipelineErrorCode : std::uint8_t {
    /// MissingStage 表示注册数不足或缺少权威阶段。
    MissingStage,
    /// DuplicateStage 表示同一权威阶段注册超过一次。
    DuplicateStage,
    /// ReorderedStage 表示注册顺序偏离冻结 pipeline。
    ReorderedStage,
    /// InvalidCallback 表示阶段没有可调用 system。
    InvalidCallback,
};

/// GameplayPipelineError 保留可机器判断的 pipeline 构造失败原因。
class GameplayPipelineError final : public std::runtime_error {
public:
    /// 构造函数绑定稳定错误码与低敏诊断文本。
    GameplayPipelineError(GameplayPipelineErrorCode code, const char* message);

    /// Code 返回不依赖诊断文本的稳定错误码。
    [[nodiscard]] GameplayPipelineErrorCode Code() const noexcept;

private:
    /// code_ 是当前构造失败的稳定分类。
    GameplayPipelineErrorCode code_;
};

/// GameplayTickContext 是各 stage 共享的单 worker Tick 所有权边界。
struct GameplayTickContext final {
    /// tick 是当前尚未提交的非零 SimulationTick。
    std::uint64_t tick;
    /// world 是当前实例唯一可写 ECS world。
    SimulationWorld& world;
    /// structural 是当前实例唯一 deferred structural buffer。
    StructuralCommandBuffer& structural;
};

/// GameplaySystem 是一个冻结 stage 的唯一注册回调。
using GameplaySystem = std::function<void(GameplayTickContext&)>;

/// GameplaySystemRegistration 将一个回调绑定到一个权威 stage。
struct GameplaySystemRegistration final {
    /// stage 必须按 AuthoritativeGameplayStages 返回的顺序出现一次。
    GameplayStage stage;
    /// system 在 simulation worker 上同步运行，不得保存 context 引用。
    GameplaySystem system;
};

/// AuthoritativeGameplayStages 返回不可修改的唯一权威 stage 顺序。
[[nodiscard]] std::span<const GameplayStage, kGameplayStageCount>
AuthoritativeGameplayStages() noexcept;

/// GameplayStageName 返回稳定、区分大小写的 stage token。
[[nodiscard]] std::string_view GameplayStageName(GameplayStage stage);

/// GameplayPipeline 验证完整注册表并按冻结顺序同步执行每个 system。
class GameplayPipeline final {
public:
    /// 构造函数拒绝缺失、重复、重排或空 callback，不做隐式补全。
    explicit GameplayPipeline(std::span<const GameplaySystemRegistration> registrations);

    /// Run 依次执行全部权威 stage；任一 system 失败时不跳过或继续后续 stage。
    void Run(GameplayTickContext& context) const;

private:
    /// systems_ 与 AuthoritativeGameplayStages 使用相同固定下标。
    std::array<GameplaySystem, kGameplayStageCount> systems_;
};

}  // namespace ihomeland::sim
