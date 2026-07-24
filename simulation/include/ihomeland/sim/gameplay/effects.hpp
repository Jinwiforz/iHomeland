#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <stdexcept>
#include <vector>

namespace ihomeland::sim {

/// GameplayMathError 是 scaled Attribute 配置或运算无法安全表示时的失败。
class GameplayMathError final : public std::runtime_error {
public:
    /// 构造函数保存低敏诊断文本。
    explicit GameplayMathError(const char* message);
};

/// EffectApplyOutcome 是 Effect stage 的稳定结果分类。
enum class EffectApplyOutcome : std::uint8_t {
    /// Applied 表示创建了新的 active effect。
    Applied,
    /// Refreshed 表示已有 stack 被增加或刷新。
    Refreshed,
    /// Immune 表示 target tag 阻止 effect，状态未修改。
    Immune,
    /// Capacity 表示 active effect hard limit 已满。
    Capacity,
};

/// EffectSpec 是 content digest 绑定的 stack/refresh/expiry 规则。
struct EffectSpec final {
    /// effect_id 是非零稳定 content identity。
    std::uint32_t effect_id;
    /// stack_key 是 target 内用于合并 stack 的非零稳定 identity。
    std::uint32_t stack_key;
    /// maximum_stacks 是正整数硬上限。
    std::uint16_t maximum_stacks;
    /// duration_ticks 是 apply Tick 到 expiry Tick 的正间隔。
    std::uint64_t duration_ticks;
    /// refresh_on_reapply 表示 reapply 是否重置 expiry boundary。
    bool refresh_on_reapply;
    /// immunity_tag 是 target 具有任一位时免疫的 GameplayTag mask。
    std::uint64_t immunity_tag;
};

/// ActiveEffect 是 Effect stage 唯一拥有的有界 Tick 生命周期。
struct ActiveEffect final {
    /// effect_id 绑定 EffectSpec。
    std::uint32_t effect_id;
    /// stack_key 用于同 target 规范合并。
    std::uint32_t stack_key;
    /// source_actor_id 是 effect authority source。
    std::uint64_t source_actor_id;
    /// target_actor_id 是 effect owner。
    std::uint64_t target_actor_id;
    /// stacks 是 [1, maximum_stacks] 内的当前层数。
    std::uint16_t stacks;
    /// expires_at_tick 在进入该 Tick 时先于新 apply 淘汰。
    std::uint64_t expires_at_tick;
};

/// EffectApplication 是 HitDetection/AbilityActivation 写给 Effect stage 的请求。
struct EffectApplication final {
    /// source_actor_id 是非零 source ActorID。
    std::uint64_t source_actor_id;
    /// target_actor_id 是非零 target ActorID。
    std::uint64_t target_actor_id;
    /// stable_sequence 是同 source Tick 内的确定 tie-break。
    std::uint64_t stable_sequence;
};

/// EffectApplyResult 返回原子 apply 后的完整有界 effect 集。
struct EffectApplyResult final {
    /// effects 在拒绝时与输入相同，成功时按 target/stack/source 规范排序。
    std::vector<ActiveEffect> effects;
    /// outcome 是稳定 apply 分类。
    EffectApplyOutcome outcome;
    /// resulting_stacks 是成功后的 stack，拒绝时为零。
    std::uint16_t resulting_stacks;
};

/// AttributeModifierOperation 的数值顺序属于 modifier canonical tuple。
enum class AttributeModifierOperation : std::uint8_t {
    /// Add 先提交 scaled signed addition。
    Add,
    /// Multiply 再按 scale toward zero 乘法。
    Multiply,
};

/// AttributeModifier 是 Effect stage 交给 Attribute stage 的规范 modifier。
struct AttributeModifier final {
    /// priority 是 content 冻结的第一排序键。
    std::int32_t priority;
    /// operation 是第二排序键。
    AttributeModifierOperation operation;
    /// source_identity 是相同 priority/operation 的稳定 tie-break。
    std::uint64_t source_identity;
    /// magnitude_scaled 是 add 数值或 multiply coefficient。
    std::int64_t magnitude_scaled;
};

/// DamageFormula 是 signed 64-bit scaled 的权威伤害参数。
struct DamageFormula final {
    /// flat_damage_scaled 是非负固定伤害。
    std::int64_t flat_damage_scaled;
    /// power_coefficient_scaled 按 scalar_scale 乘 attacker power。
    std::int64_t power_coefficient_scaled;
    /// minimum_damage_scaled 是 defense 后的非负下限。
    std::int64_t minimum_damage_scaled;
    /// scalar_scale 是正整数 fixed-point scale。
    std::int64_t scalar_scale;
};

/// DamageRequest 是 Attribute stage 的规范同 Tick mutation。
struct DamageRequest final {
    /// target_actor_id 是非零受击 ActorID。
    std::uint64_t target_actor_id;
    /// source_actor_id 是非零 damage authority source。
    std::uint64_t source_actor_id;
    /// activation_id 是非零 Ability activation identity。
    std::uint64_t activation_id;
    /// stable_sequence 是相同 tuple 的最后 tie-break。
    std::uint64_t stable_sequence;
    /// damage_scaled 是已通过公式与 modifier 计算的非负值。
    std::int64_t damage_scaled;
    /// immune 表示 Effect stage 已判定 damage immunity。
    bool immune;
};

/// ActorVitalState 是 Attribute/Death stage 的最小 actor 投影。
struct ActorVitalState final {
    /// actor_id 是非零稳定 ActorID。
    std::uint64_t actor_id;
    /// health_scaled 是 [0, maximum] 内当前生命。
    std::int64_t health_scaled;
    /// alive 表示 Death stage 是否尚未提交 death cause。
    bool alive;
};

/// AppliedDamage 是一次规范请求实际提交的 Attribute delta。
struct AppliedDamage final {
    /// target_actor_id 是实际修改的 target。
    std::uint64_t target_actor_id;
    /// source_actor_id 是本次 damage source。
    std::uint64_t source_actor_id;
    /// activation_id 用于事件/evidence 关联。
    std::uint64_t activation_id;
    /// applied_scaled 是 clamp 到剩余 health 后的非负数。
    std::int64_t applied_scaled;
    /// immune 表示本请求被免疫且 applied_scaled 为零。
    bool immune;
};

/// DeathCause 是 Death stage 对每个 actor 最多提交一次的稳定事实。
struct DeathCause final {
    /// target_actor_id 是首次降到零的 actor。
    std::uint64_t target_actor_id;
    /// source_actor_id 是造成首次 lethal mutation 的 source。
    std::uint64_t source_actor_id;
    /// activation_id 是造成首次 lethal mutation 的 activation。
    std::uint64_t activation_id;
};

/// DamageBatchResult 是同 Tick Attribute/Death pipeline 的完整结果。
struct DamageBatchResult final {
    /// actors 是按 ActorID 规范排序的最终 vital states。
    std::vector<ActorVitalState> actors;
    /// applied 只包含存活 target 的规范处理结果。
    std::vector<AppliedDamage> applied;
    /// deaths 每个 target 最多一个，按请求规范顺序产生。
    std::vector<DeathCause> deaths;
};

/// ExpireEffects 在新 apply 前淘汰 expires_at_tick <= tick 的 entries。
[[nodiscard]] std::vector<ActiveEffect> ExpireEffects(
    std::span<const ActiveEffect> effects,
    std::uint64_t tick);

/// ApplyEffect 以 stack/refresh/immunity/capacity policy 原子处理一个请求。
[[nodiscard]] EffectApplyResult ApplyEffect(
    const EffectSpec& spec,
    std::span<const ActiveEffect> current,
    const EffectApplication& application,
    std::uint64_t tick,
    std::uint64_t target_tags,
    std::size_t capacity);

/// ApplyAttributeModifiers 按 priority/operation/source 规范顺序做 checked arithmetic。
[[nodiscard]] std::int64_t ApplyAttributeModifiers(
    std::int64_t base_scaled,
    std::int64_t scalar_scale,
    std::span<const AttributeModifier> modifiers);

/// CalculateDamage 按 flat + power*coefficient/scale - defense 与 minimum 计算伤害。
[[nodiscard]] std::int64_t CalculateDamage(
    const DamageFormula& formula,
    std::int64_t attacker_power_scaled,
    std::int64_t defender_defense_scaled);

/// ApplyDamageBatch 按 source/activation/sequence/target 排序并提交唯一 Death cause。
[[nodiscard]] DamageBatchResult ApplyDamageBatch(
    std::span<const ActorVitalState> actors,
    std::span<const DamageRequest> requests);

}  // namespace ihomeland::sim
