#include "ihomeland/sim/transport/crypto_provider.hpp"

#include <sodium.h>

#include <limits>
#include <stdexcept>
#include <string>

namespace ihomeland::sim {
namespace {

/// DataOrNull 只在允许空输入的 provider 参数上返回 null。
[[nodiscard]] const unsigned char* DataOrNull(
    const std::span<const std::uint8_t> value) noexcept {
    return value.empty() ? nullptr : value.data();
}

}  // namespace

CryptoProvider::CryptoProvider() {
    if (sodium_init() < 0 ||
        crypto_scalarmult_curve25519_bytes() != Key32{}.size() ||
        crypto_aead_chacha20poly1305_ietf_keybytes() !=
            Key32{}.size() ||
        crypto_aead_chacha20poly1305_ietf_npubbytes() !=
            Nonce12{}.size() ||
        crypto_aead_chacha20poly1305_ietf_abytes() !=
            Tag16{}.size()) {
        throw std::runtime_error(
            "battle crypto provider initialization failed");
    }
}

void CryptoProvider::RandomFill(
    const std::span<std::uint8_t> output) const {
    if (output.empty()) {
        throw std::invalid_argument(
            "crypto random output is empty");
    }
    randombytes_buf(output.data(), output.size());
}

CryptoProvider::X25519KeyPair
CryptoProvider::GenerateX25519KeyPair() const {
    X25519KeyPair pair{};
    RandomFill(pair.secret_scalar);
    if (crypto_scalarmult_curve25519_base(
            pair.public_key.data(),
            pair.secret_scalar.data()) != 0) {
        SecureZero(pair.secret_scalar);
        throw std::runtime_error(
            "X25519 public key generation failed");
    }
    return pair;
}

CryptoProvider::Key32 CryptoProvider::X25519(
    const Key32& secret_scalar,
    const Key32& peer_public_key) const {
    Key32 shared{};
    if (crypto_scalarmult_curve25519(
            shared.data(),
            secret_scalar.data(),
            peer_public_key.data()) != 0) {
        SecureZero(shared);
        throw std::invalid_argument(
            "X25519 peer public key is invalid");
    }
    return shared;
}

CryptoProvider::Key32 CryptoProvider::HmacSha256(
    const std::span<const std::uint8_t> key,
    const std::span<const std::uint8_t> message) const {
    if (key.empty()) {
        throw std::invalid_argument("HMAC key is empty");
    }
    crypto_auth_hmacsha256_state state{};
    Key32 result{};
    if (crypto_auth_hmacsha256_init(
            &state,
            key.data(),
            key.size()) != 0 ||
        (!message.empty() &&
         crypto_auth_hmacsha256_update(
             &state,
             message.data(),
             static_cast<unsigned long long>(
                 message.size())) != 0) ||
        crypto_auth_hmacsha256_final(
            &state,
            result.data()) != 0) {
        sodium_memzero(&state, sizeof(state));
        SecureZero(result);
        throw std::runtime_error("HMAC-SHA-256 failed");
    }
    sodium_memzero(&state, sizeof(state));
    return result;
}

CryptoProvider::Key32 CryptoProvider::Sha256(
    const std::span<const std::uint8_t> message) const {
    if (message.empty()) {
        throw std::invalid_argument("SHA-256 message is empty");
    }
    Key32 result{};
    if (crypto_hash_sha256(
            result.data(),
            message.data(),
            static_cast<unsigned long long>(message.size())) != 0) {
        SecureZero(result);
        throw std::runtime_error("SHA-256 failed");
    }
    return result;
}

std::vector<std::uint8_t> CryptoProvider::HkdfSha256(
    const std::span<const std::uint8_t> input_key_material,
    const std::span<const std::uint8_t> salt,
    const std::span<const std::uint8_t> info,
    const std::size_t output_bytes) const {
    if (input_key_material.empty() || output_bytes == 0 ||
        output_bytes >
            crypto_kdf_hkdf_sha256_bytes_max()) {
        throw std::invalid_argument("HKDF input is invalid");
    }
    Key32 prk{};
    if (crypto_kdf_hkdf_sha256_extract(
            prk.data(),
            DataOrNull(salt),
            salt.size(),
            input_key_material.data(),
            input_key_material.size()) != 0) {
        SecureZero(prk);
        throw std::runtime_error("HKDF-SHA-256 extract failed");
    }
    std::vector<std::uint8_t> output(output_bytes);
    const auto* context = reinterpret_cast<const char*>(
        DataOrNull(info));
    const auto status = crypto_kdf_hkdf_sha256_expand(
        output.data(),
        output.size(),
        context,
        info.size(),
        prk.data());
    SecureZero(prk);
    if (status != 0) {
        SecureZero(output);
        throw std::runtime_error("HKDF-SHA-256 expand failed");
    }
    return output;
}

CryptoProvider::AeadCiphertext
CryptoProvider::EncryptChaCha20Poly1305(
    const Key32& key,
    const Nonce12& nonce,
    const std::span<const std::uint8_t> aad,
    const std::span<const std::uint8_t> plaintext) const {
    if (plaintext.empty() ||
        plaintext.size() >
            crypto_aead_chacha20poly1305_ietf_messagebytes_max()) {
        throw std::invalid_argument(
            "ChaCha20-Poly1305 plaintext size is invalid");
    }
    AeadCiphertext output{
        .bytes = std::vector<std::uint8_t>(plaintext.size()),
        .tag = {},
    };
    unsigned long long tag_bytes = 0;
    if (crypto_aead_chacha20poly1305_ietf_encrypt_detached(
            output.bytes.data(),
            output.tag.data(),
            &tag_bytes,
            plaintext.data(),
            static_cast<unsigned long long>(
                plaintext.size()),
            DataOrNull(aad),
            static_cast<unsigned long long>(aad.size()),
            nullptr,
            nonce.data(),
            key.data()) != 0 ||
        tag_bytes != output.tag.size()) {
        SecureZero(output.bytes);
        SecureZero(output.tag);
        throw std::runtime_error(
            "ChaCha20-Poly1305 encryption failed");
    }
    return output;
}

std::optional<std::vector<std::uint8_t>>
CryptoProvider::DecryptChaCha20Poly1305(
    const Key32& key,
    const Nonce12& nonce,
    const std::span<const std::uint8_t> aad,
    const std::span<const std::uint8_t> ciphertext,
    const Tag16& tag) const {
    if (ciphertext.empty() ||
        ciphertext.size() >
            crypto_aead_chacha20poly1305_ietf_messagebytes_max()) {
        return std::nullopt;
    }
    std::vector<std::uint8_t> plaintext(ciphertext.size());
    if (crypto_aead_chacha20poly1305_ietf_decrypt_detached(
            plaintext.data(),
            nullptr,
            ciphertext.data(),
            static_cast<unsigned long long>(
                ciphertext.size()),
            tag.data(),
            DataOrNull(aad),
            static_cast<unsigned long long>(aad.size()),
            nonce.data(),
            key.data()) != 0) {
        SecureZero(plaintext);
        return std::nullopt;
    }
    return plaintext;
}

bool CryptoProvider::ConstantTimeEqual(
    const std::span<const std::uint8_t> first,
    const std::span<const std::uint8_t> second) const noexcept {
    if (first.size() != second.size()) {
        return false;
    }
    if (first.empty()) {
        return true;
    }
    return sodium_memcmp(
               first.data(),
               second.data(),
               first.size()) == 0;
}

void CryptoProvider::SecureZero(
    const std::span<std::uint8_t> material) const noexcept {
    if (!material.empty()) {
        sodium_memzero(material.data(), material.size());
    }
}

}  // namespace ihomeland::sim
