#pragma once

#include <cstdint>
#include <optional>
#include <span>
#include <stdexcept>

namespace ihomeland::sim {

/// AiState 是普通怪物与 Boss 共用的有限权威状态集。
enum class AiState : std::uint8_t {
    /// Idle 表示当前无合法 target 且不产生 movement intent。
    Idle,
    /// Acquire 表示刚选择 target，下一次决策才进入 chase/attack。
    Acquire,
    /// Chase 表示只产生 navigation/movement intent，不直接写 Transform。
    Chase,
    /// Attack 表示产生 Ability intent。
    Attack,
    /// Recover 表示 attack 后的固定恢复窗口。
    Recover,
    /// Dead 表示不再选择 target 或产生 intent。
    Dead,
};

/// AiError 是 AI 输入、phase 或 random stream identity 非法时的稳定失败。
class AiError final : public std::runtime_error {
public:
    /// 构造函数保存低敏诊断文本。
    explicit AiError(const char* message);
};

/// AiTargetCandidate 是 target selection 的只读项目 value。
struct AiTargetCandidate final {
    /// actor_id 是非零稳定 ActorID。
    std::uint64_t actor_id;
    /// threat_scaled 是非负 threat，越大优先级越高。
    std::int64_t threat_scaled;
    /// distance_squared_mm 是 checked 距离平方，越小优先级越高。
    std::uint64_t distance_squared_mm;
    /// alive 表示 candidate 尚未提交 Death。
    bool alive;
};

/// AiDecisionInput 是一次有限状态迁移所需的只读事实。
struct AiDecisionInput final {
    /// current 是当前 AI state。
    AiState current;
    /// alive 表示当前 AI entity 尚未死亡。
    bool alive;
    /// has_target 表示 target selection 返回了合法 actor。
    bool has_target;
    /// target_in_attack_range 表示规范距离已进入 attack boundary。
    bool target_in_attack_range;
    /// attack_ready 表示 AbilityActivation 允许新的 attack intent。
    bool attack_ready;
    /// recovery_complete 表示固定 recovery boundary 已到达。
    bool recovery_complete;
};

/// AiIntent 是 AIIntent stage 的唯一输出，不包含 Transform mutation。
struct AiIntent final {
    /// next_state 是本 Tick 提交的有限状态。
    AiState next_state;
    /// request_navigation 表示后续 NavigationWorld 可处理 chase path。
    bool request_navigation;
    /// request_attack 表示 AbilityActivation 可处理 attack intent。
    bool request_attack;
};

/// BossPhaseState 把检测与生效拆到两个 Tick，禁止同 Tick phase reentry。
struct BossPhaseState final {
    /// current_phase 是本 Tick systems 可见的正整数 phase。
    std::uint8_t current_phase;
    /// pending_phase 是等待生效的更高 phase，零表示没有。
    std::uint8_t pending_phase;
    /// effective_tick 是 pending_phase 首次可提交的 Tick。
    std::uint64_t effective_tick;
};

/// EntityRandomStream 是 entity/system/stream 三元组独占的确定 PRNG。
class EntityRandomStream final {
public:
    /// 构造函数从 build seed 与非零 identity 派生独立 stream state。
    EntityRandomStream(
        std::uint64_t build_seed,
        std::uint64_t entity_id,
        std::uint64_t stream_id);

    /// Next 返回下一个 64-bit random value，只修改当前对象 state。
    [[nodiscard]] std::uint64_t Next() noexcept;

    /// Uniform 返回 [0, exclusive_upper_bound) 内的无共享状态结果。
    [[nodiscard]] std::uint64_t Uniform(std::uint64_t exclusive_upper_bound);

private:
    /// state_ 只属于当前 entity/system/stream 实例。
    std::uint64_t state_;
};

/// SelectAiTarget 按 threat desc、distance asc、ActorID asc 选择唯一 target。
[[nodiscard]] std::optional<std::uint64_t> SelectAiTarget(
    std::span<const AiTargetCandidate> candidates);

/// DecideAiIntent 运行普通怪物/Boss 共用的固定有限状态迁移。
[[nodiscard]] AiIntent DecideAiIntent(const AiDecisionInput& input) noexcept;

/// ScheduleBossPhase 检测 health threshold，但只登记 tick+1 生效。
[[nodiscard]] BossPhaseState ScheduleBossPhase(
    const BossPhaseState& current,
    std::int64_t health_scaled,
    std::int64_t maximum_health_scaled,
    std::uint8_t requested_phase,
    std::uint32_t threshold_percent,
    std::uint64_t tick);

/// CommitBossPhase 只在 effective_tick 到达时提交 pending phase。
[[nodiscard]] BossPhaseState CommitBossPhase(
    const BossPhaseState& current,
    std::uint64_t tick);

}  // namespace ihomeland::sim
