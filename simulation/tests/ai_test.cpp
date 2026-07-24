#include "ihomeland/sim/gameplay/ai.hpp"

#include <algorithm>
#include <iostream>
#include <stdexcept>
#include <vector>

namespace {

/// Require 把 AI 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// TestTargetTieBreak 验证 threat/distance/ActorID 顺序不依赖输入 arrival。
void TestTargetTieBreak() {
    const std::vector<ihomeland::sim::AiTargetCandidate> candidates{
        {.actor_id = 43, .threat_scaled = 1000, .distance_squared_mm = 25'000'000, .alive = true},
        {.actor_id = 42, .threat_scaled = 1000, .distance_squared_mm = 25'000'000, .alive = true},
        {.actor_id = 41, .threat_scaled = 999, .distance_squared_mm = 1, .alive = true}};
    auto reversed = candidates;
    std::reverse(reversed.begin(), reversed.end());
    Require(
        ihomeland::sim::SelectAiTarget(candidates) == 42 &&
            ihomeland::sim::SelectAiTarget(reversed) == 42,
        "AI target tie-break depends on arrival order");
}

/// TestFiniteState 验证 Acquire/Chase/Attack/Recover/Dead 的固定迁移与 intent 边界。
void TestFiniteState() {
    const auto acquired = ihomeland::sim::DecideAiIntent({
        .current = ihomeland::sim::AiState::Idle,
        .alive = true,
        .has_target = true,
        .target_in_attack_range = false,
        .attack_ready = true,
        .recovery_complete = false});
    Require(
        acquired.next_state == ihomeland::sim::AiState::Acquire &&
            !acquired.request_navigation && !acquired.request_attack,
        "AI Idle did not enter Acquire");
    const auto chase = ihomeland::sim::DecideAiIntent({
        .current = acquired.next_state,
        .alive = true,
        .has_target = true,
        .target_in_attack_range = false,
        .attack_ready = true,
        .recovery_complete = false});
    Require(
        chase.next_state == ihomeland::sim::AiState::Chase &&
            chase.request_navigation && !chase.request_attack,
        "AI Acquire did not produce navigation intent");
    const auto attack = ihomeland::sim::DecideAiIntent({
        .current = chase.next_state,
        .alive = true,
        .has_target = true,
        .target_in_attack_range = true,
        .attack_ready = true,
        .recovery_complete = false});
    Require(
        attack.next_state == ihomeland::sim::AiState::Attack &&
            attack.request_attack && !attack.request_navigation,
        "AI Chase did not produce Ability intent");
    const auto dead = ihomeland::sim::DecideAiIntent({
        .current = attack.next_state,
        .alive = false,
        .has_target = true,
        .target_in_attack_range = true,
        .attack_ready = true,
        .recovery_complete = true});
    Require(dead.next_state == ihomeland::sim::AiState::Dead, "dead AI produced intent");
}

/// TestBossNextTick 验证 threshold detection 不在同 Tick 重入 phase。
void TestBossNextTick() {
    const ihomeland::sim::BossPhaseState phase{
        .current_phase = 1,
        .pending_phase = 0,
        .effective_tick = 0};
    const auto scheduled =
        ihomeland::sim::ScheduleBossPhase(phase, 49'000, 100'000, 2, 50, 11);
    Require(
        scheduled.current_phase == 1 && scheduled.pending_phase == 2 &&
            scheduled.effective_tick == 12,
        "Boss phase threshold did not schedule next Tick");
    Require(
        ihomeland::sim::CommitBossPhase(scheduled, 11).current_phase == 1,
        "Boss phase reentered in detection Tick");
    const auto committed = ihomeland::sim::CommitBossPhase(scheduled, 12);
    Require(
        committed.current_phase == 2 && committed.pending_phase == 0,
        "Boss phase did not commit on next Tick");
}

/// TestEntityLocalRandom 验证一个 entity 多 draw 不扰动另一个 entity stream。
void TestEntityLocalRandom() {
    ihomeland::sim::EntityRandomStream actor_a(100, 1, 7);
    ihomeland::sim::EntityRandomStream actor_b(100, 2, 7);
    ihomeland::sim::EntityRandomStream actor_b_repeated(100, 2, 7);
    static_cast<void>(actor_a.Next());
    static_cast<void>(actor_a.Next());
    Require(
        actor_b.Next() == actor_b_repeated.Next() &&
            actor_b.Next() == actor_b_repeated.Next(),
        "entity-local random stream was coupled to another entity");
}

}  // namespace

/// main 执行 AI target/state/Boss phase/PRNG 的确定性回归。
int main() {
    try {
        TestTargetTieBreak();
        TestFiniteState();
        TestBossNextTick();
        TestEntityLocalRandom();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
