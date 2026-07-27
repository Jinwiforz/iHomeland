#include "ihomeland/qualification/battle/protocol_client.hpp"

#include "ihomeland/sim/transport/crypto_provider.hpp"

#define NOMINMAX
#include <windows.h>

#include <algorithm>
#include <array>
#include <limits>
#include <mutex>
#include <ranges>
#include <stdexcept>
#include <string_view>
#include <vector>

namespace ihomeland::qualification::battle {
namespace {

using CryptoProvider = ihomeland::sim::CryptoProvider;

constexpr std::array<std::uint8_t, 4> SecureMagic{
    'I', 'H', 'B', 'T'};
constexpr std::uint8_t WireVersion = 1;
constexpr std::uint8_t ClientToServerDirection = 1;
constexpr std::uint8_t ServerToClientDirection = 2;
constexpr std::string_view TrafficDomain =
    "ihomeland/battle/traffic/v1";
constexpr std::size_t MaximumProtectedPayloadBytes =
    ProtocolSecureChannel::MaximumDatagramBytes -
    ProtocolSecureChannel::SecureHeaderBytes -
    ProtocolSecureChannel::AeadTagBytes;

/// IsAllZero 拒绝无效的 session、binding 与 key material。
template <std::size_t Size>
[[nodiscard]] bool IsAllZero(
    const std::array<std::uint8_t, Size>& value) noexcept {
    return std::ranges::all_of(
        value,
        [](const std::uint8_t item) { return item == 0; });
}

/// WriteUint16BE 编码 network-order uint16。
void WriteUint16BE(
    std::uint8_t* output,
    const std::uint16_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 8U);
    output[1] = static_cast<std::uint8_t>(value);
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

/// WriteUint64BE 编码 network-order uint64。
void WriteUint64BE(
    std::uint8_t* output,
    const std::uint64_t value) noexcept {
    for (std::size_t index = 0; index < 8; ++index) {
        output[index] = static_cast<std::uint8_t>(
            value >> ((7U - index) * 8U));
    }
}

/// ReadUint16BE 解码 network-order uint16。
[[nodiscard]] std::uint16_t ReadUint16BE(
    const std::uint8_t* input) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(input[0]) << 8U) |
        static_cast<std::uint16_t>(input[1]));
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

/// ReadUint64BE 解码 network-order uint64。
[[nodiscard]] std::uint64_t ReadUint64BE(
    const std::uint8_t* input) noexcept {
    std::uint64_t value = 0;
    for (std::size_t index = 0; index < 8; ++index) {
        value = (value << 8U) | input[index];
    }
    return value;
}

/// PacketNonce 组合 epoch 与 nonzero sequence，避免方向内 nonce 重用。
[[nodiscard]] CryptoProvider::Nonce12 PacketNonce(
    const std::uint32_t epoch,
    const std::uint64_t sequence) {
    if (epoch == 0 || sequence == 0) {
        throw std::invalid_argument(
            "qualification packet nonce is invalid");
    }
    CryptoProvider::Nonce12 nonce{};
    WriteUint32BE(nonce.data(), epoch);
    WriteUint64BE(nonce.data() + 4, sequence);
    return nonce;
}

/// DirectionInfo 绑定方向、epoch 与当前 session/endpoint identity。
[[nodiscard]] std::vector<std::uint8_t> DirectionInfo(
    const std::uint8_t direction,
    const std::uint32_t epoch,
    const SecureIdentity& identity) {
    std::vector<std::uint8_t> output;
    output.reserve(
        TrafficDomain.size() + 1 + 4 + 8 + 4 + 4 + 8);
    output.insert(
        output.end(),
        TrafficDomain.begin(),
        TrafficDomain.end());
    output.push_back(direction);
    std::array<std::uint8_t, 4> encoded{};
    WriteUint32BE(encoded.data(), epoch);
    output.insert(
        output.end(),
        encoded.begin(),
        encoded.end());
    output.insert(
        output.end(),
        identity.session_id_digest.begin(),
        identity.session_id_digest.end());
    WriteUint32BE(
        encoded.data(),
        identity.battle_session_generation);
    output.insert(
        output.end(),
        encoded.begin(),
        encoded.end());
    WriteUint32BE(
        encoded.data(),
        identity.endpoint_generation);
    output.insert(
        output.end(),
        encoded.begin(),
        encoded.end());
    output.insert(
        output.end(),
        identity.binding_discriminator.begin(),
        identity.binding_discriminator.end());
    return output;
}

/// EncodeHeader 构造唯一 canonical 48-byte AAD。
[[nodiscard]] std::array<
    std::uint8_t,
    ProtocolSecureChannel::SecureHeaderBytes>
EncodeHeader(
    const PacketKind kind,
    const SecureIdentity& identity,
    const std::uint32_t epoch,
    const std::uint64_t sequence,
    const std::size_t payload_bytes) {
    std::array<
        std::uint8_t,
        ProtocolSecureChannel::SecureHeaderBytes> header{};
    std::ranges::copy(SecureMagic, header.begin());
    header[4] = WireVersion;
    header[5] = static_cast<std::uint8_t>(kind);
    std::ranges::copy(
        identity.session_id_digest,
        header.begin() + 8);
    WriteUint32BE(
        header.data() + 16,
        identity.battle_session_generation);
    WriteUint32BE(header.data() + 20, epoch);
    WriteUint64BE(header.data() + 24, sequence);
    WriteUint16BE(
        header.data() + 32,
        static_cast<std::uint16_t>(payload_bytes));
    WriteUint32BE(
        header.data() + 34,
        identity.endpoint_generation);
    std::ranges::copy(
        identity.binding_discriminator,
        header.begin() + 38);
    return header;
}

/// WipeVector 在所有退出路径清零 HKDF output。
class WipeVector final {
public:
    /// 构造函数借用 owner 生命周期覆盖本 guard 的 vector。
    WipeVector(
        CryptoProvider& crypto,
        std::vector<std::uint8_t>& value) noexcept
        : crypto_(crypto),
          value_(value) {}

    /// 析构函数执行不可优化清零。
    ~WipeVector() {
        crypto_.SecureZero(value_);
    }

    WipeVector(const WipeVector&) = delete;
    WipeVector& operator=(const WipeVector&) = delete;

private:
    /// crypto_ 提供 secure zero。
    CryptoProvider& crypto_;
    /// value_ 是 caller-owned temporary。
    std::vector<std::uint8_t>& value_;
};

}  // namespace

/// ProtocolSecureChannel::Impl 隔离 secret、replay 与串行化状态。
struct ProtocolSecureChannel::Impl final {
    /// KeyIndex 固定锁页内 key 的方向与代际语义。
    enum KeyIndex : std::size_t {
        C2STraffic = 0,
        S2CTraffic = 1,
        C2SRekey = 2,
        S2CRekey = 3,
        PreviousS2CTraffic = 4,
    };

    /// ReplayWindow 保存 highest-relative 256-bit accepted bitmap。
    struct ReplayWindow final {
        /// Classify 在 AEAD 成功后判断但不改变窗口。
        [[nodiscard]] OpenDisposition Classify(
            const std::uint64_t sequence) const noexcept {
            if (highest == 0) {
                return sequence > ReplayWindowPackets
                    ? OpenDisposition::FutureJump
                    : OpenDisposition::Accepted;
            }
            if (sequence > highest) {
                return sequence - highest >
                        ReplayWindowPackets
                    ? OpenDisposition::FutureJump
                    : OpenDisposition::Accepted;
            }
            const auto offset = highest - sequence;
            if (offset >= ReplayWindowPackets) {
                return OpenDisposition::TooOld;
            }
            const auto word =
                static_cast<std::size_t>(offset / 64U);
            const auto bit =
                static_cast<std::size_t>(offset % 64U);
            return (bitmap[word] &
                    (std::uint64_t{1} << bit)) != 0
                ? OpenDisposition::Duplicate
                : OpenDisposition::Accepted;
        }

        /// Commit 只消费已由 Classify 接受的 sequence。
        void Commit(
            const std::uint64_t sequence) noexcept {
            if (highest == 0) {
                highest = sequence;
                bitmap[0] = 1;
                return;
            }
            if (sequence > highest) {
                const auto shift = sequence - highest;
                std::array<std::uint64_t, 4> shifted{};
                for (std::size_t offset = 0;
                     offset + shift <
                     ReplayWindowPackets;
                     ++offset) {
                    if ((bitmap[offset / 64U] &
                         (std::uint64_t{1} <<
                          (offset % 64U))) == 0) {
                        continue;
                    }
                    const auto target =
                        offset +
                        static_cast<std::size_t>(shift);
                    shifted[target / 64U] |=
                        std::uint64_t{1} <<
                        (target % 64U);
                }
                bitmap = shifted;
                highest = sequence;
                bitmap[0] |= 1;
                return;
            }
            const auto offset = highest - sequence;
            bitmap[static_cast<std::size_t>(
                offset / 64U)] |=
                std::uint64_t{1} <<
                static_cast<std::size_t>(
                    offset % 64U);
        }

        /// highest 是当前已认证最大 sequence。
        std::uint64_t highest{};
        /// bitmap bit N 表示 highest-N 已接受。
        std::array<std::uint64_t, 4> bitmap{};
    };

    /// 构造函数锁页并派生初始 client direction keys。
    Impl(
        const std::array<std::uint8_t, 32>& seed,
        SecureIdentity source_identity,
        const std::uint32_t initial_epoch,
        const std::uint64_t started_unix_ms)
        : identity(std::move(source_identity)),
          key_epoch(initial_epoch),
          epoch_started_unix_ms(started_unix_ms) {
        if (IsAllZero(seed) ||
            IsAllZero(identity.session_id_digest) ||
            identity.battle_session_generation == 0 ||
            identity.endpoint_generation == 0 ||
            IsAllZero(identity.binding_discriminator) ||
            key_epoch == 0 ||
            epoch_started_unix_ms == 0 ||
            VirtualLock(keys.data(), sizeof(keys)) == 0) {
            throw std::invalid_argument(
                "qualification secure channel input is invalid");
        }
        keys_locked = true;
        try {
            auto c2s = crypto.HkdfSha256(
                seed,
                {},
                DirectionInfo(
                    ClientToServerDirection,
                    key_epoch,
                    identity),
                64);
            WipeVector c2s_wiper(crypto, c2s);
            auto s2c = crypto.HkdfSha256(
                seed,
                {},
                DirectionInfo(
                    ServerToClientDirection,
                    key_epoch,
                    identity),
                64);
            WipeVector s2c_wiper(crypto, s2c);
            std::ranges::copy_n(
                c2s.begin(),
                32,
                keys[C2STraffic].begin());
            std::ranges::copy_n(
                c2s.begin() + 32,
                32,
                keys[C2SRekey].begin());
            std::ranges::copy_n(
                s2c.begin(),
                32,
                keys[S2CTraffic].begin());
            std::ranges::copy_n(
                s2c.begin() + 32,
                32,
                keys[S2CRekey].begin());
        } catch (...) {
            SecureZeroMemory(
                keys.data(),
                sizeof(keys));
            static_cast<void>(
                VirtualUnlock(
                    keys.data(),
                    sizeof(keys)));
            keys_locked = false;
            throw;
        }
    }

    /// 析构函数清零所有 current/previous key 后解锁页面。
    ~Impl() {
        SecureZeroMemory(keys.data(), sizeof(keys));
        if (keys_locked) {
            static_cast<void>(
                VirtualUnlock(keys.data(), sizeof(keys)));
        }
    }

    /// crypto 是只提供 primitive 的 adapter。
    CryptoProvider crypto;
    /// identity 进入所有 packet AAD 与 rekey info。
    SecureIdentity identity;
    /// keys 是 locked current/previous direction key set。
    std::array<CryptoProvider::Key32, 5> keys{};
    /// current_replay 是 current S2C epoch window。
    ReplayWindow current_replay;
    /// previous_replay 只在 overlap 内存在。
    std::optional<ReplayWindow> previous_replay;
    /// key_epoch 是 current traffic epoch。
    std::uint32_t key_epoch;
    /// previous_epoch 为零表示无 overlap。
    std::uint32_t previous_epoch{};
    /// next_send_sequence 永不回退或 wrap。
    std::uint64_t next_send_sequence{1};
    /// sent_packets_current_epoch 驱动 packet rollover。
    std::uint64_t sent_packets_current_epoch{};
    /// epoch_started_unix_ms 驱动 time rollover。
    std::uint64_t epoch_started_unix_ms;
    /// previous_deadline_unix_ms 等于时销毁旧 receive key。
    std::uint64_t previous_deadline_unix_ms{};
    /// rollover_deadline_unix_ms 是 trigger 后的协商硬截止时间。
    std::uint64_t rollover_deadline_unix_ms{};
    /// send_exhausted 防止最大 sequence 使用后发生回绕。
    bool send_exhausted{false};
    /// closed 是不可逆终态。
    bool closed{false};
    /// keys_locked 保护对应 VirtualUnlock。
    bool keys_locked{false};
    /// mutex 串行化 nonce allocation、replay 与 transition。
    mutable std::mutex mutex;
};

ProtocolSecureChannel::ProtocolSecureChannel(
    const std::array<std::uint8_t, 32>& session_seed,
    SecureIdentity identity,
    const std::uint32_t key_epoch,
    const std::uint64_t epoch_started_unix_ms)
    : impl_(std::make_unique<Impl>(
          session_seed,
          std::move(identity),
          key_epoch,
          epoch_started_unix_ms)) {}

ProtocolSecureChannel::~ProtocolSecureChannel() = default;

std::optional<std::vector<std::uint8_t>>
ProtocolSecureChannel::Seal(
    const PacketKind kind,
    const std::span<const std::uint8_t> payload,
    const std::uint64_t now_unix_ms) {
    const auto wire_kind = static_cast<std::uint8_t>(kind);
    if (wire_kind < static_cast<std::uint8_t>(
                        PacketKind::Raw) ||
        wire_kind > static_cast<std::uint8_t>(
                        PacketKind::Control) ||
        payload.empty() ||
        payload.size() > MaximumProtectedPayloadBytes) {
        throw std::invalid_argument(
            "qualification secure payload is invalid");
    }
    std::scoped_lock lock(impl_->mutex);
    if (impl_->closed) {
        return std::nullopt;
    }
    const auto rollover_required =
        RolloverRequiredLocked(now_unix_ms);
    if (impl_->closed ||
        (rollover_required &&
         kind != PacketKind::Control)) {
        return std::nullopt;
    }
    if (impl_->send_exhausted ||
        impl_->next_send_sequence == 0) {
        CloseLocked();
        return std::nullopt;
    }
    const auto sequence = impl_->next_send_sequence;
    if (sequence ==
        std::numeric_limits<std::uint64_t>::max()) {
        impl_->send_exhausted = true;
    } else {
        ++impl_->next_send_sequence;
    }
    if (impl_->sent_packets_current_epoch !=
        std::numeric_limits<std::uint64_t>::max()) {
        ++impl_->sent_packets_current_epoch;
    }
    const auto header = EncodeHeader(
        kind,
        impl_->identity,
        impl_->key_epoch,
        sequence,
        payload.size());
    const auto encrypted =
        impl_->crypto.EncryptChaCha20Poly1305(
            impl_->keys[Impl::C2STraffic],
            PacketNonce(impl_->key_epoch, sequence),
            header,
            payload);
    std::vector<std::uint8_t> datagram(
        header.size() +
        encrypted.bytes.size() +
        encrypted.tag.size());
    std::ranges::copy(header, datagram.begin());
    std::ranges::copy(
        encrypted.bytes,
        datagram.begin() + header.size());
    std::ranges::copy(
        encrypted.tag,
        datagram.end() - encrypted.tag.size());
    return datagram;
}

std::vector<std::vector<std::uint8_t>>
ProtocolSecureChannel::SealSecurityNegative(
    const PacketKind kind,
    const std::span<const std::uint8_t> payload,
    const SecurityDatagramMutation mutation,
    const std::uint64_t now_unix_ms) {
    const auto wire_kind = static_cast<std::uint8_t>(kind);
    if (wire_kind < static_cast<std::uint8_t>(
                        PacketKind::Raw) ||
        wire_kind > static_cast<std::uint8_t>(
                        PacketKind::Control) ||
        payload.empty() ||
        payload.size() > MaximumProtectedPayloadBytes) {
        throw std::invalid_argument(
            "qualification security payload is invalid");
    }
    std::scoped_lock lock(impl_->mutex);
    if (impl_->closed ||
        RolloverRequiredLocked(now_unix_ms) ||
        impl_->closed ||
        impl_->send_exhausted ||
        impl_->next_send_sequence == 0) {
        return {};
    }
    const auto base = impl_->next_send_sequence;
    const auto window =
        static_cast<std::uint64_t>(ReplayWindowPackets);
    std::vector<std::uint64_t> sequences;
    const CryptoProvider::Key32* key =
        &impl_->keys[Impl::C2STraffic];
    switch (mutation) {
    case SecurityDatagramMutation::FutureSequence:
        if (base >
            std::numeric_limits<std::uint64_t>::max() -
                window) {
            CloseLocked();
            return {};
        }
        sequences.push_back(base + window);
        break;
    case SecurityDatagramMutation::TooOldSequence:
        if (base >
            std::numeric_limits<std::uint64_t>::max() -
                window) {
            CloseLocked();
            return {};
        }
        sequences = {base, base + window, base};
        break;
    case SecurityDatagramMutation::WrongDirection:
        sequences.push_back(base);
        key = &impl_->keys[Impl::S2CTraffic];
        break;
    default:
        throw std::invalid_argument(
            "qualification security mutation is invalid");
    }
    std::vector<std::vector<std::uint8_t>> output;
    output.reserve(sequences.size());
    for (const auto sequence : sequences) {
        const auto header = EncodeHeader(
            kind,
            impl_->identity,
            impl_->key_epoch,
            sequence,
            payload.size());
        const auto encrypted =
            impl_->crypto.EncryptChaCha20Poly1305(
                *key,
                PacketNonce(impl_->key_epoch, sequence),
                header,
                payload);
        std::vector<std::uint8_t> datagram(
            header.size() +
            encrypted.bytes.size() +
            encrypted.tag.size());
        std::ranges::copy(
            header,
            datagram.begin());
        std::ranges::copy(
            encrypted.bytes,
            datagram.begin() +
                header.size());
        std::ranges::copy(
            encrypted.tag,
            datagram.end() -
                encrypted.tag.size());
        output.push_back(std::move(datagram));
    }
    const auto highest =
        *std::ranges::max_element(sequences);
    if (highest ==
        std::numeric_limits<std::uint64_t>::max()) {
        impl_->send_exhausted = true;
    } else {
        impl_->next_send_sequence = highest + 1;
    }
    const auto unique_packets =
        mutation == SecurityDatagramMutation::TooOldSequence
            ? std::uint64_t{2}
            : std::uint64_t{1};
    if (impl_->sent_packets_current_epoch >
        std::numeric_limits<std::uint64_t>::max() -
            unique_packets) {
        impl_->sent_packets_current_epoch =
            std::numeric_limits<std::uint64_t>::max();
    } else {
        impl_->sent_packets_current_epoch +=
            unique_packets;
    }
    return output;
}

OpenPacket ProtocolSecureChannel::Open(
    const std::span<const std::uint8_t> datagram,
    const std::uint64_t now_unix_ms) {
    OpenPacket result{};
    if (datagram.size() <=
            SecureHeaderBytes + AeadTagBytes ||
        datagram.size() > MaximumDatagramBytes) {
        return result;
    }
    const auto header = datagram.first(SecureHeaderBytes);
    const auto wire_kind = header[5];
    const auto payload_bytes =
        ReadUint16BE(header.data() + 32);
    if (!std::ranges::equal(
            SecureMagic,
            header.first(SecureMagic.size())) ||
        header[4] != WireVersion ||
        wire_kind < static_cast<std::uint8_t>(
                        PacketKind::Raw) ||
        wire_kind > static_cast<std::uint8_t>(
                        PacketKind::Control) ||
        header[6] != 0 ||
        header[7] != 0 ||
        !std::ranges::equal(
            impl_->identity.session_id_digest,
            header.subspan(8, 8)) ||
        ReadUint32BE(header.data() + 16) !=
            impl_->identity.battle_session_generation ||
        ReadUint64BE(header.data() + 24) == 0 ||
        payload_bytes == 0 ||
        static_cast<std::size_t>(payload_bytes) +
                SecureHeaderBytes +
                AeadTagBytes !=
            datagram.size() ||
        ReadUint32BE(header.data() + 34) !=
            impl_->identity.endpoint_generation ||
        !std::ranges::equal(
            impl_->identity.binding_discriminator,
            header.subspan(38, 8)) ||
        header[46] != 0 ||
        header[47] != 0) {
        return result;
    }
    const auto epoch =
        ReadUint32BE(header.data() + 20);
    const auto sequence =
        ReadUint64BE(header.data() + 24);
    CryptoProvider::Tag16 tag{};
    std::ranges::copy(
        datagram.last(tag.size()),
        tag.begin());
    std::scoped_lock lock(impl_->mutex);
    if (impl_->closed ||
        now_unix_ms < impl_->epoch_started_unix_ms) {
        result.disposition = OpenDisposition::Closed;
        return result;
    }
    if (impl_->previous_epoch != 0 &&
        now_unix_ms >=
            impl_->previous_deadline_unix_ms) {
        impl_->crypto.SecureZero(
            impl_->keys[Impl::PreviousS2CTraffic]);
        impl_->previous_epoch = 0;
        impl_->previous_deadline_unix_ms = 0;
        impl_->previous_replay.reset();
    }
    const CryptoProvider::Key32* receive_key = nullptr;
    Impl::ReplayWindow* replay = nullptr;
    if (epoch == impl_->key_epoch) {
        receive_key = &impl_->keys[Impl::S2CTraffic];
        replay = &impl_->current_replay;
    } else if (
        epoch == impl_->previous_epoch &&
        impl_->previous_replay.has_value()) {
        receive_key =
            &impl_->keys[Impl::PreviousS2CTraffic];
        replay = &*impl_->previous_replay;
    } else {
        return result;
    }
    auto plaintext =
        impl_->crypto.DecryptChaCha20Poly1305(
            *receive_key,
            PacketNonce(epoch, sequence),
            header,
            datagram.subspan(
                SecureHeaderBytes,
                payload_bytes),
            tag);
    if (!plaintext.has_value()) {
        result.disposition =
            OpenDisposition::AuthenticationFailed;
        return result;
    }
    result.disposition = replay->Classify(sequence);
    if (result.disposition !=
        OpenDisposition::Accepted) {
        impl_->crypto.SecureZero(*plaintext);
        if (result.disposition ==
            OpenDisposition::FutureJump) {
            impl_->closed = true;
            SecureZeroMemory(
                impl_->keys.data(),
                sizeof(impl_->keys));
        }
        return result;
    }
    replay->Commit(sequence);
    result.kind = static_cast<PacketKind>(wire_kind);
    result.packet_sequence = sequence;
    result.plaintext = std::move(*plaintext);
    return result;
}

bool ProtocolSecureChannel::RolloverRequired(
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(impl_->mutex);
    return RolloverRequiredLocked(now_unix_ms);
}

bool ProtocolSecureChannel::CommitRollover(
    const std::array<std::uint8_t, 32>& rekey_nonce,
    const std::uint32_t next_epoch,
    const std::uint64_t now_unix_ms) {
    std::scoped_lock lock(impl_->mutex);
    if (impl_->closed ||
        IsAllZero(rekey_nonce) ||
        now_unix_ms < impl_->epoch_started_unix_ms ||
        (impl_->rollover_deadline_unix_ms != 0 &&
         now_unix_ms >=
             impl_->rollover_deadline_unix_ms) ||
        impl_->key_epoch ==
            std::numeric_limits<std::uint32_t>::max() ||
        next_epoch != impl_->key_epoch + 1U ||
        now_unix_ms >
            std::numeric_limits<std::uint64_t>::max() -
                PreviousEpochOverlapMilliseconds) {
        CloseLocked();
        return false;
    }
    auto next_c2s = impl_->crypto.HkdfSha256(
        impl_->keys[Impl::C2SRekey],
        rekey_nonce,
        DirectionInfo(
            ClientToServerDirection,
            next_epoch,
            impl_->identity),
        64);
    WipeVector next_c2s_wiper(
        impl_->crypto,
        next_c2s);
    auto next_s2c = impl_->crypto.HkdfSha256(
        impl_->keys[Impl::S2CRekey],
        rekey_nonce,
        DirectionInfo(
            ServerToClientDirection,
            next_epoch,
            impl_->identity),
        64);
    WipeVector next_s2c_wiper(
        impl_->crypto,
        next_s2c);

    impl_->keys[Impl::PreviousS2CTraffic] =
        impl_->keys[Impl::S2CTraffic];
    std::ranges::copy_n(
        next_c2s.begin(),
        32,
        impl_->keys[Impl::C2STraffic].begin());
    std::ranges::copy_n(
        next_c2s.begin() + 32,
        32,
        impl_->keys[Impl::C2SRekey].begin());
    std::ranges::copy_n(
        next_s2c.begin(),
        32,
        impl_->keys[Impl::S2CTraffic].begin());
    std::ranges::copy_n(
        next_s2c.begin() + 32,
        32,
        impl_->keys[Impl::S2CRekey].begin());
    impl_->previous_epoch = impl_->key_epoch;
    impl_->previous_deadline_unix_ms =
        now_unix_ms +
        PreviousEpochOverlapMilliseconds;
    impl_->previous_replay = impl_->current_replay;
    impl_->current_replay = {};
    impl_->key_epoch = next_epoch;
    impl_->next_send_sequence = 1;
    impl_->sent_packets_current_epoch = 0;
    impl_->epoch_started_unix_ms = now_unix_ms;
    impl_->rollover_deadline_unix_ms = 0;
    impl_->send_exhausted = false;
    return true;
}

bool ProtocolSecureChannel::CommitEndpointGeneration(
    const std::uint32_t expected_current,
    const std::uint32_t next_generation) {
    std::scoped_lock lock(impl_->mutex);
    if (impl_->closed ||
        expected_current == 0 ||
        impl_->identity.endpoint_generation !=
            expected_current ||
        expected_current ==
            std::numeric_limits<std::uint32_t>::max() ||
        next_generation != expected_current + 1U) {
        return false;
    }
    impl_->identity.endpoint_generation =
        next_generation;
    return true;
}

void ProtocolSecureChannel::Close() noexcept {
    std::scoped_lock lock(impl_->mutex);
    CloseLocked();
}

bool ProtocolSecureChannel::RolloverRequiredLocked(
    const std::uint64_t now_unix_ms) {
    if (impl_->closed) {
        return true;
    }
    if (now_unix_ms < impl_->epoch_started_unix_ms) {
        CloseLocked();
        return true;
    }
    if (impl_->previous_epoch != 0 &&
        now_unix_ms >=
            impl_->previous_deadline_unix_ms) {
        impl_->crypto.SecureZero(
            impl_->keys[Impl::PreviousS2CTraffic]);
        impl_->previous_epoch = 0;
        impl_->previous_deadline_unix_ms = 0;
        impl_->previous_replay.reset();
    }
    const auto required =
        now_unix_ms - impl_->epoch_started_unix_ms >=
            RekeyIntervalMilliseconds ||
        impl_->sent_packets_current_epoch >=
            RekeyPacketLimit;
    if (!required) {
        return false;
    }
    if (impl_->rollover_deadline_unix_ms == 0) {
        if (now_unix_ms >
            std::numeric_limits<std::uint64_t>::max() -
                RolloverDeadlineMilliseconds) {
            CloseLocked();
            return true;
        }
        impl_->rollover_deadline_unix_ms =
            now_unix_ms +
            RolloverDeadlineMilliseconds;
    }
    if (now_unix_ms >=
        impl_->rollover_deadline_unix_ms) {
        CloseLocked();
    }
    return true;
}

void ProtocolSecureChannel::CloseLocked() noexcept {
    impl_->closed = true;
    impl_->send_exhausted = true;
    SecureZeroMemory(
        impl_->keys.data(),
        sizeof(impl_->keys));
    impl_->current_replay = {};
    impl_->previous_replay.reset();
    impl_->previous_epoch = 0;
    impl_->previous_deadline_unix_ms = 0;
    impl_->rollover_deadline_unix_ms = 0;
}

}  // namespace ihomeland::qualification::battle
