#pragma once

#include "ihomeland/sim/transport/crypto_provider.hpp"

#include <array>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <mutex>
#include <span>
#include <vector>

namespace ihomeland::sim {

class BattleRuntimeMetrics;

/// BattleTransportRole 决定本端 send/receive 使用的独立方向 key。
enum class BattleTransportRole : std::uint8_t {
    /// Client 发送 C2S 并接收 S2C。
    Client = 1,
    /// Server 发送 S2C 并接收 C2S。
    Server = 2,
};

/// BattlePacketKind 是 48-byte secure header 登记的 protected lane。
enum class BattlePacketKind : std::uint8_t {
    /// Raw 承载有界、可替换或输入类 datagram。
    Raw = 1,
    /// Kcp 承载一个 KCP segment。
    Kcp = 2,
    /// Control 承载 authenticated transport control。
    Control = 3,
};

/// BattleSecureIdentity 是每个 packet AAD 必须精确匹配的 session routing binding。
struct BattleSecureIdentity final {
    /// session_id_digest 是非零 64-bit 低敏 routing handle。
    std::array<std::uint8_t, 8> session_id_digest;
    /// battle_session_generation 防止旧 session incarnation 重放。
    std::uint32_t battle_session_generation;
    /// endpoint_generation 防止旧 endpoint generation 重放。
    std::uint32_t endpoint_generation;
    /// binding_discriminator 是非零 ticket-derived binding handle。
    std::array<std::uint8_t, 8> binding_discriminator;
};

/// BattleSealDisposition 区分成功保护与必须关闭的 nonce exhaustion。
enum class BattleSealDisposition : std::uint8_t {
    /// Sealed 表示 datagram 已使用唯一 nonce 认证加密。
    Sealed = 1,
    /// RolloverRequired 表示只允许发送 authenticated rollover control。
    RolloverRequired = 2,
    /// SequenceExhausted 表示旧 key 不得再发送 packet。
    SequenceExhausted = 3,
    /// Closed 表示 rollover deadline、epoch或既有终态禁止继续发送。
    Closed = 4,
};

/// BattleOpenDisposition 是 application dispatch 前的 closed receive decision。
enum class BattleOpenDisposition : std::uint8_t {
    /// Accepted 表示 AEAD 与 replay window 均已提交。
    Accepted = 1,
    /// InvalidHeader 表示 fixed header、identity、epoch或长度不匹配。
    InvalidHeader = 2,
    /// AuthenticationFailed 表示 ciphertext、AAD、tag或方向 key 不匹配。
    AuthenticationFailed = 3,
    /// Duplicate 表示 sequence 已在当前 epoch 接受。
    Duplicate = 4,
    /// TooOld 表示 sequence 已落在 256-packet window 之外。
    TooOld = 5,
    /// FutureJump 表示 authenticated sequence 向前跳过超过一个完整 window，调用方必须关闭 session。
    FutureJump = 6,
};

/// BattlePacketNonce 按 network order 组合 32-bit epoch 与 64-bit sequence。
///
/// sequence 必须为正；零值会抛出 std::invalid_argument。
[[nodiscard]] CryptoProvider::Nonce12 BattlePacketNonce(
    std::uint32_t key_epoch,
    std::uint64_t packet_sequence);

/// BattleSecureChannel 为一个 session owner 管理双向独立 key、发送 sequence 与接收 replay window。
///
/// 调用方必须为同一 session 保持唯一实例；rebind 不得重建该对象，rollover 只能通过后续
/// authenticated epoch transition 更新。公开方法由内部 mutex 串行化。
class BattleSecureChannel final {
public:
    /// SecureHeaderBytes 是 canonical AAD 长度。
    static constexpr std::size_t SecureHeaderBytes = 48;
    /// AeadTagBytes 是 RFC 8439 tag 长度。
    static constexpr std::size_t AeadTagBytes = 16;
    /// MaximumDatagramBytes 禁止 IP fragmentation 依赖。
    static constexpr std::size_t MaximumDatagramBytes = 1200;
    /// ReplayWindowPackets 固定 receive bitmap 与最大安全 forward jump。
    static constexpr std::size_t ReplayWindowPackets = 256;
    /// RekeyIntervalMilliseconds 固定 current epoch 最长发送时间。
    static constexpr std::uint64_t RekeyIntervalMilliseconds = 600'000;
    /// RekeyPacketLimit 固定 current epoch rollover packet trigger。
    static constexpr std::uint64_t RekeyPacketLimit = 1'048'576;
    /// PreviousEpochOverlapMilliseconds 固定 old receive key overlap。
    static constexpr std::uint64_t PreviousEpochOverlapMilliseconds = 3'000;
    /// RolloverDeadlineMilliseconds 限制 trigger 后的 authenticated negotiation。
    static constexpr std::uint64_t RolloverDeadlineMilliseconds = 3'000;

    /// SealResult 保存成功 datagram 或 sequence exhaustion 关闭决策。
    struct SealResult final {
        /// disposition 指示是否允许发送 datagram。
        BattleSealDisposition disposition{
            BattleSealDisposition::SequenceExhausted};
        /// packet_sequence 是本次消耗的 sequence，失败时为零。
        std::uint64_t packet_sequence{};
        /// datagram 是 header、ciphertext、tag 的完整 UDP payload。
        std::vector<std::uint8_t> datagram;
    };

    /// OpenResult 保存已认证 payload 与低敏 secure header projection。
    struct OpenResult final {
        /// disposition 只有 Accepted 才允许 application dispatch。
        BattleOpenDisposition disposition{
            BattleOpenDisposition::InvalidHeader};
        /// packet_kind 只在 Accepted 时有效。
        BattlePacketKind packet_kind{BattlePacketKind::Raw};
        /// packet_sequence 只在 Accepted 时有效。
        std::uint64_t packet_sequence{};
        /// plaintext 只在 Accepted 时持有 decrypted protected payload。
        std::vector<std::uint8_t> plaintext;
    };

    /// 构造函数从 transcript-specific session seed 派生 C2S/S2C 独立 key。
    ///
    /// next_send_sequence 通常为 1；非 1 只允许同一 key epoch 的唯一 owner 连续接管，
    /// 不得复制或回退。
    BattleSecureChannel(
        CryptoProvider& crypto,
        const CryptoProvider::Key32& session_seed,
        BattleTransportRole local_role,
        BattleSecureIdentity identity,
        std::uint32_t key_epoch,
        std::uint64_t next_send_sequence,
        std::uint64_t epoch_started_unix_ms,
        std::uint64_t sent_packets_current_epoch,
        BattleRuntimeMetrics* runtime_metrics = nullptr);

    /// 析构函数清零并解锁 directional traffic/rekey secrets。
    ~BattleSecureChannel();

    BattleSecureChannel(const BattleSecureChannel&) = delete;
    BattleSecureChannel& operator=(const BattleSecureChannel&) = delete;

    /// Seal 分配严格递增 sequence，并以完整 48-byte header 作为 AAD。
    ///
    /// payload 必须非空且完整 datagram 不超过 1200 bytes；trusted caller 的 kind/size
    /// 错误会抛出 std::invalid_argument。
    [[nodiscard]] SealResult Seal(
        BattlePacketKind packet_kind,
        std::span<const std::uint8_t> payload,
        std::uint64_t now_unix_ms);

    /// Open 先验证 closed header和AEAD，再原子提交256-packet replay window。
    [[nodiscard]] OpenResult Open(
        std::span<const std::uint8_t> datagram,
        std::uint64_t now_unix_ms);

    /// RolloverRequired 在 10 分钟或 2^20 packets 到达时启动有界 deadline。
    [[nodiscard]] bool RolloverRequired(
        std::uint64_t now_unix_ms);

    /// CommitAuthenticatedRollover 在已由 current Control packet 认证的协商后切换 epoch。
    ///
    /// rekey_nonce 必须来自 authenticated rollover proposal/confirm；成功后 send sequence
    /// 从 1 开始，previous receive key/window 仅保留 3 秒。失败会关闭 channel。
    [[nodiscard]] bool CommitAuthenticatedRollover(
        const CryptoProvider::Key32& rekey_nonce,
        std::uint64_t now_unix_ms);

    /// CommitEndpointGeneration 原子推进AAD endpoint generation且保持key/sequence/replay。
    ///
    /// 仅允许authenticated rebind owner从exact current推进到current+1。
    [[nodiscard]] bool CommitEndpointGeneration(
        std::uint32_t expected_current_generation,
        std::uint32_t next_generation);

private:
    struct SecretMaterial;
    struct ReplayWindow;

    /// RolloverRequiredLocked 启动deadline并在超时/clock回退时关闭。
    [[nodiscard]] bool RolloverRequiredLocked(
        std::uint64_t now_unix_ms);
    /// CloseLocked 清零 key、window 并提交不可逆终态。
    void CloseLocked() noexcept;

    /// crypto_ 是唯一 primitive 与 secure-zero adapter。
    CryptoProvider& crypto_;
    /// local_role_ 冻结本端 send/receive 方向。
    BattleTransportRole local_role_;
    /// identity_ 是每个 packet 必须匹配的 immutable AAD binding。
    BattleSecureIdentity identity_;
    /// key_epoch_ 同时进入 key schedule、header 与 nonce prefix。
    std::uint32_t key_epoch_;
    /// next_send_sequence_ 永不回退；max 被使用后由 send_exhausted_ 终结。
    std::uint64_t next_send_sequence_;
    /// epoch_started_unix_ms_ 是 current key 的绝对启用时刻。
    std::uint64_t epoch_started_unix_ms_;
    /// sent_packets_current_epoch_ 统计成功或烧掉 nonce 的发送尝试。
    std::uint64_t sent_packets_current_epoch_;
    /// runtime_metrics_ 可选借用 node 生命周期内的低敏累计 owner。
    BattleRuntimeMetrics* runtime_metrics_;
    /// rollover_deadline_unix_ms_ 为零表示尚未触发 rollover。
    std::uint64_t rollover_deadline_unix_ms_{};
    /// previous_epoch_ 为零表示没有 overlap receive key。
    std::uint32_t previous_epoch_{};
    /// previous_deadline_unix_ms_ 等于时即销毁 previous key/window。
    std::uint64_t previous_deadline_unix_ms_{};
    /// send_exhausted_ 防止 uint64 wrap 后复用旧 key。
    bool send_exhausted_{false};
    /// closed_ 是 rollover/sequence/epoch terminal 状态。
    bool closed_{false};
    /// secrets_ 在 VirtualLock memory 中保存方向隔离 key 与 rekey root。
    std::unique_ptr<SecretMaterial> secrets_;
    /// replay_ 只在 AEAD 成功后提交 current epoch sequence。
    std::unique_ptr<ReplayWindow> replay_;
    /// previous_replay_ 保留 old epoch 已提交 bitmap，不因 rollover 重置。
    std::unique_ptr<ReplayWindow> previous_replay_;
    /// mutex_ 串行化 nonce allocation 与 replay commit。
    std::mutex mutex_;
};

}  // namespace ihomeland::sim
