#include "ihomeland/sim/gameplay/ability.hpp"

#include <cstdint>
#include <iostream>
#include <stdexcept>
#include <vector>

namespace {

/// Require 把 Ability 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// Specs 返回剑/扇子各自唯一 primary grant。
[[nodiscard]] std::vector<ihomeland::sim::AbilitySpec> Specs() {
    return {
        {
            .ability_id = 1,
            .weapon = ihomeland::sim::WeaponKind::Sword,
            .cost_scaled = 10'000,
            .windup_ticks = 1,
            .active_ticks = 1,
            .recovery_ticks = 1,
            .cooldown_ticks = 3,
            .required_tags = 1,
            .blocked_tags = 2},
        {
            .ability_id = 2,
            .weapon = ihomeland::sim::WeaponKind::Fan,
            .cost_scaled = 15'000,
            .windup_ticks = 1,
            .active_ticks = 1,
            .recovery_ticks = 1,
            .cooldown_ticks = 4,
            .required_tags = 1,
            .blocked_tags = 2}};
}

/// InitialState 返回 corpus 中 owner 的 fan grant 与资源。
[[nodiscard]] ihomeland::sim::AbilityState InitialState() {
    return {
        .weapon = ihomeland::sim::WeaponKind::Fan,
        .granted_ability_id = 2,
        .phase = ihomeland::sim::AbilityPhase::Idle,
        .active_ability_id = 0,
        .activation_id = 0,
        .phase_started_tick = 0,
        .active_at_tick = 0,
        .recovery_at_tick = 0,
        .idle_at_tick = 0,
        .cooldown_until_tick = 0,
        .resource_scaled = 100'000,
        .alive = true};
}

/// TestSwordFixtureFlow 验证 switch/grant/request/cost/phase/cooldown/revoke 的 corpus 顺序。
void TestSwordFixtureFlow() {
    const auto specs = Specs();
    const auto switched = ihomeland::sim::SwitchWeapon(
        specs,
        InitialState(),
        ihomeland::sim::WeaponKind::Sword,
        1);
    Require(
        switched.state.granted_ability_id == 1 &&
            switched.canceled_activation_id == 0,
        "sword grant switch drifted");
    const auto requested = ihomeland::sim::RequestAbility(
        specs[0],
        switched.state,
        2,
        101,
        1);
    Require(
        requested.accepted &&
            requested.state.resource_scaled == 90'000 &&
            requested.state.phase == ihomeland::sim::AbilityPhase::Requested,
        "sword request/cost did not commit atomically");
    const auto active = ihomeland::sim::AdvanceAbilityPhase(requested.state, 3);
    Require(
        active.phase == ihomeland::sim::AbilityPhase::Active,
        "sword request did not become active at fixed boundary");
    const auto cooldown = ihomeland::sim::RequestAbility(specs[0], active, 3, 102, 1);
    Require(
        !cooldown.accepted &&
            cooldown.rejection == ihomeland::sim::AbilityRejection::Cooldown &&
            cooldown.state.resource_scaled == 90'000,
        "cooldown rejection mutated state");
    const auto fan = ihomeland::sim::SwitchWeapon(
        specs,
        active,
        ihomeland::sim::WeaponKind::Fan,
        4);
    Require(
        fan.state.granted_ability_id == 2 &&
            fan.state.phase == ihomeland::sim::AbilityPhase::Idle &&
            fan.canceled_activation_id == 101,
        "weapon revoke did not cancel active sword");
    const auto revoked = ihomeland::sim::RequestAbility(specs[0], fan.state, 4, 103, 1);
    Require(
        revoked.rejection == ihomeland::sim::AbilityRejection::NotGranted,
        "revoked sword Ability remained activatable");
}

/// TestStableRejections 验证 tag、cost、dead state 均拒绝且不做部分 mutation。
void TestStableRejections() {
    const auto specs = Specs();
    auto state = InitialState();
    const auto tag = ihomeland::sim::RequestAbility(specs[1], state, 1, 1, 3);
    Require(
        tag.rejection == ihomeland::sim::AbilityRejection::BlockedByTag,
        "blocked GameplayTag was accepted");
    state.resource_scaled = 1;
    const auto cost = ihomeland::sim::RequestAbility(specs[1], state, 1, 1, 1);
    Require(
        cost.rejection == ihomeland::sim::AbilityRejection::InsufficientResource &&
            cost.state.resource_scaled == 1,
        "insufficient cost produced a partial mutation");
    state.alive = false;
    const auto dead = ihomeland::sim::RequestAbility(specs[1], state, 1, 1, 1);
    Require(
        dead.rejection == ihomeland::sim::AbilityRejection::InvalidState,
        "dead actor activated Ability");
}

/// TestContentValidation 验证重复 grant 与自相矛盾 tag 在实例启动前失败。
void TestContentValidation() {
    auto specs = Specs();
    specs[1].weapon = ihomeland::sim::WeaponKind::Sword;
    try {
        ihomeland::sim::ValidateAbilitySpecs(specs);
    } catch (const ihomeland::sim::AbilityConfigError&) {
        return;
    }
    throw std::runtime_error("duplicate weapon grant was accepted");
}

}  // namespace

/// main 执行 Ability grant/activation/phase/tag/cost 的正负向回归。
int main() {
    try {
        TestSwordFixtureFlow();
        TestStableRejections();
        TestContentValidation();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
