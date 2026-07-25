#include "ihomeland/sim/transport/handshake_cookie.hpp"

#include <algorithm>
#include <limits>
#include <stdexcept>

namespace ihomeland::sim {
namespace {

constexpr std::array<std::uint8_t, 4> ClientHelloMagic{
    'I', 'H', 'B', 'H'};
constexpr std::array<std::uint8_t, 4> RetryMagic{
    'I', 'H', 'B', 'R'};
constexpr std::uint8_t WireVersion = 1;
constexpr std::uint8_t ClientHelloKind = 1;
constexpr std::uint8_t RetryKind = 2;
constexpr std::array<std::uint8_t, 18> CookieDomain{
    'i', 'h', 'o', 'm', 'e', 'l', 'a', 'n', 'd',
    '-', 'c', 'o', 'o', 'k', 'i', 'e', '-', '1'};
constexpr std::size_t CookieInputBytes =
    CookieDomain.size() + 1 + 16 + 16 + 2 + 4 + 16 + 32 + 32;

/// IsAllZero 执行公开 input 的廉价全零检查，不替代后续 X25519 validation。
template <std::size_t Size>
[[nodiscard]] bool IsAllZero(
    const std::array<std::uint8_t, Size>& value) noexcept {
    return std::all_of(
        value.begin(),
        value.end(),
        [](const std::uint8_t item) { return item == 0; });
}

/// WriteUint32BE 写入 canonical network-order epoch。
void WriteUint32BE(
    std::span<std::uint8_t, 4> output,
    const std::uint32_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// ReadUint32BE 读取 canonical network-order epoch。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::span<const std::uint8_t, 4> input) noexcept {
    return (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

}  // namespace

BattleCookieKeyRing::BattleCookieKeyRing(
    CryptoProvider& crypto,
    const std::uint64_t initial_unix_ms)
    : crypto_(crypto),
      current_epoch_(
          static_cast<std::uint32_t>(
              initial_unix_ms / RotationMilliseconds)) {
    if ((initial_unix_ms / RotationMilliseconds) >
        std::numeric_limits<std::uint32_t>::max()) {
        throw std::invalid_argument("cookie epoch exceeds uint32");
    }
    crypto_.RandomFill(current_key_);
}

BattleCookieKeyRing::BattleCookieKeyRing(
    CryptoProvider& crypto,
    const std::uint32_t initial_epoch,
    const CryptoProvider::Key32& fixture_key)
    : crypto_(crypto),
      current_key_(fixture_key),
      current_epoch_(initial_epoch) {
    if (initial_epoch == 0 || IsAllZero(fixture_key)) {
        throw std::invalid_argument("fixture cookie key is invalid");
    }
}

BattleCookieKeyRing::~BattleCookieKeyRing() {
    std::scoped_lock lock(mutex_);
    crypto_.SecureZero(current_key_);
    if (previous_key_.has_value()) {
        crypto_.SecureZero(*previous_key_);
        previous_key_.reset();
    }
}

BattleRetry BattleCookieKeyRing::Issue(
    const BattleClientHello& hello,
    const BattleRemoteEndpoint& remote,
    const std::array<std::uint8_t, 16>& listener_identity,
    const std::uint64_t now_unix_ms) {
    const auto epoch64 = now_unix_ms / RotationMilliseconds;
    if (epoch64 == 0 ||
        epoch64 > std::numeric_limits<std::uint32_t>::max()) {
        throw std::invalid_argument("cookie time is outside wire epoch");
    }
    std::scoped_lock lock(mutex_);
    RotateLocked(static_cast<std::uint32_t>(epoch64));
    return BattleRetry{
        .cookie_epoch = current_epoch_,
        .cookie = ComputeLocked(
            current_key_,
            current_epoch_,
            hello,
            remote,
            listener_identity)};
}

bool BattleCookieKeyRing::Verify(
    const BattleClientHello& hello,
    const BattleRemoteEndpoint& remote,
    const std::array<std::uint8_t, 16>& listener_identity,
    const BattleRetry& retry,
    const std::uint64_t now_unix_ms) {
    const auto epoch64 = now_unix_ms / RotationMilliseconds;
    if (epoch64 == 0 ||
        epoch64 > std::numeric_limits<std::uint32_t>::max()) {
        return false;
    }
    std::scoped_lock lock(mutex_);
    RotateLocked(static_cast<std::uint32_t>(epoch64));
    const CryptoProvider::Key32* key = nullptr;
    if (retry.cookie_epoch == current_epoch_) {
        key = &current_key_;
    } else if (
        current_epoch_ > 0 &&
        retry.cookie_epoch == current_epoch_ - 1 &&
        previous_key_.has_value()) {
        key = &*previous_key_;
    }
    if (key == nullptr) {
        return false;
    }
    const auto expected = ComputeLocked(
        *key,
        retry.cookie_epoch,
        hello,
        remote,
        listener_identity);
    return crypto_.ConstantTimeEqual(expected, retry.cookie);
}

void BattleCookieKeyRing::RotateLocked(
    const std::uint32_t target_epoch) {
    if (target_epoch <= current_epoch_) {
        return;
    }
    if (previous_key_.has_value()) {
        crypto_.SecureZero(*previous_key_);
        previous_key_.reset();
    }
    if (target_epoch == current_epoch_ + 1) {
        previous_key_ = current_key_;
    } else {
        crypto_.SecureZero(current_key_);
    }
    crypto_.RandomFill(current_key_);
    current_epoch_ = target_epoch;
}

std::array<std::uint8_t, 16> BattleCookieKeyRing::ComputeLocked(
    const CryptoProvider::Key32& key,
    const std::uint32_t epoch,
    const BattleClientHello& hello,
    const BattleRemoteEndpoint& remote,
    const std::array<std::uint8_t, 16>& listener_identity) const {
    std::array<std::uint8_t, CookieInputBytes> input{};
    std::size_t offset = 0;
    const auto append = [&](const auto& value) {
        std::ranges::copy(value, input.begin() + offset);
        offset += value.size();
    };
    append(CookieDomain);
    input[offset++] = WireVersion;
    append(listener_identity);
    append(remote.address);
    input[offset++] = static_cast<std::uint8_t>(remote.port >> 8U);
    input[offset++] = static_cast<std::uint8_t>(remote.port);
    WriteUint32BE(
        std::span<std::uint8_t, 4>(input.data() + offset, 4),
        epoch);
    offset += 4;
    append(hello.ticket_id);
    append(hello.client_nonce);
    append(hello.client_public_key);
    const auto digest = crypto_.HmacSha256(key, input);
    std::array<std::uint8_t, 16> cookie{};
    std::ranges::copy_n(digest.begin(), cookie.size(), cookie.begin());
    return cookie;
}

BattleHandshakeCookieGate::BattleHandshakeCookieGate(
    BattleCookieKeyRing& keys,
    std::array<std::uint8_t, 16> listener_identity)
    : keys_(keys),
      listener_identity_(listener_identity) {
    if (IsAllZero(listener_identity_)) {
        throw std::invalid_argument("listener identity is zero");
    }
}

BattleHandshakeCookieGate::Decision
BattleHandshakeCookieGate::HandleClientHello(
    const std::span<const std::uint8_t> request,
    const BattleRemoteEndpoint& remote,
    const std::uint64_t now_unix_ms) {
    Decision decision{};
    const auto hello = ParseClientHello(request);
    if (!hello.has_value() ||
        remote.port == 0 ||
        IsAllZero(remote.address)) {
        return decision;
    }
    const auto retry = keys_.Issue(
        *hello,
        remote,
        listener_identity_,
        now_unix_ms);
    std::ranges::copy(RetryMagic, decision.response.begin());
    decision.response[4] = WireVersion;
    decision.response[5] = RetryKind;
    WriteUint32BE(
        std::span<std::uint8_t, 4>(
            decision.response.data() + 8,
            4),
        retry.cookie_epoch);
    std::ranges::copy(
        retry.cookie,
        decision.response.begin() + 12);
    decision.response_bytes =
        decision.response.size() <= request.size()
        ? decision.response.size()
        : 0;
    return decision;
}

bool BattleHandshakeCookieGate::ValidateCookie(
    const std::span<const std::uint8_t> repeated_hello,
    const BattleRemoteEndpoint& remote,
    const BattleRetry& retry,
    const std::uint64_t now_unix_ms) {
    const auto hello = ParseClientHello(repeated_hello);
    return hello.has_value() &&
        remote.port != 0 &&
        !IsAllZero(remote.address) &&
        keys_.Verify(
            *hello,
            remote,
            listener_identity_,
            retry,
            now_unix_ms);
}

bool BattleHandshakeCookieGate::ValidateCookieFields(
    const BattleClientHello& repeated_hello,
    const BattleRemoteEndpoint& remote,
    const BattleRetry& retry,
    const std::uint64_t now_unix_ms) {
    return remote.port != 0 &&
        !IsAllZero(remote.address) &&
        !IsAllZero(repeated_hello.ticket_id) &&
        !IsAllZero(repeated_hello.client_nonce) &&
        !IsAllZero(repeated_hello.client_public_key) &&
        keys_.Verify(
            repeated_hello,
            remote,
            listener_identity_,
            retry,
            now_unix_ms);
}

std::optional<BattleRetry>
BattleHandshakeCookieGate::ParseRetry(
    const std::span<const std::uint8_t> response) {
    if (response.size() != RetryBytes ||
        !std::ranges::equal(
            RetryMagic,
            response.first(RetryMagic.size())) ||
        response[4] != WireVersion ||
        response[5] != RetryKind ||
        response[6] != 0 ||
        response[7] != 0) {
        return std::nullopt;
    }
    BattleRetry retry{};
    retry.cookie_epoch = ReadUint32BE(
        std::span<const std::uint8_t, 4>(
            response.data() + 8,
            4));
    std::ranges::copy(
        response.subspan(12, retry.cookie.size()),
        retry.cookie.begin());
    if (retry.cookie_epoch == 0 || IsAllZero(retry.cookie)) {
        return std::nullopt;
    }
    return retry;
}

std::optional<BattleClientHello>
BattleHandshakeCookieGate::ParseClientHello(
    const std::span<const std::uint8_t> request) {
    if (request.size() < ClientHelloBytes ||
        request.size() > MaximumDatagramBytes ||
        !std::ranges::equal(
            ClientHelloMagic,
            request.first(ClientHelloMagic.size())) ||
        request[4] != WireVersion ||
        request[5] != ClientHelloKind ||
        request[6] != 0 ||
        request[7] != 0 ||
        !std::ranges::all_of(
            request.subspan(ClientHelloBytes),
            [](const std::uint8_t value) { return value == 0; })) {
        return std::nullopt;
    }
    BattleClientHello hello{};
    std::ranges::copy(
        request.subspan(8, hello.ticket_id.size()),
        hello.ticket_id.begin());
    std::ranges::copy(
        request.subspan(24, hello.client_nonce.size()),
        hello.client_nonce.begin());
    std::ranges::copy(
        request.subspan(56, hello.client_public_key.size()),
        hello.client_public_key.begin());
    if (IsAllZero(hello.ticket_id) ||
        IsAllZero(hello.client_nonce) ||
        IsAllZero(hello.client_public_key)) {
        return std::nullopt;
    }
    return hello;
}

}  // namespace ihomeland::sim
