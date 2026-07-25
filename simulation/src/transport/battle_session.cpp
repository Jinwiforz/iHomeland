#include "ihomeland/sim/transport/battle_session.hpp"

#include "ihomeland/battle/v1/battle.pb.h"

#include <algorithm>
#include <deque>
#include <limits>
#include <mutex>
#include <ranges>
#include <stdexcept>
#include <utility>

namespace ihomeland::sim {
namespace {

/// NonEmpty 验证authority string不为空。
[[nodiscard]] bool NonEmpty(
    const std::string& value) noexcept {
    return !value.empty();
}

/// Serialize 把typed lite message转换为owned bytes。
template <typename Message>
[[nodiscard]] std::vector<std::uint8_t> Serialize(
    const Message& message) {
    const auto encoded = message.SerializeAsString();
    return {
        reinterpret_cast<const std::uint8_t*>(
            encoded.data()),
        reinterpret_cast<const std::uint8_t*>(
            encoded.data()) +
            encoded.size(),
    };
}

/// HasUnexpectedInputFields 按kind拒绝未登记参数组合。
[[nodiscard]] bool HasUnexpectedInputFields(
    const ihomeland::battle::v1::BattleInputCommand&
        command) noexcept {
    using namespace ihomeland::battle::v1;
    const auto move =
        command.has_move_x_milli() ||
        command.has_move_y_milli();
    const auto aim =
        command.has_aim_yaw_millidegrees() ||
        command.has_aim_pitch_millidegrees();
    const auto interact =
        command.has_interaction_slot();
    switch (command.kind()) {
        case BATTLE_INPUT_KIND_MOVE:
            return !command.has_move_x_milli() ||
                   !command.has_move_y_milli() ||
                   aim || interact;
        case BATTLE_INPUT_KIND_AIM:
            return move ||
                   !command.has_aim_yaw_millidegrees() ||
                   !command.has_aim_pitch_millidegrees() ||
                   interact;
        case BATTLE_INPUT_KIND_JUMP:
        case BATTLE_INPUT_KIND_PRIMARY_ABILITY:
        case BATTLE_INPUT_KIND_SECONDARY_ABILITY:
            return move || aim || interact;
        case BATTLE_INPUT_KIND_INTERACT:
            return move || aim || !interact;
        default:
            return true;
    }
}

/// ToGameplayCommand 只从context取得actor/assignment并映射intent-only payload。
[[nodiscard]] std::optional<GameplayCommand>
ToGameplayCommand(
    const BattleSessionContext& context,
    const ihomeland::battle::v1::BattleInputCommand&
        input,
    const std::uint64_t input_tick) {
    if (input.command_sequence() == 0 ||
        !input.unknown_fields().empty() ||
        HasUnexpectedInputFields(input) ||
        input_tick >
            std::numeric_limits<std::uint64_t>::max() -
                6U) {
        return std::nullopt;
    }
    auto command = GameplayCommand{
        .assignment_fingerprint =
            context.AssignmentFingerprint(),
        .mapping_generation =
            context.MappingGeneration(),
        .actor_id = context.Actor().actor_id,
        .input_tick = input_tick,
        .sequence = input.command_sequence(),
        .expires_at_tick = input_tick + 6U,
        .kind = GameplayCommandKind::JumpPressed,
        .payload = JumpPressedPayload{},
    };
    using namespace ihomeland::battle::v1;
    switch (input.kind()) {
        case BATTLE_INPUT_KIND_MOVE:
            if (input.move_x_milli() < -1'000 ||
                input.move_x_milli() > 1'000 ||
                input.move_y_milli() < -1'000 ||
                input.move_y_milli() > 1'000) {
                return std::nullopt;
            }
            command.kind =
                GameplayCommandKind::
                    ContinuousIntentSample;
            command.payload = ContinuousIntentPayload{
                .move_x_permille =
                    static_cast<std::int16_t>(
                        input.move_x_milli()),
                .move_y_permille =
                    static_cast<std::int16_t>(
                        input.move_y_milli()),
            };
            break;
        case BATTLE_INPUT_KIND_AIM:
            command.kind =
                GameplayCommandKind::AimIntent;
            command.payload = AimIntentPayload{
                .yaw_millidegrees =
                    input.aim_yaw_millidegrees(),
                .pitch_millidegrees =
                    input.aim_pitch_millidegrees(),
            };
            break;
        case BATTLE_INPUT_KIND_JUMP:
            break;
        case BATTLE_INPUT_KIND_PRIMARY_ABILITY:
            command.kind =
                GameplayCommandKind::ActivateAbility;
            command.payload = ActivateAbilityPayload{
                .ability_id = 1};
            break;
        case BATTLE_INPUT_KIND_SECONDARY_ABILITY:
            command.kind =
                GameplayCommandKind::ActivateAbility;
            command.payload = ActivateAbilityPayload{
                .ability_id = 2};
            break;
        case BATTLE_INPUT_KIND_INTERACT:
            if (input.interaction_slot() == 0 ||
                input.interaction_slot() > 16) {
                return std::nullopt;
            }
            command.kind =
                GameplayCommandKind::InteractSlot;
            command.payload = InteractSlotPayload{
                .interaction_slot =
                    static_cast<std::uint8_t>(
                        input.interaction_slot())};
            break;
        default:
            return std::nullopt;
    }
    return command;
}

/// FillState 将只读simulation state量化为wire projection。
void FillState(
    ihomeland::battle::v1::BattleEntityState& output,
    const StateProjectionToken& state) {
    output.set_entity_id(state.actor_id);
    output.set_entity_generation(1);
    auto* transform = output.mutable_transform();
    transform->set_position_x_mm(
        static_cast<std::int32_t>(std::clamp(
            state.x_mm,
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::min()),
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::max()))));
    transform->set_position_y_mm(
        static_cast<std::int32_t>(std::clamp(
            state.y_mm,
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::min()),
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::max()))));
    transform->set_position_z_mm(
        static_cast<std::int32_t>(std::clamp(
            state.z_mm,
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::min()),
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::max()))));
    output.set_health_milli(
        static_cast<std::uint32_t>(std::clamp(
            state.health_scaled,
            std::int64_t{0},
            static_cast<std::int64_t>(
                std::numeric_limits<std::uint32_t>::max()))));
    output.set_state_flags(
        state.phase |
        (state.alive ? 0U : 0x80000000U));
}

}  // namespace

bool BattleSessionAuthority::Active() const noexcept {
    return reason_.load(std::memory_order_acquire) ==
           BattleSessionInvalidationReason::None;
}

void BattleSessionAuthority::Invalidate(
    const BattleSessionInvalidationReason reason) noexcept {
    if (reason ==
        BattleSessionInvalidationReason::None) {
        return;
    }
    auto expected =
        BattleSessionInvalidationReason::None;
    static_cast<void>(reason_.compare_exchange_strong(
        expected,
        reason,
        std::memory_order_acq_rel,
        std::memory_order_acquire));
}

BattleSessionInvalidationReason
BattleSessionAuthority::Reason() const noexcept {
    return reason_.load(std::memory_order_acquire);
}

BattleSessionContext::BattleSessionContext(
    std::string account_session_id,
    const std::uint64_t account_session_epoch,
    const std::uint64_t battle_session_handle,
    const std::uint32_t battle_session_generation,
    const std::uint32_t endpoint_generation,
    std::string assignment_fingerprint,
    std::string simulation_instance_id,
    const std::uint64_t mapping_generation,
    const std::uint64_t target_revision,
    BattleActorBinding actor,
    std::shared_ptr<BattleSessionAuthority> authority)
    : account_session_id_(
          std::move(account_session_id)),
      account_session_epoch_(account_session_epoch),
      battle_session_handle_(battle_session_handle),
      battle_session_generation_(
          battle_session_generation),
      endpoint_generation_(endpoint_generation),
      assignment_fingerprint_(
          std::move(assignment_fingerprint)),
      simulation_instance_id_(
          std::move(simulation_instance_id)),
      mapping_generation_(mapping_generation),
      target_revision_(target_revision),
      actor_(std::move(actor)),
      authority_(std::move(authority)) {
    if (!NonEmpty(account_session_id_) ||
        account_session_epoch_ == 0 ||
        battle_session_handle_ == 0 ||
        battle_session_generation_ == 0 ||
        endpoint_generation_ == 0 ||
        !NonEmpty(assignment_fingerprint_) ||
        !NonEmpty(simulation_instance_id_) ||
        mapping_generation_ == 0 ||
        target_revision_ == 0 ||
        !NonEmpty(actor_.player_id) ||
        actor_.actor_id == 0 ||
        actor_.actor_slot >= 8 ||
        authority_ == nullptr) {
        throw std::invalid_argument(
            "battle session context is invalid");
    }
}

const std::string&
BattleSessionContext::AccountSessionId() const noexcept {
    return account_session_id_;
}

std::uint64_t
BattleSessionContext::AccountSessionEpoch() const noexcept {
    return account_session_epoch_;
}

std::uint64_t
BattleSessionContext::BattleSessionHandle() const noexcept {
    return battle_session_handle_;
}

std::uint32_t
BattleSessionContext::BattleSessionGeneration() const noexcept {
    return battle_session_generation_;
}

std::uint32_t
BattleSessionContext::EndpointGeneration() const noexcept {
    return endpoint_generation_;
}

const std::string&
BattleSessionContext::AssignmentFingerprint() const noexcept {
    return assignment_fingerprint_;
}

const std::string&
BattleSessionContext::SimulationInstanceId() const noexcept {
    return simulation_instance_id_;
}

std::uint64_t
BattleSessionContext::MappingGeneration() const noexcept {
    return mapping_generation_;
}

std::uint64_t
BattleSessionContext::TargetRevision() const noexcept {
    return target_revision_;
}

const BattleActorBinding&
BattleSessionContext::Actor() const noexcept {
    return actor_;
}

const std::shared_ptr<BattleSessionAuthority>&
BattleSessionContext::Authority() const noexcept {
    return authority_;
}

BattleInputIngress::BattleInputIngress(
    const BattleSessionContext& context,
    CommandIngress& command_ingress,
    BattleResourceGovernor& resources,
    const std::uint64_t instance_handle)
    : context_(&context),
      command_ingress_(&command_ingress),
      resources_(&resources),
      instance_handle_(instance_handle) {
    if (instance_handle == 0) {
        throw std::invalid_argument(
            "battle ingress instance handle is zero");
    }
}

BattleIngressResult BattleInputIngress::Handle(
    const BattleRawFrameView& frame,
    const std::uint64_t now_unix_ms) {
    auto result = BattleIngressResult{
        .disposition =
            BattleIngressDisposition::InvalidPayload,
        .accepted_commands = 0,
        .rejected_commands = 0,
        .last_rejection = CommandRejection::None,
    };
    if (!context_->Authority()->Active()) {
        result.disposition =
            BattleIngressDisposition::
                AuthorityRejected;
        return result;
    }
    if (frame.policy == nullptr ||
        frame.policy->message_id != 3000 ||
        frame.policy->direction !=
            BattleRouteDirection::ClientToServer ||
        frame.application_sequence == 0 ||
        frame.application_tick == 0) {
        return result;
    }
    const auto rate = resources_->AllowMessage(
        context_->BattleSessionHandle(),
        instance_handle_,
        3000,
        frame.policy->maximum_rate_per_second,
        now_unix_ms);
    if (!rate.allowed) {
        result.disposition =
            BattleIngressDisposition::RateLimited;
        return result;
    }
    ihomeland::battle::v1::BattleInputBundle bundle;
    if (!bundle.ParseFromArray(
            frame.payload.data(),
            static_cast<int>(frame.payload.size())) ||
        !bundle.unknown_fields().empty() ||
        bundle.newest_input_tick() !=
            frame.application_tick ||
        bundle.latest_observed_server_tick() == 0 ||
        bundle.commands_size() == 0 ||
        bundle.commands_size() > 8) {
        return result;
    }
    std::uint64_t previous_sequence = 0;
    std::vector<GameplayCommand> commands;
    commands.reserve(
        static_cast<std::size_t>(
            bundle.commands_size()));
    for (const auto& input : bundle.commands()) {
        if (input.command_sequence() <=
            previous_sequence) {
            return result;
        }
        previous_sequence =
            input.command_sequence();
        auto command = ToGameplayCommand(
            *context_,
            input,
            bundle.newest_input_tick());
        if (!command.has_value()) {
            return result;
        }
        commands.push_back(std::move(*command));
    }
    for (auto& command : commands) {
        const auto budget = resources_->ReserveIngress(
            context_->BattleSessionHandle(),
            1);
        if (!budget.allowed) {
            ++result.rejected_commands;
            result.last_rejection =
                CommandRejection::Capacity;
            context_->Authority()->Invalidate(
                BattleSessionInvalidationReason::
                    Backpressure);
            result.disposition =
                BattleIngressDisposition::Backpressure;
            break;
        }
        const auto submitted =
            command_ingress_->Submit(
                std::move(command));
        resources_->ReleaseIngress(
            context_->BattleSessionHandle(),
            1);
        if (submitted.accepted) {
            ++result.accepted_commands;
        } else {
            ++result.rejected_commands;
            result.last_rejection =
                submitted.rejection;
        }
    }
    if (result.disposition ==
        BattleIngressDisposition::Backpressure) {
        return result;
    }
    result.disposition =
        result.rejected_commands == 0 ?
        BattleIngressDisposition::Accepted :
        result.accepted_commands == 0 ?
        BattleIngressDisposition::
            SimulationRejected :
        BattleIngressDisposition::Partial;
    return result;
}

/// BattleReplicationQueue::Impl 保存有界egress与低敏metrics。
struct BattleReplicationQueue::Impl final {
    /// Enqueue 验证authority、budget、lane cap并执行snapshot latest-wins。
    [[nodiscard]] bool Enqueue(
        BattleReplicationItem item,
        const bool replace_snapshot) {
        std::scoped_lock lock(mutex);
        if (!context->Authority()->Active() ||
            item.payload.empty()) {
            ++metrics.rejected;
            return false;
        }
        if (replace_snapshot) {
            for (auto iterator = queue.begin();
                 iterator != queue.end();) {
                if (iterator->lane ==
                        BattleReplicationLane::Raw &&
                    iterator->message_id ==
                        item.message_id) {
                    resources->ReleaseEgress(
                        context->BattleSessionHandle(),
                        1);
                    iterator = queue.erase(iterator);
                    ++metrics.replaced_snapshots;
                } else {
                    ++iterator;
                }
            }
        }
        const auto kcp_count = static_cast<std::size_t>(
            std::ranges::count(
                queue,
                BattleReplicationLane::Kcp,
                &BattleReplicationItem::lane));
        if (queue.size() >= QueueItems ||
            (item.lane ==
                 BattleReplicationLane::Kcp &&
             kcp_count >= KcpItems) ||
            !resources->ReserveEgress(
                 context->BattleSessionHandle(),
                 1).allowed) {
            ++metrics.rejected;
            context->Authority()->Invalidate(
                BattleSessionInvalidationReason::
                    Backpressure);
            return false;
        }
        queue.push_back(std::move(item));
        metrics.queued = queue.size();
        metrics.kcp_queued =
            kcp_count +
            (queue.back().lane ==
                     BattleReplicationLane::Kcp ?
                 1U :
                 0U);
        return true;
    }

    /// context 是immutable session authority binding。
    const BattleSessionContext* context;
    /// resources 是node/session hard budget owner。
    BattleResourceGovernor* resources;
    /// queue 保存最多256个owned items。
    std::deque<BattleReplicationItem> queue;
    /// metrics 保存低敏累计与current usage。
    BattleReplicationMetrics metrics{};
    /// mutex 串行化producer与network consumer。
    mutable std::mutex mutex;
};

BattleReplicationQueue::BattleReplicationQueue(
    const BattleSessionContext& context,
    BattleResourceGovernor& resources)
    : impl_(std::make_unique<Impl>()) {
    impl_->context = &context;
    impl_->resources = &resources;
}

BattleReplicationQueue::~BattleReplicationQueue() {
    if (impl_ != nullptr) {
        std::scoped_lock lock(impl_->mutex);
        impl_->resources->ReleaseEgress(
            impl_->context->BattleSessionHandle(),
            impl_->queue.size());
    }
}

namespace {

/// ValidReplicationIdentity 验证通用非零tick/sequence/deadline input。
[[nodiscard]] bool ValidReplicationIdentity(
    const std::uint64_t tick,
    const std::uint64_t sequence,
    const std::uint64_t now_unix_ms,
    const std::uint64_t lifetime) noexcept {
    return tick != 0 && sequence != 0 &&
           now_unix_ms != 0 &&
           now_unix_ms <=
               std::numeric_limits<std::uint64_t>::max() -
                   lifetime;
}

}  // namespace

bool BattleReplicationQueue::QueueFullSnapshot(
    const std::uint64_t server_tick,
    const std::uint64_t snapshot_sequence,
    const std::uint64_t baseline_id,
    const std::span<const StateProjectionToken> states,
    const std::uint64_t now_unix_ms) {
    if (!ValidReplicationIdentity(
            server_tick,
            snapshot_sequence,
            now_unix_ms,
            500) ||
        baseline_id == 0 || states.empty()) {
        return false;
    }
    ihomeland::battle::v1::BattleFullSnapshot message;
    message.set_server_tick(server_tick);
    message.set_snapshot_sequence(snapshot_sequence);
    message.set_baseline_id(baseline_id);
    message.set_partition_index(0);
    message.set_partition_count(1);
    for (const auto& state : states) {
        if (state.actor_id == 0) {
            return false;
        }
        FillState(*message.add_entities(), state);
    }
    auto payload = Serialize(message);
    const auto* policy =
        FindBattleRawRoutePolicy(3002);
    if (policy == nullptr ||
        payload.size() >
            policy->maximum_payload_bytes) {
        return false;
    }
    return impl_->Enqueue(
        {
            .lane = BattleReplicationLane::Raw,
            .message_id = 3002,
            .application_sequence =
                snapshot_sequence,
            .application_tick = server_tick,
            .expires_at_unix_ms =
                now_unix_ms + 500,
            .payload = std::move(payload),
        },
        true);
}

bool BattleReplicationQueue::QueueDeltaSnapshot(
    const std::uint64_t server_tick,
    const std::uint64_t snapshot_sequence,
    const std::uint64_t baseline_id,
    const std::span<const StateProjectionToken> states,
    const std::uint64_t now_unix_ms) {
    if (!ValidReplicationIdentity(
            server_tick,
            snapshot_sequence,
            now_unix_ms,
            300) ||
        baseline_id == 0 || states.empty()) {
        return false;
    }
    ihomeland::battle::v1::BattleDeltaSnapshot message;
    message.set_server_tick(server_tick);
    message.set_snapshot_sequence(snapshot_sequence);
    message.set_baseline_id(baseline_id);
    message.set_partition_index(0);
    message.set_partition_count(1);
    for (const auto& state : states) {
        if (state.actor_id == 0) {
            return false;
        }
        auto* delta = message.add_deltas();
        delta->set_entity_id(state.actor_id);
        delta->set_entity_generation(1);
        delta->set_state_mask(7);
        auto* transform =
            delta->mutable_transform();
        transform->set_position_x_mm(
            static_cast<std::int32_t>(
                std::clamp(
                    state.x_mm,
                    static_cast<std::int64_t>(
                        std::numeric_limits<
                            std::int32_t>::min()),
                    static_cast<std::int64_t>(
                        std::numeric_limits<
                            std::int32_t>::max()))));
        transform->set_position_y_mm(
            static_cast<std::int32_t>(
                std::clamp(
                    state.y_mm,
                    static_cast<std::int64_t>(
                        std::numeric_limits<
                            std::int32_t>::min()),
                    static_cast<std::int64_t>(
                        std::numeric_limits<
                            std::int32_t>::max()))));
        transform->set_position_z_mm(
            static_cast<std::int32_t>(
                std::clamp(
                    state.z_mm,
                    static_cast<std::int64_t>(
                        std::numeric_limits<
                            std::int32_t>::min()),
                    static_cast<std::int64_t>(
                        std::numeric_limits<
                            std::int32_t>::max()))));
        delta->set_health_milli(
            static_cast<std::uint32_t>(
                std::clamp(
                    state.health_scaled,
                    std::int64_t{0},
                    static_cast<std::int64_t>(
                        std::numeric_limits<
                            std::uint32_t>::max()))));
        delta->set_state_flags(
            state.phase |
            (state.alive ? 0U : 0x80000000U));
    }
    auto payload = Serialize(message);
    const auto* policy =
        FindBattleRawRoutePolicy(3003);
    if (policy == nullptr ||
        payload.size() >
            policy->maximum_payload_bytes) {
        return false;
    }
    return impl_->Enqueue(
        {
            .lane = BattleReplicationLane::Raw,
            .message_id = 3003,
            .application_sequence =
                snapshot_sequence,
            .application_tick = server_tick,
            .expires_at_unix_ms =
                now_unix_ms + 300,
            .payload = std::move(payload),
        },
        true);
}

bool BattleReplicationQueue::QueueAbilityEvent(
    const EventProjectionToken& event,
    const std::uint32_t source_generation,
    const std::uint32_t ability_id,
    const std::uint32_t phase,
    const std::uint64_t now_unix_ms) {
    if (!ValidReplicationIdentity(
            event.tick,
            event.activation_id,
            now_unix_ms,
            500) ||
        event.source_actor_id == 0 ||
        source_generation == 0 ||
        ability_id == 0 || phase < 1 || phase > 4) {
        return false;
    }
    ihomeland::battle::v1::
        BattleAbilityReliableEvent message;
    message.set_event_id(event.activation_id);
    message.set_server_tick(event.tick);
    message.set_source_entity_id(
        event.source_actor_id);
    message.set_source_entity_generation(
        source_generation);
    message.set_ability_id(ability_id);
    message.set_phase(
        static_cast<ihomeland::battle::v1::
            BattleAbilityPhase>(phase));
    if (event.target_actor_id != 0) {
        message.add_target_entity_ids(
            event.target_actor_id);
    }
    return impl_->Enqueue(
        {
            .lane = BattleReplicationLane::Kcp,
            .message_id = 3004,
            .application_sequence =
                event.activation_id,
            .application_tick = event.tick,
            .expires_at_unix_ms =
                now_unix_ms + 500,
            .payload = Serialize(message),
        },
        false);
}

bool BattleReplicationQueue::QueueEntityLifecycle(
    const EventProjectionToken& event,
    const std::uint32_t entity_generation,
    const std::uint32_t kind,
    const std::uint32_t archetype_id,
    const std::uint64_t now_unix_ms) {
    if (!ValidReplicationIdentity(
            event.tick,
            event.activation_id,
            now_unix_ms,
            500) ||
        event.source_actor_id == 0 ||
        entity_generation == 0 ||
        kind < 1 || kind > 2 ||
        (kind == 1 && archetype_id == 0) ||
        (kind == 2 && archetype_id != 0)) {
        return false;
    }
    ihomeland::battle::v1::
        BattleEntityLifecycle message;
    message.set_event_id(event.activation_id);
    message.set_server_tick(event.tick);
    message.set_entity_id(event.source_actor_id);
    message.set_entity_generation(
        entity_generation);
    message.set_kind(
        static_cast<ihomeland::battle::v1::
            BattleEntityLifecycleKind>(kind));
    message.set_archetype_id(archetype_id);
    return impl_->Enqueue(
        {
            .lane = BattleReplicationLane::Kcp,
            .message_id = 3005,
            .application_sequence =
                event.activation_id,
            .application_tick = event.tick,
            .expires_at_unix_ms =
                now_unix_ms + 500,
            .payload = Serialize(message),
        },
        false);
}

bool BattleReplicationQueue::QueueResyncResponse(
    const std::uint64_t request_sequence,
    const std::uint64_t server_tick,
    const std::uint32_t disposition,
    const std::uint64_t scheduled_baseline_id,
    const std::uint64_t now_unix_ms) {
    if (!ValidReplicationIdentity(
            server_tick,
            request_sequence,
            now_unix_ms,
            500) ||
        disposition < 1 || disposition > 3 ||
        (disposition == 1 &&
         scheduled_baseline_id == 0) ||
        (disposition != 1 &&
         scheduled_baseline_id != 0)) {
        return false;
    }
    ihomeland::battle::v1::BattleResyncResponse message;
    message.set_request_sequence(request_sequence);
    message.set_server_tick(server_tick);
    message.set_disposition(
        static_cast<ihomeland::battle::v1::
            BattleResyncDisposition>(disposition));
    message.set_scheduled_baseline_id(
        scheduled_baseline_id);
    return impl_->Enqueue(
        {
            .lane = BattleReplicationLane::Kcp,
            .message_id = 3007,
            .application_sequence =
                request_sequence,
            .application_tick = server_tick,
            .expires_at_unix_ms =
                now_unix_ms + 500,
            .payload = Serialize(message),
        },
        false);
}

std::optional<BattleReplicationItem>
BattleReplicationQueue::Pop(
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(impl_->mutex);
    if (now_unix_ms == 0 ||
        !impl_->context->Authority()->Active()) {
        ++impl_->metrics.rejected;
        return std::nullopt;
    }
    while (!impl_->queue.empty() &&
           now_unix_ms >=
               impl_->queue.front().
                   expires_at_unix_ms) {
        impl_->resources->ReleaseEgress(
            impl_->context->BattleSessionHandle(),
            1);
        impl_->queue.pop_front();
        ++impl_->metrics.expired;
    }
    if (impl_->queue.empty()) {
        impl_->metrics.queued = 0;
        impl_->metrics.kcp_queued = 0;
        return std::nullopt;
    }
    auto item = std::move(impl_->queue.front());
    impl_->queue.pop_front();
    impl_->resources->ReleaseEgress(
        impl_->context->BattleSessionHandle(),
        1);
    impl_->metrics.queued = impl_->queue.size();
    impl_->metrics.kcp_queued =
        static_cast<std::size_t>(
            std::ranges::count(
                impl_->queue,
                BattleReplicationLane::Kcp,
                &BattleReplicationItem::lane));
    return item;
}

BattleReplicationMetrics
BattleReplicationQueue::Metrics() const {
    std::scoped_lock lock(impl_->mutex);
    return impl_->metrics;
}

}  // namespace ihomeland::sim
