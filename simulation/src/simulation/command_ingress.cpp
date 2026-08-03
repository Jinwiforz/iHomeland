#include "ihomeland/sim/simulation/command_ingress.hpp"

#include "ihomeland/sim/simulation/simulation_instance.hpp"

#include <algorithm>
#include <limits>
#include <stdexcept>
#include <string>
#include <type_traits>
#include <utility>

namespace ihomeland::sim {

CommandIngress::CommandIngress(
    SimulationInstance& instance,
    InputMappingConfig mapping,
    std::vector<std::uint64_t> actor_ids,
    const std::size_t dedupe_capacity,
    std::string assignment_fingerprint)
    : instance_(&instance),
      assignment_fingerprint_(
          assignment_fingerprint.empty()
              ? instance.Identity()
                    .Assignment()
                    .Fingerprint()
              : std::move(
                    assignment_fingerprint)),
      mapping_(mapping),
      actor_ids_(std::move(actor_ids)),
      dedupe_capacity_(dedupe_capacity) {
    if (mapping.generation == 0 || mapping.base_input_tick == 0 ||
        mapping.base_simulation_tick == 0 || mapping.input_step_ns == 0 ||
        mapping.simulation_step_ns == 0 ||
        mapping.simulation_step_ns % mapping.input_step_ns != 0 ||
        dedupe_capacity == 0 ||
        actor_ids_.empty() ||
        assignment_fingerprint_.size() != 64 ||
        !std::ranges::all_of(
            assignment_fingerprint_,
            [](const char value) {
                return (value >= '0' &&
                        value <= '9') ||
                    (value >= 'a' &&
                     value <= 'f');
            })) {
        throw std::invalid_argument("InputMappingConfig or actor binding is invalid");
    }
    std::sort(actor_ids_.begin(), actor_ids_.end());
    if (actor_ids_.front() == 0 ||
        std::adjacent_find(actor_ids_.begin(), actor_ids_.end()) != actor_ids_.end()) {
        throw std::invalid_argument("actor binding contains zero or duplicate identity");
    }
    entries_.reserve(dedupe_capacity_);
}

CommandSubmitResult CommandIngress::Submit(GameplayCommand command) {
    if (command.assignment_fingerprint != assignment_fingerprint_) {
        return {false, CommandRejection::StaleAssignment, 0};
    }
    if (command.mapping_generation != mapping_.generation) {
        return {false, CommandRejection::StaleMapping, 0};
    }
    if (!std::binary_search(actor_ids_.begin(), actor_ids_.end(), command.actor_id)) {
        return {false, CommandRejection::ActorBinding, 0};
    }
    if (command.input_tick < mapping_.base_input_tick || command.expires_at_tick == 0) {
        return {false, CommandRejection::InvalidTick, 0};
    }
    if (command.sequence == 0) {
        return {false, CommandRejection::InvalidSequence, 0};
    }
    if (!ValidatePayload(command)) {
        return {false, CommandRejection::UnsafePayload, 0};
    }
    const auto input_delta = command.input_tick - mapping_.base_input_tick;
    if (input_delta >
        (std::numeric_limits<std::uint64_t>::max() / mapping_.input_step_ns)) {
        return {false, CommandRejection::ArithmeticOverflow, 0};
    }
    const auto offset =
        (input_delta * mapping_.input_step_ns) / mapping_.simulation_step_ns;
    if (offset > std::numeric_limits<std::uint64_t>::max() -
                     mapping_.base_simulation_tick) {
        return {false, CommandRejection::ArithmeticOverflow, 0};
    }
    const auto target_tick = mapping_.base_simulation_tick + offset;
    const auto current_tick = instance_->CommittedTick();
    if (current_tick >
        std::numeric_limits<std::uint64_t>::max() - mapping_.early_window_ticks) {
        return {false, CommandRejection::ArithmeticOverflow, target_tick};
    }
    if (target_tick > current_tick + mapping_.early_window_ticks) {
        return {false, CommandRejection::TooEarly, target_tick};
    }
    if (target_tick >
        std::numeric_limits<std::uint64_t>::max() - mapping_.late_window_ticks) {
        return {false, CommandRejection::ArithmeticOverflow, target_tick};
    }
    if (command.expires_at_tick < target_tick ||
        current_tick > target_tick + mapping_.late_window_ticks ||
        current_tick > command.expires_at_tick) {
        return {false, CommandRejection::Expired, target_tick};
    }

    {
        std::lock_guard lock(mutex_);
        std::erase_if(entries_, [&](const DedupeEntry& entry) {
            return current_tick > entry.target_tick + mapping_.late_window_ticks;
        });
        const auto duplicate = std::find_if(entries_.begin(), entries_.end(), [&](const auto& entry) {
            return entry.actor_id == command.actor_id &&
                   entry.input_tick == command.input_tick &&
                   entry.sequence == command.sequence;
        });
        if (duplicate != entries_.end()) {
            return {false, CommandRejection::Duplicate, target_tick};
        }
        if (entries_.size() >= dedupe_capacity_) {
            return {false, CommandRejection::Capacity, target_tick};
        }

        std::int16_t move_x_permille = 0;
        std::int16_t move_z_permille = 0;
        std::int32_t aim_yaw_millidegrees = 0;
        std::int32_t aim_pitch_millidegrees = 0;
        if (const auto* move =
                std::get_if<ContinuousIntentPayload>(
                    &command.payload)) {
            move_x_permille = move->move_x_permille;
            move_z_permille = move->move_y_permille;
        }
        if (const auto* aim =
                std::get_if<AimIntentPayload>(
                    &command.payload)) {
            aim_yaw_millidegrees =
                aim->yaw_millidegrees;
            aim_pitch_millidegrees =
                aim->pitch_millidegrees;
        }
        IngressCommand validated{
            .target_tick = target_tick,
            .actor_id = command.actor_id,
            .input_tick = command.input_tick,
            .stable_sequence = command.sequence,
            .kind = static_cast<std::uint8_t>(command.kind),
            .move_x_permille = move_x_permille,
            .move_z_permille = move_z_permille,
            .aim_yaw_millidegrees =
                aim_yaw_millidegrees,
            .aim_pitch_millidegrees =
                aim_pitch_millidegrees,
            .canonical_payload = CanonicalPayload(command)};
        const auto result = instance_->SubmitValidated(std::move(validated));
        if (result == InboxPushResult::Capacity) {
            return {false, CommandRejection::Capacity, target_tick};
        }
        if (result == InboxPushResult::Closed) {
            return {false, CommandRejection::InstanceClosed, target_tick};
        }
        entries_.push_back({
            .actor_id = command.actor_id,
            .input_tick = command.input_tick,
            .sequence = command.sequence,
            .target_tick = target_tick});
    }
    return {true, CommandRejection::None, target_tick};
}

bool CommandIngress::ValidatePayload(const GameplayCommand& command) {
    switch (command.kind) {
        case GameplayCommandKind::ContinuousIntentSample:
            if (const auto* payload = std::get_if<ContinuousIntentPayload>(&command.payload)) {
                return payload->move_x_permille >= -1000 && payload->move_x_permille <= 1000 &&
                       payload->move_y_permille >= -1000 && payload->move_y_permille <= 1000;
            }
            return false;
        case GameplayCommandKind::JumpPressed:
            return std::holds_alternative<JumpPressedPayload>(command.payload);
        case GameplayCommandKind::SwitchWeapon:
            if (const auto* payload = std::get_if<SwitchWeaponPayload>(&command.payload)) {
                return payload->weapon_id != 0;
            }
            return false;
        case GameplayCommandKind::ActivateAbility:
            if (const auto* payload = std::get_if<ActivateAbilityPayload>(&command.payload)) {
                return payload->ability_id != 0;
            }
            return false;
        case GameplayCommandKind::LifecycleDirective:
            if (const auto* payload = std::get_if<LifecycleDirectivePayload>(&command.payload)) {
                return payload->directive != 0 && payload->trusted_source;
            }
            return false;
        case GameplayCommandKind::AimIntent:
            if (const auto* payload =
                    std::get_if<AimIntentPayload>(
                        &command.payload)) {
                return payload->yaw_millidegrees >=
                           -180'000 &&
                       payload->yaw_millidegrees <=
                           180'000 &&
                       payload->pitch_millidegrees >=
                           -90'000 &&
                       payload->pitch_millidegrees <=
                           90'000;
            }
            return false;
        case GameplayCommandKind::InteractSlot:
            if (const auto* payload =
                    std::get_if<InteractSlotPayload>(
                        &command.payload)) {
                return payload->interaction_slot >= 1 &&
                       payload->interaction_slot <= 16;
            }
            return false;
    }
    return false;
}

std::string CommandIngress::CanonicalPayload(const GameplayCommand& command) {
    std::string token = std::to_string(static_cast<std::uint8_t>(command.kind));
    std::visit(
        [&](const auto& payload) {
            using Payload = std::decay_t<decltype(payload)>;
            if constexpr (std::is_same_v<Payload, ContinuousIntentPayload>) {
                token += "|" + std::to_string(payload.move_x_permille) +
                         "|" + std::to_string(payload.move_y_permille);
            } else if constexpr (std::is_same_v<Payload, SwitchWeaponPayload>) {
                token += "|" + std::to_string(payload.weapon_id);
            } else if constexpr (std::is_same_v<Payload, ActivateAbilityPayload>) {
                token += "|" + std::to_string(payload.ability_id);
            } else if constexpr (std::is_same_v<Payload, LifecycleDirectivePayload>) {
                token += "|" + std::to_string(payload.directive);
            } else if constexpr (
                std::is_same_v<Payload, AimIntentPayload>) {
                token += "|" +
                         std::to_string(
                             payload.yaw_millidegrees) +
                         "|" +
                         std::to_string(
                             payload.pitch_millidegrees);
            } else if constexpr (
                std::is_same_v<
                    Payload,
                    InteractSlotPayload>) {
                token += "|" +
                         std::to_string(
                             payload.interaction_slot);
            }
        },
        command.payload);
    return token;
}

}  // namespace ihomeland::sim
