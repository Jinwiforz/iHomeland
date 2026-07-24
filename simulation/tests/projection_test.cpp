#include "ihomeland/sim/gameplay/projection.hpp"

#include <algorithm>
#include <iostream>
#include <stdexcept>
#include <vector>

namespace {

/// Require 把 canonical projection 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// TestReadOnlyCanonicalProjection 验证 arrival reorder 不改 digest 且输入值不被回写。
void TestReadOnlyCanonicalProjection() {
    const std::vector<ihomeland::sim::StateProjectionToken> states{
        {.actor_id = 2, .x_mm = 20, .y_mm = 0, .z_mm = 0, .health_scaled = 90, .phase = 1, .alive = true},
        {.actor_id = 1, .x_mm = 10, .y_mm = 0, .z_mm = 0, .health_scaled = 0, .phase = 2, .alive = false}};
    const std::vector<ihomeland::sim::EventProjectionToken> events{
        {.tick = 2, .kind = 2, .source_actor_id = 2, .target_actor_id = 1, .activation_id = 9, .value_scaled = -10},
        {.tick = 1, .kind = 1, .source_actor_id = 1, .target_actor_id = 0, .activation_id = 0, .value_scaled = 0}};
    const std::vector<ihomeland::sim::RejectionProjectionToken> rejections{
        {.tick = 2, .actor_id = 2, .input_tick = 4, .sequence = 2, .reason = 3}};
    const std::vector<ihomeland::sim::CapacityProjectionToken> capacities{
        {.kind = 2, .limit = 64, .observed = 8, .rejected = 0},
        {.kind = 1, .limit = 8, .observed = 8, .rejected = 1}};
    auto reversed_states = states;
    auto reversed_events = events;
    auto reversed_capacities = capacities;
    std::reverse(reversed_states.begin(), reversed_states.end());
    std::reverse(reversed_events.begin(), reversed_events.end());
    std::reverse(reversed_capacities.begin(), reversed_capacities.end());
    const auto first =
        ihomeland::sim::ProjectCanonicalGameplay(states, events, rejections, capacities);
    const auto second = ihomeland::sim::ProjectCanonicalGameplay(
        reversed_states,
        reversed_events,
        rejections,
        reversed_capacities);
    Require(
        first.text == second.text && first.sha256 == second.sha256 &&
            first.sha256.size() == 64,
        "canonical gameplay projection depends on arrival order");
    Require(
        states.front().actor_id == 2 && events.front().tick == 2 &&
            capacities.front().kind == 2,
        "read-only projection mutated gameplay inputs");
}

}  // namespace

/// main 执行 state/event/rejection/capacity 只读规范投影回归。
int main() {
    try {
        TestReadOnlyCanonicalProjection();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
