#include "ihomeland_client_battle_native.h"

#include <ikcp.h>
#include <sodium.h>

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <deque>
#include <limits>
#include <mutex>
#include <new>
#include <vector>

namespace {

constexpr std::uint32_t AbiVersion = 1;
constexpr std::size_t KeyBytes = 32;
constexpr std::size_t NonceBytes = 12;
constexpr std::size_t TagBytes = 16;
constexpr std::size_t KcpHeaderBytes = 24;
constexpr std::size_t KcpMessageBytes = 1000;
constexpr std::size_t KcpSegmentBytes =
    KcpHeaderBytes + KcpMessageBytes;
constexpr std::size_t KcpOutputQueueItems = 64;
constexpr std::uint32_t KcpWindowSegments = 64;
constexpr std::uint32_t KcpUpdateMilliseconds = 10;
constexpr std::uint32_t KcpFastResend = 2;
constexpr std::uint32_t KcpMinimumRtoMilliseconds = 30;
constexpr std::uint32_t KcpMaximumRtoMilliseconds = 200;
constexpr std::uint32_t KcpDeadLinkRetransmits = 10;

/// DataOrNull 只为允许空 message/AAD/salt/info 的 libsodium 参数返回 null。
[[nodiscard]] const unsigned char* DataOrNull(
    const std::uint8_t* data,
    const std::uint32_t length) noexcept {
    return length == 0 ? nullptr : data;
}

/// ValidOptionalBuffer 要求非零长度必须提供 pointer。
[[nodiscard]] bool ValidOptionalBuffer(
    const void* data,
    const std::uint32_t length) noexcept {
    return length == 0 || data != nullptr;
}

/// CopyFront 保留不足 buffer 时的 required length 且不弹出队首。
[[nodiscard]] ihbr_status CopyFront(
    std::deque<std::vector<std::uint8_t>>& queue,
    std::uint8_t* output,
    const std::uint32_t output_capacity,
    std::uint32_t* output_length) {
    if (output_length == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    *output_length = 0;
    if (queue.empty()) {
        return IHBR_STATUS_OK;
    }
    const auto& value = queue.front();
    if (value.size() >
        static_cast<std::size_t>(
            std::numeric_limits<std::uint32_t>::max())) {
        return IHBR_STATUS_KCP_FAILURE;
    }
    *output_length = static_cast<std::uint32_t>(value.size());
    if (output == nullptr || output_capacity < *output_length) {
        return IHBR_STATUS_BUFFER_TOO_SMALL;
    }
    std::copy(value.begin(), value.end(), output);
    queue.pop_front();
    return IHBR_STATUS_OK;
}

}  // namespace

/// ihbr_kcp_context 只拥有 KCP handle 与 managed pull 模式的有界输出。
struct ihbr_kcp_context final {
    /// mutex 串行化第三方 handle 与 output queue。
    std::mutex mutex;
    /// handle 是 exact KCP 2.1.1 context。
    ikcpcb* handle{};
    /// outputs 暂存尚未由 managed secure lane 取出的 segment。
    std::deque<std::vector<std::uint8_t>> outputs;
    /// closed 表示 callback、input 或 dead-link 已进入终态。
    bool closed{};
};

namespace {

/// KcpOutput 只复制固定 MTU 内的 bytes，不拥有 socket 或 session。
int KcpOutput(
    const char* buffer,
    const int length,
    ikcpcb*,
    void* user) noexcept {
    auto* context = static_cast<ihbr_kcp_context*>(user);
    if (context == nullptr || buffer == nullptr ||
        length <= 0 ||
        static_cast<std::size_t>(length) > KcpSegmentBytes ||
        context->outputs.size() >= KcpOutputQueueItems) {
        if (context != nullptr) {
            context->closed = true;
        }
        return -1;
    }
    try {
        const auto* first =
            reinterpret_cast<const std::uint8_t*>(buffer);
        context->outputs.emplace_back(first, first + length);
        return 0;
    } catch (...) {
        context->closed = true;
        return -1;
    }
}

/// ClampKcpRto 把第三方 adaptive RTO 保持在 frozen profile 范围。
void ClampKcpRto(ihbr_kcp_context& context) noexcept {
    context.handle->rx_minrto =
        static_cast<IINT32>(KcpMinimumRtoMilliseconds);
    context.handle->rx_rto = std::clamp(
        context.handle->rx_rto,
        static_cast<IINT32>(KcpMinimumRtoMilliseconds),
        static_cast<IINT32>(KcpMaximumRtoMilliseconds));
    auto* cursor = context.handle->snd_buf.next;
    while (cursor != &context.handle->snd_buf) {
        auto* segment = iqueue_entry(cursor, IKCPSEG, node);
        segment->rto = std::clamp(
            segment->rto,
            static_cast<IUINT32>(KcpMinimumRtoMilliseconds),
            static_cast<IUINT32>(KcpMaximumRtoMilliseconds));
        cursor = cursor->next;
    }
}

}  // namespace

std::uint32_t IHBR_CALL ihbr_abi_version(void) {
    return AbiVersion;
}

ihbr_status IHBR_CALL ihbr_initialize(void) {
    if (sodium_init() < 0 ||
        crypto_scalarmult_curve25519_bytes() != KeyBytes ||
        crypto_aead_chacha20poly1305_ietf_keybytes() != KeyBytes ||
        crypto_aead_chacha20poly1305_ietf_npubbytes() != NonceBytes ||
        crypto_aead_chacha20poly1305_ietf_abytes() != TagBytes) {
        return IHBR_STATUS_CRYPTO_FAILURE;
    }
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_random_fill(
    std::uint8_t* output,
    const std::uint32_t output_length) {
    if (output == nullptr || output_length == 0) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    randombytes_buf(output, output_length);
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_x25519_public(
    const std::uint8_t* secret_scalar,
    std::uint8_t* public_key) {
    if (secret_scalar == nullptr || public_key == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    if (crypto_scalarmult_curve25519_base(
            public_key,
            secret_scalar) != 0) {
        sodium_memzero(public_key, KeyBytes);
        return IHBR_STATUS_CRYPTO_FAILURE;
    }
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_x25519_shared(
    const std::uint8_t* secret_scalar,
    const std::uint8_t* peer_public_key,
    std::uint8_t* shared_secret) {
    if (secret_scalar == nullptr ||
        peer_public_key == nullptr ||
        shared_secret == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    if (crypto_scalarmult_curve25519(
            shared_secret,
            secret_scalar,
            peer_public_key) != 0) {
        sodium_memzero(shared_secret, KeyBytes);
        return IHBR_STATUS_CRYPTO_FAILURE;
    }
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_hmac_sha256(
    const std::uint8_t* key,
    const std::uint32_t key_length,
    const std::uint8_t* message,
    const std::uint32_t message_length,
    std::uint8_t* tag) {
    if (key == nullptr || key_length == 0 ||
        !ValidOptionalBuffer(message, message_length) ||
        tag == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    crypto_auth_hmacsha256_state state{};
    if (crypto_auth_hmacsha256_init(
            &state,
            key,
            key_length) != 0 ||
        (message_length != 0 &&
         crypto_auth_hmacsha256_update(
             &state,
             message,
             message_length) != 0) ||
        crypto_auth_hmacsha256_final(&state, tag) != 0) {
        sodium_memzero(&state, sizeof(state));
        sodium_memzero(tag, KeyBytes);
        return IHBR_STATUS_CRYPTO_FAILURE;
    }
    sodium_memzero(&state, sizeof(state));
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_hkdf_sha256(
    const std::uint8_t* input_key_material,
    const std::uint32_t input_key_material_length,
    const std::uint8_t* salt,
    const std::uint32_t salt_length,
    const std::uint8_t* info,
    const std::uint32_t info_length,
    std::uint8_t* output,
    const std::uint32_t output_length) {
    if (input_key_material == nullptr ||
        input_key_material_length == 0 ||
        !ValidOptionalBuffer(salt, salt_length) ||
        !ValidOptionalBuffer(info, info_length) ||
        output == nullptr ||
        output_length == 0 ||
        output_length > crypto_kdf_hkdf_sha256_bytes_max()) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    std::uint8_t pseudorandom_key[KeyBytes]{};
    if (crypto_kdf_hkdf_sha256_extract(
            pseudorandom_key,
            DataOrNull(salt, salt_length),
            salt_length,
            input_key_material,
            input_key_material_length) != 0) {
        sodium_memzero(pseudorandom_key, sizeof(pseudorandom_key));
        return IHBR_STATUS_CRYPTO_FAILURE;
    }
    const auto status = crypto_kdf_hkdf_sha256_expand(
        output,
        output_length,
        reinterpret_cast<const char*>(DataOrNull(info, info_length)),
        info_length,
        pseudorandom_key);
    sodium_memzero(pseudorandom_key, sizeof(pseudorandom_key));
    if (status != 0) {
        sodium_memzero(output, output_length);
        return IHBR_STATUS_CRYPTO_FAILURE;
    }
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_chacha20poly1305_seal(
    const std::uint8_t* key,
    const std::uint8_t* nonce,
    const std::uint8_t* aad,
    const std::uint32_t aad_length,
    const std::uint8_t* plaintext,
    const std::uint32_t plaintext_length,
    std::uint8_t* ciphertext,
    std::uint8_t* tag) {
    if (key == nullptr || nonce == nullptr ||
        !ValidOptionalBuffer(aad, aad_length) ||
        plaintext == nullptr || plaintext_length == 0 ||
        ciphertext == nullptr || tag == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    unsigned long long tag_length = 0;
    if (crypto_aead_chacha20poly1305_ietf_encrypt_detached(
            ciphertext,
            tag,
            &tag_length,
            plaintext,
            plaintext_length,
            DataOrNull(aad, aad_length),
            aad_length,
            nullptr,
            nonce,
            key) != 0 ||
        tag_length != TagBytes) {
        sodium_memzero(ciphertext, plaintext_length);
        sodium_memzero(tag, TagBytes);
        return IHBR_STATUS_CRYPTO_FAILURE;
    }
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_chacha20poly1305_open(
    const std::uint8_t* key,
    const std::uint8_t* nonce,
    const std::uint8_t* aad,
    const std::uint32_t aad_length,
    const std::uint8_t* ciphertext,
    const std::uint32_t ciphertext_length,
    const std::uint8_t* tag,
    std::uint8_t* plaintext) {
    if (key == nullptr || nonce == nullptr ||
        !ValidOptionalBuffer(aad, aad_length) ||
        ciphertext == nullptr || ciphertext_length == 0 ||
        tag == nullptr || plaintext == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    if (crypto_aead_chacha20poly1305_ietf_decrypt_detached(
            plaintext,
            nullptr,
            ciphertext,
            ciphertext_length,
            tag,
            DataOrNull(aad, aad_length),
            aad_length,
            nonce,
            key) != 0) {
        sodium_memzero(plaintext, ciphertext_length);
        return IHBR_STATUS_AUTHENTICATION_FAILURE;
    }
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_constant_time_equal(
    const std::uint8_t* first,
    const std::uint8_t* second,
    const std::uint32_t length,
    std::uint8_t* equal) {
    if (first == nullptr || second == nullptr ||
        length == 0 || equal == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    *equal = sodium_memcmp(first, second, length) == 0 ? 1U : 0U;
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_secure_zero(
    std::uint8_t* material,
    const std::uint32_t material_length) {
    if (material == nullptr || material_length == 0) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    sodium_memzero(material, material_length);
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_kcp_create(
    const std::uint32_t conversation,
    ihbr_kcp_context** context) {
    if (conversation == 0 || context == nullptr || *context != nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    auto* value = new (std::nothrow) ihbr_kcp_context();
    if (value == nullptr) {
        return IHBR_STATUS_KCP_FAILURE;
    }
    value->handle = ikcp_create(conversation, value);
    if (value->handle == nullptr ||
        ikcp_setmtu(
            value->handle,
            static_cast<int>(KcpSegmentBytes)) != 0 ||
        ikcp_wndsize(
            value->handle,
            static_cast<int>(KcpWindowSegments),
            static_cast<int>(KcpWindowSegments)) != 0 ||
        ikcp_nodelay(
            value->handle,
            1,
            static_cast<int>(KcpUpdateMilliseconds),
            static_cast<int>(KcpFastResend),
            1) != 0) {
        if (value->handle != nullptr) {
            ikcp_release(value->handle);
        }
        delete value;
        return IHBR_STATUS_KCP_FAILURE;
    }
    value->handle->rx_minrto =
        static_cast<IINT32>(KcpMinimumRtoMilliseconds);
    value->handle->rx_rto =
        static_cast<IINT32>(KcpMaximumRtoMilliseconds);
    value->handle->dead_link = KcpDeadLinkRetransmits;
    value->handle->snd_wnd = KcpWindowSegments;
    value->handle->rcv_wnd = KcpWindowSegments;
    value->handle->stream = 0;
    ikcp_setoutput(value->handle, &KcpOutput);
    *context = value;
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_kcp_release(
    ihbr_kcp_context** context) {
    if (context == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    auto* value = *context;
    if (value == nullptr) {
        return IHBR_STATUS_OK;
    }
    {
        std::scoped_lock lock(value->mutex);
        value->closed = true;
        if (value->handle != nullptr) {
            ikcp_release(value->handle);
            value->handle = nullptr;
        }
        for (auto& output : value->outputs) {
            sodium_memzero(output.data(), output.size());
        }
        value->outputs.clear();
    }
    delete value;
    *context = nullptr;
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_kcp_send(
    ihbr_kcp_context* context,
    const std::uint8_t* message,
    const std::uint32_t message_length) {
    if (context == nullptr || message == nullptr ||
        message_length == 0 || message_length > KcpMessageBytes) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    std::scoped_lock lock(context->mutex);
    if (context->closed || context->handle == nullptr) {
        return IHBR_STATUS_CLOSED;
    }
    if (ikcp_waitsnd(context->handle) >=
        static_cast<int>(KcpOutputQueueItems)) {
        return IHBR_STATUS_QUEUE_FULL;
    }
    if (ikcp_send(
            context->handle,
            reinterpret_cast<const char*>(message),
            static_cast<int>(message_length)) < 0) {
        context->closed = true;
        return IHBR_STATUS_KCP_FAILURE;
    }
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_kcp_input(
    ihbr_kcp_context* context,
    const std::uint8_t* segment,
    const std::uint32_t segment_length) {
    if (context == nullptr || segment == nullptr ||
        segment_length < KcpHeaderBytes ||
        segment_length > KcpSegmentBytes) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    std::scoped_lock lock(context->mutex);
    if (context->closed || context->handle == nullptr) {
        return IHBR_STATUS_CLOSED;
    }
    if (ikcp_input(
            context->handle,
            reinterpret_cast<const char*>(segment),
            static_cast<long>(segment_length)) < 0) {
        return IHBR_STATUS_KCP_FAILURE;
    }
    ClampKcpRto(*context);
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_kcp_update(
    ihbr_kcp_context* context,
    const std::uint32_t monotonic_milliseconds) {
    if (context == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    std::scoped_lock lock(context->mutex);
    if (context->closed || context->handle == nullptr) {
        return IHBR_STATUS_CLOSED;
    }
    ClampKcpRto(*context);
    ikcp_update(context->handle, monotonic_milliseconds);
    ClampKcpRto(*context);
    if (context->handle->state ==
        std::numeric_limits<IUINT32>::max()) {
        context->closed = true;
        return IHBR_STATUS_CLOSED;
    }
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_kcp_next_output(
    ihbr_kcp_context* context,
    std::uint8_t* output,
    const std::uint32_t output_capacity,
    std::uint32_t* output_length) {
    if (context == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    std::scoped_lock lock(context->mutex);
    if (context->closed || context->handle == nullptr) {
        return IHBR_STATUS_CLOSED;
    }
    return CopyFront(
        context->outputs,
        output,
        output_capacity,
        output_length);
}

ihbr_status IHBR_CALL ihbr_kcp_receive(
    ihbr_kcp_context* context,
    std::uint8_t* output,
    const std::uint32_t output_capacity,
    std::uint32_t* output_length) {
    if (context == nullptr || output_length == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    std::scoped_lock lock(context->mutex);
    *output_length = 0;
    if (context->closed || context->handle == nullptr) {
        return IHBR_STATUS_CLOSED;
    }
    const auto required = ikcp_peeksize(context->handle);
    if (required < 0) {
        return IHBR_STATUS_OK;
    }
    *output_length = static_cast<std::uint32_t>(required);
    if (output == nullptr ||
        output_capacity < *output_length) {
        return IHBR_STATUS_BUFFER_TOO_SMALL;
    }
    if (ikcp_recv(
            context->handle,
            reinterpret_cast<char*>(output),
            required) != required) {
        context->closed = true;
        return IHBR_STATUS_KCP_FAILURE;
    }
    return IHBR_STATUS_OK;
}

ihbr_status IHBR_CALL ihbr_kcp_waiting(
    ihbr_kcp_context* context,
    std::uint32_t* waiting_segments) {
    if (context == nullptr || waiting_segments == nullptr) {
        return IHBR_STATUS_INVALID_ARGUMENT;
    }
    std::scoped_lock lock(context->mutex);
    if (context->closed || context->handle == nullptr) {
        return IHBR_STATUS_CLOSED;
    }
    const auto waiting = ikcp_waitsnd(context->handle);
    *waiting_segments = static_cast<std::uint32_t>(
        std::max(0, waiting));
    return IHBR_STATUS_OK;
}
