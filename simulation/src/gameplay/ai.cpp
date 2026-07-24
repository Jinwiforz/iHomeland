#include "ihomeland/sim/gameplay/ai.hpp"

#include <algorithm>
#include <limits>
#include <tuple>
#include <vector>

namespace ihomeland::sim {
namespace {

/// Mix64 是固定 SplitMix64 avalanche，用于 identity 与 draw 派生。
[[nodiscard]] std::uint64_t Mix64(std::uint64_t value) noexcept {
    value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9ULL;
    value = (value ^ (value >> 27)) * 0x94d049bb133111ebULL;
    return value ^ (value >> 31);
}

}  // namespace

AiError::AiError(const char* message)
    : std::runtime_error(message) {}

EntityRandomStream::EntityRandomStream(
    const std::uint64_t build_seed,
    const std::uint64_t entity_id,
    const std::uint64_t stream_id)
    : state_(Mix64(build_seed ^ Mix64(entity_id) ^ Mix64(stream_id))) {
    if (entity_id == 0 || stream_id == 0) {
        throw AiError("AI random stream identity must be non-zero");
    }
}

std::uint64_t EntityRandomStream::Next() noexcept {
    state_ += 0x9e3779b97f4a7c15ULL;
    return Mix64(state_);
}

std::uint64_t EntityRandomStream::Uniform(const std::uint64_t exclusive_upper_bound) {
    if (exclusive_upper_bound == 0) {
        throw AiError("AI random upper bound must be non-zero");
    }
    return Next() % exclusive_upper_bound;
}

std::optional<std::uint64_t> SelectAiTarget(
    const std::span<const AiTargetCandidate> candidates) {
    std::vector<AiTargetCandidate> canonical;
    canonical.reserve(candidates.size());
    for (const auto& candidate : candidates) {
        if (candidate.actor_id == 0 || candidate.threat_scaled < 0) {
            throw AiError("AI target candidate is invalid");
        }
        if (candidate.alive) {
            canonical.push_back(candidate);
        }
    }
    std::stable_sort(canonical.begin(), canonical.end(), [](const auto& left, const auto& right) {
        return std::tuple(
                   -left.threat_scaled,
                   left.distance_squared_mm,
                   left.actor_id) <
               std::tuple(
                   -right.threat_scaled,
                   right.distance_squared_mm,
                   right.actor_id);
    });
    if (canonical.empty()) {
        return std::nullopt;
    }
    return canonical.front().actor_id;
}

AiIntent DecideAiIntent(const AiDecisionInput& input) noexcept {
    if (!input.alive || input.current == AiState::Dead) {
        return {.next_state = AiState::Dead, .request_navigation = false, .request_attack = false};
    }
    if (!input.has_target) {
        return {.next_state = AiState::Idle, .request_navigation = false, .request_attack = false};
    }
    switch (input.current) {
        case AiState::Idle:
            return {.next_state = AiState::Acquire, .request_navigation = false, .request_attack = false};
        case AiState::Acquire:
            if (input.target_in_attack_range && input.attack_ready) {
                return {.next_state = AiState::Attack, .request_navigation = false, .request_attack = true};
            }
            return {.next_state = AiState::Chase, .request_navigation = true, .request_attack = false};
        case AiState::Chase:
            if (input.target_in_attack_range && input.attack_ready) {
                return {.next_state = AiState::Attack, .request_navigation = false, .request_attack = true};
            }
            return {.next_state = AiState::Chase, .request_navigation = true, .request_attack = false};
        case AiState::Attack:
            return {.next_state = AiState::Recover, .request_navigation = false, .request_attack = false};
        case AiState::Recover:
            if (!input.recovery_complete) {
                return {.next_state = AiState::Recover, .request_navigation = false, .request_attack = false};
            }
            if (input.target_in_attack_range && input.attack_ready) {
                return {.next_state = AiState::Attack, .request_navigation = false, .request_attack = true};
            }
            return {.next_state = AiState::Chase, .request_navigation = true, .request_attack = false};
        case AiState::Dead:
            return {.next_state = AiState::Dead, .request_navigation = false, .request_attack = false};
    }
    return {.next_state = AiState::Dead, .request_navigation = false, .request_attack = false};
}

BossPhaseState ScheduleBossPhase(
    const BossPhaseState& current,
    const std::int64_t health_scaled,
    const std::int64_t maximum_health_scaled,
    const std::uint8_t requested_phase,
    const std::uint32_t threshold_percent,
    const std::uint64_t tick) {
    if (current.current_phase == 0 || health_scaled < 0 || maximum_health_scaled <= 0 ||
        health_scaled > maximum_health_scaled || requested_phase <= current.current_phase ||
        threshold_percent > 100 || tick == 0 ||
        tick == std::numeric_limits<std::uint64_t>::max()) {
        throw AiError("Boss phase schedule input is invalid");
    }
    const auto threshold_health =
        maximum_health_scaled / 100 * threshold_percent +
        maximum_health_scaled % 100 * threshold_percent / 100;
    if (current.pending_phase != 0 || health_scaled > threshold_health) {
        return current;
    }
    auto next = current;
    next.pending_phase = requested_phase;
    next.effective_tick = tick + 1;
    return next;
}

BossPhaseState CommitBossPhase(
    const BossPhaseState& current,
    const std::uint64_t tick) {
    if (tick == 0 || current.current_phase == 0) {
        throw AiError("Boss phase commit input is invalid");
    }
    if (current.pending_phase == 0 || tick < current.effective_tick) {
        return current;
    }
    return {
        .current_phase = current.pending_phase,
        .pending_phase = 0,
        .effective_tick = 0};
}

}  // namespace ihomeland::sim
