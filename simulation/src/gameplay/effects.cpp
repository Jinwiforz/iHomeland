#include "ihomeland/sim/gameplay/effects.hpp"

#include <algorithm>
#include <iterator>
#include <limits>
#include <tuple>

namespace ihomeland::sim {
namespace {

/// CheckedAdd 执行 signed 64-bit 加法并拒绝 wrap。
[[nodiscard]] std::int64_t CheckedAdd(const std::int64_t left, const std::int64_t right) {
    if ((right > 0 && left > std::numeric_limits<std::int64_t>::max() - right) ||
        (right < 0 && left < std::numeric_limits<std::int64_t>::min() - right)) {
        throw GameplayMathError("scaled Attribute addition overflow");
    }
    return left + right;
}

/// CheckedMultiplyTowardZero 执行 signed fixed-point 乘法并向零取整。
[[nodiscard]] std::int64_t CheckedMultiplyTowardZero(
    const std::int64_t left,
    const std::int64_t right,
    const std::int64_t scale) {
    if (scale <= 0 ||
        (left == std::numeric_limits<std::int64_t>::min() && right == -1) ||
        (right == std::numeric_limits<std::int64_t>::min() && left == -1)) {
        throw GameplayMathError("scaled Attribute multiplication is invalid");
    }
    if (left != 0 &&
        (right > 0
             ? (left > 0
                    ? left > std::numeric_limits<std::int64_t>::max() / right
                    : left < std::numeric_limits<std::int64_t>::min() / right)
             : (right < 0 &&
                (left > 0
                     ? right < std::numeric_limits<std::int64_t>::min() / left
                     : left < std::numeric_limits<std::int64_t>::max() / right)))) {
        throw GameplayMathError("scaled Attribute multiplication overflow");
    }
    return left * right / scale;
}

/// SortEffects 固定 effect projection 的顺序。
void SortEffects(std::vector<ActiveEffect>& effects) {
    std::stable_sort(effects.begin(), effects.end(), [](const auto& left, const auto& right) {
        return std::tie(left.target_actor_id, left.stack_key, left.source_actor_id, left.effect_id) <
               std::tie(right.target_actor_id, right.stack_key, right.source_actor_id, right.effect_id);
    });
}

}  // namespace

GameplayMathError::GameplayMathError(const char* message)
    : std::runtime_error(message) {}

std::vector<ActiveEffect> ExpireEffects(
    const std::span<const ActiveEffect> effects,
    const std::uint64_t tick) {
    if (tick == 0) {
        throw std::invalid_argument("Effect expiry Tick must be non-zero");
    }
    std::vector<ActiveEffect> remaining;
    remaining.reserve(effects.size());
    std::copy_if(effects.begin(), effects.end(), std::back_inserter(remaining), [&](const auto& effect) {
        return effect.expires_at_tick > tick;
    });
    SortEffects(remaining);
    return remaining;
}

EffectApplyResult ApplyEffect(
    const EffectSpec& spec,
    const std::span<const ActiveEffect> current,
    const EffectApplication& application,
    const std::uint64_t tick,
    const std::uint64_t target_tags,
    const std::size_t capacity) {
    if (spec.effect_id == 0 || spec.stack_key == 0 || spec.maximum_stacks == 0 ||
        spec.duration_ticks == 0 || application.source_actor_id == 0 ||
        application.target_actor_id == 0 || application.stable_sequence == 0 ||
        tick == 0 || capacity == 0 || current.size() > capacity ||
        spec.duration_ticks > std::numeric_limits<std::uint64_t>::max() - tick) {
        throw std::invalid_argument("Effect apply contract is invalid");
    }
    std::vector<ActiveEffect> effects(current.begin(), current.end());
    SortEffects(effects);
    if ((target_tags & spec.immunity_tag) != 0) {
        return {
            .effects = std::move(effects),
            .outcome = EffectApplyOutcome::Immune,
            .resulting_stacks = 0};
    }
    const auto existing = std::find_if(effects.begin(), effects.end(), [&](const auto& effect) {
        return effect.target_actor_id == application.target_actor_id &&
               effect.stack_key == spec.stack_key;
    });
    if (existing != effects.end()) {
        existing->stacks = static_cast<std::uint16_t>(
            std::min<std::uint32_t>(existing->stacks + 1, spec.maximum_stacks));
        if (spec.refresh_on_reapply) {
            existing->expires_at_tick = tick + spec.duration_ticks;
        }
        const auto resulting_stacks = existing->stacks;
        SortEffects(effects);
        return {
            .effects = std::move(effects),
            .outcome = EffectApplyOutcome::Refreshed,
            .resulting_stacks = resulting_stacks};
    }
    if (effects.size() == capacity) {
        return {
            .effects = std::move(effects),
            .outcome = EffectApplyOutcome::Capacity,
            .resulting_stacks = 0};
    }
    effects.push_back({
        .effect_id = spec.effect_id,
        .stack_key = spec.stack_key,
        .source_actor_id = application.source_actor_id,
        .target_actor_id = application.target_actor_id,
        .stacks = 1,
        .expires_at_tick = tick + spec.duration_ticks});
    SortEffects(effects);
    return {
        .effects = std::move(effects),
        .outcome = EffectApplyOutcome::Applied,
        .resulting_stacks = 1};
}

std::int64_t ApplyAttributeModifiers(
    std::int64_t base_scaled,
    const std::int64_t scalar_scale,
    const std::span<const AttributeModifier> modifiers) {
    if (scalar_scale <= 0) {
        throw GameplayMathError("Attribute scalar scale must be positive");
    }
    std::vector<AttributeModifier> canonical(modifiers.begin(), modifiers.end());
    std::stable_sort(canonical.begin(), canonical.end(), [](const auto& left, const auto& right) {
        return std::tie(left.priority, left.operation, left.source_identity) <
               std::tie(right.priority, right.operation, right.source_identity);
    });
    for (const auto& modifier : canonical) {
        if (modifier.source_identity == 0) {
            throw GameplayMathError("Attribute modifier identity is invalid");
        }
        switch (modifier.operation) {
            case AttributeModifierOperation::Add:
                base_scaled = CheckedAdd(base_scaled, modifier.magnitude_scaled);
                break;
            case AttributeModifierOperation::Multiply:
                base_scaled = CheckedMultiplyTowardZero(
                    base_scaled,
                    modifier.magnitude_scaled,
                    scalar_scale);
                break;
            default:
                throw GameplayMathError("Attribute modifier operation is invalid");
        }
    }
    return base_scaled;
}

std::int64_t CalculateDamage(
    const DamageFormula& formula,
    const std::int64_t attacker_power_scaled,
    const std::int64_t defender_defense_scaled) {
    if (formula.flat_damage_scaled < 0 || formula.power_coefficient_scaled < 0 ||
        formula.minimum_damage_scaled < 0 || formula.scalar_scale <= 0 ||
        attacker_power_scaled < 0 || defender_defense_scaled < 0) {
        throw GameplayMathError("damage formula contains a negative domain value");
    }
    const auto scaled_power = CheckedMultiplyTowardZero(
        attacker_power_scaled,
        formula.power_coefficient_scaled,
        formula.scalar_scale);
    const auto before_defense = CheckedAdd(formula.flat_damage_scaled, scaled_power);
    const auto after_defense =
        defender_defense_scaled >= before_defense ? 0 : before_defense - defender_defense_scaled;
    return std::max(formula.minimum_damage_scaled, after_defense);
}

DamageBatchResult ApplyDamageBatch(
    const std::span<const ActorVitalState> actors,
    const std::span<const DamageRequest> requests) {
    std::vector<ActorVitalState> states(actors.begin(), actors.end());
    std::stable_sort(states.begin(), states.end(), [](const auto& left, const auto& right) {
        return left.actor_id < right.actor_id;
    });
    for (std::size_t index = 0; index < states.size(); ++index) {
        if (states[index].actor_id == 0 || states[index].health_scaled < 0 ||
            (states[index].alive && states[index].health_scaled == 0) ||
            (index != 0 && states[index - 1].actor_id == states[index].actor_id)) {
            throw std::invalid_argument("actor vital state is invalid or duplicated");
        }
    }
    std::vector<DamageRequest> canonical(requests.begin(), requests.end());
    std::stable_sort(canonical.begin(), canonical.end(), [](const auto& left, const auto& right) {
        return std::tie(
                   left.source_actor_id,
                   left.activation_id,
                   left.stable_sequence,
                   left.target_actor_id) <
               std::tie(
                   right.source_actor_id,
                   right.activation_id,
                   right.stable_sequence,
                   right.target_actor_id);
    });

    std::vector<AppliedDamage> applied;
    std::vector<DeathCause> deaths;
    applied.reserve(canonical.size());
    deaths.reserve(states.size());
    for (const auto& request : canonical) {
        if (request.target_actor_id == 0 || request.source_actor_id == 0 ||
            request.activation_id == 0 || request.stable_sequence == 0 ||
            request.damage_scaled < 0) {
            throw std::invalid_argument("damage request is invalid");
        }
        const auto target = std::lower_bound(
            states.begin(),
            states.end(),
            request.target_actor_id,
            [](const ActorVitalState& state, const std::uint64_t actor_id) {
                return state.actor_id < actor_id;
            });
        if (target == states.end() || target->actor_id != request.target_actor_id) {
            throw std::invalid_argument("damage target is not registered");
        }
        if (!target->alive) {
            continue;
        }
        const auto actual = request.immune
                                ? 0
                                : std::min(request.damage_scaled, target->health_scaled);
        target->health_scaled -= actual;
        applied.push_back({
            .target_actor_id = request.target_actor_id,
            .source_actor_id = request.source_actor_id,
            .activation_id = request.activation_id,
            .applied_scaled = actual,
            .immune = request.immune});
        if (target->health_scaled == 0 && target->alive) {
            target->alive = false;
            deaths.push_back({
                .target_actor_id = request.target_actor_id,
                .source_actor_id = request.source_actor_id,
                .activation_id = request.activation_id});
        }
    }
    return {
        .actors = std::move(states),
        .applied = std::move(applied),
        .deaths = std::move(deaths)};
}

}  // namespace ihomeland::sim
