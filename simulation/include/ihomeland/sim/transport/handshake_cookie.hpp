#pragma once

#include "ihomeland/sim/transport/crypto_provider.hpp"

#include <array>
#include <cstddef>
#include <cstdint>
#include <mutex>
#include <optional>
#include <span>

namespace ihomeland::sim {

/// BattleRemoteEndpoint 是完成 canonicalization 的 UDP source endpoint。
struct BattleRemoteEndpoint final {
    /// address 保存 IPv6 bytes；IPv4 必须使用 IPv4-mapped IPv6 形式。
    std::array<std::uint8_t, 16> address;
    /// port 是 network endpoint 的非零 UDP port。
    std::uint16_t port;
};

/// BattleClientHello 是 pre-auth datagram 中允许参与 cookie 的固定字段。
struct BattleClientHello final {
    /// ticket_id 是 HTTPS 下发的非秘密 opaque lookup identity。
    std::array<std::uint8_t, 16> ticket_id;
    /// client_nonce 是客户端为当前 transcript 生成的 256-bit nonce。
    std::array<std::uint8_t, 32> client_nonce;
    /// client_public_key 是当前 transcript 的 ephemeral X25519 public value。
    std::array<std::uint8_t, 32> client_public_key;
};

/// BattleRetry 是 stateless Retry 的固定宽度 wire projection。
struct BattleRetry final {
    /// cookie_epoch 标识签发 cookie 的短时间片。
    std::uint32_t cookie_epoch;
    /// cookie 是 HMAC-SHA-256 截断后的 128-bit address proof。
    std::array<std::uint8_t, 16> cookie;
};

/// BattleCookieKeyRing 持有仅存在于 process memory 的 current/previous cookie key。
class BattleCookieKeyRing final {
public:
    /// RotationMilliseconds 固定 cookie key 的轮换时间片。
    static constexpr std::uint64_t RotationMilliseconds = 30'000;

    /// 构造函数以 CSPRNG 初始化当前时间片的 key。
    BattleCookieKeyRing(
        CryptoProvider& crypto,
        std::uint64_t initial_unix_ms);

    /// 测试构造函数接受公开 fixture key，不得用于 production composition。
    BattleCookieKeyRing(
        CryptoProvider& crypto,
        std::uint32_t initial_epoch,
        const CryptoProvider::Key32& fixture_key);

    /// 析构函数清零 current/previous key。
    ~BattleCookieKeyRing();

    BattleCookieKeyRing(const BattleCookieKeyRing&) = delete;
    BattleCookieKeyRing& operator=(const BattleCookieKeyRing&) = delete;
    BattleCookieKeyRing(BattleCookieKeyRing&&) = delete;
    BattleCookieKeyRing& operator=(BattleCookieKeyRing&&) = delete;

    /// Issue 轮换到当前时间片并签发绑定完整 ClientHello 与 endpoint 的 cookie。
    [[nodiscard]] BattleRetry Issue(
        const BattleClientHello& hello,
        const BattleRemoteEndpoint& remote,
        const std::array<std::uint8_t, 16>& listener_identity,
        std::uint64_t now_unix_ms);

    /// Verify 只接受 current/previous epoch，并以 constant-time compare 校验 cookie。
    [[nodiscard]] bool Verify(
        const BattleClientHello& hello,
        const BattleRemoteEndpoint& remote,
        const std::array<std::uint8_t, 16>& listener_identity,
        const BattleRetry& retry,
        std::uint64_t now_unix_ms);

private:
    /// RotateLocked 在 mutex 内推进 key epoch；跨越多个时间片会丢弃 previous。
    void RotateLocked(std::uint32_t target_epoch);

    /// ComputeLocked 生成不含 secret 的 canonical cookie input。
    [[nodiscard]] std::array<std::uint8_t, 16> ComputeLocked(
        const CryptoProvider::Key32& key,
        std::uint32_t epoch,
        const BattleClientHello& hello,
        const BattleRemoteEndpoint& remote,
        const std::array<std::uint8_t, 16>& listener_identity) const;

    /// crypto_ 是项目唯一 primitive adapter。
    CryptoProvider& crypto_;
    /// mutex_ 串行化 listener worker 的轮换与签发。
    std::mutex mutex_;
    /// current_key_ 仅用于 current_epoch_。
    CryptoProvider::Key32 current_key_{};
    /// previous_key_ 最多保留紧邻 previous epoch。
    std::optional<CryptoProvider::Key32> previous_key_;
    /// current_epoch_ 是单调推进的 30 秒时间片。
    std::uint32_t current_epoch_{};
};

/// BattleHandshakeCookieGate 只执行 fixed-width ClientHello parse 与 stateless Retry。
///
/// 该类型没有 ticket registry、X25519、session、KCP 或 queue dependency，因此 cookie
/// 验证前无法触发资格查询、非对称计算或动态 session allocation。
class BattleHandshakeCookieGate final {
public:
    /// ClientHelloBytes 是无 padding ClientHello 的最小 wire 长度。
    static constexpr std::size_t ClientHelloBytes = 88;
    /// RetryBytes 是 stateless Retry 的固定 wire 长度。
    static constexpr std::size_t RetryBytes = 28;
    /// MaximumDatagramBytes 与 battle wire profile 的 MTU ceiling 一致。
    static constexpr std::size_t MaximumDatagramBytes = 1200;

    /// Decision 保存固定栈空间响应；response_bytes 为零表示静默丢弃。
    struct Decision final {
        /// response 保存完整或未使用的 Retry bytes。
        std::array<std::uint8_t, RetryBytes> response{};
        /// response_bytes 只能是零或 RetryBytes。
        std::size_t response_bytes{};
    };

    /// 构造函数绑定唯一 listener identity 与 process-local cookie key ring。
    BattleHandshakeCookieGate(
        BattleCookieKeyRing& keys,
        std::array<std::uint8_t, 16> listener_identity);

    /// HandleClientHello 对合法 hello 返回不放大的 Retry，否则静默丢弃。
    [[nodiscard]] Decision HandleClientHello(
        std::span<const std::uint8_t> request,
        const BattleRemoteEndpoint& remote,
        std::uint64_t now_unix_ms);

    /// ValidateCookie 在任何 ticket lookup 前验证 repeated hello 与 endpoint cookie。
    [[nodiscard]] bool ValidateCookie(
        std::span<const std::uint8_t> repeated_hello,
        const BattleRemoteEndpoint& remote,
        const BattleRetry& retry,
        std::uint64_t now_unix_ms);

    /// ValidateCookieFields 为 closed ClientAuth decoder 验证已解析的 repeated hello。
    [[nodiscard]] bool ValidateCookieFields(
        const BattleClientHello& repeated_hello,
        const BattleRemoteEndpoint& remote,
        const BattleRetry& retry,
        std::uint64_t now_unix_ms);

    /// ParseRetry 对客户端或 fixture 使用的固定 Retry wire 执行 closed decode。
    [[nodiscard]] static std::optional<BattleRetry> ParseRetry(
        std::span<const std::uint8_t> response);

private:
    /// ParseClientHello 拒绝未知 header、全零字段、非零 padding 与超 MTU datagram。
    [[nodiscard]] static std::optional<BattleClientHello> ParseClientHello(
        std::span<const std::uint8_t> request);

    /// keys_ 是 pre-auth 路径唯一可变状态。
    BattleCookieKeyRing& keys_;
    /// listener_identity_ 防止 cookie 在另一个 listener/node 上复用。
    std::array<std::uint8_t, 16> listener_identity_;
};

}  // namespace ihomeland::sim
