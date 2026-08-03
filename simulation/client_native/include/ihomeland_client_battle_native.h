#ifndef IHOMELAND_CLIENT_BATTLE_NATIVE_H
#define IHOMELAND_CLIENT_BATTLE_NATIVE_H

#include <stdint.h>

#if defined(_WIN32)
#if defined(IHOMELAND_CLIENT_BATTLE_NATIVE_EXPORTS)
#define IHBR_API __declspec(dllexport)
#else
#define IHBR_API __declspec(dllimport)
#endif
#define IHBR_CALL __cdecl
#else
#define IHBR_API
#define IHBR_CALL
#endif

#ifdef __cplusplus
extern "C" {
#endif

/// ihbr_status 是 versioned C ABI 的稳定结果码。
typedef enum ihbr_status {
    IHBR_STATUS_OK = 0,
    IHBR_STATUS_INVALID_ARGUMENT = 1,
    IHBR_STATUS_CRYPTO_FAILURE = 2,
    IHBR_STATUS_AUTHENTICATION_FAILURE = 3,
    IHBR_STATUS_BUFFER_TOO_SMALL = 4,
    IHBR_STATUS_QUEUE_FULL = 5,
    IHBR_STATUS_KCP_FAILURE = 6,
    IHBR_STATUS_CLOSED = 7
} ihbr_status;

/// ihbr_kcp_context 是 generation-scoped KCP primitive 的 opaque handle。
typedef struct ihbr_kcp_context ihbr_kcp_context;

/// ihbr_abi_version 返回当前 C ABI version 1。
IHBR_API uint32_t IHBR_CALL ihbr_abi_version(void);

/// ihbr_initialize 验证锁定 libsodium primitive 的固定宽度。
IHBR_API ihbr_status IHBR_CALL ihbr_initialize(void);

/// ihbr_random_fill 使用 CSPRNG 填充非空 caller buffer。
IHBR_API ihbr_status IHBR_CALL ihbr_random_fill(
    uint8_t* output,
    uint32_t output_length);

/// ihbr_x25519_public 从 32-byte secret scalar 派生 public key。
IHBR_API ihbr_status IHBR_CALL ihbr_x25519_public(
    const uint8_t* secret_scalar,
    uint8_t* public_key);

/// ihbr_x25519_shared 计算 32-byte shared secret 并拒绝 small-order peer key。
IHBR_API ihbr_status IHBR_CALL ihbr_x25519_shared(
    const uint8_t* secret_scalar,
    const uint8_t* peer_public_key,
    uint8_t* shared_secret);

/// ihbr_hmac_sha256 计算任意非空 key 的 32-byte HMAC。
IHBR_API ihbr_status IHBR_CALL ihbr_hmac_sha256(
    const uint8_t* key,
    uint32_t key_length,
    const uint8_t* message,
    uint32_t message_length,
    uint8_t* tag);

/// ihbr_hkdf_sha256 以 RFC 5869 extract/expand 生成有界 output。
IHBR_API ihbr_status IHBR_CALL ihbr_hkdf_sha256(
    const uint8_t* input_key_material,
    uint32_t input_key_material_length,
    const uint8_t* salt,
    uint32_t salt_length,
    const uint8_t* info,
    uint32_t info_length,
    uint8_t* output,
    uint32_t output_length);

/// ihbr_chacha20poly1305_seal 执行 detached AEAD seal。
IHBR_API ihbr_status IHBR_CALL ihbr_chacha20poly1305_seal(
    const uint8_t* key,
    const uint8_t* nonce,
    const uint8_t* aad,
    uint32_t aad_length,
    const uint8_t* plaintext,
    uint32_t plaintext_length,
    uint8_t* ciphertext,
    uint8_t* tag);

/// ihbr_chacha20poly1305_open 验证 detached tag 后输出 plaintext。
IHBR_API ihbr_status IHBR_CALL ihbr_chacha20poly1305_open(
    const uint8_t* key,
    const uint8_t* nonce,
    const uint8_t* aad,
    uint32_t aad_length,
    const uint8_t* ciphertext,
    uint32_t ciphertext_length,
    const uint8_t* tag,
    uint8_t* plaintext);

/// ihbr_constant_time_equal 比较相同宽度的 secret，不暴露提前退出。
IHBR_API ihbr_status IHBR_CALL ihbr_constant_time_equal(
    const uint8_t* first,
    const uint8_t* second,
    uint32_t length,
    uint8_t* equal);

/// ihbr_secure_zero 清零 caller-owned secret buffer。
IHBR_API ihbr_status IHBR_CALL ihbr_secure_zero(
    uint8_t* material,
    uint32_t material_length);

/// ihbr_kcp_create 创建 exact profile 的单个 KCP context。
IHBR_API ihbr_status IHBR_CALL ihbr_kcp_create(
    uint32_t conversation,
    ihbr_kcp_context** context);

/// ihbr_kcp_release 幂等释放 caller 保存的 handle 并置 null。
IHBR_API ihbr_status IHBR_CALL ihbr_kcp_release(
    ihbr_kcp_context** context);

/// ihbr_kcp_send 向 KCP 写入不超过 1000 bytes 的单个 message。
IHBR_API ihbr_status IHBR_CALL ihbr_kcp_send(
    ihbr_kcp_context* context,
    const uint8_t* message,
    uint32_t message_length);

/// ihbr_kcp_input 输入不超过 1024 bytes 的单个或聚合 KCP segment。
IHBR_API ihbr_status IHBR_CALL ihbr_kcp_input(
    ihbr_kcp_context* context,
    const uint8_t* segment,
    uint32_t segment_length);

/// ihbr_kcp_update 以 caller monotonic millisecond 驱动固定 10 ms pump。
IHBR_API ihbr_status IHBR_CALL ihbr_kcp_update(
    ihbr_kcp_context* context,
    uint32_t monotonic_milliseconds);

/// ihbr_kcp_next_output 取出一个待交给 managed secure lane 的 segment。
IHBR_API ihbr_status IHBR_CALL ihbr_kcp_next_output(
    ihbr_kcp_context* context,
    uint8_t* output,
    uint32_t output_capacity,
    uint32_t* output_length);

/// ihbr_kcp_receive 取出一个已重组的 application message。
IHBR_API ihbr_status IHBR_CALL ihbr_kcp_receive(
    ihbr_kcp_context* context,
    uint8_t* output,
    uint32_t output_capacity,
    uint32_t* output_length);

/// ihbr_kcp_waiting 返回当前 third-party send queue/buffer segment 数。
IHBR_API ihbr_status IHBR_CALL ihbr_kcp_waiting(
    ihbr_kcp_context* context,
    uint32_t* waiting_segments);

#ifdef __cplusplus
}
#endif

#endif
