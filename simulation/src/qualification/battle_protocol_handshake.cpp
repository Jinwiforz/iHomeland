#include "ihomeland/qualification/battle/protocol_client.hpp"

#include "ihomeland/sim/transport/crypto_provider.hpp"

#define NOMINMAX
#include <windows.h>

#include <algorithm>
#include <array>
#include <ranges>
#include <stdexcept>
#include <string_view>
#include <vector>

namespace ihomeland::qualification::battle {
namespace {

using CryptoProvider = ihomeland::sim::CryptoProvider;

constexpr std::array<std::uint8_t, 4> ClientHelloMagic{
    'I', 'H', 'B', 'H'};
constexpr std::array<std::uint8_t, 4> RetryMagic{
    'I', 'H', 'B', 'R'};
constexpr std::array<std::uint8_t, 4> ClientAuthMagic{
    'I', 'H', 'B', 'A'};
constexpr std::array<std::uint8_t, 4> ServerAcceptMagic{
    'I', 'H', 'B', 'S'};
constexpr std::uint8_t WireVersion = 1;
constexpr std::uint8_t ClientHelloKind = 1;
constexpr std::uint8_t RetryKind = 2;
constexpr std::uint8_t ClientAuthKind = 3;
constexpr std::uint8_t ServerAcceptKind = 4;
constexpr std::size_t ClientAuthProofOffset = 108;
constexpr std::size_t AcceptAadBytes = 76;
constexpr std::size_t AcceptPlaintextBytes = 64;
constexpr std::size_t SessionScheduleBytes = 76;
constexpr std::size_t ProofKeyBytes = 32;
constexpr std::string_view ProofDomain =
    "ihomeland/battle/client-auth/v1";
constexpr std::string_view ProofKeyDerivationDomain =
    "ihomeland/battle-ticket/proof-key/v2";
constexpr std::string_view ScheduleDomain =
    "ihomeland/battle/session-schedule/v1";

/// IsAllZero 拒绝全部为零的公开 identity、nonce、key 与 proof。
template <std::size_t Size>
[[nodiscard]] bool IsAllZero(
    const std::array<std::uint8_t, Size>& value) noexcept {
    return std::ranges::all_of(
        value,
        [](const std::uint8_t item) { return item == 0; });
}

/// ReadUint32BE 解码 network-order uint32。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::uint8_t* input) noexcept {
    return
        (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// WriteUint32BE 编码 network-order uint32。
void WriteUint32BE(
    std::uint8_t* output,
    const std::uint32_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// SecretWiper 在所有 return/exception 路径清零临时 secret。
class SecretWiper final {
public:
    /// 构造函数借用在 guard 析构前保持有效的 mutable memory。
    SecretWiper(
        CryptoProvider& crypto,
        const std::span<std::uint8_t> material) noexcept
        : crypto_(crypto),
          material_(material) {}

    /// 析构函数执行不可优化清零。
    ~SecretWiper() {
        crypto_.SecureZero(material_);
    }

    SecretWiper(const SecretWiper&) = delete;
    SecretWiper& operator=(const SecretWiper&) = delete;

private:
    /// crypto_ 提供统一 secure zero primitive。
    CryptoProvider& crypto_;
    /// material_ 的 owner 生命周期覆盖 guard。
    std::span<std::uint8_t> material_;
};

}  // namespace

TicketCredential::~TicketCredential() {
    SecureZeroMemory(
        ticket_secret.data(),
        ticket_secret.size());
}

TicketCredential::TicketCredential(
    TicketCredential&& source) noexcept
    : ticket_id(source.ticket_id),
      ticket_secret(source.ticket_secret),
      expires_at_unix_ms(
          source.expires_at_unix_ms) {
    SecureZeroMemory(
        source.ticket_secret.data(),
        source.ticket_secret.size());
    source.expires_at_unix_ms = 0;
}

TicketCredential& TicketCredential::operator=(
    TicketCredential&& source) noexcept {
    if (this == &source) {
        return *this;
    }
    SecureZeroMemory(
        ticket_secret.data(),
        ticket_secret.size());
    ticket_id = source.ticket_id;
    ticket_secret = source.ticket_secret;
    expires_at_unix_ms =
        source.expires_at_unix_ms;
    SecureZeroMemory(
        source.ticket_secret.data(),
        source.ticket_secret.size());
    source.expires_at_unix_ms = 0;
    return *this;
}

/// ProtocolHandshake::Impl 保存单一 transcript 的全部 secret 与状态。
struct ProtocolHandshake::Impl final {
    /// State 是禁止跳步或重复接受不同响应的 closed handshake 状态。
    enum class State : std::uint8_t {
        /// HelloReady 表示只允许 Retry。
        HelloReady = 1,
        /// AuthReady 表示只允许 exact ServerAccept。
        AuthReady = 2,
        /// Established 表示 secret 已转交且不能再次接受。
        Established = 3,
        /// Closed 表示任何失败或 deadline 后不可恢复。
        Closed = 4,
    };

    /// 构造函数锁页、复制 credential 并生成一次性 ephemeral transcript。
    Impl(
        TicketCredential source,
        const std::uint64_t started)
        : credential(std::move(source)),
          started_unix_ms(started) {
        if (started_unix_ms == 0 ||
            credential.expires_at_unix_ms <= started_unix_ms ||
            IsAllZero(credential.ticket_id) ||
            IsAllZero(credential.ticket_secret) ||
            VirtualLock(&credential, sizeof(credential)) == 0) {
            throw std::invalid_argument(
                "battle protocol credential is invalid");
        }
        credential_locked = true;
        try {
            const std::vector<std::uint8_t> proof_info(
                ProofKeyDerivationDomain.begin(),
                ProofKeyDerivationDomain.end());
            auto derived_proof = crypto.HkdfSha256(
                credential.ticket_secret,
                credential.ticket_id,
                proof_info,
                ProofKeyBytes);
            SecretWiper proof_wiper(
                crypto,
                derived_proof);
            if (derived_proof.size() != proof_key.size()) {
                throw std::runtime_error(
                    "battle protocol proof derivation failed");
            }
            std::ranges::copy(
                derived_proof,
                proof_key.begin());
            if (VirtualLock(
                    proof_key.data(),
                    proof_key.size()) == 0) {
                throw std::runtime_error(
                    "battle protocol proof key lock failed");
            }
            proof_locked = true;
            keys = crypto.GenerateX25519KeyPair();
            if (VirtualLock(
                    keys.secret_scalar.data(),
                    keys.secret_scalar.size()) == 0) {
                throw std::runtime_error(
                    "battle protocol ephemeral key lock failed");
            }
            scalar_locked = true;
            crypto.RandomFill(client_nonce);
            std::ranges::copy(
                ClientHelloMagic,
                hello.begin());
            hello[4] = WireVersion;
            hello[5] = ClientHelloKind;
            std::ranges::copy(
                credential.ticket_id,
                hello.begin() + 8);
            std::ranges::copy(
                client_nonce,
                hello.begin() + 24);
            std::ranges::copy(
                keys.public_key,
                hello.begin() + 56);
        } catch (...) {
            ReleaseSecrets();
            throw;
        }
    }

    /// 析构函数清零 secret 后解锁页面。
    ~Impl() {
        ReleaseSecrets();
        crypto.SecureZero(client_nonce);
        SecureZeroMemory(auth.data(), auth.size());
    }

    /// ReleaseSecrets 按 ownership 逆序清零并解锁全部敏感 material。
    void ReleaseSecrets() noexcept {
        crypto.SecureZero(keys.secret_scalar);
        if (scalar_locked) {
            static_cast<void>(VirtualUnlock(
                keys.secret_scalar.data(),
                keys.secret_scalar.size()));
            scalar_locked = false;
        }
        crypto.SecureZero(proof_key);
        if (proof_locked) {
            static_cast<void>(VirtualUnlock(
                proof_key.data(),
                proof_key.size()));
            proof_locked = false;
        }
        SecureZeroMemory(&credential, sizeof(credential));
        if (credential_locked) {
            static_cast<void>(
                VirtualUnlock(&credential, sizeof(credential)));
            credential_locked = false;
        }
    }

    /// Alive 验证单调阶段 deadline 与 ticket expiry。
    [[nodiscard]] bool Alive(
        const std::uint64_t now_unix_ms) const noexcept {
        return now_unix_ms >= started_unix_ms &&
            now_unix_ms <
                started_unix_ms +
                    HandshakeTimeoutMilliseconds &&
            now_unix_ms < credential.expires_at_unix_ms;
    }

    /// crypto 是锁定 primitive adapter，不含服务端 handshake 状态。
    CryptoProvider crypto;
    /// credential 仅存在于锁页，任何输出不得引用。
    TicketCredential credential{};
    /// proof_key 从公开 ticket ID 与 secret 本地派生，只存在于锁页。
    CryptoProvider::Key32 proof_key{};
    /// keys 是当前 transcript 的 ephemeral X25519 pair。
    CryptoProvider::X25519KeyPair keys{};
    /// client_nonce 防止同 credential transcript 重放。
    CryptoProvider::Key32 client_nonce{};
    /// hello 是 byte-exact repeated authority-free request。
    std::array<std::uint8_t, ClientHelloBytes> hello{};
    /// auth 是唯一允许发送的 ClientAuth transcript。
    std::array<std::uint8_t, ClientAuthBytes> auth{};
    /// started_unix_ms 是整个 handshake deadline 起点。
    std::uint64_t started_unix_ms;
    /// state 禁止跳步、重复 ServerAccept 与失败后恢复。
    State state{State::HelloReady};
    /// credential_locked 保护对应 VirtualUnlock。
    bool credential_locked{false};
    /// scalar_locked 保护对应 VirtualUnlock。
    bool scalar_locked{false};
    /// proof_locked 保护对应 VirtualUnlock。
    bool proof_locked{false};
};

ProtocolHandshake::ProtocolHandshake(
    TicketCredential credential,
    const std::uint64_t started_unix_ms)
    : impl_(std::make_unique<Impl>(
          std::move(credential),
          started_unix_ms)) {}

ProtocolHandshake::~ProtocolHandshake() = default;

std::array<std::uint8_t, ProtocolHandshake::ClientHelloBytes>
ProtocolHandshake::ClientHello() const {
    if (impl_->state != Impl::State::HelloReady) {
        throw std::logic_error(
            "battle protocol hello is no longer available");
    }
    return impl_->hello;
}

bool ProtocolHandshake::MatchesRetryEnvelope(
    const std::span<const std::uint8_t> response) noexcept {
    if (response.size() != RetryBytes ||
        !std::ranges::equal(
            RetryMagic,
            response.first(RetryMagic.size())) ||
        response[4] != WireVersion ||
        response[5] != RetryKind ||
        response[6] != 0 ||
        response[7] != 0 ||
        ReadUint32BE(response.data() + 8) == 0) {
        return false;
    }
    std::array<std::uint8_t, 16> cookie{};
    std::ranges::copy(
        response.subspan(12, cookie.size()),
        cookie.begin());
    return !IsAllZero(cookie);
}

bool ProtocolHandshake::MatchesServerAcceptEnvelope(
    const std::span<const std::uint8_t> response) noexcept {
    if (response.size() != ServerAcceptBytes ||
        !std::ranges::equal(
            ServerAcceptMagic,
            response.first(ServerAcceptMagic.size())) ||
        response[4] != WireVersion ||
        response[5] != ServerAcceptKind ||
        response[6] != 0 ||
        response[7] != 0 ||
        response[72] != 0 ||
        response[73] != AcceptPlaintextBytes ||
        response[74] != 0 ||
        response[75] != 0) {
        return false;
    }
    std::array<std::uint8_t, 32> server_public{};
    std::ranges::copy(
        response.subspan(40, server_public.size()),
        server_public.begin());
    return !IsAllZero(server_public);
}

std::optional<
    std::array<std::uint8_t, ProtocolHandshake::ClientAuthBytes>>
ProtocolHandshake::AcceptRetry(
    const std::span<const std::uint8_t> response,
    const std::uint64_t now_unix_ms) {
    if (impl_->state != Impl::State::HelloReady ||
        !impl_->Alive(now_unix_ms) ||
        !MatchesRetryEnvelope(response)) {
        impl_->state = Impl::State::Closed;
        return std::nullopt;
    }
    const auto cookie_epoch =
        ReadUint32BE(response.data() + 8);
    std::array<std::uint8_t, 16> cookie{};
    std::ranges::copy(
        response.subspan(12, cookie.size()),
        cookie.begin());
    std::ranges::copy(
        ClientAuthMagic,
        impl_->auth.begin());
    impl_->auth[4] = WireVersion;
    impl_->auth[5] = ClientAuthKind;
    std::ranges::copy(
        std::span(impl_->hello).subspan(8),
        impl_->auth.begin() + 8);
    WriteUint32BE(
        impl_->auth.data() + 88,
        cookie_epoch);
    std::ranges::copy(
        cookie,
        impl_->auth.begin() + 92);
    std::vector<std::uint8_t> proof_material(
        ProofDomain.begin(),
        ProofDomain.end());
    proof_material.insert(
        proof_material.end(),
        impl_->auth.begin(),
        impl_->auth.begin() + ClientAuthProofOffset);
    auto proof = impl_->crypto.HmacSha256(
        impl_->proof_key,
        proof_material);
    SecretWiper proof_wiper(impl_->crypto, proof);
    std::ranges::copy(
        proof,
        impl_->auth.begin() + ClientAuthProofOffset);
    impl_->state = Impl::State::AuthReady;
    return impl_->auth;
}

std::optional<SessionParameters>
ProtocolHandshake::AcceptServer(
    const std::span<const std::uint8_t> response,
    const std::uint64_t now_unix_ms) {
    if (impl_->state != Impl::State::AuthReady ||
        !impl_->Alive(now_unix_ms) ||
        !MatchesServerAcceptEnvelope(response)) {
        impl_->state = Impl::State::Closed;
        return std::nullopt;
    }

    CryptoProvider::Key32 server_public{};
    std::ranges::copy(
        response.subspan(40, server_public.size()),
        server_public.begin());
    try {
        auto shared = impl_->crypto.X25519(
            impl_->keys.secret_scalar,
            server_public);
        SecretWiper shared_wiper(impl_->crypto, shared);
        const auto request_digest =
            impl_->crypto.Sha256(impl_->auth);
        std::vector<std::uint8_t> schedule_material(
            ScheduleDomain.begin(),
            ScheduleDomain.end());
        schedule_material.insert(
            schedule_material.end(),
            request_digest.begin(),
            request_digest.end());
        schedule_material.insert(
            schedule_material.end(),
            response.begin() + 8,
            response.begin() + 72);
        const auto transcript_hash =
            impl_->crypto.Sha256(schedule_material);
        std::vector<std::uint8_t> hkdf_info(
            ScheduleDomain.begin(),
            ScheduleDomain.end());
        hkdf_info.insert(
            hkdf_info.end(),
            transcript_hash.begin(),
            transcript_hash.end());
        auto schedule = impl_->crypto.HkdfSha256(
            shared,
            impl_->proof_key,
            hkdf_info,
            SessionScheduleBytes);
        SecretWiper schedule_wiper(impl_->crypto, schedule);

        CryptoProvider::Key32 accept_key{};
        SecretWiper accept_key_wiper(
            impl_->crypto,
            accept_key);
        CryptoProvider::Nonce12 accept_nonce{};
        SecretWiper accept_nonce_wiper(
            impl_->crypto,
            accept_nonce);
        std::ranges::copy_n(
            schedule.begin(),
            accept_key.size(),
            accept_key.begin());
        std::ranges::copy_n(
            schedule.begin() + accept_key.size(),
            accept_nonce.size(),
            accept_nonce.begin());
        CryptoProvider::Tag16 tag{};
        std::ranges::copy(
            response.last(tag.size()),
            tag.begin());
        auto plaintext =
            impl_->crypto.DecryptChaCha20Poly1305(
                accept_key,
                accept_nonce,
                response.first(AcceptAadBytes),
                response.subspan(
                    AcceptAadBytes,
                    AcceptPlaintextBytes),
                tag);
        if (!plaintext.has_value()) {
            impl_->state = Impl::State::Closed;
            return std::nullopt;
        }
        SecretWiper plaintext_wiper(
            impl_->crypto,
            *plaintext);

        SessionParameters parameters{};
        std::ranges::copy_n(
            schedule.begin() + accept_key.size() +
                accept_nonce.size(),
            parameters.session_seed.size(),
            parameters.session_seed.begin());
        std::ranges::copy_n(
            plaintext->begin(),
            parameters.session_id.size(),
            parameters.session_id.begin());
        parameters.battle_session_generation =
            ReadUint32BE(plaintext->data() + 16);
        parameters.key_epoch =
            ReadUint32BE(plaintext->data() + 20);
        parameters.endpoint_generation =
            ReadUint32BE(plaintext->data() + 24);
        parameters.actor_slot = (*plaintext)[28];
        parameters.role = (*plaintext)[29];
        std::ranges::copy(
            plaintext->begin() + 32,
            plaintext->end(),
            parameters.binding_fingerprint.begin());
        if (IsAllZero(parameters.session_id) ||
            IsAllZero(parameters.session_seed) ||
            IsAllZero(parameters.binding_fingerprint) ||
            parameters.battle_session_generation == 0 ||
            parameters.key_epoch != 1 ||
            parameters.endpoint_generation != 1 ||
            parameters.actor_slot >= 8 ||
            (parameters.role != 1 && parameters.role != 2)) {
            impl_->crypto.SecureZero(parameters.session_seed);
            impl_->crypto.SecureZero(
                parameters.binding_fingerprint);
            impl_->state = Impl::State::Closed;
            return std::nullopt;
        }
        impl_->state = Impl::State::Established;
        impl_->crypto.SecureZero(
            impl_->keys.secret_scalar);
        impl_->crypto.SecureZero(impl_->proof_key);
        return parameters;
    } catch (...) {
        impl_->state = Impl::State::Closed;
        return std::nullopt;
    }
}

}  // namespace ihomeland::qualification::battle
