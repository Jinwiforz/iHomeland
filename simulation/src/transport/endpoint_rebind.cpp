#include "ihomeland/sim/transport/endpoint_rebind.hpp"

#include <algorithm>
#include <limits>
#include <mutex>
#include <ranges>
#include <stdexcept>
#include <vector>

namespace ihomeland::sim {
namespace {

/// SameEndpoint 对canonical IP与port执行exact compare。
[[nodiscard]] bool SameEndpoint(
    const BattleRemoteEndpoint& left,
    const BattleRemoteEndpoint& right) noexcept {
    return left.port == right.port &&
           left.address == right.address;
}

/// ValidEndpoint 拒绝zero IP与port。
[[nodiscard]] bool ValidEndpoint(
    const BattleRemoteEndpoint& endpoint) noexcept {
    return endpoint.port != 0 &&
           std::ranges::any_of(
               endpoint.address,
               [](const std::uint8_t value) {
                   return value != 0;
               });
}

/// WriteUint32BE 写入canonical generation。
void WriteUint32BE(
    std::uint8_t* output,
    const std::uint32_t value) noexcept {
    output[0] =
        static_cast<std::uint8_t>(value >> 24U);
    output[1] =
        static_cast<std::uint8_t>(value >> 16U);
    output[2] =
        static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// WriteUint64BE 写入canonical deadline。
void WriteUint64BE(
    std::uint8_t* output,
    const std::uint64_t value) noexcept {
    for (std::size_t index = 0; index < 8; ++index) {
        output[7 - index] =
            static_cast<std::uint8_t>(
                value >> (index * 8U));
    }
}

}  // namespace

/// BattleEndpointRebinder::Impl 保存单session rebind state。
struct BattleEndpointRebinder::Impl final {
    /// Cookie 计算candidate/generation/nonce/deadline的address proof。
    [[nodiscard]] std::array<std::uint8_t, 16>
    Cookie(
        const BattleRebindChallenge& challenge) const {
        std::array<std::uint8_t, 50> material{};
        std::ranges::copy(
            challenge.candidate.address,
            material.begin());
        material[16] = static_cast<std::uint8_t>(
            challenge.candidate.port >> 8U);
        material[17] = static_cast<std::uint8_t>(
            challenge.candidate.port);
        WriteUint32BE(
            material.data() + 18,
            challenge.current_generation);
        WriteUint32BE(
            material.data() + 22,
            challenge.next_generation);
        std::ranges::copy(
            challenge.nonce,
            material.begin() + 26);
        WriteUint64BE(
            material.data() + 42,
            challenge.expires_at_unix_ms);
        const auto digest =
            crypto->HmacSha256(cookie_key, material);
        std::array<std::uint8_t, 16> result{};
        std::ranges::copy_n(
            digest.begin(),
            result.size(),
            result.begin());
        return result;
    }

    /// crypto 是唯一primitive/secure-zero owner。
    CryptoProvider* crypto;
    /// channel 是既有session secure state。
    BattleSecureChannel* channel;
    /// active_endpoint 是当前唯一remote。
    BattleRemoteEndpoint active_endpoint;
    /// generation 是current endpoint generation。
    std::uint32_t generation;
    /// cookie_key 只存在于process memory。
    CryptoProvider::Key32 cookie_key{};
    /// pending 最多保存一个candidate challenge。
    std::optional<BattleRebindChallenge> pending;
    /// rate_window_started_ms 是当前一秒窗口。
    std::uint64_t rate_window_started_ms{};
    /// rate_window_count 是窗口内Begin次数。
    std::uint8_t rate_window_count{};
    /// mutex 串行化single-active transition。
    mutable std::mutex mutex;
};

namespace {

/// ValidateBinding 验证rebind固定binding。
void ValidateBinding(
    const BattleRemoteEndpoint initial_endpoint,
    const std::uint32_t initial_generation,
    const CryptoProvider::Key32& key) {
    if (!ValidEndpoint(initial_endpoint) ||
        initial_generation == 0 ||
        std::ranges::all_of(
            key,
            [](const std::uint8_t value) {
                return value == 0;
            })) {
        throw std::invalid_argument(
            "battle rebind binding is invalid");
    }
}

}  // namespace

BattleEndpointRebinder::BattleEndpointRebinder(
    CryptoProvider& crypto,
    BattleSecureChannel& channel,
    const BattleRemoteEndpoint initial_endpoint,
    const std::uint32_t initial_generation) {
    auto key = CryptoProvider::Key32{};
    crypto.RandomFill(key);
    ValidateBinding(
        initial_endpoint,
        initial_generation,
        key);
    impl_ = std::make_unique<Impl>();
    impl_->crypto = &crypto;
    impl_->channel = &channel;
    impl_->active_endpoint = initial_endpoint;
    impl_->generation = initial_generation;
    impl_->cookie_key = key;
    crypto.SecureZero(key);
}

BattleEndpointRebinder::BattleEndpointRebinder(
    CryptoProvider& crypto,
    BattleSecureChannel& channel,
    const BattleRemoteEndpoint initial_endpoint,
    const std::uint32_t initial_generation,
    const CryptoProvider::Key32& fixture_key)
    : impl_(std::make_unique<Impl>()) {
    ValidateBinding(
        initial_endpoint,
        initial_generation,
        fixture_key);
    impl_->crypto = &crypto;
    impl_->channel = &channel;
    impl_->active_endpoint = initial_endpoint;
    impl_->generation = initial_generation;
    impl_->cookie_key = fixture_key;
}

BattleEndpointRebinder::~BattleEndpointRebinder() {
    if (impl_ != nullptr) {
        impl_->crypto->SecureZero(
            impl_->cookie_key);
    }
}

BattleRebindResult BattleEndpointRebinder::Begin(
    const BattleRemoteEndpoint& current_remote,
    const BattleRemoteEndpoint& candidate,
    const std::array<std::uint8_t, 16>& nonce,
    const bool authenticated,
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(impl_->mutex);
    if (!authenticated) {
        return {
            BattleRebindDisposition::
                AuthenticationRequired,
            std::nullopt};
    }
    if (!SameEndpoint(
            current_remote,
            impl_->active_endpoint) ||
        !ValidEndpoint(candidate) ||
        SameEndpoint(candidate, impl_->active_endpoint)) {
        return {
            BattleRebindDisposition::EndpointMismatch,
            std::nullopt};
    }
    if (impl_->pending.has_value()) {
        return {
            BattleRebindDisposition::Concurrent,
            std::nullopt};
    }
    if (now_unix_ms == 0 ||
        std::ranges::all_of(
            nonce,
            [](const std::uint8_t value) {
                return value == 0;
            }) ||
        now_unix_ms >
            std::numeric_limits<std::uint64_t>::max() -
                ChallengeLifetimeMilliseconds) {
        return {
            BattleRebindDisposition::InvalidChallenge,
            std::nullopt};
    }
    if (impl_->generation ==
        std::numeric_limits<std::uint32_t>::max()) {
        return {
            BattleRebindDisposition::
                GenerationExhausted,
            std::nullopt};
    }
    if (impl_->rate_window_started_ms != 0 &&
        now_unix_ms <
            impl_->rate_window_started_ms) {
        return {
            BattleRebindDisposition::RateLimited,
            std::nullopt};
    }
    if (impl_->rate_window_started_ms == 0 ||
        now_unix_ms -
                impl_->rate_window_started_ms >=
            1'000) {
        impl_->rate_window_started_ms = now_unix_ms;
        impl_->rate_window_count = 0;
    }
    if (impl_->rate_window_count >= 2) {
        return {
            BattleRebindDisposition::RateLimited,
            std::nullopt};
    }
    ++impl_->rate_window_count;
    auto challenge = BattleRebindChallenge{
        .candidate = candidate,
        .current_generation = impl_->generation,
        .next_generation = impl_->generation + 1U,
        .nonce = nonce,
        .expires_at_unix_ms =
            now_unix_ms +
            ChallengeLifetimeMilliseconds,
        .cookie = {},
    };
    challenge.cookie = impl_->Cookie(challenge);
    impl_->pending = challenge;
    return {
        BattleRebindDisposition::ChallengeIssued,
        challenge};
}

BattleRebindDisposition
BattleEndpointRebinder::Confirm(
    const BattleRemoteEndpoint& candidate_remote,
    const BattleRebindChallenge& challenge,
    const bool authenticated,
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(impl_->mutex);
    if (!authenticated) {
        return BattleRebindDisposition::
            AuthenticationRequired;
    }
    if (!SameEndpoint(
            candidate_remote,
            challenge.candidate)) {
        return BattleRebindDisposition::
            EndpointMismatch;
    }
    if (now_unix_ms == 0 ||
        now_unix_ms >=
            challenge.expires_at_unix_ms) {
        if (impl_->pending.has_value() &&
            impl_->pending->cookie ==
                challenge.cookie) {
            impl_->pending.reset();
        }
        return BattleRebindDisposition::Expired;
    }
    if (!impl_->pending.has_value() ||
        impl_->pending->candidate.address !=
            challenge.candidate.address ||
        impl_->pending->candidate.port !=
            challenge.candidate.port ||
        impl_->pending->current_generation !=
            challenge.current_generation ||
        impl_->pending->next_generation !=
            challenge.next_generation ||
        impl_->pending->nonce != challenge.nonce ||
        impl_->pending->cookie != challenge.cookie ||
        !impl_->crypto->ConstantTimeEqual(
            impl_->Cookie(challenge),
            challenge.cookie)) {
        return BattleRebindDisposition::
            InvalidChallenge;
    }
    if (!impl_->channel->CommitEndpointGeneration(
            impl_->generation,
            challenge.next_generation)) {
        return BattleRebindDisposition::
            InvalidChallenge;
    }
    impl_->active_endpoint = candidate_remote;
    impl_->generation = challenge.next_generation;
    impl_->pending.reset();
    return BattleRebindDisposition::Committed;
}

BattleRemoteEndpoint
BattleEndpointRebinder::ActiveEndpoint() const {
    std::scoped_lock lock(impl_->mutex);
    return impl_->active_endpoint;
}

std::uint32_t
BattleEndpointRebinder::EndpointGeneration() const {
    std::scoped_lock lock(impl_->mutex);
    return impl_->generation;
}

}  // namespace ihomeland::sim
