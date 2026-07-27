#include "ihomeland/qualification/battle/protocol_client.hpp"
#include "ihomeland/sim/transport/crypto_provider.hpp"
#include "ihomeland/battle/v1/battle.pb.h"

#include <fcntl.h>
#include <io.h>
#define NOMINMAX
#include <winsock2.h>
#include <ws2tcpip.h>

#include <algorithm>
#include <array>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <iostream>
#include <limits>
#include <memory>
#include <ranges>
#include <span>
#include <stdexcept>
#include <string_view>
#include <thread>
#include <utility>
#include <vector>

namespace {

namespace battle =
    ihomeland::qualification::battle;

constexpr std::array<std::uint8_t, 4> FrameMagic{
    'I', 'H', 'B', 'Q'};
constexpr std::size_t PrefixBytes = 4;
constexpr std::size_t HeaderBytes = 28;
constexpr std::size_t MaximumFrameBytes = 65'536;
constexpr std::uint8_t ContractVersion = 1;
constexpr std::uint8_t DescribeRequest = 1;
constexpr std::uint8_t DescribeReceipt = 129;
constexpr std::uint8_t ShutdownRequest = 2;
constexpr std::uint8_t ShutdownReceipt = 130;
constexpr std::uint8_t SessionStartRequest = 3;
constexpr std::uint8_t SessionEvent = 131;
constexpr std::uint8_t WorkloadCommandRequest = 4;
constexpr std::uint8_t WorkloadEvent = 132;
constexpr std::uint8_t NetworkTransitionRequest = 5;
constexpr std::uint8_t NetworkTransitionEvent = 133;
constexpr std::uint8_t PollRequest = 6;
constexpr std::uint8_t PollReceipt = 134;
constexpr std::uint8_t HandshakeAttackRequest = 7;
constexpr std::uint8_t HandshakeAttackReceipt = 135;
constexpr std::size_t BuildIdentityBytes = 32;
constexpr std::size_t SessionStartPayloadBytes = 76;
constexpr std::uint8_t MinimumClientSlot = 1;
constexpr std::uint8_t MaximumClientSlot = 8;
constexpr std::uint8_t AddressFamilyIPv4 = 4;
constexpr std::uint8_t AddressFamilyIPv6 = 6;
constexpr std::size_t EndpointAddressOffset = 4;
constexpr std::size_t TicketIdOffset = 20;
constexpr std::size_t TicketSecretOffset = 36;
constexpr std::size_t TicketExpiryOffset = 68;
constexpr std::uint8_t SessionEstablishedEvent = 1;
constexpr std::uint8_t SessionFailedEvent = 2;
constexpr std::uint8_t SessionStartFailureInvalidRequest = 1;
constexpr std::uint8_t SessionStartFailureDeadline = 2;
constexpr std::uint8_t SessionStartFailureSocket = 3;
constexpr std::uint8_t SessionStartFailureHandshakeSetup = 4;
constexpr std::uint8_t SessionStartFailureRetryExchange = 5;
constexpr std::uint8_t SessionStartFailureRetryValidation = 6;
constexpr std::uint8_t SessionStartFailureAcceptExchange = 7;
constexpr std::uint8_t SessionStartFailureAcceptValidation = 8;
constexpr std::uint8_t SessionStartFailureTransportSetup = 9;
constexpr std::uint8_t SessionStartFailureUnknown = 10;
constexpr std::size_t MaximumHandshakeAttempts = 2;
constexpr std::size_t MaximumIgnoredHandshakeDatagrams = 16;
constexpr std::size_t WorkloadCommandPayloadBytes = 32;
constexpr std::size_t WorkloadEventPayloadBytes = 32;
constexpr std::size_t NetworkTransitionPayloadBytes = 24;
constexpr std::size_t NetworkTransitionEventPayloadBytes = 8;
constexpr std::size_t PollPayloadBytes = 8;
constexpr std::size_t PollReceiptPayloadBytes = 144;
constexpr std::size_t HandshakeAttackPayloadBytes = 80;
constexpr std::size_t HandshakeAttackReceiptBytes = 16;
constexpr std::uint8_t HandshakeCookieLess = 1;
constexpr std::uint8_t HandshakeProofForgery = 2;
constexpr std::uint8_t HandshakeTicketReplay = 3;
constexpr std::uint8_t HandshakeSpoofedSource = 4;
constexpr std::array<std::uint8_t, 4>
    HandshakeClientAuthMagic{
        'I', 'H', 'B', 'A'};
constexpr std::uint8_t HandshakeWireVersion = 1;
constexpr std::uint8_t HandshakeClientAuthKind = 3;
constexpr std::size_t HandshakeProofOffset = 108;
constexpr std::uint64_t NegativeResponseWaitMilliseconds = 250;
constexpr std::uint8_t PollFailureInvalidRequest = 1;
constexpr std::uint8_t PollFailureAuthentication = 2;
constexpr std::uint8_t PollFailureSnapshot = 3;
constexpr std::uint8_t PollFailureKcp = 4;
constexpr std::uint8_t PollFailureUnexpectedLane = 5;
constexpr std::uint8_t PollFailureTransport = 6;
constexpr std::uint8_t PollFailureKcpInflightExpiry = 7;
constexpr std::size_t MaximumTransitionInterleavedDatagrams = 32;
constexpr std::uint8_t WorkloadInputBundle = 1;
constexpr std::uint8_t WorkloadProbe = 2;
constexpr std::uint8_t WorkloadResyncRequest = 3;
constexpr std::uint8_t WorkloadDatagramSent = 1;
constexpr std::uint8_t WorkloadReliableQueued = 4;
constexpr std::uint8_t DeliveryUnmodified = 0;
constexpr std::uint8_t DeliveryExactReplay = 1;
constexpr std::uint8_t DeliveryTamperedTag = 2;
constexpr std::uint8_t DeliveryOversizePrefix = 3;
constexpr std::uint8_t DeliveryAadTampered = 4;
constexpr std::uint8_t DeliveryCiphertextTampered = 5;
constexpr std::uint8_t DeliveryFutureSequence = 6;
constexpr std::uint8_t DeliveryTooOldSequence = 7;
constexpr std::uint8_t DeliveryWrongDirection = 8;
constexpr std::uint8_t DeliveryWrongLane = 9;
constexpr std::uint8_t DeliveryRebindHijack = 10;
constexpr std::uint8_t DeliveryKcpExpired = 11;
constexpr std::uint8_t DeliveryMalformedPrefix = 12;
constexpr std::uint8_t TamperBitMask = 1;
constexpr std::uint8_t NetworkRebind = 1;
constexpr std::uint8_t NetworkRekey = 2;
constexpr std::uint8_t NetworkClose = 3;
constexpr std::uint8_t NetworkOldEpochProbe = 4;
constexpr std::uint8_t TransitionFailureInvalidRequest = 1;
constexpr std::uint8_t TransitionFailureRebindRequest = 2;
constexpr std::uint8_t TransitionFailureRebindChallengeReceive = 3;
constexpr std::uint8_t TransitionFailureRebindChallengeValidate = 4;
constexpr std::uint8_t TransitionFailureRebindConfirm = 5;
constexpr std::uint8_t TransitionFailureRebindCommitReceive = 6;
constexpr std::uint8_t TransitionFailureRebindCommitValidate = 7;
constexpr std::uint8_t TransitionFailureOtherOperation = 8;
constexpr std::uint8_t TransitionFailureRebindApplicationPacket = 9;
constexpr std::uint8_t TransitionFailureRebindRuntime = 10;
constexpr std::uint8_t TransitionFailureRebindStandard = 11;
constexpr std::uint8_t TransitionFailureRebindUnknown = 12;
constexpr std::uint8_t TransitionFailureRebindChallengeAuthentication = 13;
constexpr std::uint8_t TransitionFailureRebindChallengeApplication = 14;
constexpr std::uint8_t TransitionFailureRebindChallengeInterleave = 15;
constexpr std::uint8_t TransitionFailureRebindCommitAuthentication = 16;
constexpr std::uint8_t TransitionFailureRebindCommitApplication = 17;
constexpr std::uint8_t TransitionFailureRebindCommitInterleave = 18;
constexpr std::uint8_t TransitionFailureRekeyPrepare = 19;
constexpr std::uint8_t TransitionFailureRekeyProposal = 20;
constexpr std::uint8_t TransitionFailureRekeyCommitReceive = 21;
constexpr std::uint8_t TransitionFailureRekeyCommitAuthentication = 22;
constexpr std::uint8_t TransitionFailureRekeyCommitApplication = 23;
constexpr std::uint8_t TransitionFailureRekeyCommitInterleave = 24;
constexpr std::uint8_t TransitionFailureRekeyCommitValidate = 25;
constexpr std::uint8_t TransitionFailureOldEpochSend = 26;
constexpr std::uint8_t TransitionFailureCloseRequest = 27;
constexpr std::uint8_t TransitionFailureCloseAckReceive = 28;
constexpr std::uint8_t TransitionFailureCloseAckAuthentication = 29;
constexpr std::uint8_t TransitionFailureCloseAckApplication = 30;
constexpr std::uint8_t TransitionFailureCloseAckInterleave = 31;
constexpr std::uint8_t TransitionFailureCloseAckValidate = 32;
constexpr std::uint8_t TransitionFailureRekeyUnhandled = 33;
constexpr std::uint8_t TransitionFailureCloseUnhandled = 34;
constexpr std::uint8_t TransitionFailureOldEpochUnhandled = 35;
constexpr std::array<std::uint8_t, 4> ControlMagic{
    'I', 'H', 'B', 'C'};
constexpr std::uint8_t ControlVersion = 1;
constexpr std::size_t ControlEnvelopeBytes = 8;
constexpr std::uint8_t ControlRebindRequest = 1;
constexpr std::uint8_t ControlRebindChallenge = 2;
constexpr std::uint8_t ControlRebindConfirm = 3;
constexpr std::uint8_t ControlRebindCommitted = 4;
constexpr std::uint8_t ControlRekeyProposal = 5;
constexpr std::uint8_t ControlRekeyCommitted = 6;
constexpr std::uint8_t ControlCloseRequest = 7;
constexpr std::uint8_t ControlCloseAcknowledged = 8;
constexpr std::size_t RebindRequestBytes = 34;
constexpr std::size_t RebindChallengeBytes = 48;
constexpr std::size_t RebindCommittedBytes = 20;
constexpr std::size_t RekeyBytes = 32;
constexpr std::size_t CloseBytes = 1;
constexpr std::uint8_t ClientRequestedClose = 1;
constexpr std::size_t DescribePayloadBytes =
    BuildIdentityBytes + 1 + 4;

/// PollException 只携带封闭失败码，不保存 endpoint、payload 或 credential。
class PollException final : public std::exception {
public:
    /// 构造函数保存可安全回传的 closed code。
    explicit PollException(
        const std::uint8_t code) noexcept
        : code_(code) {}

    /// Code 返回 poll-receipt-v1 的低敏失败枚举。
    [[nodiscard]] std::uint8_t Code() const noexcept {
        return code_;
    }

    /// what 避免把底层异常文本传播到 stdio contract。
    [[nodiscard]] const char* what() const noexcept override {
        return "protocol client poll failed";
    }

private:
    /// code_ 是 poll-receipt-v1 登记的非零失败枚举。
    std::uint8_t code_;
};

/// TransitionException 只携带封闭迁移失败码，不保存 endpoint、payload 或密钥。
class TransitionException final : public std::exception {
public:
    /// 构造函数保存 network-transition-event-v1 的非零失败枚举。
    explicit TransitionException(
        const std::uint8_t code) noexcept
        : code_(code) {}

    /// Code 返回 supervisor 可安全归类的迁移失败枚举。
    [[nodiscard]] std::uint8_t Code() const noexcept {
        return code_;
    }

    /// what 避免把底层 socket 或协议异常传播到 stdio contract。
    [[nodiscard]] const char* what() const noexcept override {
        return "protocol client transition failed";
    }

private:
    /// code_ 是 network-transition-event-v1 登记的非零失败枚举。
    std::uint8_t code_;
};

/// SessionStartException 只携带封闭握手阶段，不保存 credential 或网络内容。
class SessionStartException final : public std::exception {
public:
    /// 构造函数保存 session-event-v1 的非零失败枚举。
    explicit SessionStartException(
        const std::uint8_t code) noexcept
        : code_(code) {}

    /// Code 返回 supervisor 可安全归类的 session-start 阶段。
    [[nodiscard]] std::uint8_t Code() const noexcept {
        return code_;
    }

    /// what 不传播底层 socket、endpoint、payload 或 credential。
    [[nodiscard]] const char* what() const noexcept override {
        return "protocol client session start failed";
    }

private:
    /// code_ 是 session-event-v1 登记的非零失败枚举。
    std::uint8_t code_;
};

/// ExecuteSessionStartStep 把底层异常收敛为稳定、低敏的握手阶段。
template <typename Operation>
decltype(auto) ExecuteSessionStartStep(
    const std::uint8_t failure_code,
    Operation&& operation) {
    try {
        return std::forward<Operation>(
            operation)();
    } catch (const SessionStartException&) {
        throw;
    } catch (...) {
        throw SessionStartException(
            failure_code);
    }
}

/// UnhandledTransitionFailure 将未进入显式阶段的异常绑定到 operation。
[[nodiscard]] std::uint8_t UnhandledTransitionFailure(
    const std::uint8_t operation) noexcept {
    switch (operation) {
    case NetworkRebind:
        return TransitionFailureRebindUnknown;
    case NetworkRekey:
        return TransitionFailureRekeyUnhandled;
    case NetworkClose:
        return TransitionFailureCloseUnhandled;
    case NetworkOldEpochProbe:
        return TransitionFailureOldEpochUnhandled;
    default:
        return TransitionFailureInvalidRequest;
    }
}

/// ExecuteTransitionStep 把任意底层异常收敛为稳定、低敏的迁移阶段。
template <typename Operation>
decltype(auto) ExecuteTransitionStep(
    const std::uint8_t failure_code,
    Operation&& operation) {
    try {
        return std::forward<Operation>(
            operation)();
    } catch (const TransitionException&) {
        throw;
    } catch (...) {
        throw TransitionException(
            failure_code);
    }
}

/// ApplicationPacketFailure 是已认证 application lane 的封闭验证结果。
enum class ApplicationPacketFailure : std::uint8_t {
    Snapshot,
    Kcp,
    UnexpectedLane,
};

/// ApplicationPacketException 在 poll 与 control demultiplexing 间保留失败语义。
class ApplicationPacketException final : public std::exception {
public:
    /// 构造函数保存不含 payload 或 endpoint 的封闭失败类别。
    explicit ApplicationPacketException(
        const ApplicationPacketFailure failure) noexcept
        : failure_(failure) {}

    /// Failure 返回调用方可映射的验证失败类别。
    [[nodiscard]] ApplicationPacketFailure Failure() const noexcept {
        return failure_;
    }

    /// what 只提供稳定诊断，不传播收到的网络内容。
    [[nodiscard]] const char* what() const noexcept override {
        return "protocol client application packet failed";
    }

private:
    /// failure_ 区分 raw、KCP 与 lane contract 失败。
    ApplicationPacketFailure failure_;
};

/// ObservedUnixMilliseconds 返回 request deadline 使用的 UTC 毫秒。
[[nodiscard]] std::uint64_t
ObservedUnixMilliseconds() {
    const auto observed =
        std::chrono::duration_cast<
            std::chrono::milliseconds>(
            std::chrono::system_clock::now()
                .time_since_epoch())
            .count();
    if (observed <= 0) {
        throw std::runtime_error(
            "protocol client clock is invalid");
    }
    return static_cast<std::uint64_t>(
        observed);
}

/// SocketOwner 在全部成功和失败路径关闭唯一 connected UDP socket。
class SocketOwner final {
public:
    /// 构造函数接管有效 WinSock handle。
    explicit SocketOwner(const SOCKET socket)
        : socket_(socket) {}

    /// 析构函数关闭 handle。
    ~SocketOwner() {
        Close();
    }

    SocketOwner(const SocketOwner&) = delete;
    SocketOwner& operator=(const SocketOwner&) = delete;

    /// Get 返回当前 handle。
    [[nodiscard]] SOCKET Get() const noexcept {
        return socket_;
    }

    /// Close 幂等关闭 handle。
    void Close() noexcept {
        if (socket_ != INVALID_SOCKET) {
            closesocket(socket_);
            socket_ = INVALID_SOCKET;
        }
    }

private:
    /// socket_ 是当前 session 唯一 UDP handle。
    SOCKET socket_{INVALID_SOCKET};
};

/// WinSockOwner 绑定 process 内一次 WinSock 初始化与清理。
class WinSockOwner final {
public:
    /// 构造函数要求 Windows Sockets 2.2。
    WinSockOwner() {
        WSADATA data{};
        const auto startup_result =
            WSAStartup(
                MAKEWORD(2, 2),
                &data);
        if (startup_result != 0) {
            throw std::runtime_error(
                "protocol client socket runtime failed");
        }
        if (LOBYTE(data.wVersion) != 2 ||
            HIBYTE(data.wVersion) != 2) {
            WSACleanup();
            throw std::runtime_error(
                "protocol client socket version is unsupported");
        }
        started_ = true;
    }

    /// 析构函数平衡成功的 WSAStartup。
    ~WinSockOwner() {
        if (started_) {
            WSACleanup();
        }
    }

    WinSockOwner(const WinSockOwner&) = delete;
    WinSockOwner& operator=(const WinSockOwner&) = delete;

private:
    /// started_ 防止失败构造路径清理未初始化 runtime。
    bool started_{false};
};

/// SessionState 保存握手后唯一 socket 与 independent secure owner。
struct SessionState final {
    /// socket 独占 connected UDP handle。
    std::unique_ptr<SocketOwner> socket;
    /// secure 独占 traffic key、nonce 与 replay state。
    std::unique_ptr<battle::ProtocolSecureChannel> secure;
    /// raw 独占 C2S 编码与 S2C baseline 状态。
    std::unique_ptr<battle::ProtocolRawLane> raw;
    /// kcp 独占可靠 lane primitive、重传与有界 reassembly。
    std::unique_ptr<battle::ProtocolKcpLane> kcp;
    /// server_address 保存 session-start 的 numeric endpoint，供 rebind 新 socket 使用。
    std::array<std::uint8_t, 16> server_address{};
    /// server_address_family 是 closed IPv4/IPv6 discriminator。
    std::uint8_t server_address_family{};
    /// server_port 是唯一 production UDP listener port。
    std::uint16_t server_port{};
    /// endpoint_generation 是双方最近 committed endpoint epoch。
    std::uint32_t endpoint_generation{1};
    /// key_epoch 是双方最近 committed traffic key epoch。
    std::uint32_t key_epoch{1};
    /// snapshot_count 是通过完整 raw baseline validator 的累计快照数。
    std::uint64_t snapshot_count{};
    /// reliable_count 是通过 KCP application validator 的累计消息数。
    std::uint64_t reliable_count{};
    /// baseline_gap_count 是 raw decoder 观测到未知 baseline 的累计次数。
    std::uint64_t baseline_gap_count{};
    /// secure_kcp_datagrams 是通过 AEAD/replay 后进入 KCP lane 的累计 datagram 数。
    std::uint64_t secure_kcp_datagrams{};
    /// latest_application_sequence 是最近接受的 raw/KCP application identity。
    std::uint64_t latest_application_sequence{};
    /// client_slot 是 run-local 低敏 correlation。
    std::uint8_t client_slot{};
};

/// SecureClear 以 volatile writes 清理 inherited secret frame 与临时 seed。
void SecureClear(
    const std::span<std::uint8_t> material) noexcept {
    auto* output =
        reinterpret_cast<
            volatile std::uint8_t*>(
            material.data());
    for (std::size_t index = 0;
         index < material.size();
         ++index) {
        output[index] = 0;
    }
}

/// ReadUint32BE 解码 frame length。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::uint8_t* input) noexcept {
    return
        (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// ReadUint16BE 解码 control payload length 与 UDP port。
[[nodiscard]] std::uint16_t ReadUint16BE(
    const std::uint8_t* input) noexcept {
    return static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(input[0]) << 8U) |
        static_cast<std::uint16_t>(input[1]));
}

/// ReadUint64BE 解码 sequence/deadline。
[[nodiscard]] std::uint64_t ReadUint64BE(
    const std::uint8_t* input) noexcept {
    std::uint64_t output = 0;
    for (std::size_t index = 0; index < 8; ++index) {
        output = (output << 8U) | input[index];
    }
    return output;
}

/// WriteUint32BE 编码 frame length。
void WriteUint32BE(
    std::uint8_t* output,
    const std::uint32_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// WriteUint16BE 编码 control payload length 与 UDP port。
void WriteUint16BE(
    std::uint8_t* output,
    const std::uint16_t value) noexcept {
    output[0] = static_cast<std::uint8_t>(value >> 8U);
    output[1] = static_cast<std::uint8_t>(value);
}

/// WriteUint64BE 编码 sequence/deadline。
void WriteUint64BE(
    std::uint8_t* output,
    const std::uint64_t value) noexcept {
    for (std::size_t index = 0; index < 8; ++index) {
        output[index] = static_cast<std::uint8_t>(
            value >> ((7U - index) * 8U));
    }
}

/// HandshakeDeadline 取 request、ticket 与固定 handshake timeout 的最早值。
[[nodiscard]] std::uint64_t HandshakeDeadline(
    const std::uint64_t request_deadline_unix_ms,
    const std::uint64_t ticket_expiry_unix_ms,
    const std::uint64_t started_unix_ms) {
    if (started_unix_ms >
        std::numeric_limits<std::uint64_t>::max() -
            battle::ProtocolHandshake::
                HandshakeTimeoutMilliseconds) {
        throw std::runtime_error(
            "protocol client handshake deadline overflow");
    }
    const auto protocol_deadline =
        started_unix_ms +
        battle::ProtocolHandshake::
            HandshakeTimeoutMilliseconds;
    const auto deadline = std::min(
        request_deadline_unix_ms,
        std::min(
            ticket_expiry_unix_ms,
            protocol_deadline));
    if (deadline <= started_unix_ms) {
        throw std::runtime_error(
            "protocol client handshake deadline expired");
    }
    return deadline;
}

/// ConfigureReceiveTimeout 以单次时钟快照映射 WinSock millisecond timeout。
///
/// @return false 仅表示允许空 poll 的 deadline 已到；其他路径仍 fail closed。
[[nodiscard]] bool ConfigureReceiveTimeout(
    const SOCKET socket,
    const std::uint64_t deadline_unix_ms,
    const bool expired_is_empty = false) {
    const auto now = ObservedUnixMilliseconds();
    if (deadline_unix_ms <= now) {
        if (expired_is_empty) {
            return false;
        }
        throw std::runtime_error(
            "protocol client socket deadline expired");
    }
    constexpr std::uint64_t MaximumReceiveSliceMilliseconds =
        1'000;
    const auto timeout_value = std::min(
        deadline_unix_ms - now,
        MaximumReceiveSliceMilliseconds);
    const auto timeout =
        static_cast<DWORD>(timeout_value);
    if (setsockopt(
            socket,
            SOL_SOCKET,
            SO_RCVTIMEO,
            reinterpret_cast<const char*>(
                &timeout),
            sizeof(timeout)) != 0) {
        throw std::runtime_error(
            "protocol client socket timeout failed");
    }
    return true;
}

/// ExchangeDatagram 重放 exact request 处理一次响应丢失，不改变 transcript。
///
/// 同 endpoint 的旧 secure datagram 可能在 UDP 端口复用后延迟到达；
/// 只允许 closed matcher 认可的当前握手响应进入 transcript，其他流量在
/// 固定总 deadline 与有界计数内丢弃。
[[nodiscard]] std::vector<std::uint8_t>
ExchangeDatagram(
    const SOCKET socket,
    const std::span<const std::uint8_t> request,
    bool (*matches_response)(
        std::span<const std::uint8_t>) noexcept,
    const std::uint64_t deadline_unix_ms) {
    if (matches_response == nullptr) {
        throw std::runtime_error(
            "protocol client UDP response matcher is missing");
    }
    std::array<
        std::uint8_t,
        battle::ProtocolSecureChannel::
            MaximumDatagramBytes> response{};
    std::size_t ignored_datagrams = 0;
    for (std::size_t attempt = 0;
         attempt < MaximumHandshakeAttempts;
         ++attempt) {
        const auto sent = send(
            socket,
            reinterpret_cast<const char*>(
                request.data()),
            static_cast<int>(request.size()),
            0);
        if (sent !=
            static_cast<int>(request.size())) {
            throw std::runtime_error(
                "protocol client UDP send failed");
        }
        for (;;) {
            static_cast<void>(
                ConfigureReceiveTimeout(
                    socket,
                    deadline_unix_ms));
            const auto received = recv(
                socket,
                reinterpret_cast<char*>(
                    response.data()),
                static_cast<int>(response.size()),
                0);
            if (received == SOCKET_ERROR) {
                const auto error = WSAGetLastError();
                if ((error == WSAETIMEDOUT ||
                     error == WSAEWOULDBLOCK) &&
                    attempt + 1 <
                        MaximumHandshakeAttempts &&
                    ObservedUnixMilliseconds() <
                        deadline_unix_ms) {
                    break;
                }
                throw std::runtime_error(
                    "protocol client UDP receive failed");
            }
            const auto candidate = std::span(
                response.data(),
                static_cast<std::size_t>(received));
            if (!matches_response(candidate)) {
                ++ignored_datagrams;
                if (ignored_datagrams >
                    MaximumIgnoredHandshakeDatagrams) {
                    throw std::runtime_error(
                        "protocol client UDP response budget exceeded");
                }
                continue;
            }
            return {
                response.begin(),
                response.begin() + received,
            };
        }
    }
    throw std::runtime_error(
        "protocol client UDP exchange exhausted");
}

/// ConnectSessionSocket 创建并 connect 到 fixed numeric session-start endpoint。
[[nodiscard]] std::unique_ptr<SocketOwner>
ConnectSessionSocket(
    const std::span<const std::uint8_t, 16> address,
    const std::uint8_t address_family,
    const std::uint16_t port) {
    if (port == 0) {
        throw std::runtime_error(
            "protocol client endpoint port is zero");
    }
    if (address_family == AddressFamilyIPv4) {
        const std::array<std::uint8_t, 12>
            mapped_prefix{
                0, 0, 0, 0,
                0, 0, 0, 0,
                0, 0, 0xff, 0xff,
            };
        if (!std::ranges::equal(
                address.first<12>(),
                mapped_prefix)) {
            throw std::runtime_error(
                "protocol client IPv4 address is not mapped");
        }
        sockaddr_in endpoint{};
        endpoint.sin_family = AF_INET;
        endpoint.sin_port = htons(port);
        std::ranges::copy(
            address.last<4>(),
            reinterpret_cast<std::uint8_t*>(
                &endpoint.sin_addr));
        auto socket = std::make_unique<
            SocketOwner>(
            ::socket(
                AF_INET,
                SOCK_DGRAM,
                IPPROTO_UDP));
        if (socket->Get() == INVALID_SOCKET ||
            connect(
                socket->Get(),
                reinterpret_cast<const sockaddr*>(
                    &endpoint),
                sizeof(endpoint)) != 0) {
            throw std::runtime_error(
                "protocol client IPv4 connect failed");
        }
        return socket;
    }
    if (address_family == AddressFamilyIPv6) {
        sockaddr_in6 endpoint{};
        endpoint.sin6_family = AF_INET6;
        endpoint.sin6_port = htons(port);
        std::ranges::copy(
            address,
            reinterpret_cast<std::uint8_t*>(
                &endpoint.sin6_addr));
        if (IN6_IS_ADDR_UNSPECIFIED(
                &endpoint.sin6_addr) ||
            IN6_IS_ADDR_MULTICAST(
                &endpoint.sin6_addr)) {
            throw std::runtime_error(
                "protocol client IPv6 endpoint is invalid");
        }
        auto socket = std::make_unique<
            SocketOwner>(
            ::socket(
                AF_INET6,
                SOCK_DGRAM,
                IPPROTO_UDP));
        if (socket->Get() == INVALID_SOCKET ||
            connect(
                socket->Get(),
                reinterpret_cast<const sockaddr*>(
                    &endpoint),
                sizeof(endpoint)) != 0) {
            throw std::runtime_error(
                "protocol client IPv6 connect failed");
        }
        return socket;
    }
    throw std::runtime_error(
        "protocol client address family is invalid");
}

/// StartSession 消费固定 76-byte credential 并完成真实 UDP handshake。
[[nodiscard]] SessionState StartSession(
    const std::span<std::uint8_t> payload,
    const std::uint64_t request_deadline_unix_ms) {
    if (payload.size() !=
            SessionStartPayloadBytes ||
        payload[0] < MinimumClientSlot ||
        payload[0] > MaximumClientSlot) {
        throw SessionStartException(
            SessionStartFailureInvalidRequest);
    }
    const auto client_slot = payload[0];
    const auto address_family = payload[1];
    const auto port = static_cast<std::uint16_t>(
        (static_cast<std::uint16_t>(
             payload[2])
         << 8U) |
        payload[3]);
    std::array<std::uint8_t, 16> address{};
    std::ranges::copy_n(
        payload.begin() + EndpointAddressOffset,
        address.size(),
        address.begin());
    battle::TicketCredential credential;
    std::ranges::copy_n(
        payload.begin() + TicketIdOffset,
        credential.ticket_id.size(),
        credential.ticket_id.begin());
    std::ranges::copy_n(
        payload.begin() + TicketSecretOffset,
        credential.ticket_secret.size(),
        credential.ticket_secret.begin());
    credential.expires_at_unix_ms =
        ReadUint64BE(
            payload.data() +
            TicketExpiryOffset);
    SecureClear(payload);
    const auto started_unix_ms =
        ObservedUnixMilliseconds();
    const auto deadline_unix_ms =
        ExecuteSessionStartStep(
            SessionStartFailureDeadline,
            [&] {
                return HandshakeDeadline(
                    request_deadline_unix_ms,
                    credential.expires_at_unix_ms,
                    started_unix_ms);
            });
    auto socket = ExecuteSessionStartStep(
        SessionStartFailureSocket,
        [&] {
            return ConnectSessionSocket(
                address,
                address_family,
                port);
        });
    auto handshake = ExecuteSessionStartStep(
        SessionStartFailureHandshakeSetup,
        [&] {
            return std::make_unique<
                battle::ProtocolHandshake>(
                std::move(credential),
                started_unix_ms);
        });
    const auto hello =
        handshake->ClientHello();
    const auto retry = ExecuteSessionStartStep(
        SessionStartFailureRetryExchange,
        [&] {
            return ExchangeDatagram(
                socket->Get(),
                hello,
                battle::ProtocolHandshake::
                    MatchesRetryEnvelope,
                deadline_unix_ms);
        });
    const auto auth =
        handshake->AcceptRetry(
            retry,
            ObservedUnixMilliseconds());
    if (!auth.has_value()) {
        throw SessionStartException(
            SessionStartFailureRetryValidation);
    }
    const auto accept = ExecuteSessionStartStep(
        SessionStartFailureAcceptExchange,
        [&] {
            return ExchangeDatagram(
                socket->Get(),
                *auth,
                battle::ProtocolHandshake::
                    MatchesServerAcceptEnvelope,
                deadline_unix_ms);
        });
    auto parameters =
        handshake->AcceptServer(
            accept,
            ObservedUnixMilliseconds());
    if (!parameters.has_value()) {
        throw SessionStartException(
            SessionStartFailureAcceptValidation);
    }
    ihomeland::sim::CryptoProvider crypto;
    const auto digest =
        crypto.Sha256(
            parameters->session_id);
    const auto conversation_handle =
        ReadUint64BE(digest.data());
    const auto conversation_lower =
        static_cast<std::uint32_t>(
            conversation_handle);
    const auto conversation =
        conversation_lower != 0
            ? conversation_lower
            : static_cast<std::uint32_t>(
                  conversation_handle >> 32U);
    if (conversation == 0) {
        throw std::runtime_error(
            "protocol client KCP conversation is zero");
    }
    battle::SecureIdentity identity{};
    std::ranges::copy_n(
        digest.begin(),
        identity.session_id_digest.size(),
        identity.session_id_digest.begin());
    identity.battle_session_generation =
        parameters->
            battle_session_generation;
    identity.endpoint_generation =
        parameters->endpoint_generation;
    std::ranges::copy_n(
        parameters->
            binding_fingerprint.begin(),
        identity.binding_discriminator.size(),
        identity.binding_discriminator.begin());
    auto secure = ExecuteSessionStartStep(
        SessionStartFailureTransportSetup,
        [&] {
            return std::make_unique<
                battle::ProtocolSecureChannel>(
                parameters->session_seed,
                identity,
                parameters->key_epoch,
                ObservedUnixMilliseconds());
        });
    SecureClear(parameters->session_seed);
    SecureClear(
        parameters->binding_fingerprint);
    handshake.reset();
    return SessionState{
        .socket = std::move(socket),
        .secure = std::move(secure),
        .raw = std::make_unique<
            battle::ProtocolRawLane>(),
        .kcp = std::make_unique<
            battle::ProtocolKcpLane>(
            conversation),
        .server_address = address,
        .server_address_family = address_family,
        .server_port = port,
        .endpoint_generation =
            parameters->endpoint_generation,
        .key_epoch = parameters->key_epoch,
        .client_slot = client_slot,
    };
}

/// SerializeMessage 返回 protobuf exact bytes，不允许空或超出 raw/KCP ceiling。
template <typename Message>
[[nodiscard]] std::vector<std::uint8_t>
SerializeMessage(const Message& message) {
    const auto encoded = message.SerializeAsString();
    if (encoded.empty() ||
        encoded.size() >
            battle::ProtocolRawLane::MaximumPayloadBytes) {
        throw std::runtime_error(
            "protocol client protobuf payload is invalid");
    }
    return {
        reinterpret_cast<const std::uint8_t*>(
            encoded.data()),
        reinterpret_cast<const std::uint8_t*>(
            encoded.data()) +
            encoded.size(),
    };
}

/// SendExactDatagram 把一个已冻结 datagram 完整写入 connected UDP socket。
void SendExactDatagram(
    const SOCKET socket,
    const std::span<const std::uint8_t> datagram) {
    if (datagram.empty() ||
        datagram.size() >
            battle::ProtocolSecureChannel::
                MaximumDatagramBytes ||
        send(
            socket,
            reinterpret_cast<const char*>(
                datagram.data()),
            static_cast<int>(datagram.size()),
            0) != static_cast<int>(datagram.size())) {
        throw std::runtime_error(
            "protocol client secure UDP send failed");
    }
}

/// DeliverDatagram 执行闭合真实 socket mutation，并返回实际 send 次数。
[[nodiscard]] std::uint8_t DeliverDatagram(
    const SOCKET socket,
    const std::span<const std::uint8_t> datagram,
    const std::uint8_t delivery) {
    if (delivery == DeliveryUnmodified) {
        SendExactDatagram(socket, datagram);
        return 1;
    }
    if (delivery == DeliveryExactReplay) {
        SendExactDatagram(socket, datagram);
        SendExactDatagram(socket, datagram);
        return 2;
    }
    if (delivery == DeliveryTamperedTag) {
        auto tampered = std::vector<std::uint8_t>(
            datagram.begin(),
            datagram.end());
        tampered.back() ^= TamperBitMask;
        SendExactDatagram(socket, tampered);
        SendExactDatagram(socket, datagram);
        return 2;
    }
    if (delivery == DeliveryOversizePrefix) {
        std::array<
            std::uint8_t,
            battle::ProtocolSecureChannel::
                MaximumDatagramBytes + 1> oversize{};
        std::ranges::copy(
            datagram,
            oversize.begin());
        if (send(
                socket,
                reinterpret_cast<const char*>(
                    oversize.data()),
                static_cast<int>(oversize.size()),
                0) !=
            static_cast<int>(oversize.size())) {
            throw std::runtime_error(
                "protocol client oversize UDP send failed");
        }
        SendExactDatagram(socket, datagram);
        return 2;
    }
    if (delivery == DeliveryAadTampered) {
        auto tampered = std::vector<std::uint8_t>(
            datagram.begin(),
            datagram.end());
        tampered[5] =
            tampered[5] ==
                    static_cast<std::uint8_t>(
                        battle::PacketKind::Raw)
                ? static_cast<std::uint8_t>(
                      battle::PacketKind::Kcp)
                : static_cast<std::uint8_t>(
                      battle::PacketKind::Raw);
        SendExactDatagram(socket, tampered);
        SendExactDatagram(socket, datagram);
        return 2;
    }
    if (delivery == DeliveryCiphertextTampered) {
        auto tampered = std::vector<std::uint8_t>(
            datagram.begin(),
            datagram.end());
        tampered[
            battle::ProtocolSecureChannel::
                SecureHeaderBytes] ^=
            TamperBitMask;
        SendExactDatagram(socket, tampered);
        SendExactDatagram(socket, datagram);
        return 2;
    }
    if (delivery == DeliveryMalformedPrefix) {
        const std::array<std::uint8_t, 8>
            malformed{
                'I', 'H', 'B', 'T',
                1, 1, 0, 0,
            };
        SendExactDatagram(socket, malformed);
        SendExactDatagram(socket, datagram);
        return 2;
    }
    throw std::runtime_error(
        "protocol client delivery mutation is invalid");
}

/// SealAndDeliver 认证保护后执行闭合真实 socket mutation。
[[nodiscard]] std::uint8_t SealAndDeliver(
    SessionState& session,
    const battle::PacketKind kind,
    const std::span<const std::uint8_t> plaintext,
    const std::uint64_t now_unix_ms,
    const std::uint8_t delivery) {
    if (delivery == DeliveryFutureSequence ||
        delivery == DeliveryTooOldSequence ||
        delivery == DeliveryWrongDirection) {
        const auto mutation =
            delivery == DeliveryFutureSequence
                ? battle::SecurityDatagramMutation::
                      FutureSequence
            : delivery == DeliveryTooOldSequence
                ? battle::SecurityDatagramMutation::
                      TooOldSequence
                : battle::SecurityDatagramMutation::
                      WrongDirection;
        const auto datagrams =
            session.secure->
                SealSecurityNegative(
                    kind,
                    plaintext,
                    mutation,
                    now_unix_ms);
        if (datagrams.empty() ||
            datagrams.size() >
                std::numeric_limits<
                    std::uint8_t>::max()) {
            throw std::runtime_error(
                "protocol client security seal failed");
        }
        for (const auto& datagram : datagrams) {
            SendExactDatagram(
                session.socket->Get(),
                datagram);
        }
        return static_cast<std::uint8_t>(
            datagrams.size());
    }
    if (delivery == DeliveryWrongLane) {
        const auto sealed = session.secure->Seal(
            kind == battle::PacketKind::Kcp
                ? battle::PacketKind::Raw
                : battle::PacketKind::Kcp,
            plaintext,
            now_unix_ms);
        if (!sealed.has_value()) {
            throw std::runtime_error(
                "protocol client wrong lane seal failed");
        }
        SendExactDatagram(
            session.socket->Get(),
            *sealed);
        return 1;
    }
    const auto sealed = session.secure->Seal(
        kind,
        plaintext,
        now_unix_ms);
    if (!sealed.has_value()) {
        throw std::runtime_error(
            "protocol client secure seal failed");
    }
    if (delivery == DeliveryRebindHijack) {
        auto hijacker = ConnectSessionSocket(
            session.server_address,
            session.server_address_family,
            session.server_port);
        SendExactDatagram(
            hijacker->Get(),
            *sealed);
        SendExactDatagram(
            session.socket->Get(),
            *sealed);
        return 2;
    }
    return DeliverDatagram(
        session.socket->Get(),
        *sealed,
        delivery);
}

/// SealAndSend 经独立 client traffic owner 发送一个 lane plaintext。
void SealAndSend(
    SessionState& session,
    const battle::PacketKind kind,
    const std::span<const std::uint8_t> plaintext,
    const std::uint64_t now_unix_ms) {
    const auto sealed = session.secure->Seal(
        kind,
        plaintext,
        now_unix_ms);
    if (!sealed.has_value()) {
        throw std::runtime_error(
            "protocol client secure seal failed");
    }
    SendExactDatagram(
        session.socket->Get(),
        *sealed);
}

/// ReceiveDatagram 在 deadline 内读取一个 MTU-bounded connected UDP datagram。
[[nodiscard]] std::optional<std::vector<std::uint8_t>>
ReceiveDatagram(
    const SOCKET socket,
    const std::uint64_t deadline_unix_ms,
    const bool timeout_is_empty) {
    std::array<
        std::uint8_t,
        battle::ProtocolSecureChannel::
            MaximumDatagramBytes> datagram{};
    for (;;) {
        if (!ConfigureReceiveTimeout(
                socket,
                deadline_unix_ms,
                timeout_is_empty)) {
            return std::nullopt;
        }
        const auto received = recv(
            socket,
            reinterpret_cast<char*>(datagram.data()),
            static_cast<int>(datagram.size()),
            0);
        if (received == SOCKET_ERROR) {
            const auto error = WSAGetLastError();
            if (error == WSAETIMEDOUT ||
                error == WSAEWOULDBLOCK) {
                if (timeout_is_empty) {
                    return std::nullopt;
                }
                // SO_RCVTIMEO 只切分长 operation 的等待，不能覆盖
                // request envelope 已冻结的完整 deadline。
                continue;
            }
            throw std::runtime_error(
                "protocol client secure UDP receive failed");
        }
        if (received <= 0) {
            throw std::runtime_error(
                "protocol client secure UDP response is empty");
        }
        return std::vector<std::uint8_t>(
            datagram.begin(),
            datagram.begin() + received);
    }
}

/// ExecuteHandshakeAttack 消费独立 ticket 并执行 cookie/proof/replay/source 真实负例。
[[nodiscard]] std::array<
    std::uint8_t,
    HandshakeAttackReceiptBytes>
ExecuteHandshakeAttack(
    const std::span<std::uint8_t> payload,
    const std::uint64_t request_deadline_unix_ms) {
    if (payload.size() !=
            HandshakeAttackPayloadBytes ||
        payload[0] < HandshakeCookieLess ||
        payload[0] > HandshakeSpoofedSource ||
        payload[1] != 0 || payload[2] != 0 ||
        payload[3] != 0) {
        throw std::runtime_error(
            "protocol client handshake attack is invalid");
    }
    const auto mutation = payload[0];
    const auto start = payload.subspan(4);
    if (start[0] < MinimumClientSlot ||
        start[0] > MaximumClientSlot) {
        throw std::runtime_error(
            "protocol client handshake attack slot is invalid");
    }
    const auto address_family = start[1];
    const auto port = ReadUint16BE(
        start.data() + 2);
    std::array<std::uint8_t, 16> address{};
    std::ranges::copy_n(
        start.begin() + EndpointAddressOffset,
        address.size(),
        address.begin());
    battle::TicketCredential credential;
    std::ranges::copy_n(
        start.begin() + TicketIdOffset,
        credential.ticket_id.size(),
        credential.ticket_id.begin());
    std::ranges::copy_n(
        start.begin() + TicketSecretOffset,
        credential.ticket_secret.size(),
        credential.ticket_secret.begin());
    credential.expires_at_unix_ms =
        ReadUint64BE(
            start.data() +
            TicketExpiryOffset);
    SecureClear(payload.subspan(4));
    const auto started_unix_ms =
        ObservedUnixMilliseconds();
    const auto deadline_unix_ms =
        HandshakeDeadline(
            request_deadline_unix_ms,
            credential.expires_at_unix_ms,
            started_unix_ms);
    auto source = ConnectSessionSocket(
        address,
        address_family,
        port);
    auto handshake =
        std::make_unique<
            battle::ProtocolHandshake>(
            std::move(credential),
            started_unix_ms);
    const auto hello =
        handshake->ClientHello();
    std::array<
        std::uint8_t,
        battle::ProtocolHandshake::
            ClientAuthBytes> auth{};
    if (mutation == HandshakeCookieLess) {
        std::ranges::copy(
            HandshakeClientAuthMagic,
            auth.begin());
        auth[4] = HandshakeWireVersion;
        auth[5] = HandshakeClientAuthKind;
        std::ranges::copy(
            std::span(hello).subspan(8),
            auth.begin() + 8);
    } else {
        const auto retry = ExchangeDatagram(
            source->Get(),
            hello,
            battle::ProtocolHandshake::
                MatchesRetryEnvelope,
            deadline_unix_ms);
        const auto generated =
            handshake->AcceptRetry(
                retry,
                ObservedUnixMilliseconds());
        if (!generated.has_value()) {
            throw std::runtime_error(
                "protocol client attack retry rejected");
        }
        auth = *generated;
    }
    if (mutation == HandshakeProofForgery) {
        auth[HandshakeProofOffset] ^=
            TamperBitMask;
    }
    if (mutation == HandshakeTicketReplay) {
        const auto accept = ExchangeDatagram(
            source->Get(),
            auth,
            battle::ProtocolHandshake::
                MatchesServerAcceptEnvelope,
            deadline_unix_ms);
        auto parameters =
            handshake->AcceptServer(
                accept,
                ObservedUnixMilliseconds());
        if (!parameters.has_value()) {
            throw std::runtime_error(
                "protocol client replay setup rejected");
        }
        SecureClear(
            parameters->session_seed);
        SecureClear(
            parameters->
                binding_fingerprint);
    }
    SocketOwner* attack_source = source.get();
    std::unique_ptr<SocketOwner> spoofed;
    if (mutation == HandshakeSpoofedSource) {
        spoofed = ConnectSessionSocket(
            address,
            address_family,
            port);
        attack_source = spoofed.get();
    }
    SendExactDatagram(
        attack_source->Get(),
        auth);
    const auto observed = ObservedUnixMilliseconds();
    const auto response_deadline =
        std::min(
            deadline_unix_ms,
            observed +
                NegativeResponseWaitMilliseconds);
    const auto response = ReceiveDatagram(
        attack_source->Get(),
        response_deadline,
        true);
    SecureClear(auth);
    std::array<
        std::uint8_t,
        HandshakeAttackReceiptBytes> receipt{};
    receipt[0] = mutation;
    receipt[1] = 1;
    WriteUint32BE(receipt.data() + 4, 1);
    WriteUint32BE(
        receipt.data() + 8,
        response.has_value() ? 1U : 0U);
    return receipt;
}

/// FlushKcpOutput 按 client clock 推进 KCP，并把全部待发 segment 写入真实 socket。
[[nodiscard]] std::size_t FlushKcpOutput(
    SessionState& session,
    const std::uint64_t observed_unix_ms) {
    session.kcp->Update(observed_unix_ms);
    if (session.kcp->Closed()) {
        throw ApplicationPacketException(
            ApplicationPacketFailure::Kcp);
    }
    auto segments = session.kcp->TakeSegments();
    for (const auto& segment : segments) {
        SealAndSend(
            session,
            battle::PacketKind::Kcp,
            segment,
            observed_unix_ms);
    }
    return segments.size();
}

/// ObserveApplicationPacket 消费一个已认证 raw/KCP packet 并更新累计证据。
void ObserveApplicationPacket(
    SessionState& session,
    const battle::OpenPacket& opened,
    const std::uint64_t observed_unix_ms) {
    if (opened.kind == battle::PacketKind::Raw) {
        const auto decoded =
            session.raw->DecodeSnapshot(
                opened.plaintext);
        if (decoded.disposition ==
            battle::RawDecodeDisposition::Rejected) {
            throw ApplicationPacketException(
                ApplicationPacketFailure::Snapshot);
        }
        if (decoded.disposition ==
            battle::RawDecodeDisposition::BaselineGap) {
            ++session.baseline_gap_count;
        }
        if (decoded.disposition !=
                battle::RawDecodeDisposition::Accepted ||
            !decoded.message.has_value()) {
            return;
        }
        ++session.snapshot_count;
        session.latest_application_sequence =
            decoded.message->application_sequence;
        return;
    }
    if (opened.kind == battle::PacketKind::Kcp) {
        ++session.secure_kcp_datagrams;
        if (!session.kcp->Input(
                opened.plaintext,
                observed_unix_ms)) {
            throw ApplicationPacketException(
                ApplicationPacketFailure::Kcp);
        }
        static_cast<void>(
            FlushKcpOutput(
                session,
                observed_unix_ms));
        auto messages = session.kcp->TakeMessages();
        for (const auto& message : messages) {
            ++session.reliable_count;
            session.latest_application_sequence =
                message.application_sequence;
        }
        return;
    }
    throw ApplicationPacketException(
        ApplicationPacketFailure::UnexpectedLane);
}

/// ReceiveExpectedControl 在 deadline 内分流并记账交错到达的 application output。
[[nodiscard]] battle::OpenPacket ReceiveExpectedControl(
    SessionState& session,
    const std::uint64_t deadline_unix_ms,
    const std::uint8_t receive_failure = 0,
    const std::uint8_t authentication_failure = 0,
    const std::uint8_t application_failure = 0,
    const std::uint8_t interleave_failure = 0,
    const std::span<const std::uint8_t>
        retry_datagram = {}) {
    std::size_t interleaved_datagrams = 0;
    for (;;) {
        std::optional<std::vector<std::uint8_t>>
            datagram;
        try {
            auto receive_deadline_unix_ms =
                deadline_unix_ms;
            if (!retry_datagram.empty()) {
                const auto now_unix_ms =
                    ObservedUnixMilliseconds();
                constexpr std::uint64_t
                    RetryIntervalMilliseconds = 1'000;
                if (deadline_unix_ms <= now_unix_ms) {
                    throw std::runtime_error(
                        "protocol client control deadline expired");
                }
                receive_deadline_unix_ms =
                    std::min(
                        deadline_unix_ms,
                        now_unix_ms >
                                std::numeric_limits<
                                    std::uint64_t>::max() -
                                    RetryIntervalMilliseconds
                            ? deadline_unix_ms
                            : now_unix_ms +
                                  RetryIntervalMilliseconds);
            }
            datagram = ReceiveDatagram(
                session.socket->Get(),
                receive_deadline_unix_ms,
                !retry_datagram.empty());
            if (!datagram.has_value()) {
                if (ObservedUnixMilliseconds() >=
                    deadline_unix_ms) {
                    throw std::runtime_error(
                        "protocol client control deadline expired");
                }
                SendExactDatagram(
                    session.socket->Get(),
                    retry_datagram);
                continue;
            }
        } catch (...) {
            if (receive_failure != 0) {
                throw TransitionException(
                    receive_failure);
            }
            throw;
        }
        const auto observed_unix_ms =
            ObservedUnixMilliseconds();
        const auto opened = session.secure->Open(
            *datagram,
            observed_unix_ms);
        if (opened.disposition !=
            battle::OpenDisposition::Accepted) {
            if (authentication_failure != 0) {
                throw TransitionException(
                    authentication_failure);
            }
            throw std::runtime_error(
                "protocol client secure response rejected");
        }
        if (opened.kind ==
            battle::PacketKind::Control) {
            return opened;
        }
        if (interleaved_datagrams ==
            MaximumTransitionInterleavedDatagrams) {
            if (interleave_failure != 0) {
                throw TransitionException(
                    interleave_failure);
            }
            throw std::runtime_error(
                "protocol client transition interleave limit exceeded");
        }
        try {
            ObserveApplicationPacket(
                session,
                opened,
                observed_unix_ms);
        } catch (...) {
            if (application_failure != 0) {
                throw TransitionException(
                    application_failure);
            }
            throw;
        }
        ++interleaved_datagrams;
    }
}

/// EncodeControl 构造独立 client 使用的 closed IHBC plaintext。
[[nodiscard]] std::vector<std::uint8_t>
EncodeControl(
    const std::uint8_t kind,
    const std::span<const std::uint8_t> payload) {
    if (kind < ControlRebindRequest ||
        kind > ControlCloseAcknowledged ||
        payload.empty() ||
        payload.size() >
            std::numeric_limits<std::uint16_t>::max()) {
        throw std::runtime_error(
            "protocol client control payload is invalid");
    }
    std::vector<std::uint8_t> output(
        ControlEnvelopeBytes + payload.size());
    std::ranges::copy(ControlMagic, output.begin());
    output[4] = ControlVersion;
    output[5] = kind;
    WriteUint16BE(
        output.data() + 6,
        static_cast<std::uint16_t>(payload.size()));
    std::ranges::copy(
        payload,
        output.begin() + ControlEnvelopeBytes);
    return output;
}

/// ControlPayload 验证 expected kind、长度并返回 payload view。
[[nodiscard]] std::span<const std::uint8_t>
ControlPayload(
    const battle::OpenPacket& opened,
    const std::uint8_t expected_kind,
    const std::size_t expected_bytes) {
    const auto& plaintext = opened.plaintext;
    if (plaintext.size() !=
            ControlEnvelopeBytes +
                expected_bytes ||
        !std::ranges::equal(
            ControlMagic,
            std::span(plaintext).first<4>()) ||
        plaintext[4] != ControlVersion ||
        plaintext[5] != expected_kind ||
        ReadUint16BE(plaintext.data() + 6) !=
            expected_bytes) {
        throw std::runtime_error(
            "protocol client control response drifted");
    }
    return std::span(plaintext).subspan(
        ControlEnvelopeBytes);
}

/// AdvertisedEndpoint 返回 runner 提供的 server-facing NAT successor 与随机 challenge。
[[nodiscard]] std::array<
    std::uint8_t,
    RebindRequestBytes>
AdvertisedEndpoint(
    const std::span<const std::uint8_t> transition) {
    std::array<std::uint8_t, RebindRequestBytes> output{};
    std::ranges::copy(
        transition.subspan(8, 16),
        output.begin());
    WriteUint16BE(
        output.data() + 16,
        ReadUint16BE(transition.data() + 4));
    ihomeland::sim::CryptoProvider crypto;
    crypto.RandomFill(
        std::span(output).subspan(18, 16));
    return output;
}

/// ExecuteWorkload 编码并发送 typed raw/KCP workload，返回低敏 send event。
[[nodiscard]] std::array<
    std::uint8_t,
    WorkloadEventPayloadBytes>
ExecuteWorkload(
    SessionState& session,
    const std::span<const std::uint8_t> payload,
    const std::uint64_t request_deadline_unix_ms) {
    if (payload.size() !=
            WorkloadCommandPayloadBytes ||
        payload[0] != session.client_slot ||
        payload[1] < WorkloadInputBundle ||
        payload[1] > WorkloadResyncRequest ||
        payload[3] == 0 || payload[3] > 32 ||
        ReadUint64BE(payload.data() + 4) == 0 ||
        ReadUint64BE(payload.data() + 12) == 0 ||
        payload[28] > DeliveryMalformedPrefix ||
        payload[29] != 0 ||
        payload[30] != 0 ||
        payload[31] != 0 ||
        ObservedUnixMilliseconds() >=
            request_deadline_unix_ms) {
        throw std::runtime_error(
            "protocol client workload command is invalid");
    }
    const auto operation = payload[1];
    const auto command_kind = payload[2];
    const auto repeat_count = payload[3];
    const auto application_sequence =
        ReadUint64BE(payload.data() + 4);
    const auto application_tick =
        ReadUint64BE(payload.data() + 12);
    const auto value_a = static_cast<std::int32_t>(
        ReadUint32BE(payload.data() + 20));
    const auto value_b = static_cast<std::int32_t>(
        ReadUint32BE(payload.data() + 24));
    const auto delivery = payload[28];
    if (operation == WorkloadResyncRequest &&
        delivery != DeliveryUnmodified &&
        delivery != DeliveryKcpExpired) {
        throw std::runtime_error(
            "protocol client KCP delivery mutation is invalid");
    }
    if (operation != WorkloadResyncRequest &&
        delivery == DeliveryKcpExpired) {
        throw std::runtime_error(
            "protocol client KCP expiry mutation route is invalid");
    }
    std::size_t datagrams_sent = 0;
    for (std::uint8_t index = 0;
         index < repeat_count;
         ++index) {
        if (application_sequence >
            std::numeric_limits<std::uint64_t>::max() -
                index) {
            throw std::runtime_error(
                "protocol client workload sequence overflow");
        }
        const auto sequence =
            application_sequence + index;
        const auto now_unix_ms =
            ObservedUnixMilliseconds();
        if (now_unix_ms >=
            request_deadline_unix_ms) {
            throw std::runtime_error(
                "protocol client workload deadline expired");
        }
        if (operation == WorkloadInputBundle) {
            if (command_kind == 0 ||
                command_kind > 6) {
                throw std::runtime_error(
                    "protocol client input kind is invalid");
            }
            ihomeland::battle::v1::
                BattleInputBundle bundle;
            bundle.set_newest_input_tick(
                application_tick);
            bundle.set_latest_observed_server_tick(
                session.raw->LatestServerTick());
            auto* command = bundle.add_commands();
            command->set_command_sequence(sequence);
            command->set_kind(
                static_cast<
                    ihomeland::battle::v1::
                        BattleInputKind>(
                    command_kind));
            if (command_kind == 1) {
                command->set_move_x_milli(value_a);
                command->set_move_y_milli(value_b);
            } else if (command_kind == 2) {
                command->set_aim_yaw_millidegrees(
                    value_a);
                command->set_aim_pitch_millidegrees(
                    value_b);
            } else if (command_kind == 6) {
                if (value_b != 0 || value_a < 0) {
                    throw std::runtime_error(
                        "protocol client interact value is invalid");
                }
                command->set_interaction_slot(
                    static_cast<std::uint32_t>(
                        value_a));
            } else if (value_a != 0 ||
                       value_b != 0) {
                throw std::runtime_error(
                    "protocol client input value is invalid");
            }
            const auto message =
                SerializeMessage(bundle);
            const auto plaintext =
                session.raw->EncodeInputBundle(
                    sequence,
                    message);
            datagrams_sent += SealAndDeliver(
                session,
                battle::PacketKind::Raw,
                plaintext,
                now_unix_ms,
                delivery);
        } else if (operation == WorkloadProbe) {
            if (command_kind != 0 ||
                value_a != 0 || value_b != 0) {
                throw std::runtime_error(
                    "protocol client probe value is invalid");
            }
            ihomeland::battle::v1::BattleProbe probe;
            probe.set_probe_sequence(sequence);
            probe.set_latest_snapshot_sequence(
                application_tick);
            probe.set_client_monotonic_time_us(
                now_unix_ms * 1'000);
            const auto message =
                SerializeMessage(probe);
            const auto plaintext =
                session.raw->EncodeProbe(
                    sequence,
                    message);
            datagrams_sent += SealAndDeliver(
                session,
                battle::PacketKind::Raw,
                plaintext,
                now_unix_ms,
                delivery);
        } else {
            if (command_kind != 0 ||
                value_a < 1 || value_a > 3 ||
                value_b != 0 ||
                !session.kcp->QueueResync(
                    sequence,
                    SerializeMessage(
                        [&] {
                            ihomeland::battle::v1::
                                BattleResyncRequest request;
                            request.set_request_sequence(
                                sequence);
                            request.set_latest_server_tick(
                                application_tick);
                            request.set_missing_baseline_id(
                                session.raw->BaselineID());
                            request.set_latest_snapshot_sequence(
                                session.raw->
                                    LatestSnapshotSequence());
                            request.set_reason(
                                static_cast<
                                    ihomeland::battle::v1::
                                        BattleResyncReason>(
                                    value_a));
                            return request;
                        }()),
                    now_unix_ms)) {
                throw std::runtime_error(
                    "protocol client resync queue failed");
            }
            const auto segments_sent =
                FlushKcpOutput(
                    session,
                    now_unix_ms);
            if (delivery == DeliveryKcpExpired) {
                std::this_thread::sleep_for(
                    std::chrono::milliseconds(
                        battle::ProtocolKcpLane::
                            ResyncExpiryMilliseconds +
                        1));
            }
            datagrams_sent += segments_sent;
        }
    }
    if ((datagrams_sent == 0 &&
         operation != WorkloadResyncRequest) ||
        datagrams_sent >
            std::numeric_limits<std::uint8_t>::max()) {
        throw std::runtime_error(
            "protocol client workload produced no bounded datagram");
    }
    std::array<
        std::uint8_t,
        WorkloadEventPayloadBytes> event{};
    event[0] = session.client_slot;
    event[1] = datagrams_sent == 0
        ? WorkloadReliableQueued
        : WorkloadDatagramSent;
    event[2] = operation;
    event[3] =
        static_cast<std::uint8_t>(
            datagrams_sent);
    WriteUint64BE(
        event.data() + 12,
        application_sequence);
    WriteUint64BE(
        event.data() + 20,
        application_tick);
    return event;
}

/// ExecuteTransition 完成 rebind/rekey/close 的真实 control round trip。
[[nodiscard]] std::array<
    std::uint8_t,
    NetworkTransitionEventPayloadBytes>
ExecuteTransition(
    SessionState& session,
    const std::span<const std::uint8_t> payload,
    const std::uint64_t request_deadline_unix_ms) {
    if (payload.size() !=
            NetworkTransitionPayloadBytes ||
        payload[0] != session.client_slot ||
        payload[1] < NetworkRebind ||
        payload[1] > NetworkOldEpochProbe ||
        payload[3] != 0 ||
        ReadUint16BE(payload.data() + 6) != 0) {
        throw TransitionException(
            TransitionFailureInvalidRequest);
    }
    const auto operation = payload[1];
    const auto endpoint_bytes =
        payload.subspan(8, 16);
    const auto endpoint_nonzero =
        std::ranges::any_of(
            endpoint_bytes,
            [](const auto value) {
                return value != 0;
            });
    if ((operation == NetworkRebind &&
         (payload[2] != AddressFamilyIPv4 &&
          payload[2] != AddressFamilyIPv6 ||
          ReadUint16BE(payload.data() + 4) == 0 ||
          !endpoint_nonzero)) ||
        (operation != NetworkRebind &&
         (payload[2] != 0 ||
         ReadUint16BE(payload.data() + 4) != 0 ||
          endpoint_nonzero))) {
        throw TransitionException(
            TransitionFailureInvalidRequest);
    }
    std::uint32_t generation = 0;
    if (operation == NetworkRebind) {
        if (session.endpoint_generation ==
            std::numeric_limits<std::uint32_t>::max()) {
            throw TransitionException(
                TransitionFailureInvalidRequest);
        }
        ExecuteTransitionStep(
            TransitionFailureRebindRequest,
            [&] {
                const auto request_payload =
                    AdvertisedEndpoint(payload);
                const auto request = EncodeControl(
                    ControlRebindRequest,
                    request_payload);
                SealAndSend(
                    session,
                    battle::PacketKind::Control,
                    request,
                    ObservedUnixMilliseconds());
            });
        const auto challenge =
            ExecuteTransitionStep(
                TransitionFailureRebindChallengeReceive,
                [&] {
                    return ReceiveExpectedControl(
                        session,
                        request_deadline_unix_ms,
                        TransitionFailureRebindChallengeReceive,
                        TransitionFailureRebindChallengeAuthentication,
                        TransitionFailureRebindChallengeApplication,
                        TransitionFailureRebindChallengeInterleave);
                });
        const auto challenge_payload =
            ExecuteTransitionStep(
                TransitionFailureRebindChallengeValidate,
                [&] {
                    return ControlPayload(
                        challenge,
                        ControlRebindChallenge,
                        RebindChallengeBytes);
                });
        if (ReadUint32BE(
                challenge_payload.data()) !=
                session.endpoint_generation ||
            ReadUint32BE(
                challenge_payload.data() + 4) !=
                session.endpoint_generation + 1) {
            throw TransitionException(
                TransitionFailureRebindChallengeValidate);
        }
        ExecuteTransitionStep(
            TransitionFailureRebindConfirm,
            [&] {
                const auto confirmation = EncodeControl(
                    ControlRebindConfirm,
                    challenge_payload);
                const auto confirmation_sealed =
                    session.secure->Seal(
                        battle::PacketKind::Control,
                        confirmation,
                        ObservedUnixMilliseconds());
                if (!confirmation_sealed.has_value()) {
                    throw TransitionException(
                        TransitionFailureRebindConfirm);
                }
                SendExactDatagram(
                    session.socket->Get(),
                    *confirmation_sealed);
            });
        const auto committed =
            ExecuteTransitionStep(
                TransitionFailureRebindCommitReceive,
                [&] {
                    return ReceiveExpectedControl(
                        session,
                        request_deadline_unix_ms,
                        TransitionFailureRebindCommitReceive,
                        TransitionFailureRebindCommitAuthentication,
                        TransitionFailureRebindCommitApplication,
                        TransitionFailureRebindCommitInterleave);
                });
        const auto committed_payload =
            ExecuteTransitionStep(
                TransitionFailureRebindCommitValidate,
                [&] {
                    return ControlPayload(
                        committed,
                        ControlRebindCommitted,
                        RebindCommittedBytes);
                });
        generation = ReadUint32BE(
            committed_payload.data());
        if (generation !=
                session.endpoint_generation + 1 ||
            !std::ranges::equal(
                committed_payload.subspan(4),
                challenge_payload.subspan(8, 16)) ||
            !session.secure->
                CommitEndpointGeneration(
                    session.endpoint_generation,
                    generation)) {
            throw TransitionException(
                TransitionFailureRebindCommitValidate);
        }
        session.endpoint_generation = generation;
    } else if (
        operation == NetworkRekey ||
        operation == NetworkOldEpochProbe) {
        if (session.key_epoch ==
            std::numeric_limits<std::uint32_t>::max()) {
            throw TransitionException(
                TransitionFailureRekeyPrepare);
        }
        std::optional<std::vector<std::uint8_t>>
            predecessor;
        if (operation == NetworkOldEpochProbe) {
            predecessor = ExecuteTransitionStep(
                TransitionFailureRekeyPrepare,
                [&] {
                    const std::array<
                        std::uint8_t,
                        CloseBytes> held_payload{
                            ClientRequestedClose};
                    return session.secure->Seal(
                        battle::PacketKind::Control,
                        EncodeControl(
                            ControlCloseRequest,
                            held_payload),
                        ObservedUnixMilliseconds());
                });
            if (!predecessor.has_value()) {
                throw TransitionException(
                    TransitionFailureRekeyPrepare);
            }
        }
        std::array<std::uint8_t, RekeyBytes> nonce{};
        ExecuteTransitionStep(
            TransitionFailureRekeyProposal,
            [&] {
                ihomeland::sim::CryptoProvider crypto;
                crypto.RandomFill(nonce);
                const auto proposal = EncodeControl(
                    ControlRekeyProposal,
                    nonce);
                SealAndSend(
                    session,
                    battle::PacketKind::Control,
                    proposal,
                    ObservedUnixMilliseconds());
            });
        const auto committed =
            ExecuteTransitionStep(
                TransitionFailureRekeyCommitReceive,
                [&] {
                    return ReceiveExpectedControl(
                        session,
                        request_deadline_unix_ms,
                        TransitionFailureRekeyCommitReceive,
                        TransitionFailureRekeyCommitAuthentication,
                        TransitionFailureRekeyCommitApplication,
                        TransitionFailureRekeyCommitInterleave);
                });
        const auto committed_payload =
            ExecuteTransitionStep(
                TransitionFailureRekeyCommitValidate,
                [&] {
                    return ControlPayload(
                        committed,
                        ControlRekeyCommitted,
                        RekeyBytes);
                });
        const auto next_epoch =
            session.key_epoch + 1;
        try {
            ExecuteTransitionStep(
                TransitionFailureRekeyCommitValidate,
                [&] {
                    if (!std::ranges::equal(
                            committed_payload,
                            nonce) ||
                        !session.secure->CommitRollover(
                            nonce,
                            next_epoch,
                            ObservedUnixMilliseconds())) {
                        throw std::runtime_error(
                            "protocol client rekey commit failed");
                    }
                });
        } catch (...) {
            SecureClear(nonce);
            throw;
        }
        SecureClear(nonce);
        session.key_epoch = next_epoch;
        generation = next_epoch;
        if (operation == NetworkOldEpochProbe) {
            ExecuteTransitionStep(
                TransitionFailureOldEpochSend,
                [&] {
                    std::this_thread::sleep_for(
                        std::chrono::milliseconds(
                            battle::ProtocolSecureChannel::
                                PreviousEpochOverlapMilliseconds +
                            1));
                    SendExactDatagram(
                        session.socket->Get(),
                        *predecessor);
                });
        }
    } else {
        const std::array<std::uint8_t, CloseBytes>
            close_payload{ClientRequestedClose};
        const auto sealed_close_request =
            ExecuteTransitionStep(
            TransitionFailureCloseRequest,
            [&] {
                const auto request = EncodeControl(
                    ControlCloseRequest,
                    close_payload);
                const auto sealed =
                    session.secure->Seal(
                    battle::PacketKind::Control,
                    request,
                    ObservedUnixMilliseconds());
                if (!sealed.has_value()) {
                    throw std::runtime_error(
                        "protocol client close seal failed");
                }
                SendExactDatagram(
                    session.socket->Get(),
                    *sealed);
                return sealed;
            });
        const auto acknowledged =
            ExecuteTransitionStep(
                TransitionFailureCloseAckReceive,
                [&] {
                    return ReceiveExpectedControl(
                        session,
                        request_deadline_unix_ms,
                        TransitionFailureCloseAckReceive,
                        TransitionFailureCloseAckAuthentication,
                        TransitionFailureCloseAckApplication,
                        TransitionFailureCloseAckInterleave,
                        *sealed_close_request);
                });
        const auto acknowledged_payload =
            ExecuteTransitionStep(
                TransitionFailureCloseAckValidate,
                [&] {
                    return ControlPayload(
                        acknowledged,
                        ControlCloseAcknowledged,
                        CloseBytes);
                });
        if (acknowledged_payload.front() !=
            ClientRequestedClose) {
            throw TransitionException(
                TransitionFailureCloseAckValidate);
        }
        ExecuteTransitionStep(
            TransitionFailureCloseAckValidate,
            [&] {
                session.secure->Close();
                session.socket->Close();
            });
    }
    std::array<
        std::uint8_t,
        NetworkTransitionEventPayloadBytes> event{};
    event[0] = session.client_slot;
    event[1] = operation;
    event[2] = 1;
    WriteUint32BE(
        event.data() + 4,
        generation);
    return event;
}

/// PopulatePollReceiptState 写入成功与失败共同使用的累计 application/KCP 状态。
void PopulatePollReceiptState(
    std::array<
        std::uint8_t,
        PollReceiptPayloadBytes>& receipt,
    const SessionState& session) {
    receipt[0] = session.client_slot;
    const auto kcp = session.kcp->Status();
    receipt[3] =
        static_cast<std::uint8_t>(
            kcp.close_reason);
    WriteUint64BE(
        receipt.data() + 8,
        session.snapshot_count);
    WriteUint64BE(
        receipt.data() + 16,
        session.reliable_count);
    WriteUint64BE(
        receipt.data() + 24,
        session.raw->LatestServerTick());
    WriteUint64BE(
        receipt.data() + 32,
        session.latest_application_sequence);
    WriteUint64BE(
        receipt.data() + 40,
        session.raw->LatestSnapshotSequence());
    WriteUint64BE(
        receipt.data() + 48,
        session.raw->BaselineID());
    WriteUint64BE(
        receipt.data() + 56,
        session.raw->LastProcessedInputTick());
    WriteUint64BE(
        receipt.data() + 64,
        session.baseline_gap_count);
    WriteUint64BE(
        receipt.data() + 72,
        session.secure_kcp_datagrams);
    WriteUint64BE(
        receipt.data() + 80,
        kcp.input_datagrams);
    WriteUint64BE(
        receipt.data() + 88,
        kcp.input_ack_commands);
    WriteUint64BE(
        receipt.data() + 96,
        kcp.input_push_commands);
    WriteUint64BE(
        receipt.data() + 104,
        kcp.output_datagrams);
    WriteUint64BE(
        receipt.data() + 112,
        kcp.reconciled_messages);
    WriteUint64BE(
        receipt.data() + 120,
        kcp.queued_messages);
    WriteUint64BE(
        receipt.data() + 128,
        kcp.inflight_messages);
    WriteUint64BE(
        receipt.data() + 136,
        kcp.waiting_segments);
}

/// PollKcpFailureCode 把 client KCP 终态收敛为低敏 poll 分类。
[[nodiscard]] std::uint8_t PollKcpFailureCode(
    const SessionState& session) noexcept {
    switch (session.kcp->CloseReason()) {
    case battle::KcpCloseReason::InflightExpiry:
        return PollFailureKcpInflightExpiry;
    default:
        return PollFailureKcp;
    }
}

/// PollSession 收取真实 raw/KCP output 并推进 client KCP acknowledgements。
[[nodiscard]] std::array<
    std::uint8_t,
    PollReceiptPayloadBytes>
PollSession(
    SessionState& session,
    const std::span<const std::uint8_t> payload,
    const std::uint64_t request_deadline_unix_ms) {
    if (payload.size() != PollPayloadBytes ||
        payload[0] != session.client_slot ||
        payload[1] == 0 || payload[1] > 32 ||
        ReadUint16BE(payload.data() + 2) != 0) {
        throw PollException(
            PollFailureInvalidRequest);
    }
    const auto wait_milliseconds =
        ReadUint32BE(payload.data() + 4);
    if (wait_milliseconds == 0 ||
        wait_milliseconds > 1'000) {
        throw PollException(
            PollFailureInvalidRequest);
    }
    const auto now_unix_ms =
        ObservedUnixMilliseconds();
    if (now_unix_ms >
        std::numeric_limits<std::uint64_t>::max() -
            wait_milliseconds) {
        throw PollException(
            PollFailureInvalidRequest);
    }
    const auto receive_deadline =
        std::min(
            request_deadline_unix_ms,
            now_unix_ms + wait_milliseconds);
    try {
        static_cast<void>(
            FlushKcpOutput(
                session,
                now_unix_ms));
    } catch (
        const ApplicationPacketException&) {
        throw PollException(
            PollKcpFailureCode(session));
    } catch (...) {
        throw PollException(
            PollFailureTransport);
    }
    std::uint8_t event_count = 0;
    for (; event_count < payload[1];
         ++event_count) {
        const auto datagram = ReceiveDatagram(
            session.socket->Get(),
            receive_deadline,
            true);
        if (!datagram.has_value()) {
            break;
        }
        const auto observed =
            ObservedUnixMilliseconds();
        const auto opened = session.secure->Open(
            *datagram,
            observed);
        if (battle::IsPollReplaySuppressed(
                opened.disposition)) {
            continue;
        }
        if (opened.disposition !=
            battle::OpenDisposition::Accepted) {
            throw PollException(
                PollFailureAuthentication);
        }
        try {
            ObserveApplicationPacket(
                session,
                opened,
                observed);
        } catch (
            const ApplicationPacketException& failure) {
            switch (failure.Failure()) {
            case ApplicationPacketFailure::Snapshot:
                throw PollException(
                    PollFailureSnapshot);
            case ApplicationPacketFailure::Kcp:
                throw PollException(
                    PollKcpFailureCode(session));
            case ApplicationPacketFailure::UnexpectedLane:
                throw PollException(
                    PollFailureUnexpectedLane);
            }
            throw PollException(
                PollFailureTransport);
        }
        if (ObservedUnixMilliseconds() >=
            receive_deadline) {
            ++event_count;
            break;
        }
    }
    std::array<
        std::uint8_t,
        PollReceiptPayloadBytes> receipt{};
    receipt[1] = event_count;
    PopulatePollReceiptState(receipt, session);
    return receipt;
}

/// HexNibble 解码编译期 lowercase build identity。
[[nodiscard]] std::uint8_t HexNibble(
    const char value) {
    if (value >= '0' && value <= '9') {
        return static_cast<std::uint8_t>(value - '0');
    }
    if (value >= 'a' && value <= 'f') {
        return static_cast<std::uint8_t>(
            value - 'a' + 10);
    }
    throw std::runtime_error(
        "qualification client build identity is invalid");
}

/// BuildIdentityBytesValue 返回不含本机路径的 exact source/build identity。
[[nodiscard]] std::array<
    std::uint8_t,
    BuildIdentityBytes>
BuildIdentityBytesValue() {
    constexpr std::string_view identity =
        IHOMELAND_PROTOCOL_CLIENT_BUILD_IDENTITY;
    static_assert(
        identity.size() == BuildIdentityBytes * 2);
    std::array<std::uint8_t, BuildIdentityBytes> output{};
    for (std::size_t index = 0;
         index < output.size();
         ++index) {
        output[index] = static_cast<std::uint8_t>(
            (HexNibble(identity[index * 2]) << 4U) |
            HexNibble(identity[index * 2 + 1]));
    }
    return output;
}

/// ReadExact 从 binary stdin 读取 exact bytes。
[[nodiscard]] bool ReadExact(
    const std::span<std::uint8_t> output,
    const bool allow_clean_eof) {
    std::size_t offset = 0;
    while (offset < output.size()) {
        std::cin.read(
            reinterpret_cast<char*>(
                output.data() + offset),
            static_cast<std::streamsize>(
                output.size() - offset));
        const auto received =
            static_cast<std::size_t>(
                std::cin.gcount());
        if (received == 0) {
            return allow_clean_eof &&
                offset == 0 &&
                std::cin.eof();
        }
        offset += received;
    }
    return true;
}

/// ReadFrame 读取 4-byte length 与 closed binary body。
[[nodiscard]] std::vector<std::uint8_t>
ReadFrame(bool& clean_eof) {
    std::array<std::uint8_t, PrefixBytes> prefix{};
    clean_eof = false;
    if (!ReadExact(prefix, true)) {
        throw std::runtime_error(
            "qualification client stdin prefix is truncated");
    }
    if (std::cin.eof()) {
        clean_eof = true;
        return {};
    }
    const auto length = ReadUint32BE(prefix.data());
    if (length < HeaderBytes ||
        length > MaximumFrameBytes) {
        throw std::runtime_error(
            "qualification client stdin frame exceeds contract");
    }
    std::vector<std::uint8_t> frame(length);
    if (!ReadExact(frame, false)) {
        throw std::runtime_error(
            "qualification client stdin body is truncated");
    }
    return frame;
}

/// WriteFrame 输出 low-sensitive closed binary receipt。
void WriteFrame(
    const std::uint8_t kind,
    const std::uint64_t sequence,
    const std::span<const std::uint8_t> payload) {
    if (payload.size() >
            MaximumFrameBytes - HeaderBytes ||
        payload.size() >
            std::numeric_limits<std::uint32_t>::max()) {
        throw std::runtime_error(
            "qualification client stdout payload exceeds contract");
    }
    std::vector<std::uint8_t> frame(
        HeaderBytes + payload.size());
    std::ranges::copy(
        FrameMagic,
        frame.begin());
    frame[4] = ContractVersion;
    frame[5] = kind;
    WriteUint64BE(frame.data() + 8, sequence);
    WriteUint64BE(frame.data() + 16, 0);
    WriteUint32BE(
        frame.data() + 24,
        static_cast<std::uint32_t>(payload.size()));
    std::ranges::copy(
        payload,
        frame.begin() + HeaderBytes);
    std::array<std::uint8_t, PrefixBytes> prefix{};
    WriteUint32BE(
        prefix.data(),
        static_cast<std::uint32_t>(frame.size()));
    std::cout.write(
        reinterpret_cast<const char*>(prefix.data()),
        static_cast<std::streamsize>(prefix.size()));
    std::cout.write(
        reinterpret_cast<const char*>(frame.data()),
        static_cast<std::streamsize>(frame.size()));
    std::cout.flush();
    if (!std::cout.good()) {
        throw std::runtime_error(
            "qualification client stdout write failed");
    }
}

/// ValidEnvelope 执行 magic/version/flags/sequence/deadline/length closed 校验。
[[nodiscard]] bool ValidEnvelope(
    const std::span<const std::uint8_t> frame,
    const std::uint64_t expected_sequence,
    std::uint8_t& kind,
    std::uint64_t& deadline_unix_ms) {
    if (frame.size() < HeaderBytes ||
        !std::ranges::equal(
            FrameMagic,
            frame.first(FrameMagic.size())) ||
        frame[4] != ContractVersion ||
        frame[6] != 0 ||
        frame[7] != 0 ||
        ReadUint64BE(frame.data() + 8) !=
            expected_sequence ||
        ReadUint64BE(frame.data() + 16) <=
            ObservedUnixMilliseconds() ||
        ReadUint32BE(frame.data() + 24) !=
            frame.size() - HeaderBytes) {
        return false;
    }
    kind = frame[5];
    deadline_unix_ms =
        ReadUint64BE(
            frame.data() + 16);
    return true;
}

/// DescribePayload 编码 build identity、contract version 与 frame ceiling。
[[nodiscard]] std::array<
    std::uint8_t,
    DescribePayloadBytes>
DescribePayload() {
    std::array<std::uint8_t, DescribePayloadBytes> output{};
    const auto identity = BuildIdentityBytesValue();
    std::ranges::copy(identity, output.begin());
    output[BuildIdentityBytes] = ContractVersion;
    WriteUint32BE(
        output.data() + BuildIdentityBytes + 1,
        static_cast<std::uint32_t>(
            MaximumFrameBytes));
    return output;
}

/// RunStdio 执行严格递增、fail-closed 的 child control session。
[[nodiscard]] int RunStdio() {
    std::uint64_t expected_sequence = 1;
    std::unique_ptr<WinSockOwner>
        socket_runtime;
    std::unique_ptr<SessionState> session;
    while (true) {
        bool clean_eof = false;
        auto frame = ReadFrame(clean_eof);
        if (clean_eof) {
            return 0;
        }
        std::uint8_t kind = 0;
        std::uint64_t deadline_unix_ms = 0;
        if (!ValidEnvelope(
                frame,
                expected_sequence,
                kind,
                deadline_unix_ms)) {
            return 2;
        }
        auto payload =
            std::span(frame).subspan(HeaderBytes);
        if (kind == DescribeRequest &&
            payload.empty()) {
            const auto description =
                DescribePayload();
            WriteFrame(
                DescribeReceipt,
                expected_sequence,
                description);
        } else if (
            kind == SessionStartRequest &&
            session == nullptr) {
            if (payload.size() !=
                SessionStartPayloadBytes) {
                SecureClear(payload);
                throw std::runtime_error(
                    "protocol client session start payload invalid");
            }
            const std::uint8_t requested_slot =
                payload[0] >= MinimumClientSlot &&
                        payload[0] <= MaximumClientSlot
                    ? payload[0]
                    : std::uint8_t{0};
            auto failure_code =
                SessionStartFailureUnknown;
            try {
                socket_runtime =
                    std::make_unique<
                        WinSockOwner>();
                session =
                    std::make_unique<
                        SessionState>(
                        StartSession(
                            payload,
                            deadline_unix_ms));
            } catch (
                const SessionStartException&
                    failure) {
                failure_code = failure.Code();
            } catch (...) {
            }
            SecureClear(payload);
            if (session != nullptr) {
                const std::array<std::uint8_t, 2>
                    event{
                    session->client_slot,
                    SessionEstablishedEvent,
                };
                WriteFrame(
                    SessionEvent,
                    expected_sequence,
                    event);
            } else {
                socket_runtime.reset();
                const std::array<std::uint8_t, 3>
                    event{
                        requested_slot,
                        SessionFailedEvent,
                        failure_code,
                    };
                WriteFrame(
                    SessionEvent,
                    expected_sequence,
                    event);
            }
        } else if (
            kind == HandshakeAttackRequest &&
            session == nullptr) {
            try {
                socket_runtime =
                    std::make_unique<
                        WinSockOwner>();
                const auto receipt =
                    ExecuteHandshakeAttack(
                        payload,
                        deadline_unix_ms);
                SecureClear(payload);
                WriteFrame(
                    HandshakeAttackReceipt,
                    expected_sequence,
                    receipt);
            } catch (...) {
                SecureClear(payload);
                throw;
            }
        } else if (
            kind == WorkloadCommandRequest &&
            session != nullptr) {
            const auto event = ExecuteWorkload(
                *session,
                payload,
                deadline_unix_ms);
            WriteFrame(
                WorkloadEvent,
                expected_sequence,
                event);
        } else if (
            kind == NetworkTransitionRequest &&
            session != nullptr) {
            const auto operation =
                static_cast<std::uint8_t>(
                payload.size() ==
                        NetworkTransitionPayloadBytes
                    ? payload[1]
                    : 0);
            std::array<
                std::uint8_t,
                NetworkTransitionEventPayloadBytes> event{};
            try {
                event = ExecuteTransition(
                    *session,
                    payload,
                    deadline_unix_ms);
            } catch (
                const TransitionException& failure) {
                event[0] = session->client_slot;
                event[1] = operation;
                event[3] = failure.Code();
            } catch (
                const ApplicationPacketException&) {
                event[0] = session->client_slot;
                event[1] = operation;
                event[3] = operation == NetworkRebind
                    ? TransitionFailureRebindApplicationPacket
                    : UnhandledTransitionFailure(operation);
            } catch (const std::runtime_error&) {
                event[0] = session->client_slot;
                event[1] = operation;
                event[3] = operation == NetworkRebind
                    ? TransitionFailureRebindRuntime
                    : UnhandledTransitionFailure(operation);
            } catch (const std::exception&) {
                event[0] = session->client_slot;
                event[1] = operation;
                event[3] = operation == NetworkRebind
                    ? TransitionFailureRebindStandard
                    : UnhandledTransitionFailure(operation);
            } catch (...) {
                event[0] = session->client_slot;
                event[1] = operation;
                event[3] = UnhandledTransitionFailure(
                    operation);
            }
            WriteFrame(
                NetworkTransitionEvent,
                expected_sequence,
                event);
            if (operation == NetworkClose) {
                session.reset();
                socket_runtime.reset();
            }
        } else if (
            kind == PollRequest &&
            session != nullptr) {
            std::array<
                std::uint8_t,
                PollReceiptPayloadBytes> receipt{};
            try {
                receipt = PollSession(
                    *session,
                    payload,
                    deadline_unix_ms);
            } catch (
                const PollException& failure) {
                receipt[2] = failure.Code();
                PopulatePollReceiptState(
                    receipt,
                    *session);
            } catch (...) {
                receipt[2] =
                    PollFailureTransport;
                PopulatePollReceiptState(
                    receipt,
                    *session);
            }
            WriteFrame(
                PollReceipt,
                expected_sequence,
                receipt);
        } else if (
            kind == ShutdownRequest &&
            payload.empty()) {
            if (session != nullptr) {
                session->secure->Close();
                session->socket->Close();
                session.reset();
                socket_runtime.reset();
            }
            WriteFrame(
                ShutdownReceipt,
                expected_sequence,
                {});
            return 0;
        } else {
            return 2;
        }
        if (expected_sequence ==
            std::numeric_limits<std::uint64_t>::max()) {
            return 2;
        }
        ++expected_sequence;
    }
}

}  // namespace

int main(const int argc, const char* const* argv) {
    if (argc != 2 ||
        std::string_view(argv[1]) != "--stdio" ||
        _setmode(_fileno(stdin), _O_BINARY) == -1 ||
        _setmode(_fileno(stdout), _O_BINARY) == -1) {
        return 2;
    }
    try {
        return RunStdio();
    } catch (...) {
        return 2;
    }
}
