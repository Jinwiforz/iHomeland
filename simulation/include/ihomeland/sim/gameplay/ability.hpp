#pragma once

#include <cstdint>
#include <span>
#include <stdexcept>

namespace ihomeland::sim {

/// WeaponKind 是首版冻结的权威武器集合。
enum class WeaponKind : std::uint8_t {
    /// None 不授予任何攻击 Ability。
    None,
    /// Sword 授予近战 primary Ability。
    Sword,
    /// Fan 授予 deferred projectile primary Ability。
    Fan,
};

/// AbilityPhase 是 AbilityActivation stage 唯一拥有的闭合状态机。
enum class AbilityPhase : std::uint8_t {
    /// Idle 表示当前没有进行中的 activation。
    Idle,
    /// Requested 表示 cost 已提交且等待 active Tick。
    Requested,
    /// Active 表示 HitDetection 可以消费当前 activation。
    Active,
    /// Recovery 表示 active 已结束但尚不能再次请求。
    Recovery,
};

/// AbilityRejection 是 command-facing 的稳定拒绝分类。
enum class AbilityRejection : std::uint8_t {
    /// None 表示 request 已原子提交。
    None,
    /// NotGranted 表示当前武器没有授予 requested Ability。
    NotGranted,
    /// Cooldown 表示 phase 或 cooldown window 尚未结束。
    Cooldown,
    /// InsufficientResource 表示 cost 无法完整提交。
    InsufficientResource,
    /// BlockedByTag 表示 actor tag 不满足 required/blocked policy。
    BlockedByTag,
    /// InvalidState 表示 actor 已死亡或 activation identity 非法。
    InvalidState,
};

/// AbilityConfigError 是实例启动前 content validation 的稳定失败。
class AbilityConfigError final : public std::runtime_error {
public:
    /// 构造函数保存低敏诊断文本。
    explicit AbilityConfigError(const char* message);
};

/// AbilitySpec 是经 content digest 绑定的不可变 activation 规则。
struct AbilitySpec final {
    /// ability_id 是非零稳定 content identity。
    std::uint32_t ability_id;
    /// weapon 是唯一授予当前 Ability 的武器。
    WeaponKind weapon;
    /// cost_scaled 是 request 成功时原子扣除的非负资源。
    std::int64_t cost_scaled;
    /// windup_ticks 是 Requested 到 Active 的正 Tick 数。
    std::uint64_t windup_ticks;
    /// active_ticks 是 Active 持续的正 Tick 数。
    std::uint64_t active_ticks;
    /// recovery_ticks 是 Recovery 持续的正 Tick 数。
    std::uint64_t recovery_ticks;
    /// cooldown_ticks 是从 request Tick 起算的最小再次激活间隔。
    std::uint64_t cooldown_ticks;
    /// required_tags 是 actor 必须完整具有的 GameplayTag bit mask。
    std::uint64_t required_tags;
    /// blocked_tags 是 actor 具有任一位时拒绝 activation 的 mask。
    std::uint64_t blocked_tags;
};

/// AbilityState 是 actor-local 的单一 grant/activation 状态，不创建 Ability entity。
struct AbilityState final {
    /// weapon 是当前权威装备。
    WeaponKind weapon;
    /// granted_ability_id 是当前武器唯一授予的 Ability，零表示无 grant。
    std::uint32_t granted_ability_id;
    /// phase 是当前 activation phase。
    AbilityPhase phase;
    /// active_ability_id 是进行中的 Ability，Idle 时为零。
    std::uint32_t active_ability_id;
    /// activation_id 是 actor stream 内非零稳定 identity，Idle 时为零。
    std::uint64_t activation_id;
    /// phase_started_tick 是当前 phase 首次生效的 SimulationTick。
    std::uint64_t phase_started_tick;
    /// active_at_tick 是 Requested 转为 Active 的首个 Tick。
    std::uint64_t active_at_tick;
    /// recovery_at_tick 是 Active 转为 Recovery 的首个 Tick。
    std::uint64_t recovery_at_tick;
    /// idle_at_tick 是 Recovery 转为 Idle 的首个 Tick。
    std::uint64_t idle_at_tick;
    /// cooldown_until_tick 是允许下一次 request 的首个 Tick。
    std::uint64_t cooldown_until_tick;
    /// resource_scaled 是 cost 唯一消费的 signed 64-bit scaled Attribute。
    std::int64_t resource_scaled;
    /// alive 表示 Death stage 尚未提交死亡。
    bool alive;
};

/// WeaponSwitchResult 描述 grant replacement 与 activation cancel。
struct WeaponSwitchResult final {
    /// state 是切换成功后的完整 actor-local Ability 状态。
    AbilityState state;
    /// canceled_activation_id 是被 revoke 的 activation，零表示没有 cancel。
    std::uint64_t canceled_activation_id;
};

/// AbilityRequestResult 描述 request 的原子提交或无 mutation 拒绝。
struct AbilityRequestResult final {
    /// state 在 accepted 时包含 cost/phase mutation，拒绝时等于输入 state。
    AbilityState state;
    /// rejection 是稳定 gameplay reason。
    AbilityRejection rejection;
    /// accepted 表示 request 已完整提交。
    bool accepted;
};

/// ValidateAbilitySpecs 拒绝重复 identity、非法 duration、cost 与 tag policy。
void ValidateAbilitySpecs(std::span<const AbilitySpec> specs);

/// GrantedAbilityForWeapon 返回当前 content 表中武器唯一授予的 Ability identity。
[[nodiscard]] std::uint32_t GrantedAbilityForWeapon(
    std::span<const AbilitySpec> specs,
    WeaponKind weapon);

/// SwitchWeapon 原子 revoke 旧 grant/activation 并安装新武器唯一 grant。
[[nodiscard]] WeaponSwitchResult SwitchWeapon(
    std::span<const AbilitySpec> specs,
    const AbilityState& current,
    WeaponKind weapon,
    std::uint64_t tick);

/// RequestAbility 验证 grant、phase、cooldown、tag、cost 后原子进入 Requested。
[[nodiscard]] AbilityRequestResult RequestAbility(
    const AbilitySpec& spec,
    const AbilityState& current,
    std::uint64_t tick,
    std::uint64_t activation_id,
    std::uint64_t actor_tags);

/// AdvanceAbilityPhase 在 Tick 开始按固定边界推进 Requested/Active/Recovery。
[[nodiscard]] AbilityState AdvanceAbilityPhase(
    const AbilityState& current,
    std::uint64_t tick);

}  // namespace ihomeland::sim
