#include "ihomeland/sim/gameplay/projection.hpp"

#include "ihomeland/sim/core/sha256.hpp"

#include <algorithm>
#include <array>
#include <charconv>
#include <stdexcept>
#include <tuple>
#include <vector>

namespace ihomeland::sim {
namespace {

/// AppendInteger 用 std::to_chars 写十进制，禁止 locale 与 stream formatting。
template <typename Integer>
void AppendInteger(std::string& output, const Integer value) {
    std::array<char, 32> buffer{};
    const auto result = std::to_chars(buffer.data(), buffer.data() + buffer.size(), value);
    if (result.ec != std::errc{}) {
        throw std::runtime_error("canonical projection integer encoding failed");
    }
    output.append(buffer.data(), result.ptr);
}

/// AppendSeparator 写入没有转义歧义的单字节 token separator。
void AppendSeparator(std::string& output, const char separator) {
    output.push_back(separator);
}

}  // namespace

CanonicalGameplayProjection ProjectCanonicalGameplay(
    const std::span<const StateProjectionToken> states,
    const std::span<const EventProjectionToken> events,
    const std::span<const RejectionProjectionToken> rejections,
    const std::span<const CapacityProjectionToken> capacities) {
    std::vector<StateProjectionToken> canonical_states(states.begin(), states.end());
    std::stable_sort(canonical_states.begin(), canonical_states.end(), [](const auto& left, const auto& right) {
        return left.actor_id < right.actor_id;
    });
    std::vector<EventProjectionToken> canonical_events(events.begin(), events.end());
    std::stable_sort(canonical_events.begin(), canonical_events.end(), [](const auto& left, const auto& right) {
        return std::tie(
                   left.tick,
                   left.kind,
                   left.source_actor_id,
                   left.target_actor_id,
                   left.activation_id,
                   left.value_scaled) <
               std::tie(
                   right.tick,
                   right.kind,
                   right.source_actor_id,
                   right.target_actor_id,
                   right.activation_id,
                   right.value_scaled);
    });
    std::vector<RejectionProjectionToken> canonical_rejections(
        rejections.begin(),
        rejections.end());
    std::stable_sort(
        canonical_rejections.begin(),
        canonical_rejections.end(),
        [](const auto& left, const auto& right) {
            return std::tie(
                       left.tick,
                       left.actor_id,
                       left.input_tick,
                       left.sequence,
                       left.reason) <
                   std::tie(
                       right.tick,
                       right.actor_id,
                       right.input_tick,
                       right.sequence,
                       right.reason);
        });
    std::vector<CapacityProjectionToken> canonical_capacities(
        capacities.begin(),
        capacities.end());
    std::stable_sort(
        canonical_capacities.begin(),
        canonical_capacities.end(),
        [](const auto& left, const auto& right) {
            return std::tie(left.kind, left.limit, left.observed, left.rejected) <
                   std::tie(right.kind, right.limit, right.observed, right.rejected);
        });

    std::string text;
    text.reserve(
        canonical_states.size() * 112 +
        canonical_events.size() * 64 +
        canonical_rejections.size() * 48 +
        canonical_capacities.size() * 32);
    text.append("S:");
    for (const auto& state : canonical_states) {
        AppendInteger(text, state.actor_id);
        AppendSeparator(text, ',');
        AppendInteger(text, state.x_mm);
        AppendSeparator(text, ',');
        AppendInteger(text, state.y_mm);
        AppendSeparator(text, ',');
        AppendInteger(text, state.z_mm);
        AppendSeparator(text, ',');
        AppendInteger(
            text,
            state.yaw_millidegrees);
        AppendSeparator(text, ',');
        AppendInteger(
            text,
            state.velocity_x_mm_per_second);
        AppendSeparator(text, ',');
        AppendInteger(
            text,
            state.velocity_y_mm_per_second);
        AppendSeparator(text, ',');
        AppendInteger(
            text,
            state.velocity_z_mm_per_second);
        AppendSeparator(text, ',');
        AppendInteger(text, state.health_scaled);
        AppendSeparator(text, ',');
        AppendInteger(text, state.phase);
        AppendSeparator(text, ',');
        AppendInteger(text, state.alive ? 1 : 0);
        AppendSeparator(text, ',');
        AppendInteger(text, state.grounded ? 1 : 0);
        AppendSeparator(text, ';');
    }
    text.append("|E:");
    for (const auto& event : canonical_events) {
        AppendInteger(text, event.tick);
        AppendSeparator(text, ',');
        AppendInteger(text, event.kind);
        AppendSeparator(text, ',');
        AppendInteger(text, event.source_actor_id);
        AppendSeparator(text, ',');
        AppendInteger(text, event.target_actor_id);
        AppendSeparator(text, ',');
        AppendInteger(text, event.activation_id);
        AppendSeparator(text, ',');
        AppendInteger(text, event.value_scaled);
        AppendSeparator(text, ';');
    }
    text.append("|R:");
    for (const auto& rejection : canonical_rejections) {
        AppendInteger(text, rejection.tick);
        AppendSeparator(text, ',');
        AppendInteger(text, rejection.actor_id);
        AppendSeparator(text, ',');
        AppendInteger(text, rejection.input_tick);
        AppendSeparator(text, ',');
        AppendInteger(text, rejection.sequence);
        AppendSeparator(text, ',');
        AppendInteger(text, rejection.reason);
        AppendSeparator(text, ';');
    }
    text.append("|C:");
    for (const auto& capacity : canonical_capacities) {
        AppendInteger(text, capacity.kind);
        AppendSeparator(text, ',');
        AppendInteger(text, capacity.limit);
        AppendSeparator(text, ',');
        AppendInteger(text, capacity.observed);
        AppendSeparator(text, ',');
        AppendInteger(text, capacity.rejected);
        AppendSeparator(text, ';');
    }
    return {.text = text, .sha256 = Sha256Text(text)};
}

}  // namespace ihomeland::sim
