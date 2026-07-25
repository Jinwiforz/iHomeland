#include "ihomeland/sim/transport/crypto_provider.hpp"

#include <nlohmann/json.hpp>

#include <algorithm>
#include <array>
#include <cstdint>
#include <fstream>
#include <iterator>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

using Json = nlohmann::json;
using ihomeland::sim::CryptoProvider;

/// Require 把 crypto parity 失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// RequireFailure 要求 malformed/small-order 输入 fail closed。
template <typename Action>
void RequireFailure(Action action, const char* message) {
    try {
        action();
    } catch (const std::exception&) {
        return;
    }
    throw std::runtime_error(message);
}

/// HexValue 解码单个 lowercase hex nibble。
[[nodiscard]] std::uint8_t HexValue(const char value) {
    if (value >= '0' && value <= '9') {
        return static_cast<std::uint8_t>(value - '0');
    }
    if (value >= 'a' && value <= 'f') {
        return static_cast<std::uint8_t>(value - 'a' + 10);
    }
    throw std::invalid_argument("crypto vector is not lowercase hex");
}

/// DecodeHex 解码任意偶数长度 canonical hex。
[[nodiscard]] std::vector<std::uint8_t> DecodeHex(
    const std::string& encoded) {
    if (encoded.empty() || encoded.size() % 2 != 0) {
        throw std::invalid_argument("crypto vector hex size is invalid");
    }
    std::vector<std::uint8_t> result(encoded.size() / 2);
    for (std::size_t index = 0; index < result.size(); ++index) {
        result[index] = static_cast<std::uint8_t>(
            (HexValue(encoded[index * 2]) << 4U) |
            HexValue(encoded[index * 2 + 1]));
    }
    return result;
}

/// DecodeFixed 解码固定宽度 crypto value。
template <std::size_t Size>
[[nodiscard]] std::array<std::uint8_t, Size> DecodeFixed(
    const std::string& encoded) {
    const auto decoded = DecodeHex(encoded);
    if (decoded.size() != Size) {
        throw std::invalid_argument(
            "crypto vector fixed width drifted");
    }
    std::array<std::uint8_t, Size> result{};
    std::copy(decoded.begin(), decoded.end(), result.begin());
    return result;
}

/// LoadVectors 读取只读 RFC/cross-language corpus。
[[nodiscard]] Json LoadVectors() {
    std::ifstream input(
        IHOMELAND_BATTLE_CRYPTO_VECTORS,
        std::ios::binary);
    if (!input) {
        throw std::runtime_error(
            "crypto vector corpus is missing");
    }
    return Json::parse(
        std::istreambuf_iterator<char>(input),
        std::istreambuf_iterator<char>(),
        nullptr,
        true,
        true);
}

/// TestRFCVectors 验证 provider 与 RFC 7748/5869/8439 完全一致。
void TestRFCVectors(
    CryptoProvider& provider,
    const Json& vectors) {
    const auto& x25519 = vectors.at("x25519");
    const auto alice_private =
        DecodeFixed<32>(
            x25519.at("alice_private_hex")
                .get<std::string>());
    const auto alice_public =
        DecodeFixed<32>(
            x25519.at("alice_public_hex")
                .get<std::string>());
    const auto bob_private =
        DecodeFixed<32>(
            x25519.at("bob_private_hex")
                .get<std::string>());
    const auto bob_public =
        DecodeFixed<32>(
            x25519.at("bob_public_hex")
                .get<std::string>());
    const auto expected_shared =
        DecodeFixed<32>(
            x25519.at("shared_secret_hex")
                .get<std::string>());
    CryptoProvider::Key32 basepoint{};
    basepoint.front() = 9;
    Require(
        provider.ConstantTimeEqual(
            provider.X25519(alice_private, basepoint),
            alice_public),
        "RFC 7748 Alice public key drifted");
    Require(
        provider.ConstantTimeEqual(
            provider.X25519(bob_private, basepoint),
            bob_public),
        "RFC 7748 Bob public key drifted");
    const auto alice_shared =
        provider.X25519(alice_private, bob_public);
    const auto bob_shared =
        provider.X25519(bob_private, alice_public);
    Require(
        provider.ConstantTimeEqual(
            alice_shared,
            expected_shared) &&
            provider.ConstantTimeEqual(
                alice_shared,
                bob_shared),
        "RFC 7748 shared secret drifted");
    CryptoProvider::Key32 small_order{};
    RequireFailure(
        [&] {
            static_cast<void>(
                provider.X25519(
                    alice_private,
                    small_order));
        },
        "X25519 small-order input was accepted");

    const auto& hkdf = vectors.at("hkdf_sha256");
    const auto ikm =
        DecodeHex(hkdf.at("ikm_hex").get<std::string>());
    const auto salt =
        DecodeHex(hkdf.at("salt_hex").get<std::string>());
    const auto info =
        DecodeHex(hkdf.at("info_hex").get<std::string>());
    const auto expected_prk =
        DecodeFixed<32>(
            hkdf.at("prk_hex").get<std::string>());
    const auto expected_okm =
        DecodeHex(hkdf.at("okm_hex").get<std::string>());
    Require(
        provider.ConstantTimeEqual(
            provider.HmacSha256(salt, ikm),
            expected_prk),
        "RFC 5869 extract PRK drifted");
    const auto okm = provider.HkdfSha256(
        ikm,
        salt,
        info,
        hkdf.at("output_bytes").get<std::size_t>());
    Require(
        provider.ConstantTimeEqual(okm, expected_okm),
        "RFC 5869 OKM drifted");

    const auto& hmac = vectors.at("hmac_sha256");
    Require(
        provider.ConstantTimeEqual(
            provider.HmacSha256(
                DecodeHex(
                    hmac.at("key_hex")
                        .get<std::string>()),
                DecodeHex(
                    hmac.at("data_hex")
                        .get<std::string>())),
            DecodeFixed<32>(
                hmac.at("tag_hex")
                    .get<std::string>())),
        "RFC 4231 HMAC-SHA-256 drifted");

    const auto& aead =
        vectors.at("chacha20_poly1305");
    const auto key =
        DecodeFixed<32>(
            aead.at("key_hex").get<std::string>());
    const auto nonce =
        DecodeFixed<12>(
            aead.at("nonce_hex").get<std::string>());
    const auto aad =
        DecodeHex(aead.at("aad_hex").get<std::string>());
    const auto plaintext =
        DecodeHex(
            aead.at("plaintext_hex").get<std::string>());
    const auto expected_ciphertext =
        DecodeHex(
            aead.at("ciphertext_hex").get<std::string>());
    const auto expected_tag =
        DecodeFixed<16>(
            aead.at("tag_hex").get<std::string>());
    const auto encrypted =
        provider.EncryptChaCha20Poly1305(
            key,
            nonce,
            aad,
            plaintext);
    Require(
        provider.ConstantTimeEqual(
            encrypted.bytes,
            expected_ciphertext) &&
            provider.ConstantTimeEqual(
                encrypted.tag,
                expected_tag),
        "RFC 8439 AEAD output drifted");
    const auto decrypted =
        provider.DecryptChaCha20Poly1305(
            key,
            nonce,
            aad,
            encrypted.bytes,
            encrypted.tag);
    Require(
        decrypted.has_value() &&
            provider.ConstantTimeEqual(
                *decrypted,
                plaintext),
        "RFC 8439 AEAD decrypt drifted");
    auto changed_tag = encrypted.tag;
    changed_tag.front() ^= 1U;
    Require(
        !provider.DecryptChaCha20Poly1305(
             key,
             nonce,
             aad,
             encrypted.bytes,
             changed_tag)
             .has_value(),
        "tampered AEAD tag was accepted");
    auto changed_aad = aad;
    changed_aad.front() ^= 1U;
    Require(
        !provider.DecryptChaCha20Poly1305(
             key,
             nonce,
             changed_aad,
             encrypted.bytes,
             encrypted.tag)
             .has_value(),
        "tampered AEAD AAD was accepted");
}

/// TestRuntimeContracts 验证 CSPRNG、constant compare 与安全清零契约。
void TestRuntimeContracts(CryptoProvider& provider) {
    CryptoProvider::Key32 first{};
    CryptoProvider::Key32 second{};
    provider.RandomFill(first);
    provider.RandomFill(second);
    const CryptoProvider::Key32 zero{};
    Require(
        !provider.ConstantTimeEqual(first, zero) &&
            !provider.ConstantTimeEqual(first, second),
        "CSPRNG returned zero or repeated material");
    const auto pair = provider.GenerateX25519KeyPair();
    Require(
        !provider.ConstantTimeEqual(pair.secret_scalar, zero) &&
            !provider.ConstantTimeEqual(pair.public_key, zero),
        "ephemeral X25519 key pair is empty");
    auto copy = first;
    Require(
        provider.ConstantTimeEqual(first, copy),
        "constant-time compare rejected equal material");
    copy.back() ^= 1U;
    Require(
        !provider.ConstantTimeEqual(first, copy),
        "constant-time compare accepted drifted material");
    provider.SecureZero(first);
    Require(
        provider.ConstantTimeEqual(first, zero),
        "secure zero did not clear caller buffer");
}

}  // namespace

/// main 运行锁定 provider 的 RFC 与 runtime contract parity。
int main() {
    try {
        CryptoProvider provider;
        const auto vectors = LoadVectors();
        TestRFCVectors(provider, vectors);
        TestRuntimeContracts(provider);
        return 0;
    } catch (const std::exception&) {
        return 1;
    }
}
