#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <vector>

namespace ihomeland::sim {

/// CryptoProvider 是 battle transport 唯一可见的标准密码 primitive adapter。
///
/// Public contract 不暴露 libsodium 类型；调用方仍负责 transcript、方向、nonce 与 key
/// lifecycle，provider 只执行固定算法和安全清零。
class CryptoProvider final {
public:
    /// Key32 是 X25519、HMAC、HKDF PRK 与 ChaCha20 key 的固定宽度值。
    using Key32 = std::array<std::uint8_t, 32>;
    /// Nonce12 是 IETF ChaCha20-Poly1305 的 96-bit nonce。
    using Nonce12 = std::array<std::uint8_t, 12>;
    /// Tag16 是 Poly1305 authentication tag。
    using Tag16 = std::array<std::uint8_t, 16>;

    /// X25519KeyPair 保存一次握手的 ephemeral private/public material。
    struct X25519KeyPair final {
        /// secret_scalar 是一次性 X25519 scalar，必须在握手完成或失败后清零。
        Key32 secret_scalar;
        /// public_key 可进入 ClientHello 或 ServerAccept transcript。
        Key32 public_key;
    };

    /// AeadCiphertext 保存 detached ciphertext 与 tag。
    struct AeadCiphertext final {
        /// bytes 与 plaintext 等长。
        std::vector<std::uint8_t> bytes;
        /// tag 必须在 dispatch 前通过认证。
        Tag16 tag;
    };

    /// 构造函数初始化并自检锁定的 provider。
    CryptoProvider();

    /// RandomFill 用系统 CSPRNG 填满非空输出。
    void RandomFill(std::span<std::uint8_t> output) const;

    /// GenerateX25519KeyPair 生成一次握手使用的 ephemeral key pair。
    [[nodiscard]] X25519KeyPair GenerateX25519KeyPair() const;

    /// X25519 计算 RFC 7748 shared secret，并拒绝 small-order/all-zero 输出。
    [[nodiscard]] Key32 X25519(
        const Key32& secret_scalar,
        const Key32& peer_public_key) const;

    /// HmacSha256 计算任意非空 key 的 RFC 2104 HMAC-SHA-256。
    [[nodiscard]] Key32 HmacSha256(
        std::span<const std::uint8_t> key,
        std::span<const std::uint8_t> message) const;

    /// Sha256 计算 transcript 与公开 binding material 的固定摘要。
    [[nodiscard]] Key32 Sha256(
        std::span<const std::uint8_t> message) const;

    /// HkdfSha256 执行 RFC 5869 extract+expand，输出不得超过 255*HashLen。
    [[nodiscard]] std::vector<std::uint8_t> HkdfSha256(
        std::span<const std::uint8_t> input_key_material,
        std::span<const std::uint8_t> salt,
        std::span<const std::uint8_t> info,
        std::size_t output_bytes) const;

    /// EncryptChaCha20Poly1305 以 nonce 与 AAD 生成 detached ciphertext/tag。
    [[nodiscard]] AeadCiphertext EncryptChaCha20Poly1305(
        const Key32& key,
        const Nonce12& nonce,
        std::span<const std::uint8_t> aad,
        std::span<const std::uint8_t> plaintext) const;

    /// DecryptChaCha20Poly1305 认证成功才返回 plaintext；失败会清零临时输出。
    [[nodiscard]] std::optional<std::vector<std::uint8_t>>
    DecryptChaCha20Poly1305(
        const Key32& key,
        const Nonce12& nonce,
        std::span<const std::uint8_t> aad,
        std::span<const std::uint8_t> ciphertext,
        const Tag16& tag) const;

    /// ConstantTimeEqual 对相同长度 byte strings 执行 provider constant-time compare。
    [[nodiscard]] bool ConstantTimeEqual(
        std::span<const std::uint8_t> first,
        std::span<const std::uint8_t> second) const noexcept;

    /// SecureZero 对 caller-owned mutable secret buffer 执行不可优化清零。
    void SecureZero(std::span<std::uint8_t> material) const noexcept;
};

}  // namespace ihomeland::sim
