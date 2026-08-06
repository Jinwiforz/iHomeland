#include "ihomeland/sim/transport/battle_session.hpp"

#include "ihomeland/battle/v1/battle.pb.h"
#include "ihomeland/sim/simulation/input_timeline.hpp"

#include <algorithm>
#include <deque>
#include <limits>
#include <mutex>
#include <ranges>
#include <stdexcept>
#include <utility>

namespace ihomeland::sim {
namespace {

constexpr std::uint64_t InputCommandLifetimeTicks = 6;
constexpr std::int32_t MaximumMovePermille = 1'000;
constexpr std::uint32_t PrimaryAbilityId = 1;
constexpr std::uint32_t SecondaryAbilityId = 2;
constexpr std::uint32_t MaximumInteractionSlot = 16;
constexpr std::uint32_t CompleteEntityStateMask = 15;

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
        case BATTLE_INPUT_KIND_SWITCH_WEAPON:
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
                InputCommandLifetimeTicks) {
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
        .expires_at_tick =
            input_tick + InputCommandLifetimeTicks,
        .kind = GameplayCommandKind::JumpPressed,
        .payload = JumpPressedPayload{},
    };
    using namespace ihomeland::battle::v1;
    switch (input.kind()) {
        case BATTLE_INPUT_KIND_MOVE:
            if (input.move_x_milli() <
                    -MaximumMovePermille ||
                input.move_x_milli() >
                    MaximumMovePermille ||
                input.move_y_milli() <
                    -MaximumMovePermille ||
                input.move_y_milli() >
                    MaximumMovePermille) {
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
                .ability_id = PrimaryAbilityId};
            break;
        case BATTLE_INPUT_KIND_SECONDARY_ABILITY:
            command.kind =
                GameplayCommandKind::ActivateAbility;
            command.payload = ActivateAbilityPayload{
                .ability_id = SecondaryAbilityId};
            break;
        case BATTLE_INPUT_KIND_SWITCH_WEAPON:
            command.kind = GameplayCommandKind::SwitchWeapon;
            command.payload = SwitchWeaponPayload{};
            break;
        case BATTLE_INPUT_KIND_INTERACT:
            if (input.interaction_slot() == 0 ||
                input.interaction_slot() >
                    MaximumInteractionSlot) {
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
    output.set_entity_generation(
        state.entity_generation);
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
    transform->set_yaw_millidegrees(
        state.yaw_millidegrees);
    transform->set_velocity_x_mm_per_second(
        static_cast<std::int32_t>(std::clamp(
            state.velocity_x_mm_per_second,
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::min()),
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::max()))));
    transform->set_velocity_y_mm_per_second(
        static_cast<std::int32_t>(std::clamp(
            state.velocity_y_mm_per_second,
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::min()),
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::max()))));
    transform->set_velocity_z_mm_per_second(
        static_cast<std::int32_t>(std::clamp(
            state.velocity_z_mm_per_second,
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
        (state.grounded
             ? BattleEntityStateFlags::Grounded
             : 0U) |
        (state.alive
             ? 0U
             : BattleEntityStateFlags::Dead));
    output.set_archetype_id(state.archetype_id);
    output.set_equipped_weapon_id(
        state.equipped_weapon_id);
    output.set_max_health_milli(
        state.max_health_scaled);
}

/// FillDelta 将只读 simulation state 转为完整替换语义的 delta projection。
void FillDelta(
    ihomeland::battle::v1::BattleEntityDelta& output,
    const StateProjectionToken& state) {
    output.set_entity_id(state.actor_id);
    output.set_entity_generation(
        state.entity_generation);
    output.set_state_mask(CompleteEntityStateMask);
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
    transform->set_yaw_millidegrees(
        state.yaw_millidegrees);
    transform->set_velocity_x_mm_per_second(
        static_cast<std::int32_t>(std::clamp(
            state.velocity_x_mm_per_second,
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::min()),
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::max()))));
    transform->set_velocity_y_mm_per_second(
        static_cast<std::int32_t>(std::clamp(
            state.velocity_y_mm_per_second,
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::min()),
            static_cast<std::int64_t>(
                std::numeric_limits<std::int32_t>::max()))));
    transform->set_velocity_z_mm_per_second(
        static_cast<std::int32_t>(std::clamp(
            state.velocity_z_mm_per_second,
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
        (state.grounded
             ? BattleEntityStateFlags::Grounded
             : 0U) |
        (state.alive
             ? 0U
             : BattleEntityStateFlags::Dead));
    output.set_equipped_weapon_id(
        state.equipped_weapon_id);
}

/// ContentProjectionValid 验证 production actor/weapon mapping 与 health invariant。
[[nodiscard]] bool ContentProjectionValid(
    const StateProjectionToken& state) noexcept {
    const auto player = state.archetype_id == 1U;
    const auto known_archetype =
        state.archetype_id == 1U ||
        state.archetype_id == 2U ||
        state.archetype_id == 3U ||
        state.archetype_id == 301U;
    return state.entity_generation > 0U &&
        known_archetype &&
        state.max_health_scaled > 0U &&
        state.health_scaled >= 0 &&
        static_cast<std::uint64_t>(state.health_scaled) <=
            state.max_health_scaled &&
        (player
             ? state.equipped_weapon_id == 101U ||
                   state.equipped_weapon_id == 102U
             : state.equipped_weapon_id == 0U);
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
    BattleSessionResourceGovernor& resources)
    : context_(&context),
      command_ingress_(&command_ingress),
      resources_(&resources) {}

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
        if (item.lane == BattleReplicationLane::Kcp &&
            next_kcp_application_sequence == 0) {
            ++metrics.rejected;
            context->Authority()->Invalidate(
                BattleSessionInvalidationReason::Protocol);
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
                 1).allowed) {
            ++metrics.rejected;
            context->Authority()->Invalidate(
                BattleSessionInvalidationReason::
                    Backpressure);
            return false;
        }
        if (item.lane == BattleReplicationLane::Kcp) {
            item.application_sequence =
                next_kcp_application_sequence;
            next_kcp_application_sequence =
                next_kcp_application_sequence ==
                        std::numeric_limits<std::uint64_t>::max()
                    ? 0
                    : next_kcp_application_sequence + 1;
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

    /// EnqueueSnapshotBatch 原子替换同 route 的完整 partition set。
    [[nodiscard]] bool EnqueueSnapshotBatch(
        std::vector<BattleReplicationItem> items) {
        std::scoped_lock lock(mutex);
        if (!context->Authority()->Active() ||
            items.empty() ||
            std::ranges::any_of(
                items,
                [](const BattleReplicationItem& item) {
                    return item.lane !=
                               BattleReplicationLane::Raw ||
                        item.payload.empty();
                })) {
            ++metrics.rejected;
            return false;
        }
        const auto message_id =
            items.front().message_id;
        if (std::ranges::any_of(
                items,
                [message_id](
                    const BattleReplicationItem& item) {
                    return item.message_id !=
                        message_id;
                })) {
            ++metrics.rejected;
            return false;
        }
        const auto replaced = static_cast<std::size_t>(
            std::ranges::count_if(
                queue,
                [message_id](
                    const BattleReplicationItem& item) {
                    return item.lane ==
                               BattleReplicationLane::Raw &&
                        item.message_id == message_id;
                }));
        const auto retained = queue.size() - replaced;
        if (items.size() >
            QueueItems - retained) {
            ++metrics.rejected;
            context->Authority()->Invalidate(
                BattleSessionInvalidationReason::
                    Backpressure);
            return false;
        }
        if (items.size() > replaced &&
            !resources->ReserveEgress(
                 items.size() - replaced).allowed) {
            ++metrics.rejected;
            context->Authority()->Invalidate(
                BattleSessionInvalidationReason::
                    Backpressure);
            return false;
        }
        std::erase_if(
            queue,
            [message_id](
                const BattleReplicationItem& item) {
                return item.lane ==
                           BattleReplicationLane::Raw &&
                    item.message_id == message_id;
            });
        if (replaced > items.size()) {
            resources->ReleaseEgress(
                replaced - items.size());
        }
        for (auto& item : items) {
            queue.push_back(std::move(item));
        }
        if (replaced != 0) {
            ++metrics.replaced_snapshots;
        }
        metrics.queued = queue.size();
        metrics.kcp_queued =
            static_cast<std::size_t>(
                std::ranges::count(
                    queue,
                    BattleReplicationLane::Kcp,
                    &BattleReplicationItem::lane));
        return true;
    }

    /// context 是immutable session authority binding。
    const BattleSessionContext* context;
    /// resources 是当前session拥有的node hard budget视图。
    BattleSessionResourceGovernor* resources;
    /// queue 保存最多256个owned items。
    std::deque<BattleReplicationItem> queue;
    /// metrics 保存低敏累计与current usage。
    BattleReplicationMetrics metrics{};
    /// next_kcp_application_sequence 是可靠lane envelope的唯一严格递增owner。
    std::uint64_t next_kcp_application_sequence{1};
    /// mutex 串行化producer与network consumer。
    mutable std::mutex mutex;
};

BattleReplicationQueue::BattleReplicationQueue(
    const BattleSessionContext& context,
    BattleSessionResourceGovernor& resources)
    : impl_(std::make_unique<Impl>()) {
    impl_->context = &context;
    impl_->resources = &resources;
}

BattleReplicationQueue::~BattleReplicationQueue() {
    if (impl_ != nullptr) {
        std::scoped_lock lock(impl_->mutex);
        impl_->resources->ReleaseEgress(
            impl_->queue.size());
    }
}

namespace {

constexpr std::uint32_t FullSnapshotMessageID = 3'002;
constexpr std::uint32_t DeltaSnapshotMessageID = 3'003;
constexpr std::uint32_t AbilityEventMessageID = 3'004;
constexpr std::uint32_t EntityLifecycleMessageID = 3'005;
constexpr std::uint32_t ResyncResponseMessageID = 3'007;
constexpr std::uint64_t MicrosecondsPerMillisecond = 1'000;

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

/// RawReplicationDeadline 从 immutable raw route 构造 producer queue 绝对期限。
[[nodiscard]] std::optional<std::uint64_t>
RawReplicationDeadline(
    const BattleRawRoutePolicy& policy,
    const std::uint64_t tick,
    const std::uint64_t sequence,
    const std::uint64_t now_unix_ms) noexcept {
    if (policy.direction !=
            BattleRouteDirection::ServerToClient ||
        policy.expiry_microseconds == 0 ||
        policy.expiry_microseconds %
                MicrosecondsPerMillisecond !=
            0) {
        return std::nullopt;
    }
    const auto lifetime =
        policy.expiry_microseconds /
        MicrosecondsPerMillisecond;
    if (!ValidReplicationIdentity(
            tick,
            sequence,
            now_unix_ms,
            lifetime)) {
        return std::nullopt;
    }
    return now_unix_ms + lifetime;
}

/// KcpReplicationDeadline 从 immutable KCP route 构造 producer queue 绝对期限。
[[nodiscard]] std::optional<std::uint64_t>
KcpReplicationDeadline(
    const std::uint32_t message_id,
    const std::uint64_t tick,
    const std::uint64_t sequence,
    const std::uint64_t now_unix_ms) noexcept {
    const auto* policy =
        FindBattleKcpRoutePolicy(message_id);
    if (policy == nullptr ||
        policy->direction !=
            BattleRouteDirection::ServerToClient ||
        !ValidReplicationIdentity(
            tick,
            sequence,
            now_unix_ms,
            policy->expiry_milliseconds)) {
        return std::nullopt;
    }
    return now_unix_ms +
        policy->expiry_milliseconds;
}

/// BuildSnapshotPartitions 以实际 Protobuf encoded size 切分有界 state 集合。
template <
    typename Snapshot,
    typename CreateSnapshot,
    typename AddState,
    typename RemoveLast,
    typename StateCount>
[[nodiscard]] std::optional<std::vector<Snapshot>>
BuildSnapshotPartitions(
    const std::span<const StateProjectionToken> states,
    const std::size_t maximum_payload_bytes,
    CreateSnapshot create_snapshot,
    AddState add_state,
    RemoveLast remove_last,
    StateCount state_count) {
    if (states.empty() ||
        maximum_payload_bytes == 0) {
        return std::nullopt;
    }
    std::vector<Snapshot> partitions;
    partitions.reserve(
        BattleReplicationQueue::
            SnapshotMaximumPartitions);
    auto current = create_snapshot();
    for (const auto& state : states) {
        add_state(current, state);
        if (current.ByteSizeLong() <=
            maximum_payload_bytes) {
            continue;
        }
        remove_last(current);
        if (state_count(current) == 0 ||
            partitions.size() >=
                BattleReplicationQueue::
                    SnapshotMaximumPartitions) {
            return std::nullopt;
        }
        partitions.push_back(std::move(current));
        current = create_snapshot();
        add_state(current, state);
        if (current.ByteSizeLong() >
            maximum_payload_bytes) {
            return std::nullopt;
        }
    }
    if (state_count(current) == 0 ||
        partitions.size() >=
            BattleReplicationQueue::
                SnapshotMaximumPartitions) {
        return std::nullopt;
    }
    partitions.push_back(std::move(current));
    const auto partition_count =
        static_cast<std::uint32_t>(
            partitions.size());
    for (std::size_t index = 0;
         index < partitions.size();
         ++index) {
        partitions[index].set_partition_index(
            static_cast<std::uint32_t>(index));
        partitions[index].set_partition_count(
            partition_count);
        if (partitions[index].ByteSizeLong() >
            maximum_payload_bytes) {
            return std::nullopt;
        }
    }
    return partitions;
}

}  // namespace

bool BattleReplicationQueue::QueueFullSnapshot(
    const std::uint64_t server_tick,
    const std::uint64_t snapshot_sequence,
    const std::uint64_t baseline_id,
    const InputAcknowledgementProjection&
        acknowledgement,
    const std::span<const StateProjectionToken> states,
    const std::uint64_t now_unix_ms) {
    const auto* policy =
        FindBattleRawRoutePolicy(
            FullSnapshotMessageID);
    const auto deadline = policy == nullptr ?
        std::optional<std::uint64_t>{} :
        RawReplicationDeadline(
            *policy,
            server_tick,
            snapshot_sequence,
            now_unix_ms);
    if (!deadline.has_value() ||
        baseline_id == 0 || states.empty() ||
        std::ranges::any_of(
            states,
            [](const StateProjectionToken& state) {
                return state.actor_id == 0 ||
                    !ContentProjectionValid(state) ||
                    state.yaw_millidegrees <
                        -180'000 ||
                    state.yaw_millidegrees >=
                        180'000 ||
                    (state.phase &
                     ~BattleEntityStateFlags::
                         PhaseMask) != 0;
            }) ||
        acknowledgement.actor_id !=
            impl_->context->Actor().actor_id ||
        acknowledgement.mapping_generation !=
            impl_->context->MappingGeneration()) {
        return false;
    }
    using Snapshot =
        ihomeland::battle::v1::
            BattleFullSnapshot;
    const auto partitions =
        BuildSnapshotPartitions<Snapshot>(
            states,
            policy->maximum_payload_bytes,
            [&]() {
                Snapshot message;
                message.set_server_tick(server_tick);
                message.set_snapshot_sequence(
                    snapshot_sequence);
                message.set_baseline_id(baseline_id);
                message.set_partition_index(0);
                message.set_partition_count(
                    static_cast<std::uint32_t>(
                        SnapshotMaximumPartitions));
                message.set_last_processed_input_tick(
                    acknowledgement
                        .last_processed_input_tick);
                return message;
            },
            [](Snapshot& message,
               const StateProjectionToken& state) {
                FillState(
                    *message.add_entities(),
                    state);
            },
            [](Snapshot& message) {
                message.mutable_entities()->
                    RemoveLast();
            },
            [](const Snapshot& message) {
                return message.entities_size();
            });
    if (!partitions.has_value()) {
        return false;
    }
    std::vector<BattleReplicationItem> items;
    items.reserve(partitions->size());
    for (const auto& message : *partitions) {
        items.push_back({
            .lane = BattleReplicationLane::Raw,
            .message_id = FullSnapshotMessageID,
            .application_sequence =
                snapshot_sequence,
            .application_tick = server_tick,
            .partition_index =
                static_cast<std::uint8_t>(
                    message.partition_index()),
            .partition_count =
                static_cast<std::uint8_t>(
                    message.partition_count()),
            .expires_at_unix_ms = *deadline,
            .payload = Serialize(message),
        });
    }
    return impl_->EnqueueSnapshotBatch(
        std::move(items));
}

bool BattleReplicationQueue::QueueDeltaSnapshot(
    const std::uint64_t server_tick,
    const std::uint64_t snapshot_sequence,
    const std::uint64_t baseline_id,
    const InputAcknowledgementProjection&
        acknowledgement,
    const std::span<const StateProjectionToken> states,
    const std::uint64_t now_unix_ms) {
    const auto* policy =
        FindBattleRawRoutePolicy(
            DeltaSnapshotMessageID);
    const auto deadline = policy == nullptr ?
        std::optional<std::uint64_t>{} :
        RawReplicationDeadline(
            *policy,
            server_tick,
            snapshot_sequence,
            now_unix_ms);
    if (!deadline.has_value() ||
        baseline_id == 0 || states.empty() ||
        std::ranges::any_of(
            states,
            [](const StateProjectionToken& state) {
                return state.actor_id == 0 ||
                    !ContentProjectionValid(state) ||
                    state.yaw_millidegrees <
                        -180'000 ||
                    state.yaw_millidegrees >=
                        180'000 ||
                    (state.phase &
                     ~BattleEntityStateFlags::
                         PhaseMask) != 0;
            }) ||
        acknowledgement.actor_id !=
            impl_->context->Actor().actor_id ||
        acknowledgement.mapping_generation !=
            impl_->context->MappingGeneration()) {
        return false;
    }
    using Snapshot =
        ihomeland::battle::v1::
            BattleDeltaSnapshot;
    const auto partitions =
        BuildSnapshotPartitions<Snapshot>(
            states,
            policy->maximum_payload_bytes,
            [&]() {
                Snapshot message;
                message.set_server_tick(server_tick);
                message.set_snapshot_sequence(
                    snapshot_sequence);
                message.set_baseline_id(baseline_id);
                message.set_partition_index(0);
                message.set_partition_count(
                    static_cast<std::uint32_t>(
                        SnapshotMaximumPartitions));
                message.set_last_processed_input_tick(
                    acknowledgement
                        .last_processed_input_tick);
                return message;
            },
            [](Snapshot& message,
               const StateProjectionToken& state) {
                FillDelta(
                    *message.add_deltas(),
                    state);
            },
            [](Snapshot& message) {
                message.mutable_deltas()->
                    RemoveLast();
            },
            [](const Snapshot& message) {
                return message.deltas_size();
            });
    if (!partitions.has_value()) {
        return false;
    }
    std::vector<BattleReplicationItem> items;
    items.reserve(partitions->size());
    for (const auto& message : *partitions) {
        items.push_back({
            .lane = BattleReplicationLane::Raw,
            .message_id = DeltaSnapshotMessageID,
            .application_sequence =
                snapshot_sequence,
            .application_tick = server_tick,
            .partition_index =
                static_cast<std::uint8_t>(
                    message.partition_index()),
            .partition_count =
                static_cast<std::uint8_t>(
                    message.partition_count()),
            .expires_at_unix_ms = *deadline,
            .payload = Serialize(message),
        });
    }
    return impl_->EnqueueSnapshotBatch(
        std::move(items));
}

bool BattleReplicationQueue::QueueAbilityEvent(
    const CombatAbilityEvent& event,
    const std::uint64_t now_unix_ms) {
    const auto deadline =
        KcpReplicationDeadline(
            AbilityEventMessageID,
            event.tick,
            event.event_sequence,
            now_unix_ms);
    if (!deadline.has_value() ||
        event.event_sequence == 0 ||
        event.source_actor_id == 0 ||
        event.source_generation == 0 ||
        event.ability_id == 0 ||
        event.activation_id == 0 ||
        event.phase < CombatAbilityEventPhase::Started ||
        event.phase > CombatAbilityEventPhase::Cancelled ||
        !std::ranges::is_sorted(event.target_actor_ids) ||
        std::ranges::adjacent_find(event.target_actor_ids) !=
            event.target_actor_ids.end() ||
        std::ranges::any_of(
            event.target_actor_ids,
            [](const std::uint64_t target) {
                return target == 0;
            })) {
        return false;
    }
    ihomeland::battle::v1::
        BattleAbilityReliableEvent message;
    message.set_event_id(event.event_sequence);
    message.set_server_tick(event.tick);
    message.set_source_entity_id(
        event.source_actor_id);
    message.set_source_entity_generation(
        event.source_generation);
    message.set_ability_id(event.ability_id);
    message.set_phase(
        static_cast<ihomeland::battle::v1::
            BattleAbilityPhase>(event.phase));
    for (const auto target_actor_id :
         event.target_actor_ids) {
        message.add_target_entity_ids(
            target_actor_id);
    }
    return impl_->Enqueue(
        {
            .lane = BattleReplicationLane::Kcp,
            .message_id = AbilityEventMessageID,
            .application_sequence =
                event.event_sequence,
            .application_tick = event.tick,
            .expires_at_unix_ms = *deadline,
            .payload = Serialize(message),
        },
        false);
}

bool BattleReplicationQueue::QueueEntityLifecycle(
    const CombatLifecycleEvent& event,
    const std::uint64_t now_unix_ms) {
    const auto deadline =
        KcpReplicationDeadline(
            EntityLifecycleMessageID,
            event.tick,
            event.event_sequence,
            now_unix_ms);
    const auto kind = static_cast<std::uint32_t>(event.kind);
    if (!deadline.has_value() ||
        event.event_sequence == 0 ||
        event.entity_id == 0 ||
        event.entity_generation == 0 ||
        kind < 1 || kind > 2 ||
        (kind == 1 &&
         (event.archetype_id == 0 ||
          !event.initial_state.has_value() ||
          event.initial_state->actor_id != event.entity_id ||
          event.initial_state->archetype_id != event.archetype_id ||
          !ContentProjectionValid(*event.initial_state))) ||
        (kind == 2 && event.initial_state.has_value())) {
        return false;
    }
    ihomeland::battle::v1::
        BattleEntityLifecycle message;
    message.set_event_id(event.event_sequence);
    message.set_server_tick(event.tick);
    message.set_entity_id(event.entity_id);
    message.set_entity_generation(
        event.entity_generation);
    message.set_kind(
        static_cast<ihomeland::battle::v1::
            BattleEntityLifecycleKind>(kind));
    message.set_archetype_id(
        kind == 1 ? event.archetype_id : 0);
    if (kind == 1) {
        FillState(
            *message.mutable_initial_state(),
            *event.initial_state);
    }
    return impl_->Enqueue(
        {
            .lane = BattleReplicationLane::Kcp,
            .message_id = EntityLifecycleMessageID,
            .application_sequence =
                event.event_sequence,
            .application_tick = event.tick,
            .expires_at_unix_ms = *deadline,
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
    const auto deadline =
        KcpReplicationDeadline(
            ResyncResponseMessageID,
            server_tick,
            request_sequence,
            now_unix_ms);
    if (!deadline.has_value() ||
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
    message.set_retry_after_ms(0);
    return impl_->Enqueue(
        {
            .lane = BattleReplicationLane::Kcp,
            .message_id = ResyncResponseMessageID,
            .application_sequence =
                request_sequence,
            .application_tick = server_tick,
            .expires_at_unix_ms = *deadline,
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
