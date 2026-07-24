#include "ihomeland/sim/simulation/input_timeline.hpp"

#include "ihomeland/sim/ecs/entity_registry.hpp"

#include <algorithm>
#include <limits>
#include <stdexcept>
#include <tuple>
#include <utility>

namespace ihomeland::sim {

InputTimeline::InputTimeline(
    InputMappingConfig mapping,
    std::vector<std::uint64_t> actor_ids,
    const std::uint32_t continuous_hold_ticks,
    const std::uint32_t gap_expiry_ticks,
    const std::size_t pending_capacity_per_actor)
    : mapping_(mapping),
      continuous_hold_ticks_(continuous_hold_ticks),
      gap_expiry_ticks_(gap_expiry_ticks),
      pending_capacity_per_actor_(pending_capacity_per_actor) {
    if (mapping.generation == 0 || mapping.base_input_tick == 0 ||
        mapping.base_simulation_tick == 0 || mapping.input_step_ns == 0 ||
        mapping.simulation_step_ns == 0 || actor_ids.empty() ||
        pending_capacity_per_actor == 0) {
        throw std::invalid_argument("InputTimeline configuration is invalid");
    }
    std::sort(actor_ids.begin(), actor_ids.end());
    if (actor_ids.front() == 0 ||
        std::adjacent_find(actor_ids.begin(), actor_ids.end()) != actor_ids.end()) {
        throw std::invalid_argument("InputTimeline actor binding is invalid");
    }
    states_.reserve(actor_ids.size());
    for (const auto actor_id : actor_ids) {
        ActorState state{
            .actor_id = actor_id,
            .last_processed_input_tick = mapping_.base_input_tick - 1};
        state.received_input_ticks.reserve(pending_capacity_per_actor_);
        states_.push_back(std::move(state));
    }
}

std::vector<ActorInputResolution> InputTimeline::Resolve(
    const std::uint64_t simulation_tick,
    const std::span<const IngressCommand> commands) {
    std::vector<const IngressCommand*> ordered;
    ordered.reserve(commands.size());
    for (const auto& command : commands) {
        ordered.push_back(&command);
    }
    std::sort(ordered.begin(), ordered.end(), [](const auto* left, const auto* right) {
        return std::tie(
                   left->target_tick,
                   left->actor_id,
                   left->input_tick,
                   left->stable_sequence,
                   left->kind) <
               std::tie(
                   right->target_tick,
                   right->actor_id,
                   right->input_tick,
                   right->stable_sequence,
                   right->kind);
    });

    std::vector<ActorInputResolution> resolutions;
    resolutions.reserve(states_.size());
    for (auto& state : states_) {
        std::vector<std::uint64_t> discrete;
        std::optional<const IngressCommand*> latest_continuous;
        for (const auto* command : ordered) {
            if (command->actor_id != state.actor_id ||
                command->target_tick != simulation_tick) {
                continue;
            }
            if (std::find(
                    state.received_input_ticks.begin(),
                    state.received_input_ticks.end(),
                    command->input_tick) == state.received_input_ticks.end()) {
                if (state.received_input_ticks.size() >= pending_capacity_per_actor_) {
                    throw EcsError(EcsErrorCode::Capacity, "input timeline pending capacity exhausted");
                }
                state.received_input_ticks.push_back(command->input_tick);
            }
            if (command->kind ==
                static_cast<std::uint8_t>(GameplayCommandKind::ContinuousIntentSample)) {
                latest_continuous = command;
            } else {
                discrete.push_back(command->stable_sequence);
            }
        }
        if (latest_continuous) {
            state.last_continuous_payload = (*latest_continuous)->canonical_payload;
            state.last_continuous_tick = simulation_tick;
        }
        std::sort(
            state.received_input_ticks.begin(),
            state.received_input_ticks.end());
        while (state.last_processed_input_tick !=
               std::numeric_limits<std::uint64_t>::max()) {
            const auto next = state.last_processed_input_tick + 1;
            const auto received = std::lower_bound(
                state.received_input_ticks.begin(),
                state.received_input_ticks.end(),
                next);
            if (received != state.received_input_ticks.end() && *received == next) {
                state.received_input_ticks.erase(received);
                state.last_processed_input_tick = next;
                continue;
            }
            const auto mapped = MapInputTick(next);
            if (mapped <= std::numeric_limits<std::uint64_t>::max() - gap_expiry_ticks_ &&
                simulation_tick > mapped + gap_expiry_ticks_) {
                state.last_processed_input_tick = next;
                continue;
            }
            break;
        }

        const bool has_sample = state.last_continuous_payload.has_value();
        const bool within_hold = has_sample &&
                                 simulation_tick >= state.last_continuous_tick &&
                                 simulation_tick - state.last_continuous_tick <=
                                     continuous_hold_ticks_;
        resolutions.push_back({
            .actor_id = state.actor_id,
            .continuous_payload = within_hold ? *state.last_continuous_payload : "0|0|0",
            .held = within_hold && state.last_continuous_tick != simulation_tick,
            .discrete_sequences = std::move(discrete),
            .last_processed_input_tick = state.last_processed_input_tick});
    }
    return resolutions;
}

void InputTimeline::Reset() {
    for (auto& state : states_) {
        state.last_continuous_payload.reset();
        state.last_continuous_tick = 0;
        state.last_processed_input_tick = mapping_.base_input_tick - 1;
        state.received_input_ticks.clear();
    }
}

std::uint64_t InputTimeline::MapInputTick(const std::uint64_t input_tick) const {
    if (input_tick < mapping_.base_input_tick) {
        throw EcsError(EcsErrorCode::Conflict, "InputTick precedes mapping anchor");
    }
    const auto delta = input_tick - mapping_.base_input_tick;
    if (delta > std::numeric_limits<std::uint64_t>::max() / mapping_.input_step_ns) {
        throw EcsError(EcsErrorCode::Conflict, "InputTick mapping overflow");
    }
    const auto offset =
        (delta * mapping_.input_step_ns) / mapping_.simulation_step_ns;
    if (offset > std::numeric_limits<std::uint64_t>::max() -
                     mapping_.base_simulation_tick) {
        throw EcsError(EcsErrorCode::Conflict, "SimulationTick mapping overflow");
    }
    return mapping_.base_simulation_tick + offset;
}

InputTimeline::ActorState& InputTimeline::FindActor(const std::uint64_t actor_id) {
    const auto iterator = std::lower_bound(
        states_.begin(),
        states_.end(),
        actor_id,
        [](const ActorState& state, const std::uint64_t value) {
            return state.actor_id < value;
        });
    if (iterator == states_.end() || iterator->actor_id != actor_id) {
        throw EcsError(EcsErrorCode::CrossWorld, "input timeline actor is not bound");
    }
    return *iterator;
}

}  // namespace ihomeland::sim
