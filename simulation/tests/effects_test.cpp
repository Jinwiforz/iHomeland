#include "ihomeland/sim/gameplay/effects.hpp"

#include <algorithm>
#include <iostream>
#include <limits>
#include <stdexcept>
#include <vector>

namespace {

/// Require 把 Effect/Attribute/Death 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// TestEffectLifecycle 验证 expiry-before-apply、stack refresh、immunity 与容量。
void TestEffectLifecycle() {
    const ihomeland::sim::EffectSpec spec{
        .effect_id = 1,
        .stack_key = 7,
        .maximum_stacks = 2,
        .duration_ticks = 3,
        .refresh_on_reapply = true,
        .immunity_tag = 2};
    const std::vector<ihomeland::sim::ActiveEffect> old{{
        .effect_id = 1,
        .stack_key = 7,
        .source_actor_id = 42,
        .target_actor_id = 99,
        .stacks = 1,
        .expires_at_tick = 2}};
    const auto expired = ihomeland::sim::ExpireEffects(old, 2);
    Require(expired.empty(), "expired Effect survived new apply boundary");
    const ihomeland::sim::EffectApplication application{
        .source_actor_id = 42,
        .target_actor_id = 99,
        .stable_sequence = 1};
    const auto applied = ihomeland::sim::ApplyEffect(spec, expired, application, 2, 0, 1);
    Require(
        applied.outcome == ihomeland::sim::EffectApplyOutcome::Applied &&
            applied.effects[0].expires_at_tick == 5,
        "new Effect apply drifted");
    const auto refreshed =
        ihomeland::sim::ApplyEffect(spec, applied.effects, application, 3, 0, 1);
    Require(
        refreshed.outcome == ihomeland::sim::EffectApplyOutcome::Refreshed &&
            refreshed.resulting_stacks == 2 &&
            refreshed.effects[0].expires_at_tick == 6,
        "Effect stack/refresh drifted");
    const auto immune =
        ihomeland::sim::ApplyEffect(spec, expired, application, 2, 2, 1);
    Require(
        immune.outcome == ihomeland::sim::EffectApplyOutcome::Immune &&
            immune.effects.empty(),
        "Effect immunity mutated state");
}

/// TestScaledMath 验证 modifier 规范排序、toward-zero 与 corpus damage formula。
void TestScaledMath() {
    const std::vector<ihomeland::sim::AttributeModifier> modifiers{
        {
            .priority = 2,
            .operation = ihomeland::sim::AttributeModifierOperation::Multiply,
            .source_identity = 2,
            .magnitude_scaled = 333},
        {
            .priority = 1,
            .operation = ihomeland::sim::AttributeModifierOperation::Add,
            .source_identity = 1,
            .magnitude_scaled = -1}};
    Require(
        ihomeland::sim::ApplyAttributeModifiers(2, 1000, modifiers) == 0,
        "scaled multiplication did not round toward zero");
    const ihomeland::sim::DamageFormula formula{
        .flat_damage_scaled = 3000,
        .power_coefficient_scaled = 1100,
        .minimum_damage_scaled = 1000,
        .scalar_scale = 1000};
    Require(
        ihomeland::sim::CalculateDamage(formula, 10'000, 1000) == 13'000,
        "corpus damage formula drifted");
    const std::vector<ihomeland::sim::AttributeModifier> overflow{{
        .priority = 1,
        .operation = ihomeland::sim::AttributeModifierOperation::Add,
        .source_identity = 1,
        .magnitude_scaled = 1}};
    try {
        static_cast<void>(ihomeland::sim::ApplyAttributeModifiers(
            std::numeric_limits<std::int64_t>::max(),
            1000,
            overflow));
    } catch (const ihomeland::sim::GameplayMathError&) {
        return;
    }
    throw std::runtime_error("Attribute overflow was accepted");
}

/// TestDamageAndDeath 验证 arrival reorder、immunity、health clamp 与唯一 death cause。
void TestDamageAndDeath() {
    const std::vector<ihomeland::sim::ActorVitalState> actors{
        {.actor_id = 99, .health_scaled = 20'000, .alive = true},
        {.actor_id = 100, .health_scaled = 30'000, .alive = true}};
    const std::vector<ihomeland::sim::DamageRequest> requests{
        {
            .target_actor_id = 99,
            .source_actor_id = 42,
            .activation_id = 1,
            .stable_sequence = 1,
            .damage_scaled = 13'000,
            .immune = false},
        {
            .target_actor_id = 100,
            .source_actor_id = 43,
            .activation_id = 2,
            .stable_sequence = 1,
            .damage_scaled = 8'000,
            .immune = true},
        {
            .target_actor_id = 99,
            .source_actor_id = 43,
            .activation_id = 2,
            .stable_sequence = 2,
            .damage_scaled = 8'000,
            .immune = false},
        {
            .target_actor_id = 99,
            .source_actor_id = 44,
            .activation_id = 3,
            .stable_sequence = 3,
            .damage_scaled = 1000,
            .immune = false}};
    auto reversed = requests;
    std::reverse(reversed.begin(), reversed.end());
    const auto first = ihomeland::sim::ApplyDamageBatch(actors, requests);
    const auto second = ihomeland::sim::ApplyDamageBatch(actors, reversed);
    Require(
        first.actors[0].health_scaled == 0 && !first.actors[0].alive &&
            first.actors[1].health_scaled == 30'000 &&
            first.deaths.size() == 1 &&
            first.deaths[0].source_actor_id == 43 &&
            second.deaths.size() == 1 &&
            second.deaths[0].source_actor_id == 43,
        "same-Tick damage/death depends on arrival order");
    Require(
        first.applied.size() == 3 &&
            first.applied[1].immune &&
            first.applied[2].applied_scaled == 7000,
        "damage clamp/immunity/post-death policy drifted");
}

}  // namespace

/// main 执行 Effect、scaled Attribute、damage 与唯一 Death cause 回归。
int main() {
    try {
        TestEffectLifecycle();
        TestScaledMath();
        TestDamageAndDeath();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
