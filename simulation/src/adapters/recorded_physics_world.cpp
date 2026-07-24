#include "ihomeland/sim/fixture/recorded_physics_world.hpp"

#include "ihomeland/sim/gameplay/hit_detection.hpp"

#include <utility>

namespace ihomeland::sim {
namespace {

/// ValidateQuery 拒绝无法稳定关联或没有容量的 PhysicsQuery。
void ValidateQuery(const PhysicsQuery& query) {
    if (query.query_id == 0 || query.tick == 0 || query.maximum_hits == 0) {
        throw RecordedPhysicsError(
            RecordedPhysicsErrorCode::InvalidRecord,
            "recorded PhysicsQuery identity or capacity is invalid");
    }
}

}  // namespace

RecordedPhysicsError::RecordedPhysicsError(
    const RecordedPhysicsErrorCode code,
    const char* message)
    : std::runtime_error(message), code_(code) {}

RecordedPhysicsErrorCode RecordedPhysicsError::Code() const noexcept {
    return code_;
}

RecordedPhysicsWorld::RecordedPhysicsWorld(
    std::vector<RecordedPhysicsExchange> exchanges,
    const std::size_t maximum_exchanges,
    const std::size_t maximum_hits_per_query)
    : exchanges_(std::move(exchanges)),
      maximum_hits_per_query_(maximum_hits_per_query) {
    if (maximum_exchanges == 0 || maximum_hits_per_query == 0 ||
        exchanges_.size() > maximum_exchanges) {
        throw RecordedPhysicsError(
            RecordedPhysicsErrorCode::Capacity,
            "recorded physics trace exceeds hard capacity");
    }
    for (auto& exchange : exchanges_) {
        ValidateQuery(exchange.query);
        if (exchange.hits.size() > maximum_hits_per_query_) {
            throw RecordedPhysicsError(
                RecordedPhysicsErrorCode::Capacity,
                "recorded physics hit set exceeds hard capacity");
        }
        exchange.hits = CanonicalizePhysicsHits(exchange.hits);
    }
}

PhysicsQueryResult RecordedPhysicsWorld::Query(const PhysicsQuery& query) {
    ValidateQuery(query);
    if (next_ == exchanges_.size()) {
        throw RecordedPhysicsError(
            RecordedPhysicsErrorCode::Exhausted,
            "recorded physics trace is exhausted");
    }
    const auto& exchange = exchanges_[next_];
    if (!(exchange.query == query)) {
        throw RecordedPhysicsError(
            RecordedPhysicsErrorCode::QueryOrder,
            "runtime PhysicsQuery differs from the next recorded query");
    }
    if (exchange.hits.size() > query.maximum_hits ||
        exchange.hits.size() > maximum_hits_per_query_) {
        throw RecordedPhysicsError(
            RecordedPhysicsErrorCode::Capacity,
            "recorded physics result exceeds runtime query capacity");
    }
    ++next_;
    return {.query_id = query.query_id, .hits = exchange.hits};
}

void RecordedPhysicsWorld::Reset() noexcept {
    next_ = 0;
}

std::size_t RecordedPhysicsWorld::Consumed() const noexcept {
    return next_;
}

}  // namespace ihomeland::sim
