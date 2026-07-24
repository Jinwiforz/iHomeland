#include "ihomeland/sim/history/history_ring.hpp"

#include <algorithm>
#include <limits>

namespace ihomeland::sim {
namespace {

/// kMaximumHistoryFrames 是 network profile 冻结的 hard ring 长度。
constexpr std::uint32_t kMaximumHistoryFrames = 16;
/// kMaximumHistoryBytes 是 network profile 冻结的 8 MiB hard target。
constexpr std::size_t kMaximumHistoryBytes = 8U * 1024U * 1024U;
/// kSlotAccountingBytes 保守覆盖 slot identity、optional 标记与 vector owner。
constexpr std::size_t kSlotAccountingBytes =
    sizeof(std::uint64_t) * 5U +
    sizeof(std::vector<HistoryActorProjection>);

/// CheckedReservedBytes 计算 slots 与每帧 actor reserve 的 hard accounting。
[[nodiscard]] std::size_t CheckedReservedBytes(
    const std::uint32_t frames,
    const std::uint32_t actors) {
    if (frames == 0 || frames > kMaximumHistoryFrames || actors == 0) {
        throw HistoryError(HistoryErrorCode::InvalidConfig, "history capacity is invalid");
    }
    const auto slot_bytes =
        static_cast<std::size_t>(frames) * kSlotAccountingBytes;
    if (actors > std::numeric_limits<std::size_t>::max() /
                     sizeof(HistoryActorProjection) / frames) {
        throw HistoryError(HistoryErrorCode::Capacity, "history reserve accounting overflow");
    }
    return slot_bytes +
           static_cast<std::size_t>(frames) * actors * sizeof(HistoryActorProjection);
}

}  // namespace

HistoryError::HistoryError(const HistoryErrorCode code, const char* message)
    : std::runtime_error(message), code_(code) {}

HistoryErrorCode HistoryError::Code() const noexcept {
    return code_;
}

HistoryRing::HistoryRing(
    const std::uint64_t assignment_generation,
    const std::uint64_t mapping_generation,
    const std::uint32_t frame_capacity,
    const std::uint32_t actor_capacity_per_frame,
    const std::size_t byte_limit,
    const std::span<const std::uint64_t> authorized_requesters)
    : assignment_generation_(assignment_generation),
      mapping_generation_(mapping_generation),
      frame_capacity_(frame_capacity),
      actor_capacity_per_frame_(actor_capacity_per_frame),
      byte_limit_(byte_limit),
      reserved_bytes_(CheckedReservedBytes(frame_capacity, actor_capacity_per_frame)),
      slots_(frame_capacity),
      authorized_requesters_(authorized_requesters.begin(), authorized_requesters.end()) {
    if (assignment_generation == 0 || mapping_generation == 0 ||
        frame_capacity == 0 || frame_capacity > kMaximumHistoryFrames ||
        actor_capacity_per_frame == 0 || byte_limit == 0 ||
        byte_limit > kMaximumHistoryBytes || reserved_bytes_ > byte_limit) {
        throw HistoryError(HistoryErrorCode::InvalidConfig, "history configuration is invalid");
    }
    for (auto& slot : slots_) {
        slot.actors.reserve(actor_capacity_per_frame_);
    }
    std::stable_sort(authorized_requesters_.begin(), authorized_requesters_.end());
    if (authorized_requesters_.empty() || authorized_requesters_.front() == 0 ||
        std::adjacent_find(
            authorized_requesters_.begin(),
            authorized_requesters_.end()) != authorized_requesters_.end()) {
        throw HistoryError(HistoryErrorCode::InvalidConfig, "history requester policy is invalid");
    }
}

void HistoryRing::Commit(
    const std::uint64_t tick,
    const std::span<const HistoryActorProjection> actors) {
    if (tick == 0 || tick <= latest_tick_) {
        throw HistoryError(
            HistoryErrorCode::NonMonotonicTick,
            "history commit Tick must increase monotonically");
    }
    if (actors.size() > actor_capacity_per_frame_) {
        throw HistoryError(HistoryErrorCode::Capacity, "history frame actor capacity exceeded");
    }
    for (std::size_t index = 0; index < actors.size(); ++index) {
        if (actors[index].actor_id == 0 ||
            actors[index].hit_volume_id == 0) {
            throw HistoryError(
                HistoryErrorCode::InvalidConfig,
                "history actor projection is invalid or duplicated");
        }
        for (std::size_t previous = 0; previous < index; ++previous) {
            if (actors[previous].actor_id == actors[index].actor_id) {
                throw HistoryError(
                    HistoryErrorCode::InvalidConfig,
                    "history actor projection is invalid or duplicated");
            }
        }
    }

    const auto slot_index =
        static_cast<std::size_t>(tick % frame_capacity_);
    auto& slot = slots_[slot_index];
    if (slot.identity) {
        used_actor_count_ -= slot.actors.size();
    } else {
        ++committed_frames_;
    }
    slot.actors.assign(actors.begin(), actors.end());
    std::stable_sort(
        slot.actors.begin(),
        slot.actors.end(),
        [](const auto& left, const auto& right) {
            return left.actor_id < right.actor_id;
        });
    used_actor_count_ += slot.actors.size();
    ++write_generation_;
    slot.identity = SlotIdentity{
        .assignment_generation = assignment_generation_,
        .mapping_generation = mapping_generation_,
        .tick = tick,
        .write_generation = write_generation_};
    latest_tick_ = tick;
}

HistoryQueryResult HistoryRing::Query(const HistoryQuery& query) const {
    if (query.requester_actor_id == 0 || query.target_actor_id == 0 ||
        query.requested_tick == 0 || query.current_tick == 0 ||
        query.maximum_lookback_ticks == 0) {
        throw HistoryError(HistoryErrorCode::InvalidQuery, "history query identity is invalid");
    }
    if (query.assignment_generation != assignment_generation_ ||
        query.mapping_generation != mapping_generation_) {
        throw HistoryError(
            HistoryErrorCode::StaleGeneration,
            "history query generation differs from current ring");
    }
    if (query.current_tick > latest_tick_) {
        throw HistoryError(HistoryErrorCode::InvalidQuery, "history current Tick is not committed");
    }
    switch (query.kind) {
        case HistoryQueryKind::MeleeSweep:
        case HistoryQueryKind::ProjectileRay:
            break;
        default:
            throw HistoryError(HistoryErrorCode::InvalidQuery, "history query kind is invalid");
    }
    if (!std::binary_search(
            authorized_requesters_.begin(),
            authorized_requesters_.end(),
            query.requester_actor_id)) {
        throw HistoryError(
            HistoryErrorCode::ActorPolicy,
            "history requester is not authorized");
    }
    const auto effective_tick = std::min(query.requested_tick, query.current_tick);
    if (query.current_tick - effective_tick > query.maximum_lookback_ticks ||
        latest_tick_ - effective_tick >= frame_capacity_) {
        throw HistoryError(HistoryErrorCode::Expired, "history query Tick was evicted");
    }
    const auto& slot =
        slots_[static_cast<std::size_t>(effective_tick % frame_capacity_)];
    if (!slot.identity || slot.identity->tick != effective_tick ||
        slot.identity->assignment_generation != assignment_generation_ ||
        slot.identity->mapping_generation != mapping_generation_) {
        throw HistoryError(HistoryErrorCode::Missing, "history frame is missing");
    }
    const auto actor = std::lower_bound(
        slot.actors.begin(),
        slot.actors.end(),
        query.target_actor_id,
        [](const HistoryActorProjection& projection, const std::uint64_t actor_id) {
            return projection.actor_id < actor_id;
        });
    if (actor == slot.actors.end() || actor->actor_id != query.target_actor_id) {
        throw HistoryError(HistoryErrorCode::Missing, "history actor projection is missing");
    }
    return {
        .effective_tick = effective_tick,
        .future_clamped = query.requested_tick > query.current_tick,
        .actor = *actor};
}

void HistoryRing::Reset(
    const std::uint64_t assignment_generation,
    const std::uint64_t mapping_generation) {
    if (assignment_generation == 0 || mapping_generation == 0) {
        throw HistoryError(HistoryErrorCode::InvalidConfig, "history reset generation is invalid");
    }
    assignment_generation_ = assignment_generation;
    mapping_generation_ = mapping_generation;
    latest_tick_ = 0;
    committed_frames_ = 0;
    used_actor_count_ = 0;
    ++write_generation_;
    for (auto& slot : slots_) {
        slot.identity.reset();
        slot.actors.clear();
    }
}

HistoryCapacitySnapshot HistoryRing::CapacitySnapshot() const noexcept {
    const auto used_bytes =
        static_cast<std::size_t>(committed_frames_) * kSlotAccountingBytes +
        used_actor_count_ * sizeof(HistoryActorProjection);
    return {
        .frame_limit = frame_capacity_,
        .actor_limit_per_frame = actor_capacity_per_frame_,
        .byte_limit = byte_limit_,
        .reserved_bytes = reserved_bytes_,
        .used_bytes = used_bytes,
        .committed_frames = committed_frames_};
}

std::uint64_t HistoryRing::LatestTick() const noexcept {
    return latest_tick_;
}

}  // namespace ihomeland::sim
