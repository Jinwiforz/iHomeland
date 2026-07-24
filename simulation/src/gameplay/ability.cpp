#include "ihomeland/sim/gameplay/ability.hpp"

#include <algorithm>
#include <limits>
#include <unordered_set>

namespace ihomeland::sim {
namespace {

/// CheckedTickAdd 拒绝 phase/cooldown Tick 溢出。
[[nodiscard]] std::uint64_t CheckedTickAdd(
    const std::uint64_t tick,
    const std::uint64_t delta) {
    if (delta > std::numeric_limits<std::uint64_t>::max() - tick) {
        throw AbilityConfigError("Ability Tick boundary overflow");
    }
    return tick + delta;
}

/// ResetActivation 清空 activation-local 字段但保留 grant/resource/cooldown。
void ResetActivation(AbilityState& state, const std::uint64_t tick) noexcept {
    state.phase = AbilityPhase::Idle;
    state.active_ability_id = 0;
    state.activation_id = 0;
    state.phase_started_tick = tick;
    state.active_at_tick = 0;
    state.recovery_at_tick = 0;
    state.idle_at_tick = 0;
}

}  // namespace

AbilityConfigError::AbilityConfigError(const char* message)
    : std::runtime_error(message) {}

void ValidateAbilitySpecs(const std::span<const AbilitySpec> specs) {
    std::unordered_set<std::uint32_t> identities;
    std::unordered_set<std::uint8_t> weapons;
    for (const auto& spec : specs) {
        const auto durations_overflow =
            spec.active_ticks >
                std::numeric_limits<std::uint64_t>::max() - spec.windup_ticks ||
            (!(
                 spec.active_ticks >
                 std::numeric_limits<std::uint64_t>::max() - spec.windup_ticks) &&
             spec.recovery_ticks >
                 std::numeric_limits<std::uint64_t>::max() -
                     (spec.windup_ticks + spec.active_ticks));
        const auto minimum_cooldown =
            durations_overflow ? std::numeric_limits<std::uint64_t>::max()
                               : spec.windup_ticks + spec.active_ticks;
        if (spec.ability_id == 0 || spec.weapon == WeaponKind::None ||
            spec.cost_scaled < 0 || spec.windup_ticks == 0 ||
            spec.active_ticks == 0 || spec.recovery_ticks == 0 ||
            durations_overflow || spec.cooldown_ticks < minimum_cooldown ||
            (spec.required_tags & spec.blocked_tags) != 0) {
            throw AbilityConfigError("Ability content rule is invalid");
        }
        if (!identities.insert(spec.ability_id).second) {
            throw AbilityConfigError("Ability content identity is duplicated");
        }
        if (!weapons.insert(static_cast<std::uint8_t>(spec.weapon)).second) {
            throw AbilityConfigError("weapon grants more than one primary Ability");
        }
    }
}

std::uint32_t GrantedAbilityForWeapon(
    const std::span<const AbilitySpec> specs,
    const WeaponKind weapon) {
    ValidateAbilitySpecs(specs);
    if (weapon == WeaponKind::None) {
        return 0;
    }
    const auto iterator = std::find_if(specs.begin(), specs.end(), [&](const AbilitySpec& spec) {
        return spec.weapon == weapon;
    });
    if (iterator == specs.end()) {
        throw AbilityConfigError("weapon has no registered primary Ability");
    }
    return iterator->ability_id;
}

WeaponSwitchResult SwitchWeapon(
    const std::span<const AbilitySpec> specs,
    const AbilityState& current,
    const WeaponKind weapon,
    const std::uint64_t tick) {
    if (tick == 0) {
        throw std::invalid_argument("weapon switch Tick must be non-zero");
    }
    auto next = current;
    const auto canceled =
        current.phase == AbilityPhase::Idle ? 0 : current.activation_id;
    ResetActivation(next, tick);
    next.weapon = weapon;
    next.granted_ability_id = GrantedAbilityForWeapon(specs, weapon);
    return {.state = next, .canceled_activation_id = canceled};
}

AbilityRequestResult RequestAbility(
    const AbilitySpec& spec,
    const AbilityState& current,
    const std::uint64_t tick,
    const std::uint64_t activation_id,
    const std::uint64_t actor_tags) {
    ValidateAbilitySpecs(std::span<const AbilitySpec>(&spec, 1));
    const auto rejected = [&](const AbilityRejection rejection) {
        return AbilityRequestResult{
            .state = current,
            .rejection = rejection,
            .accepted = false};
    };
    if (!current.alive || tick == 0 || activation_id == 0) {
        return rejected(AbilityRejection::InvalidState);
    }
    if (current.granted_ability_id != spec.ability_id ||
        current.weapon != spec.weapon) {
        return rejected(AbilityRejection::NotGranted);
    }
    if (current.phase != AbilityPhase::Idle ||
        tick < current.cooldown_until_tick) {
        return rejected(AbilityRejection::Cooldown);
    }
    if ((actor_tags & spec.required_tags) != spec.required_tags ||
        (actor_tags & spec.blocked_tags) != 0) {
        return rejected(AbilityRejection::BlockedByTag);
    }
    if (current.resource_scaled < spec.cost_scaled) {
        return rejected(AbilityRejection::InsufficientResource);
    }

    auto next = current;
    next.resource_scaled -= spec.cost_scaled;
    next.phase = AbilityPhase::Requested;
    next.active_ability_id = spec.ability_id;
    next.activation_id = activation_id;
    next.phase_started_tick = tick;
    next.active_at_tick = CheckedTickAdd(tick, spec.windup_ticks);
    next.recovery_at_tick = CheckedTickAdd(next.active_at_tick, spec.active_ticks);
    next.idle_at_tick = CheckedTickAdd(next.recovery_at_tick, spec.recovery_ticks);
    next.cooldown_until_tick = CheckedTickAdd(tick, spec.cooldown_ticks);
    return {
        .state = next,
        .rejection = AbilityRejection::None,
        .accepted = true};
}

AbilityState AdvanceAbilityPhase(
    const AbilityState& current,
    const std::uint64_t tick) {
    if (tick == 0) {
        throw std::invalid_argument("Ability phase Tick must be non-zero");
    }
    auto next = current;
    if (next.phase == AbilityPhase::Requested && tick >= next.active_at_tick) {
        next.phase = AbilityPhase::Active;
        next.phase_started_tick = next.active_at_tick;
    }
    if (next.phase == AbilityPhase::Active && tick >= next.recovery_at_tick) {
        next.phase = AbilityPhase::Recovery;
        next.phase_started_tick = next.recovery_at_tick;
    }
    if (next.phase == AbilityPhase::Recovery && tick >= next.idle_at_tick) {
        ResetActivation(next, next.idle_at_tick);
    }
    return next;
}

}  // namespace ihomeland::sim
