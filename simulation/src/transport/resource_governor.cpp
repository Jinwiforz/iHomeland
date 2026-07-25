#include "ihomeland/sim/transport/resource_governor.hpp"

#include <algorithm>
#include <limits>
#include <memory>
#include <mutex>
#include <stdexcept>

namespace ihomeland::sim {
namespace {

/// TokenBucket 使用整数milli-token避免浮点与clock相关漂移。
struct TokenBucket final {
    /// handle 是调用方提供的低敏identity。
    std::uint64_t handle{};
    /// secondary 区分同session的numeric message。
    std::uint32_t secondary{};
    /// tokens_milli 是当前可用额度。
    std::uint64_t tokens_milli{};
    /// last_refill_unix_ms 是最后一次refill时刻。
    std::uint64_t last_refill_unix_ms{};
    /// used 表示fixed slot已占用。
    bool used{false};
};

/// BudgetEntry 保存单session当前有界item数。
struct BudgetEntry final {
    /// handle 是低敏session handle。
    std::uint64_t handle{};
    /// ingress 是当前ingress item数。
    std::size_t ingress{};
    /// egress 是当前egress item数。
    std::size_t egress{};
    /// used 表示fixed slot已占用。
    bool used{false};
};

/// IpBucket 保存exact canonical IP，port不参与pre-auth聚合。
struct IpBucket final {
    /// address 是canonical IPv6或IPv4-mapped bytes。
    std::array<std::uint8_t, 16> address{};
    /// bucket 保存fixed-point token state。
    TokenBucket bucket{};
};

/// Reject 累计稳定原因并返回拒绝。
[[nodiscard]] BattleResourceDecision Reject(
    BattleResourceMetrics& metrics,
    const BattleResourceRejection rejection) noexcept {
    ++metrics.rejected.at(
        static_cast<std::size_t>(rejection));
    return {.allowed = false, .rejection = rejection};
}

/// Consume 执行固定rate/burst token bucket；clock回退fail closed。
[[nodiscard]] bool Consume(
    TokenBucket& bucket,
    const std::uint64_t handle,
    const std::uint32_t secondary,
    const std::uint16_t rate_per_second,
    const std::uint16_t burst,
    const std::uint64_t now_unix_ms) noexcept {
    if (!bucket.used) {
        bucket = {
            .handle = handle,
            .secondary = secondary,
            .tokens_milli =
                static_cast<std::uint64_t>(burst) *
                1'000,
            .last_refill_unix_ms = now_unix_ms,
            .used = true,
        };
    }
    if (now_unix_ms <
        bucket.last_refill_unix_ms) {
        return false;
    }
    const auto elapsed =
        now_unix_ms -
        bucket.last_refill_unix_ms;
    const auto ceiling =
        static_cast<std::uint64_t>(burst) *
        1'000;
    const auto refill =
        elapsed >
                std::numeric_limits<std::uint64_t>::max() /
                    rate_per_second ?
            ceiling :
            elapsed * rate_per_second;
    bucket.tokens_milli = std::min(
        ceiling,
        bucket.tokens_milli + refill);
    bucket.last_refill_unix_ms = now_unix_ms;
    if (bucket.tokens_milli < 1'000) {
        return false;
    }
    bucket.tokens_milli -= 1'000;
    return true;
}

}  // namespace

/// BattleResourceGovernor::Impl 保存固定内存资源状态。
struct BattleResourceGovernor::Impl final {
    /// ip_buckets 是pre-auth唯一可变表。
    std::array<IpBucket, RegistryItems> ip_buckets{};
    /// ticket_buckets 限制credential lookup。
    std::array<TokenBucket, RegistryItems>
        ticket_buckets{};
    /// session_buckets 限制authenticated aggregate。
    std::array<TokenBucket, RegistryItems>
        session_buckets{};
    /// message_buckets 限制session+message。
    std::array<TokenBucket, RegistryItems>
        message_buckets{};
    /// instance_buckets 限制instance aggregate。
    std::array<TokenBucket, RegistryItems>
        instance_buckets{};
    /// budgets 保存单session ingress/egress items。
    std::array<BudgetEntry, RegistryItems> budgets{};
    /// node_ingress 是node当前ingress items。
    std::size_t node_ingress{};
    /// node_egress 是node当前egress items。
    std::size_t node_egress{};
    /// metrics 仅保存低基数累计。
    BattleResourceMetrics metrics{};
    /// mutex 串行化fixed tables与budgets。
    mutable std::mutex mutex;
};

BattleResourceGovernor::BattleResourceGovernor()
    : impl_(std::make_unique<Impl>()) {}

BattleResourceGovernor::~BattleResourceGovernor() =
    default;

BattleResourceDecision
BattleResourceGovernor::AllowPreAuthIp(
    const BattleRemoteEndpoint& remote,
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(impl_->mutex);
    auto* free = static_cast<IpBucket*>(nullptr);
    for (auto& entry : impl_->ip_buckets) {
        if (entry.bucket.used &&
            entry.address == remote.address) {
            if (!Consume(
                    entry.bucket,
                    1,
                    0,
                    20,
                    40,
                    now_unix_ms)) {
                return Reject(
                    impl_->metrics,
                    BattleResourceRejection::IpRate);
            }
            ++impl_->metrics.accepted;
            return {
                .allowed = true,
                .rejection =
                    BattleResourceRejection::None,
            };
        }
        if (!entry.bucket.used && free == nullptr) {
            free = &entry;
        }
    }
    if (free == nullptr || now_unix_ms == 0) {
        return Reject(
            impl_->metrics,
            BattleResourceRejection::
                RegistryCapacity);
    }
    free->address = remote.address;
    static_cast<void>(Consume(
        free->bucket,
        1,
        0,
        20,
        40,
        now_unix_ms));
    ++impl_->metrics.accepted;
    return {
        .allowed = true,
        .rejection = BattleResourceRejection::None,
    };
}

BattleResourceDecision
BattleResourceGovernor::AllowTicket(
    const std::uint64_t ticket_handle,
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(impl_->mutex);
    if (ticket_handle == 0 || now_unix_ms == 0) {
        return Reject(
            impl_->metrics,
            BattleResourceRejection::TicketRate);
    }
    auto* free = static_cast<TokenBucket*>(nullptr);
    for (auto& entry : impl_->ticket_buckets) {
        if (entry.used &&
            entry.handle == ticket_handle) {
            if (!Consume(
                    entry,
                    ticket_handle,
                    0,
                    4,
                    8,
                    now_unix_ms)) {
                return Reject(
                    impl_->metrics,
                    BattleResourceRejection::
                        TicketRate);
            }
            ++impl_->metrics.accepted;
            return {true, BattleResourceRejection::None};
        }
        if (!entry.used && free == nullptr) {
            free = &entry;
        }
    }
    if (free == nullptr) {
        return Reject(
            impl_->metrics,
            BattleResourceRejection::
                RegistryCapacity);
    }
    static_cast<void>(Consume(
        *free,
        ticket_handle,
        0,
        4,
        8,
        now_unix_ms));
    ++impl_->metrics.accepted;
    return {true, BattleResourceRejection::None};
}

BattleResourceDecision
BattleResourceGovernor::AllowMessage(
    const std::uint64_t session_handle,
    const std::uint64_t instance_handle,
    const std::uint32_t message_id,
    const std::uint16_t message_rate_per_second,
    const std::uint64_t now_unix_ms) {
    if (session_handle == 0 || instance_handle == 0 ||
        message_id == 0 ||
        message_rate_per_second == 0 ||
        now_unix_ms == 0) {
        std::scoped_lock lock(impl_->mutex);
        return Reject(
            impl_->metrics,
            BattleResourceRejection::MessageRate);
    }
    std::scoped_lock lock(impl_->mutex);
    const auto consume_table =
        [&](auto& table,
            const std::uint64_t handle,
            const std::uint32_t secondary,
            const std::uint16_t rate,
            const std::uint16_t burst,
            const BattleResourceRejection rejection) {
            TokenBucket* free = nullptr;
            for (auto& entry : table) {
                if (entry.used &&
                    entry.handle == handle &&
                    entry.secondary == secondary) {
                    return Consume(
                               entry,
                               handle,
                               secondary,
                               rate,
                               burst,
                               now_unix_ms) ?
                        BattleResourceRejection::None :
                        rejection;
                }
                if (!entry.used && free == nullptr) {
                    free = &entry;
                }
            }
            if (free == nullptr) {
                return BattleResourceRejection::
                    RegistryCapacity;
            }
            static_cast<void>(Consume(
                *free,
                handle,
                secondary,
                rate,
                burst,
                now_unix_ms));
            return BattleResourceRejection::None;
        };
    const auto session = consume_table(
        impl_->session_buckets,
        session_handle,
        0,
        80,
        160,
        BattleResourceRejection::SessionRate);
    if (session != BattleResourceRejection::None) {
        return Reject(impl_->metrics, session);
    }
    const auto message = consume_table(
        impl_->message_buckets,
        session_handle,
        message_id,
        message_rate_per_second,
        static_cast<std::uint16_t>(
            message_rate_per_second * 2U),
        BattleResourceRejection::MessageRate);
    if (message != BattleResourceRejection::None) {
        return Reject(impl_->metrics, message);
    }
    const auto instance = consume_table(
        impl_->instance_buckets,
        instance_handle,
        0,
        160,
        256,
        BattleResourceRejection::InstanceRate);
    if (instance != BattleResourceRejection::None) {
        return Reject(impl_->metrics, instance);
    }
    ++impl_->metrics.accepted;
    return {true, BattleResourceRejection::None};
}

namespace {

/// FindBudget 返回existing或首个free fixed budget slot。
[[nodiscard]] BudgetEntry* FindBudget(
    std::array<BudgetEntry,
               BattleResourceGovernor::RegistryItems>&
        budgets,
    const std::uint64_t handle) noexcept {
    BudgetEntry* free = nullptr;
    for (auto& entry : budgets) {
        if (entry.used && entry.handle == handle) {
            return &entry;
        }
        if (!entry.used && free == nullptr) {
            free = &entry;
        }
    }
    if (free != nullptr) {
        free->used = true;
        free->handle = handle;
    }
    return free;
}

}  // namespace

BattleResourceDecision
BattleResourceGovernor::ReserveIngress(
    const std::uint64_t session_handle,
    const std::size_t items) {
    std::scoped_lock lock(impl_->mutex);
    auto* budget = session_handle == 0 ?
        nullptr :
        FindBudget(impl_->budgets, session_handle);
    if (budget == nullptr) {
        return Reject(
            impl_->metrics,
            BattleResourceRejection::
                RegistryCapacity);
    }
    if (items == 0 ||
        items > SessionItems - budget->ingress ||
        items > NodeItems - impl_->node_ingress) {
        return Reject(
            impl_->metrics,
            BattleResourceRejection::IngressBudget);
    }
    budget->ingress += items;
    impl_->node_ingress += items;
    impl_->metrics.ingress_high_watermark =
        std::max(
            impl_->metrics.ingress_high_watermark,
            impl_->node_ingress);
    ++impl_->metrics.accepted;
    return {true, BattleResourceRejection::None};
}

void BattleResourceGovernor::ReleaseIngress(
    const std::uint64_t session_handle,
    const std::size_t items) noexcept {
    std::scoped_lock lock(impl_->mutex);
    for (auto& entry : impl_->budgets) {
        if (entry.used &&
            entry.handle == session_handle) {
            const auto released =
                std::min(items, entry.ingress);
            entry.ingress -= released;
            impl_->node_ingress -= released;
            return;
        }
    }
}

BattleResourceDecision
BattleResourceGovernor::ReserveEgress(
    const std::uint64_t session_handle,
    const std::size_t items) {
    std::scoped_lock lock(impl_->mutex);
    auto* budget = session_handle == 0 ?
        nullptr :
        FindBudget(impl_->budgets, session_handle);
    if (budget == nullptr) {
        return Reject(
            impl_->metrics,
            BattleResourceRejection::
                RegistryCapacity);
    }
    if (items == 0 ||
        items > SessionItems - budget->egress ||
        items > NodeItems - impl_->node_egress) {
        return Reject(
            impl_->metrics,
            BattleResourceRejection::EgressBudget);
    }
    budget->egress += items;
    impl_->node_egress += items;
    impl_->metrics.egress_high_watermark =
        std::max(
            impl_->metrics.egress_high_watermark,
            impl_->node_egress);
    ++impl_->metrics.accepted;
    return {true, BattleResourceRejection::None};
}

void BattleResourceGovernor::ReleaseEgress(
    const std::uint64_t session_handle,
    const std::size_t items) noexcept {
    std::scoped_lock lock(impl_->mutex);
    for (auto& entry : impl_->budgets) {
        if (entry.used &&
            entry.handle == session_handle) {
            const auto released =
                std::min(items, entry.egress);
            entry.egress -= released;
            impl_->node_egress -= released;
            return;
        }
    }
}

BattleResourceMetrics
BattleResourceGovernor::Metrics() const noexcept {
    std::scoped_lock lock(impl_->mutex);
    return impl_->metrics;
}

}  // namespace ihomeland::sim
