#include "ihomeland_client_battle_native.h"

#include <algorithm>
#include <array>
#include <cstdint>
#include <iostream>
#include <ranges>
#include <stdexcept>
#include <string_view>
#include <vector>

namespace {

/// Require 把 native ABI contract failure 转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// HexValue 解码单个 lowercase hex nibble。
[[nodiscard]] std::uint8_t HexValue(const char value) {
    if (value >= '0' && value <= '9') {
        return static_cast<std::uint8_t>(value - '0');
    }
    if (value >= 'a' && value <= 'f') {
        return static_cast<std::uint8_t>(value - 'a' + 10);
    }
    throw std::invalid_argument("native test vector is not lowercase hex");
}

/// DecodeHex 解码 canonical lowercase hex。
[[nodiscard]] std::vector<std::uint8_t> DecodeHex(
    const std::string_view encoded) {
    if (encoded.empty() || encoded.size() % 2 != 0) {
        throw std::invalid_argument("native test vector width is invalid");
    }
    std::vector<std::uint8_t> output(encoded.size() / 2);
    for (std::size_t index = 0; index < output.size(); ++index) {
        output[index] = static_cast<std::uint8_t>(
            (HexValue(encoded[index * 2]) << 4U) |
            HexValue(encoded[index * 2 + 1]));
    }
    return output;
}

/// DecodeFixed 解码固定宽度 crypto value。
template <std::size_t Size>
[[nodiscard]] std::array<std::uint8_t, Size> DecodeFixed(
    const std::string_view encoded) {
    const auto decoded = DecodeHex(encoded);
    if (decoded.size() != Size) {
        throw std::invalid_argument("native fixed vector width drifted");
    }
    std::array<std::uint8_t, Size> output{};
    std::copy(decoded.begin(), decoded.end(), output.begin());
    return output;
}

/// TestCryptoAbi 验证 RFC vector、tamper、CSPRNG、constant compare 与清零。
void TestCryptoAbi() {
    Require(ihbr_abi_version() == 1, "native ABI version drifted");
    Require(
        ihbr_initialize() == IHBR_STATUS_OK,
        "native crypto initialization failed");
    std::array<std::uint8_t, 32> invalid_output{};
    Require(
        ihbr_random_fill(nullptr, 32) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_random_fill(invalid_output.data(), 0) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_x25519_public(nullptr, invalid_output.data()) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_x25519_shared(
                invalid_output.data(),
                nullptr,
                invalid_output.data()) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_hmac_sha256(
                nullptr,
                32,
                nullptr,
                0,
                invalid_output.data()) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_hkdf_sha256(
                invalid_output.data(),
                32,
                nullptr,
                0,
                nullptr,
                0,
                invalid_output.data(),
                0) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_constant_time_equal(
                nullptr,
                invalid_output.data(),
                32,
                invalid_output.data()) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_secure_zero(nullptr, 32) ==
                IHBR_STATUS_INVALID_ARGUMENT,
        "native crypto ABI accepted invalid pointer or width");

    auto alice_private = DecodeFixed<32>(
        "77076d0a7318a57d3c16c17251b26645"
        "df4c2f87ebc0992ab177fba51db92c2a");
    const auto bob_public = DecodeFixed<32>(
        "de9edb7d7b7dc1b4d35b61c2ece43537"
        "3f8343c85b78674dadfc7e146f882b4f");
    const auto expected_alice_public = DecodeFixed<32>(
        "8520f0098930a754748b7ddcb43ef75a0"
        "dbf3a0d26381af4eba4a98eaa9b4e6a");
    const auto expected_shared = DecodeFixed<32>(
        "4a5d9d5ba4ce2de1728e3bf480350f25"
        "e07e21c947d19e3376f09b3c1e161742");
    std::array<std::uint8_t, 32> actual{};
    Require(
        ihbr_x25519_public(
            alice_private.data(),
            actual.data()) == IHBR_STATUS_OK &&
            actual == expected_alice_public,
        "RFC 7748 public key drifted");
    Require(
        ihbr_x25519_shared(
            alice_private.data(),
            bob_public.data(),
            actual.data()) == IHBR_STATUS_OK &&
            actual == expected_shared,
        "RFC 7748 shared secret drifted");
    std::array<std::uint8_t, 32> small_order{};
    Require(
        ihbr_x25519_shared(
            alice_private.data(),
            small_order.data(),
            actual.data()) == IHBR_STATUS_CRYPTO_FAILURE &&
            actual == small_order,
        "X25519 small-order input did not fail closed");

    const auto hmac_key = DecodeHex(
        "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b");
    const auto hmac_message = DecodeHex("4869205468657265");
    const auto expected_hmac = DecodeFixed<32>(
        "b0344c61d8db38535ca8afceaf0bf12b"
        "881dc200c9833da726e9376c2e32cff7");
    Require(
        ihbr_hmac_sha256(
            hmac_key.data(),
            static_cast<std::uint32_t>(hmac_key.size()),
            hmac_message.data(),
            static_cast<std::uint32_t>(hmac_message.size()),
            actual.data()) == IHBR_STATUS_OK &&
            actual == expected_hmac,
        "RFC 4231 HMAC drifted");

    const auto ikm = DecodeHex(
        "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b"
        "0b0b0b0b0b0b0b");
    const auto salt = DecodeHex("000102030405060708090a0b0c");
    const auto info = DecodeHex("f0f1f2f3f4f5f6f7f8f9");
    const auto expected_okm = DecodeHex(
        "3cb25f25faacd57a90434f64d0362f2a"
        "2d2d0a90cf1a5a4c5db02d56ecc4c5bf"
        "34007208d5b887185865");
    std::vector<std::uint8_t> okm(expected_okm.size());
    Require(
        ihbr_hkdf_sha256(
            ikm.data(),
            static_cast<std::uint32_t>(ikm.size()),
            salt.data(),
            static_cast<std::uint32_t>(salt.size()),
            info.data(),
            static_cast<std::uint32_t>(info.size()),
            okm.data(),
            static_cast<std::uint32_t>(okm.size())) ==
            IHBR_STATUS_OK &&
            okm == expected_okm,
        "RFC 5869 HKDF drifted");

    std::array<std::uint8_t, 32> random{};
    Require(
        ihbr_random_fill(
            random.data(),
            static_cast<std::uint32_t>(random.size())) ==
            IHBR_STATUS_OK &&
            random != small_order,
        "native CSPRNG returned zero");
    std::uint8_t equal = 0;
    Require(
        ihbr_constant_time_equal(
            random.data(),
            random.data(),
            static_cast<std::uint32_t>(random.size()),
            &equal) == IHBR_STATUS_OK &&
            equal == 1,
        "native constant compare rejected equal values");
    Require(
        ihbr_secure_zero(
            random.data(),
            static_cast<std::uint32_t>(random.size())) ==
            IHBR_STATUS_OK &&
            random == small_order,
        "native secure zero retained material");

    const auto key = DecodeFixed<32>(
        "808182838485868788898a8b8c8d8e8f"
        "909192939495969798999a9b9c9d9e9f");
    const auto nonce = DecodeFixed<12>("070000004041424344454647");
    const auto aad = DecodeHex("50515253c0c1c2c3c4c5c6c7");
    const auto plaintext = DecodeHex("010203040506070809");
    std::vector<std::uint8_t> ciphertext(plaintext.size());
    std::array<std::uint8_t, 16> tag{};
    std::vector<std::uint8_t> opened(plaintext.size());
    Require(
        ihbr_chacha20poly1305_seal(
            key.data(),
            nonce.data(),
            aad.data(),
            static_cast<std::uint32_t>(aad.size()),
            plaintext.data(),
            static_cast<std::uint32_t>(plaintext.size()),
            ciphertext.data(),
            tag.data()) == IHBR_STATUS_OK &&
        ihbr_chacha20poly1305_open(
            key.data(),
            nonce.data(),
            aad.data(),
            static_cast<std::uint32_t>(aad.size()),
            ciphertext.data(),
            static_cast<std::uint32_t>(ciphertext.size()),
            tag.data(),
            opened.data()) == IHBR_STATUS_OK &&
            opened == plaintext,
        "native AEAD round trip failed");
    tag.front() ^= 1U;
    Require(
        ihbr_chacha20poly1305_open(
            key.data(),
            nonce.data(),
            aad.data(),
            static_cast<std::uint32_t>(aad.size()),
            ciphertext.data(),
            static_cast<std::uint32_t>(ciphertext.size()),
            tag.data(),
            opened.data()) == IHBR_STATUS_AUTHENTICATION_FAILURE &&
            std::ranges::all_of(
                opened,
                [](const std::uint8_t value) {
                    return value == 0;
                }),
        "native AEAD tamper did not fail closed");
}

/// TestKcpAbi 验证 exact profile、malformed input、queue hard cap 与幂等释放。
void TestKcpAbi() {
    ihbr_kcp_context* invalid = nullptr;
    Require(
        ihbr_kcp_create(0, &invalid) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_kcp_create(7, nullptr) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_kcp_release(nullptr) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_kcp_send(nullptr, nullptr, 0) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_kcp_input(nullptr, nullptr, 0) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_kcp_update(nullptr, 0) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
            ihbr_kcp_waiting(nullptr, nullptr) ==
                IHBR_STATUS_INVALID_ARGUMENT,
        "native KCP ABI accepted invalid handle or argument");
    ihbr_kcp_context* first = nullptr;
    ihbr_kcp_context* second = nullptr;
    Require(
        ihbr_kcp_create(7, &first) == IHBR_STATUS_OK &&
        ihbr_kcp_create(7, &second) == IHBR_STATUS_OK,
        "native KCP creation failed");
    const std::array<std::uint8_t, 5> message{1, 2, 3, 4, 5};
    const auto send_status = ihbr_kcp_send(
        first,
        message.data(),
        static_cast<std::uint32_t>(message.size()));
    const auto update_status = ihbr_kcp_update(first, 10);
    if (send_status != IHBR_STATUS_OK ||
        update_status != IHBR_STATUS_OK) {
        std::cerr
            << "KCP statuses send="
            << static_cast<int>(send_status)
            << " update="
            << static_cast<int>(update_status)
            << '\n';
    }
    Require(
        send_status == IHBR_STATUS_OK &&
            update_status == IHBR_STATUS_OK,
        "native KCP send/update failed");
    std::uint32_t waiting = 0;
    Require(
        ihbr_kcp_waiting(first, &waiting) ==
                IHBR_STATUS_OK &&
            waiting == 1,
        "native KCP waiting count drifted");
    std::uint32_t segment_length = 0;
    Require(
        ihbr_kcp_next_output(
            first,
            nullptr,
            0,
            &segment_length) ==
                IHBR_STATUS_BUFFER_TOO_SMALL &&
            segment_length >= 24 &&
            segment_length <= 1024,
        "native KCP output did not preserve required length");
    std::array<std::uint8_t, 1024> segment{};
    Require(
        ihbr_kcp_next_output(
            first,
            segment.data(),
            static_cast<std::uint32_t>(segment.size()),
            &segment_length) ==
                IHBR_STATUS_OK &&
        ihbr_kcp_input(
            second,
            segment.data(),
            segment_length) ==
                IHBR_STATUS_OK,
        "native KCP exact output/input failed");
    std::array<std::uint8_t, 1000> received{};
    std::uint32_t received_length = 0;
    Require(
        ihbr_kcp_receive(
            second,
            nullptr,
            0,
            &received_length) ==
                IHBR_STATUS_BUFFER_TOO_SMALL &&
            received_length ==
                static_cast<std::uint32_t>(message.size()) &&
        ihbr_kcp_receive(
            second,
            received.data(),
            static_cast<std::uint32_t>(received.size()),
            &received_length) == IHBR_STATUS_OK &&
            received_length ==
                static_cast<std::uint32_t>(message.size()) &&
            std::equal(
                message.begin(),
                message.end(),
                received.begin()),
        "native KCP receive drifted");
    const std::array<std::uint8_t, 24> malformed{};
    Require(
        ihbr_kcp_input(
            second,
            malformed.data(),
            static_cast<std::uint32_t>(malformed.size())) ==
            IHBR_STATUS_KCP_FAILURE,
        "native KCP accepted malformed segment");
    const std::array<std::uint8_t, 23> short_segment{};
    const std::array<std::uint8_t, 1025> oversized_segment{};
    Require(
        ihbr_kcp_input(
            second,
            short_segment.data(),
            static_cast<std::uint32_t>(short_segment.size())) ==
                IHBR_STATUS_INVALID_ARGUMENT &&
        ihbr_kcp_input(
            second,
            oversized_segment.data(),
            static_cast<std::uint32_t>(oversized_segment.size())) ==
                IHBR_STATUS_INVALID_ARGUMENT,
        "native KCP segment ceiling drifted");
    Require(
        ihbr_kcp_release(&first) == IHBR_STATUS_OK &&
            first == nullptr &&
        ihbr_kcp_release(&first) == IHBR_STATUS_OK &&
        ihbr_kcp_release(&second) == IHBR_STATUS_OK &&
            second == nullptr,
        "native KCP release is not idempotent");

    ihbr_kcp_context* bounded = nullptr;
    Require(
        ihbr_kcp_create(8, &bounded) == IHBR_STATUS_OK,
        "bounded KCP creation failed");
    const std::array<std::uint8_t, 1001> oversized_message{};
    Require(
        ihbr_kcp_send(
            bounded,
            oversized_message.data(),
            static_cast<std::uint32_t>(oversized_message.size())) ==
            IHBR_STATUS_INVALID_ARGUMENT,
        "native KCP message ceiling drifted");
    for (std::uint32_t index = 0; index < 64; ++index) {
        Require(
            ihbr_kcp_send(
                bounded,
                message.data(),
                static_cast<std::uint32_t>(message.size())) ==
                IHBR_STATUS_OK,
            "native KCP queue rejected before hard cap");
    }
    Require(
        ihbr_kcp_waiting(bounded, &waiting) ==
                IHBR_STATUS_OK &&
            waiting == 64,
        "native KCP queue count drifted before hard cap");
    Require(
        ihbr_kcp_send(
            bounded,
            message.data(),
            static_cast<std::uint32_t>(message.size())) ==
            IHBR_STATUS_QUEUE_FULL,
        "native KCP queue exceeded hard cap");
    Require(
        ihbr_kcp_release(&bounded) == IHBR_STATUS_OK,
        "bounded KCP release failed");
}

}  // namespace

/// main 运行 client native ABI 的 crypto、KCP 与 malformed contract。
int main() {
    try {
        TestCryptoAbi();
        TestKcpAbi();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
