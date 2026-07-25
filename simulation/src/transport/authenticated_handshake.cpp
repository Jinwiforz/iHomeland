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

/// LockedSessionSeed 在 replay/session owner 内保存 VirtualLock 的派生 seed。
class LockedSessionSeed final {
public:
    /// 构造函数锁页并复制非零 seed。
    explicit LockedSessionSeed(
        const CryptoProvider::Key32& source) {
        if (IsAllZero(source) ||
            VirtualLock(material_.data(), material_.size()) == 0) {
            throw std::runtime_error(
                "battle session seed lock failed");
        }
        locked_ = true;
        std::ranges::copy(source, material_.begin());
    }

    /// 析构函数先清零再解锁。
    ~LockedSessionSeed() {
        SecureZeroMemory(material_.data(), material_.size());
        if (locked_) {
            static_cast<void>(
                VirtualUnlock(material_.data(), material_.size()));
        }
    }

    LockedSessionSeed(const LockedSessionSeed&) = delete;
    LockedSessionSeed& operator=(const LockedSessionSeed&) = delete;

private:
    /// material_ 由后续 packet key schedule owner消费。
    CryptoProvider::Key32 material_{};
    /// locked_ 防止无效 VirtualUnlock。
    bool locked_{false};
};

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
    /// session_seed 保存后续方向 key schedule 的唯一根。
    std::unique_ptr<LockedSessionSeed> session_seed;
};

BattleAuthenticatedHandshake::BattleAuthenticatedHandshake(
    CryptoProvider& crypto,
    BattleHandshakeCookieGate& cookie_gate,
    SimulationNode& node,
    std::string simulation_node_id,
    std::string advertised_host,
    const std::uint16_t advertised_port)
    : crypto_(crypto),
      cookie_gate_(cookie_gate),
      node_(node),
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
        node_.AuthenticateAndConsumeBattleTicket(
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
                    pending->session_seed =
                        std::make_unique<LockedSessionSeed>(
                            session_seed);
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
    ++next_generation_;
    replays_.push_back(std::move(pending));
    const auto& established = *replays_.back();
    try {
        result.outcome = Outcome::Accepted;
        result.response = established.response;
        result.response_bytes = result.response.size();
        result.simulation_instance_id =
            established.simulation_instance_id;
        result.actor_slot = established.actor_slot;
        result.battle_session_generation =
            established.generation;
    } catch (...) {
        return Result{};
    }
    return result;
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
