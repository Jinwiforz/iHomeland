#include "ihomeland/sim/transport/secure_datagram.hpp"

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

constexpr std::array<std::uint8_t, 4> SecureMagic{
    'I', 'H', 'B', 'T'};
constexpr std::uint8_t WireVersion = 1;
constexpr std::string_view TrafficDomain =
    "ihomeland/battle/traffic/v1";
constexpr std::uint8_t ClientToServerDirection = 1;
constexpr std::uint8_t ServerToClientDirection = 2;
constexpr std::size_t MaximumProtectedPayloadBytes =
    BattleSecureChannel::MaximumDatagramBytes -
    BattleSecureChannel::SecureHeaderBytes -
    BattleSecureChannel::AeadTagBytes;

/// IsAllZero 拒绝公开 routing identity 与 session seed 零值。
template <std::size_t Size>
[[nodiscard]] bool IsAllZero(
    const std::array<std::uint8_t, Size>& value) noexcept {
    return std::ranges::all_of(
        value,
        [](const std::uint8_t item) { return item == 0; });
}

/// WriteUint16BE 写入 network-order uint16。
void WriteUint16BE(
    std::uint8_t* output,
    const std::uint16_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 8U);
    output[1] = static_cast<std::uint8_t>(value);
}

/// WriteUint32BE 写入 network-order uint32。
void WriteUint32BE(
    std::uint8_t* output,
    const std::uint32_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// WriteUint64BE 写入 network-order uint64。
void WriteUint64BE(
    std::uint8_t* output,
    const std::uint64_t value) noexcept {
    for (std::size_t index = 0; index < 8; ++index) {
        output[index] = static_cast<std::uint8_t>(
            value >> ((7U - index) * 8U));
    }
}

/// ReadUint16BE 读取 network-order uint16。
[[nodiscard]] std::uint16_t ReadUint16BE(
    const std::uint8_t* input) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(input[0]) << 8U) |
        static_cast<std::uint16_t>(input[1]));
}

/// ReadUint32BE 读取 network-order uint32。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::uint8_t* input) noexcept {
    return (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// ReadUint64BE 读取 network-order uint64。
[[nodiscard]] std::uint64_t ReadUint64BE(
    const std::uint8_t* input) noexcept {
    std::uint64_t value = 0;
    for (std::size_t index = 0; index < 8; ++index) {
        value = (value << 8U) | input[index];
    }
    return value;
}

/// DirectionInfo 绑定方向、epoch 与 immutable session identity。
[[nodiscard]] std::vector<std::uint8_t> DirectionInfo(
    const std::uint8_t direction,
    const std::uint32_t key_epoch,
    const BattleSecureIdentity& identity) {
    std::vector<std::uint8_t> info;
    info.reserve(
        TrafficDomain.size() + 1 + 4 + 8 + 4 + 4 + 8);
    info.insert(
        info.end(),
        TrafficDomain.begin(),
        TrafficDomain.end());
    info.push_back(direction);
    std::array<std::uint8_t, 4> encoded{};
    WriteUint32BE(encoded.data(), key_epoch);
    info.insert(info.end(), encoded.begin(), encoded.end());
    info.insert(
        info.end(),
        identity.session_id_digest.begin(),
        identity.session_id_digest.end());
    WriteUint32BE(
        encoded.data(),
        identity.battle_session_generation);
    info.insert(info.end(), encoded.begin(), encoded.end());
    WriteUint32BE(
        encoded.data(),
        identity.endpoint_generation);
    info.insert(info.end(), encoded.begin(), encoded.end());
    info.insert(
        info.end(),
        identity.binding_discriminator.begin(),
        identity.binding_discriminator.end());
    return info;
}

/// EncodeHeader 构造唯一 canonical 48-byte AAD。
[[nodiscard]] std::array<
    std::uint8_t,
    BattleSecureChannel::SecureHeaderBytes>
EncodeHeader(
    const BattlePacketKind packet_kind,
    const BattleSecureIdentity& identity,
    const std::uint32_t key_epoch,
    const std::uint64_t packet_sequence,
    const std::size_t protected_payload_bytes) {
    std::array<
        std::uint8_t,
        BattleSecureChannel::SecureHeaderBytes> header{};
    std::ranges::copy(SecureMagic, header.begin());
    header[4] = WireVersion;
    header[5] = static_cast<std::uint8_t>(packet_kind);
    std::ranges::copy(
        identity.session_id_digest,
        header.begin() + 8);
    WriteUint32BE(
        header.data() + 16,
        identity.battle_session_generation);
    WriteUint32BE(header.data() + 20, key_epoch);
    WriteUint64BE(header.data() + 24, packet_sequence);
    WriteUint16BE(
        header.data() + 32,
        static_cast<std::uint16_t>(
            protected_payload_bytes));
    WriteUint32BE(
        header.data() + 34,
        identity.endpoint_generation);
    std::ranges::copy(
        identity.binding_discriminator,
        header.begin() + 38);
    return header;
}

/// WipeVector 在异常与正常路径清零临时 HKDF output。
class WipeVector final {
public:
    /// 构造函数借用 owner 生命周期覆盖本 guard 的 mutable bytes。
    WipeVector(
        CryptoProvider& crypto,
        std::vector<std::uint8_t>& bytes) noexcept
        : crypto_(crypto),
          bytes_(bytes) {}

    /// 析构函数执行不可优化清零。
    ~WipeVector() {
        crypto_.SecureZero(bytes_);
    }

    WipeVector(const WipeVector&) = delete;
    WipeVector& operator=(const WipeVector&) = delete;

private:
    /// crypto_ 提供统一 secure zero。
    CryptoProvider& crypto_;
    /// bytes_ 是 caller-owned temporary。
    std::vector<std::uint8_t>& bytes_;
};

}  // namespace

/// SecretMaterial 以一个 locked allocation 保存 send/receive traffic 与 rekey key。
struct BattleSecureChannel::SecretMaterial final {
    /// Keys 的索引语义固定，禁止方向复用。
    enum Index : std::size_t {
        SendTraffic = 0,
        ReceiveTraffic = 1,
        SendRekey = 2,
        ReceiveRekey = 3,
        PreviousReceiveTraffic = 4,
    };

    /// 构造函数先锁页，调用方随后复制派生 key。
    SecretMaterial() {
        if (VirtualLock(keys.data(), sizeof(keys)) == 0) {
            throw std::runtime_error(
                "battle traffic key memory lock failed");
        }
        locked = true;
    }

    /// 析构函数先清零所有 key 再解锁。
    ~SecretMaterial() {
        SecureZeroMemory(keys.data(), sizeof(keys));
        if (locked) {
            static_cast<void>(
                VirtualUnlock(keys.data(), sizeof(keys)));
        }
    }

    SecretMaterial(const SecretMaterial&) = delete;
    SecretMaterial& operator=(const SecretMaterial&) = delete;

    /// keys 同时驻留四个固定 32-byte secrets。
    std::array<CryptoProvider::Key32, 5> keys{};
    /// locked 防止构造失败路径执行无效 unlock。
    bool locked{false};
};

/// ReplayWindow 保存 highest-relative 256-bit accepted bitmap。
struct BattleSecureChannel::ReplayWindow final {
    /// Classify 不改变状态，future jump 只能在AEAD成功后调用。
    [[nodiscard]] BattleOpenDisposition Classify(
        const std::uint64_t sequence) const noexcept {
        if (highest == 0) {
            return sequence > ReplayWindowPackets
                ? BattleOpenDisposition::FutureJump
                : BattleOpenDisposition::Accepted;
        }
        if (sequence > highest) {
            return sequence - highest > ReplayWindowPackets
                ? BattleOpenDisposition::FutureJump
                : BattleOpenDisposition::Accepted;
        }
        const auto offset = highest - sequence;
        if (offset >= ReplayWindowPackets) {
            return BattleOpenDisposition::TooOld;
        }
        const auto word = static_cast<std::size_t>(
            offset / 64U);
        const auto bit = static_cast<std::size_t>(
            offset % 64U);
        return (bitmap[word] & (std::uint64_t{1} << bit)) != 0
            ? BattleOpenDisposition::Duplicate
            : BattleOpenDisposition::Accepted;
    }

    /// Commit 只接收 Classify 已接受的 sequence。
    void Commit(const std::uint64_t sequence) noexcept {
        if (highest == 0) {
            highest = sequence;
            bitmap[0] = 1;
            return;
        }
        if (sequence > highest) {
            const auto shift = sequence - highest;
            std::array<std::uint64_t, 4> shifted{};
            for (std::size_t offset = 0;
                 offset + shift < ReplayWindowPackets;
                 ++offset) {
                const auto word = offset / 64U;
                const auto bit = offset % 64U;
                if ((bitmap[word] &
                     (std::uint64_t{1} << bit)) != 0) {
                    const auto target =
                        offset +
                        static_cast<std::size_t>(shift);
                    shifted[target / 64U] |=
                        std::uint64_t{1} << (target % 64U);
                }
            }
            bitmap = shifted;
            highest = sequence;
            bitmap[0] |= 1;
            return;
        }
        const auto offset = highest - sequence;
        bitmap[static_cast<std::size_t>(offset / 64U)] |=
            std::uint64_t{1} <<
            static_cast<std::size_t>(offset % 64U);
    }

    /// highest 是当前已认证最大 sequence，零表示空 window。
    std::uint64_t highest{};
    /// bitmap bit N 表示 highest-N 已经接受。
    std::array<std::uint64_t, 4> bitmap{};
};

CryptoProvider::Nonce12 BattlePacketNonce(
    const std::uint32_t key_epoch,
    const std::uint64_t packet_sequence) {
    if (key_epoch == 0 || packet_sequence == 0) {
        throw std::invalid_argument(
            "battle packet nonce input is invalid");
    }
    CryptoProvider::Nonce12 nonce{};
    WriteUint32BE(nonce.data(), key_epoch);
    WriteUint64BE(nonce.data() + 4, packet_sequence);
    return nonce;
}

BattleSecureChannel::BattleSecureChannel(
    CryptoProvider& crypto,
    const CryptoProvider::Key32& session_seed,
    const BattleTransportRole local_role,
    BattleSecureIdentity identity,
    const std::uint32_t key_epoch,
    const std::uint64_t next_send_sequence,
    const std::uint64_t epoch_started_unix_ms,
    const std::uint64_t sent_packets_current_epoch)
    : crypto_(crypto),
      local_role_(local_role),
      identity_(std::move(identity)),
      key_epoch_(key_epoch),
      next_send_sequence_(next_send_sequence),
      epoch_started_unix_ms_(epoch_started_unix_ms),
      sent_packets_current_epoch_(
          sent_packets_current_epoch),
      replay_(std::make_unique<ReplayWindow>()) {
    if ((local_role_ != BattleTransportRole::Client &&
         local_role_ != BattleTransportRole::Server) ||
        IsAllZero(session_seed) ||
        IsAllZero(identity_.session_id_digest) ||
        identity_.battle_session_generation == 0 ||
        identity_.endpoint_generation == 0 ||
        IsAllZero(identity_.binding_discriminator) ||
        key_epoch_ == 0 ||
        next_send_sequence_ == 0 ||
        epoch_started_unix_ms_ == 0) {
        throw std::invalid_argument(
            "battle secure channel input is invalid");
    }
    secrets_ = std::make_unique<SecretMaterial>();
    auto c2s = crypto_.HkdfSha256(
        session_seed,
        {},
        DirectionInfo(
            ClientToServerDirection,
            key_epoch_,
            identity_),
        64);
    WipeVector c2s_wiper(crypto_, c2s);
    auto s2c = crypto_.HkdfSha256(
        session_seed,
        {},
        DirectionInfo(
            ServerToClientDirection,
            key_epoch_,
            identity_),
        64);
    WipeVector s2c_wiper(crypto_, s2c);
    const auto& send =
        local_role_ == BattleTransportRole::Client
        ? c2s
        : s2c;
    const auto& receive =
        local_role_ == BattleTransportRole::Client
        ? s2c
        : c2s;
    std::ranges::copy_n(
        send.begin(),
        32,
        secrets_->keys[SecretMaterial::SendTraffic].begin());
    std::ranges::copy_n(
        receive.begin(),
        32,
        secrets_->keys[
            SecretMaterial::ReceiveTraffic].begin());
    std::ranges::copy_n(
        send.begin() + 32,
        32,
        secrets_->keys[SecretMaterial::SendRekey].begin());
    std::ranges::copy_n(
        receive.begin() + 32,
        32,
        secrets_->keys[
            SecretMaterial::ReceiveRekey].begin());
}

BattleSecureChannel::~BattleSecureChannel() {
    std::scoped_lock lock(mutex_);
    secrets_.reset();
    replay_.reset();
}

BattleSecureChannel::SealResult BattleSecureChannel::Seal(
    const BattlePacketKind packet_kind,
    const std::span<const std::uint8_t> payload,
    const std::uint64_t now_unix_ms) {
    const auto kind = static_cast<std::uint8_t>(packet_kind);
    if (kind < static_cast<std::uint8_t>(
                   BattlePacketKind::Raw) ||
        kind > static_cast<std::uint8_t>(
                   BattlePacketKind::Control) ||
        payload.empty() ||
        payload.size() > MaximumProtectedPayloadBytes) {
        throw std::invalid_argument(
            "battle secure payload is invalid");
    }
    std::scoped_lock lock(mutex_);
    if (closed_ || secrets_ == nullptr) {
        return {
            .disposition =
                BattleSealDisposition::Closed};
    }
    const auto rollover_required =
        RolloverRequiredLocked(now_unix_ms);
    if (closed_) {
        return {
            .disposition =
                BattleSealDisposition::Closed};
    }
    if (rollover_required &&
        packet_kind != BattlePacketKind::Control) {
        return {
            .disposition =
                BattleSealDisposition::RolloverRequired};
    }
    if (send_exhausted_) {
        CloseLocked();
        return {
            .disposition =
                BattleSealDisposition::
                    SequenceExhausted};
    }
    const auto sequence = next_send_sequence_;
    if (sequence ==
        std::numeric_limits<std::uint64_t>::max()) {
        send_exhausted_ = true;
    } else {
        ++next_send_sequence_;
    }
    if (sent_packets_current_epoch_ !=
        std::numeric_limits<std::uint64_t>::max()) {
        ++sent_packets_current_epoch_;
    }
    const auto header = EncodeHeader(
        packet_kind,
        identity_,
        key_epoch_,
        sequence,
        payload.size());
    const auto nonce = BattlePacketNonce(
        key_epoch_,
        sequence);
    const auto encrypted =
        crypto_.EncryptChaCha20Poly1305(
            secrets_->keys[
                SecretMaterial::SendTraffic],
            nonce,
            header,
            payload);
    SealResult result{
        .disposition = BattleSealDisposition::Sealed,
        .packet_sequence = sequence,
        .datagram =
            std::vector<std::uint8_t>(
                SecureHeaderBytes +
                encrypted.bytes.size() +
                encrypted.tag.size()),
    };
    std::ranges::copy(
        header,
        result.datagram.begin());
    std::ranges::copy(
        encrypted.bytes,
        result.datagram.begin() + SecureHeaderBytes);
    std::ranges::copy(
        encrypted.tag,
        result.datagram.end() - AeadTagBytes);
    return result;
}

BattleSecureChannel::OpenResult BattleSecureChannel::Open(
    const std::span<const std::uint8_t> datagram,
    const std::uint64_t now_unix_ms) {
    OpenResult result{};
    if (datagram.size() <=
            SecureHeaderBytes + AeadTagBytes ||
        datagram.size() > MaximumDatagramBytes) {
        return result;
    }
    const auto header =
        datagram.first(SecureHeaderBytes);
    const auto kind = header[5];
    const auto protected_bytes =
        ReadUint16BE(header.data() + 32);
    if (!std::ranges::equal(
            SecureMagic,
            header.first(SecureMagic.size())) ||
        header[4] != WireVersion ||
        kind < static_cast<std::uint8_t>(
                   BattlePacketKind::Raw) ||
        kind > static_cast<std::uint8_t>(
                   BattlePacketKind::Control) ||
        header[6] != 0 ||
        header[7] != 0 ||
        !std::ranges::equal(
            identity_.session_id_digest,
            header.subspan(8, 8)) ||
        ReadUint32BE(header.data() + 16) !=
            identity_.battle_session_generation ||
        ReadUint64BE(header.data() + 24) == 0 ||
        protected_bytes == 0 ||
        static_cast<std::size_t>(protected_bytes) +
                SecureHeaderBytes + AeadTagBytes !=
            datagram.size() ||
        ReadUint32BE(header.data() + 34) !=
            identity_.endpoint_generation ||
        !std::ranges::equal(
            identity_.binding_discriminator,
            header.subspan(38, 8)) ||
        header[46] != 0 ||
        header[47] != 0) {
        return result;
    }
    const auto sequence =
        ReadUint64BE(header.data() + 24);
    const auto packet_epoch =
        ReadUint32BE(header.data() + 20);
    CryptoProvider::Tag16 tag{};
    std::ranges::copy(
        datagram.last(AeadTagBytes),
        tag.begin());
    const auto nonce = BattlePacketNonce(
        packet_epoch,
        sequence);
    std::scoped_lock lock(mutex_);
    if (closed_ || secrets_ == nullptr ||
        now_unix_ms < epoch_started_unix_ms_) {
        return result;
    }
    if (previous_epoch_ != 0 &&
        now_unix_ms >= previous_deadline_unix_ms_) {
        crypto_.SecureZero(
            secrets_->keys[
                SecretMaterial::PreviousReceiveTraffic]);
        previous_epoch_ = 0;
        previous_deadline_unix_ms_ = 0;
        previous_replay_.reset();
    }
    const CryptoProvider::Key32* receive_key = nullptr;
    ReplayWindow* replay = nullptr;
    if (packet_epoch == key_epoch_) {
        receive_key = &secrets_->keys[
            SecretMaterial::ReceiveTraffic];
        replay = replay_.get();
    } else if (
        packet_epoch == previous_epoch_ &&
        previous_replay_ != nullptr) {
        receive_key = &secrets_->keys[
            SecretMaterial::PreviousReceiveTraffic];
        replay = previous_replay_.get();
    } else {
        return result;
    }
    auto plaintext =
        crypto_.DecryptChaCha20Poly1305(
            *receive_key,
            nonce,
            header,
            datagram.subspan(
                SecureHeaderBytes,
                protected_bytes),
            tag);
    if (!plaintext.has_value()) {
        result.disposition =
            BattleOpenDisposition::AuthenticationFailed;
        return result;
    }
    const auto replay_disposition =
        replay->Classify(sequence);
    if (replay_disposition !=
        BattleOpenDisposition::Accepted) {
        crypto_.SecureZero(*plaintext);
        result.disposition = replay_disposition;
        return result;
    }
    replay->Commit(sequence);
    result.disposition =
        BattleOpenDisposition::Accepted;
    result.packet_kind =
        static_cast<BattlePacketKind>(kind);
    result.packet_sequence = sequence;
    result.plaintext = std::move(*plaintext);
    return result;
}

bool BattleSecureChannel::RolloverRequired(
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(mutex_);
    return RolloverRequiredLocked(now_unix_ms);
}

bool BattleSecureChannel::CommitAuthenticatedRollover(
    const CryptoProvider::Key32& rekey_nonce,
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(mutex_);
    if (closed_ || secrets_ == nullptr ||
        IsAllZero(rekey_nonce) ||
        now_unix_ms < epoch_started_unix_ms_ ||
        (rollover_deadline_unix_ms_ != 0 &&
         now_unix_ms >= rollover_deadline_unix_ms_) ||
        key_epoch_ ==
            std::numeric_limits<std::uint32_t>::max() ||
        now_unix_ms >
            std::numeric_limits<std::uint64_t>::max() -
                PreviousEpochOverlapMilliseconds) {
        CloseLocked();
        return false;
    }
    const auto next_epoch = key_epoch_ + 1;
    const auto send_direction =
        local_role_ == BattleTransportRole::Client
        ? ClientToServerDirection
        : ServerToClientDirection;
    const auto receive_direction =
        local_role_ == BattleTransportRole::Client
        ? ServerToClientDirection
        : ClientToServerDirection;
    auto next_send = crypto_.HkdfSha256(
        secrets_->keys[SecretMaterial::SendRekey],
        rekey_nonce,
        DirectionInfo(
            send_direction,
            next_epoch,
            identity_),
        64);
    WipeVector next_send_wiper(crypto_, next_send);
    auto next_receive = crypto_.HkdfSha256(
        secrets_->keys[SecretMaterial::ReceiveRekey],
        rekey_nonce,
        DirectionInfo(
            receive_direction,
            next_epoch,
            identity_),
        64);
    WipeVector next_receive_wiper(
        crypto_,
        next_receive);
    auto next_replay = std::make_unique<ReplayWindow>();

    secrets_->keys[
        SecretMaterial::PreviousReceiveTraffic] =
        secrets_->keys[
            SecretMaterial::ReceiveTraffic];
    std::ranges::copy_n(
        next_send.begin(),
        32,
        secrets_->keys[
            SecretMaterial::SendTraffic].begin());
    std::ranges::copy_n(
        next_send.begin() + 32,
        32,
        secrets_->keys[
            SecretMaterial::SendRekey].begin());
    std::ranges::copy_n(
        next_receive.begin(),
        32,
        secrets_->keys[
            SecretMaterial::ReceiveTraffic].begin());
    std::ranges::copy_n(
        next_receive.begin() + 32,
        32,
        secrets_->keys[
            SecretMaterial::ReceiveRekey].begin());
    previous_epoch_ = key_epoch_;
    previous_deadline_unix_ms_ =
        now_unix_ms +
        PreviousEpochOverlapMilliseconds;
    previous_replay_ = std::move(replay_);
    replay_ = std::move(next_replay);
    key_epoch_ = next_epoch;
    next_send_sequence_ = 1;
    sent_packets_current_epoch_ = 0;
    epoch_started_unix_ms_ = now_unix_ms;
    rollover_deadline_unix_ms_ = 0;
    send_exhausted_ = false;
    return true;
}

bool BattleSecureChannel::CommitEndpointGeneration(
    const std::uint32_t expected_current_generation,
    const std::uint32_t next_generation) {
    std::scoped_lock lock(mutex_);
    if (closed_ ||
        expected_current_generation == 0 ||
        identity_.endpoint_generation !=
            expected_current_generation ||
        expected_current_generation ==
            std::numeric_limits<std::uint32_t>::max() ||
        next_generation !=
            expected_current_generation + 1U) {
        return false;
    }
    identity_.endpoint_generation = next_generation;
    return true;
}

bool BattleSecureChannel::RolloverRequiredLocked(
    const std::uint64_t now_unix_ms) {
    if (closed_ || secrets_ == nullptr) {
        return true;
    }
    if (now_unix_ms < epoch_started_unix_ms_) {
        CloseLocked();
        return true;
    }
    if (previous_epoch_ != 0 &&
        now_unix_ms >= previous_deadline_unix_ms_) {
        crypto_.SecureZero(
            secrets_->keys[
                SecretMaterial::PreviousReceiveTraffic]);
        previous_epoch_ = 0;
        previous_deadline_unix_ms_ = 0;
        previous_replay_.reset();
    }
    const auto required =
        now_unix_ms - epoch_started_unix_ms_ >=
            RekeyIntervalMilliseconds ||
        sent_packets_current_epoch_ >=
            RekeyPacketLimit;
    if (!required) {
        return false;
    }
    if (rollover_deadline_unix_ms_ == 0) {
        if (now_unix_ms >
            std::numeric_limits<std::uint64_t>::max() -
                RolloverDeadlineMilliseconds) {
            CloseLocked();
            return true;
        }
        rollover_deadline_unix_ms_ =
            now_unix_ms +
            RolloverDeadlineMilliseconds;
    }
    if (now_unix_ms >=
        rollover_deadline_unix_ms_) {
        CloseLocked();
    }
    return true;
}

void BattleSecureChannel::CloseLocked() noexcept {
    closed_ = true;
    send_exhausted_ = true;
    secrets_.reset();
    replay_.reset();
    previous_replay_.reset();
    previous_epoch_ = 0;
    previous_deadline_unix_ms_ = 0;
}

}  // namespace ihomeland::sim
