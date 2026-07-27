#include "ihomeland/sim/transport/authenticated_handshake.hpp"

#define NOMINMAX
#include <windows.h>

#include <algorithm>
#include <array>
#include <limits>
#include <stdexcept>
#include <string_view>
#include <utility>

namespace ihomeland::sim {
namespace {

constexpr std::array<std::uint8_t, 4> ClientAuthMagic{
    'I', 'H', 'B', 'A'};
constexpr std::array<std::uint8_t, 4> ServerAcceptMagic{
    'I', 'H', 'B', 'S'};
constexpr std::uint8_t WireVersion = 1;
constexpr std::uint8_t ClientAuthKind = 3;
constexpr std::uint8_t ServerAcceptKind = 4;
constexpr std::size_t ClientAuthProofOffset = 108;
constexpr std::size_t AcceptAadBytes = 76;
constexpr std::size_t AcceptPlaintextBytes = 64;
constexpr std::string_view ProofDomain =
    "ihomeland/battle/client-auth/v1";
constexpr std::string_view ScheduleDomain =
    "ihomeland/battle/session-schedule/v1";

/// IsAllZero 拒绝公开 identity、nonce、key-share 与 proof 的无效零值。
template <std::size_t Size>
[[nodiscard]] bool IsAllZero(
    const std::array<std::uint8_t, Size>& value) noexcept {
    return std::ranges::all_of(
        value,
        [](const std::uint8_t item) { return item == 0; });
}

/// ReadUint32BE 解码 network-order epoch。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::span<const std::uint8_t, 4> input) noexcept {
    return (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// WriteUint32BE 编码 network-order generation。
void WriteUint32BE(
    const std::span<std::uint8_t, 4> output,
    const std::uint32_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// Base64UrlTicketID 将 16-byte wire identity恢复为canonical Go TicketID。
[[nodiscard]] std::string Base64UrlTicketID(
    const std::array<std::uint8_t, 16>& material) {
    constexpr std::string_view alphabet =
        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
    std::string output = "btk1_";
    output.reserve(27);
    std::uint32_t accumulator = 0;
    int bits = 0;
    for (const auto value : material) {
        accumulator = (accumulator << 8U) | value;
        bits += 8;
        while (bits >= 6) {
            bits -= 6;
            output.push_back(
                alphabet[(accumulator >> bits) & 0x3fU]);
        }
    }
    if (bits > 0) {
        output.push_back(
            alphabet[(accumulator << (6 - bits)) & 0x3fU]);
    }
    return output;
}

/// DecodeLowerHex32 恢复已由 install 校验的 SHA-256 binding fingerprint。
[[nodiscard]] CryptoProvider::Key32 DecodeLowerHex32(
    const std::string& text) {
    if (text.size() != 64) {
        throw std::runtime_error(
            "ticket binding fingerprint is invalid");
    }
    const auto nibble = [](const char value) -> std::uint8_t {
        if (value >= '0' && value <= '9') {
            return static_cast<std::uint8_t>(value - '0');
        }
        if (value >= 'a' && value <= 'f') {
            return static_cast<std::uint8_t>(
                value - 'a' + 10);
        }
        throw std::runtime_error(
            "ticket binding fingerprint is invalid");
    };
    CryptoProvider::Key32 output{};
    for (std::size_t index = 0; index < output.size(); ++index) {
        output[index] = static_cast<std::uint8_t>(
            (nibble(text[index * 2]) << 4U) |
            nibble(text[index * 2 + 1]));
    }
    return output;
}

/// EndpointEqual 比较 canonical source endpoint。
[[nodiscard]] bool EndpointEqual(
    const BattleRemoteEndpoint& first,
    const BattleRemoteEndpoint& second) noexcept {
    return first.address == second.address &&
        first.port == second.port;
}

/// SecretWiper 保证异常路径也清零 caller-owned temporary。
class SecretWiper final {
public:
    /// 构造函数借用在自身析构前保持有效的 mutable secret。
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
    /// material_ 的 owner 生命周期覆盖本 guard。
    std::span<std::uint8_t> material_;
};

}  // namespace

/// SessionBootstrap::Impl 保存唯一 locked seed 与不可变 session authority。
struct BattleAuthenticatedHandshake::SessionBootstrap::Impl final {
    /// 析构函数先清零 seed 再释放 locked page。
    ~Impl() {
        SecureZeroMemory(
            session_seed.data(),
            session_seed.size());
        if (seed_locked) {
            static_cast<void>(VirtualUnlock(
                session_seed.data(),
                session_seed.size()));
        }
    }

    /// session_seed 只允许 ConsumeSessionSeed 同步借用一次。
    CryptoProvider::Key32 session_seed{};
    /// binding_fingerprint 绑定完整 Go authority facts。
    CryptoProvider::Key32 binding_fingerprint{};
    /// session_id 是 ServerAccept 公开给 authenticated client 的 runtime identity。
    std::array<std::uint8_t, 16> session_id{};
    /// battle_session_generation 是 process-local incarnation。
    std::uint32_t battle_session_generation{};
    /// key_epoch 是 secure channel initial epoch。
    std::uint32_t key_epoch{};
    /// endpoint_generation 是 secure AAD initial endpoint generation。
    std::uint32_t endpoint_generation{};
    /// actor_slot 是 install 冻结的 0..7 slot。
    std::uint8_t actor_slot{};
    /// role 是 owner=1、visitor=2 的 closed projection。
    std::uint8_t role{};
    /// binding 是 Go 安装的完整 immutable authority facts。
    BattleTicketBinding binding;
    /// remote 是 ticket 被消费时已通过 cookie 验证的 endpoint。
    BattleRemoteEndpoint remote{};
    /// seed_locked 保护对应 VirtualUnlock。
    bool seed_locked{false};
    /// seed_consumed 防止第二个 secure channel 读取同一 seed。
    bool seed_consumed{false};
};

BattleAuthenticatedHandshake::SessionBootstrap::SessionBootstrap(
    const CryptoProvider::Key32& session_seed,
    CryptoProvider::Key32 binding_fingerprint,
    std::array<std::uint8_t, 16> session_id,
    const std::uint32_t battle_session_generation,
    const std::uint32_t key_epoch,
    const std::uint32_t endpoint_generation,
    const std::uint8_t actor_slot,
    const std::uint8_t role,
    BattleTicketBinding binding,
    const BattleRemoteEndpoint remote)
    : impl_(std::make_unique<Impl>()) {
    if (IsAllZero(session_seed) ||
        IsAllZero(binding_fingerprint) ||
        IsAllZero(session_id) ||
        battle_session_generation == 0 ||
        key_epoch != 1 ||
        endpoint_generation != 1 ||
        actor_slot >= 8 ||
        (role != 1 && role != 2) ||
        binding.actor_slot != actor_slot ||
        ((role == 1 && binding.role != "owner") ||
         (role == 2 && binding.role != "visitor")) ||
        binding.simulation_instance_id.empty() ||
        remote.port == 0 ||
        IsAllZero(remote.address) ||
        VirtualLock(
            impl_->session_seed.data(),
            impl_->session_seed.size()) == 0) {
        throw std::invalid_argument(
            "battle session bootstrap is invalid");
    }
    impl_->seed_locked = true;
    std::ranges::copy(
        session_seed,
        impl_->session_seed.begin());
    impl_->binding_fingerprint =
        std::move(binding_fingerprint);
    impl_->session_id = std::move(session_id);
    impl_->battle_session_generation =
        battle_session_generation;
    impl_->key_epoch = key_epoch;
    impl_->endpoint_generation = endpoint_generation;
    impl_->actor_slot = actor_slot;
    impl_->role = role;
    impl_->binding = std::move(binding);
    impl_->remote = remote;
}

BattleAuthenticatedHandshake::SessionBootstrap::
    ~SessionBootstrap() = default;

BattleAuthenticatedHandshake::SessionBootstrap::SessionBootstrap(
    SessionBootstrap&& source) noexcept = default;

BattleAuthenticatedHandshake::SessionBootstrap&
BattleAuthenticatedHandshake::SessionBootstrap::operator=(
    SessionBootstrap&& source) noexcept = default;

void BattleAuthenticatedHandshake::SessionBootstrap::
ConsumeSessionSeed(
    const SessionSeedConsumer& consumer) {
    if (impl_ == nullptr ||
        impl_->seed_consumed ||
        !consumer) {
        throw std::logic_error(
            "battle session seed is unavailable");
    }
    impl_->seed_consumed = true;
    try {
        consumer(impl_->session_seed);
    } catch (...) {
        SecureZeroMemory(
            impl_->session_seed.data(),
            impl_->session_seed.size());
        throw;
    }
    SecureZeroMemory(
        impl_->session_seed.data(),
        impl_->session_seed.size());
}

const CryptoProvider::Key32&
BattleAuthenticatedHandshake::SessionBootstrap::
BindingFingerprint() const noexcept {
    return impl_->binding_fingerprint;
}

const std::array<std::uint8_t, 16>&
BattleAuthenticatedHandshake::SessionBootstrap::
SessionId() const noexcept {
    return impl_->session_id;
}

std::uint32_t
BattleAuthenticatedHandshake::SessionBootstrap::
BattleSessionGeneration() const noexcept {
    return impl_->battle_session_generation;
}

std::uint32_t
BattleAuthenticatedHandshake::SessionBootstrap::
KeyEpoch() const noexcept {
    return impl_->key_epoch;
}

std::uint32_t
BattleAuthenticatedHandshake::SessionBootstrap::
EndpointGeneration() const noexcept {
    return impl_->endpoint_generation;
}

std::uint8_t
BattleAuthenticatedHandshake::SessionBootstrap::
ActorSlot() const noexcept {
    return impl_->actor_slot;
}

std::uint8_t
BattleAuthenticatedHandshake::SessionBootstrap::
Role() const noexcept {
    return impl_->role;
}

const BattleTicketBinding&
BattleAuthenticatedHandshake::SessionBootstrap::
Binding() const noexcept {
    return impl_->binding;
}

const BattleRemoteEndpoint&
BattleAuthenticatedHandshake::SessionBootstrap::
Remote() const noexcept {
    return impl_->remote;
}

/// ParsedClientAuth 是 cookie 后才允许进入 registry lookup 的固定字段。
struct BattleAuthenticatedHandshake::ParsedClientAuth final {
    /// 析构函数清零已复制的 transcript proof。
    ~ParsedClientAuth() {
        SecureZeroMemory(proof.data(), proof.size());
    }

    /// hello 是 repeated ClientHello authority-free fields。
    BattleClientHello hello;
    /// retry 是客户端回显的 exact cookie。
    BattleRetry retry;
    /// proof 是完整 ClientAuth transcript HMAC。
    CryptoProvider::Key32 proof;
};

/// ReplayEntry 绑定 exact ticket、endpoint、transcript 与首次 accept。
struct BattleAuthenticatedHandshake::ReplayEntry final {
    /// ticket_id 是 canonical registry lookup identity。
    std::string ticket_id;
    /// remote 必须与首次消费 endpoint 完全一致。
    BattleRemoteEndpoint remote;
    /// request_digest 绑定包括 proof 的完整 ClientAuth bytes。
    CryptoProvider::Key32 request_digest;
    /// response 是首次生成且只允许 byte-exact 重放的 accept。
    std::array<std::uint8_t, ServerAcceptBytes> response;
    /// simulation_instance_id 是低敏 routing projection。
    std::string simulation_instance_id;
    /// actor_slot 是 install 冻结的 authority slot。
    std::uint8_t actor_slot;
    /// generation 不因 accept replay 重置。
    std::uint32_t generation;
    /// expires_at_unix_ms 限制 replay 与 session seed 生命周期。
    std::uint64_t expires_at_unix_ms;
    /// bootstrap 只在首次 accepted result 转移，replay entry 不保留 seed。
    std::unique_ptr<SessionBootstrap> bootstrap;
};

BattleAuthenticatedHandshake::BattleAuthenticatedHandshake(
    CryptoProvider& crypto,
    BattleHandshakeCookieGate& cookie_gate,
    BattleTicketAuthenticator& ticket_authenticator,
    std::string simulation_node_id,
    std::string advertised_host,
    const std::uint16_t advertised_port)
    : crypto_(crypto),
      cookie_gate_(cookie_gate),
      ticket_authenticator_(ticket_authenticator),
      simulation_node_id_(std::move(simulation_node_id)),
      advertised_host_(std::move(advertised_host)),
      advertised_port_(advertised_port) {
    if (simulation_node_id_.empty() ||
        advertised_host_.empty() ||
        advertised_port_ == 0) {
        throw std::invalid_argument(
            "authenticated handshake target is invalid");
    }
    replays_.reserve(ReplayLimit);
}

BattleAuthenticatedHandshake::~BattleAuthenticatedHandshake() {
    std::scoped_lock lock(mutex_);
    replays_.clear();
}

BattleAuthenticatedHandshake::Result
BattleAuthenticatedHandshake::HandleClientAuth(
    const std::span<const std::uint8_t> request,
    const BattleRemoteEndpoint& remote,
    const std::uint64_t now_unix_ms) {
    Result result{};
    auto auth = ParseClientAuth(request);
    if (auth == nullptr ||
        !cookie_gate_.ValidateCookieFields(
            auth->hello,
            remote,
            auth->retry,
            now_unix_ms)) {
        return result;
    }
    const auto ticket_id = Base64UrlTicketID(
        auth->hello.ticket_id);
    const auto request_digest = crypto_.Sha256(request);
    std::scoped_lock lock(mutex_);
    std::erase_if(
        replays_,
        [&](const auto& entry) {
            return now_unix_ms >= entry->expires_at_unix_ms;
        });
    const auto replay = std::find_if(
        replays_.begin(),
        replays_.end(),
        [&](const auto& entry) {
            return entry->ticket_id == ticket_id;
        });
    if (replay != replays_.end()) {
        if (!EndpointEqual((*replay)->remote, remote) ||
            !crypto_.ConstantTimeEqual(
                (*replay)->request_digest,
                request_digest)) {
            return result;
        }
        result.outcome = Outcome::Replayed;
        result.response = (*replay)->response;
        result.response_bytes = result.response.size();
        result.simulation_instance_id =
            (*replay)->simulation_instance_id;
        result.actor_slot = (*replay)->actor_slot;
        result.battle_session_generation =
            (*replay)->generation;
        return result;
    }
    if (replays_.size() >= ReplayLimit ||
        next_generation_ == 0 ||
        next_generation_ ==
            std::numeric_limits<std::uint32_t>::max()) {
        return result;
    }
    auto pending = std::make_unique<ReplayEntry>();
    pending->ticket_id = ticket_id;
    pending->remote = remote;
    pending->request_digest = request_digest;
    pending->generation = next_generation_;
    try {
        ticket_authenticator_.AuthenticateAndConsumeBattleTicket(
                ticket_id,
                simulation_node_id_,
                advertised_host_,
                advertised_port_,
                now_unix_ms,
                [&](
                    const auto proof_key,
                    const auto& binding,
                    const auto& binding_fingerprint) {
                    std::vector<std::uint8_t> proof_material;
                    proof_material.reserve(
                        ProofDomain.size() +
                        ClientAuthProofOffset);
                    proof_material.insert(
                        proof_material.end(),
                        ProofDomain.begin(),
                        ProofDomain.end());
                    proof_material.insert(
                        proof_material.end(),
                        request.begin(),
                        request.begin() +
                            ClientAuthProofOffset);
                    auto expected = crypto_.HmacSha256(
                        proof_key,
                        proof_material);
                    SecretWiper expected_wiper(
                        crypto_,
                        expected);
                    if (!crypto_.ConstantTimeEqual(
                            expected,
                            auth->proof)) {
                        return false;
                    }

                    auto server_key =
                        crypto_.GenerateX25519KeyPair();
                    SecretWiper server_key_wiper(
                        crypto_,
                        server_key.secret_scalar);
                    auto shared = crypto_.X25519(
                        server_key.secret_scalar,
                        auth->hello.client_public_key);
                    SecretWiper shared_wiper(crypto_, shared);
                    std::array<std::uint8_t, 32> server_nonce{};
                    crypto_.RandomFill(server_nonce);

                    auto& response = pending->response;
                    std::ranges::copy(
                        ServerAcceptMagic,
                        response.begin());
                    response[4] = WireVersion;
                    response[5] = ServerAcceptKind;
                    std::ranges::copy(
                        server_nonce,
                        response.begin() + 8);
                    std::ranges::copy(
                        server_key.public_key,
                        response.begin() + 40);
                    response[72] = 0;
                    response[73] = static_cast<std::uint8_t>(
                        AcceptPlaintextBytes);

                    std::vector<std::uint8_t> schedule_info;
                    schedule_info.reserve(
                        ScheduleDomain.size() +
                        request_digest.size() +
                        server_nonce.size() +
                        server_key.public_key.size());
                    schedule_info.insert(
                        schedule_info.end(),
                        ScheduleDomain.begin(),
                        ScheduleDomain.end());
                    schedule_info.insert(
                        schedule_info.end(),
                        request_digest.begin(),
                        request_digest.end());
                    schedule_info.insert(
                        schedule_info.end(),
                        server_nonce.begin(),
                        server_nonce.end());
                    schedule_info.insert(
                        schedule_info.end(),
                        server_key.public_key.begin(),
                        server_key.public_key.end());
                    const auto transcript_hash =
                        crypto_.Sha256(schedule_info);
                    std::vector<std::uint8_t> hkdf_info(
                        ScheduleDomain.begin(),
                        ScheduleDomain.end());
                    hkdf_info.insert(
                        hkdf_info.end(),
                        transcript_hash.begin(),
                        transcript_hash.end());
                    auto schedule = crypto_.HkdfSha256(
                        shared,
                        proof_key,
                        hkdf_info,
                        76);
                    SecretWiper schedule_wiper(
                        crypto_,
                        schedule);
                    CryptoProvider::Key32 accept_key{};
                    SecretWiper accept_key_wiper(
                        crypto_,
                        accept_key);
                    CryptoProvider::Nonce12 accept_nonce{};
                    SecretWiper accept_nonce_wiper(
                        crypto_,
                        accept_nonce);
                    CryptoProvider::Key32 session_seed{};
                    SecretWiper session_seed_wiper(
                        crypto_,
                        session_seed);
                    std::ranges::copy_n(
                        schedule.begin(),
                        accept_key.size(),
                        accept_key.begin());
                    std::ranges::copy_n(
                        schedule.begin() + 32,
                        accept_nonce.size(),
                        accept_nonce.begin());
                    std::ranges::copy_n(
                        schedule.begin() + 44,
                        session_seed.size(),
                        session_seed.begin());

                    std::array<std::uint8_t, AcceptPlaintextBytes>
                        plaintext{};
                    SecretWiper plaintext_wiper(
                        crypto_,
                        plaintext);
                    crypto_.RandomFill(
                        std::span(plaintext).first(16));
                    WriteUint32BE(
                        std::span<std::uint8_t, 4>(
                            plaintext.data() + 16,
                            4),
                        pending->generation);
                    WriteUint32BE(
                        std::span<std::uint8_t, 4>(
                            plaintext.data() + 20,
                            4),
                        1);
                    WriteUint32BE(
                        std::span<std::uint8_t, 4>(
                            plaintext.data() + 24,
                            4),
                        1);
                    plaintext[28] = binding.actor_slot;
                    plaintext[29] =
                        binding.role == "owner" ? 1 : 2;
                    const auto fingerprint =
                        DecodeLowerHex32(
                            binding_fingerprint);
                    std::ranges::copy(
                        fingerprint,
                        plaintext.begin() + 32);
                    const auto encrypted =
                        crypto_.EncryptChaCha20Poly1305(
                            accept_key,
                            accept_nonce,
                            std::span(response).first(
                                AcceptAadBytes),
                            plaintext);
                    std::ranges::copy(
                        encrypted.bytes,
                        response.begin() + AcceptAadBytes);
                    std::ranges::copy(
                        encrypted.tag,
                        response.begin() +
                            AcceptAadBytes +
                            AcceptPlaintextBytes);
                    std::array<std::uint8_t, 16>
                        session_id{};
                    std::ranges::copy_n(
                        plaintext.begin(),
                        session_id.size(),
                        session_id.begin());
                    pending->bootstrap =
                        std::make_unique<SessionBootstrap>(
                            session_seed,
                            fingerprint,
                            session_id,
                            pending->generation,
                            1,
                            1,
                            binding.actor_slot,
                            static_cast<std::uint8_t>(
                                binding.role == "owner"
                                    ? 1
                                    : 2),
                            binding,
                            remote);
                    pending->simulation_instance_id =
                        binding.simulation_instance_id;
                    pending->actor_slot =
                        binding.actor_slot;
                    pending->expires_at_unix_ms =
                        binding.expires_at_unix_ms;
                    return true;
                });
    } catch (...) {
        return result;
    }
    Result accepted{};
    try {
        accepted.outcome = Outcome::Accepted;
        accepted.response = pending->response;
        accepted.response_bytes =
            accepted.response.size();
        accepted.simulation_instance_id =
            pending->simulation_instance_id;
        accepted.actor_slot = pending->actor_slot;
        accepted.battle_session_generation =
            pending->generation;
        accepted.bootstrap =
            std::move(pending->bootstrap);
    } catch (...) {
        return Result{};
    }
    replays_.push_back(std::move(pending));
    ++next_generation_;
    return accepted;
}

std::unique_ptr<BattleAuthenticatedHandshake::ParsedClientAuth>
BattleAuthenticatedHandshake::ParseClientAuth(
    const std::span<const std::uint8_t> request) {
    if (request.size() != ClientAuthBytes ||
        !std::ranges::equal(
            ClientAuthMagic,
            request.first(ClientAuthMagic.size())) ||
        request[4] != WireVersion ||
        request[5] != ClientAuthKind ||
        request[6] != 0 ||
        request[7] != 0) {
        return nullptr;
    }
    auto parsed = std::make_unique<ParsedClientAuth>();
    std::ranges::copy(
        request.subspan(8, 16),
        parsed->hello.ticket_id.begin());
    std::ranges::copy(
        request.subspan(24, 32),
        parsed->hello.client_nonce.begin());
    std::ranges::copy(
        request.subspan(56, 32),
        parsed->hello.client_public_key.begin());
    parsed->retry.cookie_epoch = ReadUint32BE(
        std::span<const std::uint8_t, 4>(
            request.data() + 88,
            4));
    std::ranges::copy(
        request.subspan(92, 16),
        parsed->retry.cookie.begin());
    std::ranges::copy(
        request.subspan(ClientAuthProofOffset, 32),
        parsed->proof.begin());
    if (IsAllZero(parsed->hello.ticket_id) ||
        IsAllZero(parsed->hello.client_nonce) ||
        IsAllZero(parsed->hello.client_public_key) ||
        parsed->retry.cookie_epoch == 0 ||
        IsAllZero(parsed->retry.cookie) ||
        IsAllZero(parsed->proof)) {
        return nullptr;
    }
    return parsed;
}

}  // namespace ihomeland::sim
