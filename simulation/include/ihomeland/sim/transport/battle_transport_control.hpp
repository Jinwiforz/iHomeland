#pragma once

#include "ihomeland/sim/transport/endpoint_rebind.hpp"
#include "ihomeland/sim/transport/udp_listener.hpp"

#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <span>

namespace ihomeland::sim {

class BattleRuntimeMetrics;

/// BattleTransportControlKind 是 authenticated control envelope 的闭合消息类型。
enum class BattleTransportControlKind : std::uint8_t {
    /// RebindRequest 从 current endpoint 声明唯一 candidate 与 nonce。
    RebindRequest = 1,
    /// RebindChallenge 由 server 发往 candidate，携带短期 address proof。
    RebindChallenge = 2,
    /// RebindConfirm 从 candidate 回显 exact challenge。
    RebindConfirm = 3,
    /// RebindCommitted 在切换前确认 next endpoint generation。
    RebindCommitted = 4,
    /// RekeyProposal 请求用非零 nonce 原子推进 key epoch。
    RekeyProposal = 5,
    /// RekeyCommitted 在切换前确认同一 rollover nonce。
    RekeyCommitted = 6,
    /// CloseRequest 请求终结当前 authenticated session。
    CloseRequest = 7,
    /// CloseAcknowledged 在终结前确认 close reason。
    CloseAcknowledged = 8,
};

/// BattleTransportCloseCode 是 control close payload 的低敏稳定原因。
enum class BattleTransportCloseCode : std::uint8_t {
    /// ClientRequested 表示 authenticated client 主动离开 battle transport。
    ClientRequested = 1,
};

/// BattleTransportControlDisposition 是单 session control owner 的闭合结果。
enum class BattleTransportControlDisposition : std::uint8_t {
    /// RebindChallengeQueued 表示 challenge 已排入唯一 listener。
    RebindChallengeQueued = 1,
    /// RebindCommitted 表示 endpoint 与 generation 已原子切换。
    RebindCommitted = 2,
    /// RekeyCommitted 表示 acknowledgement 已排队且 epoch 已推进。
    RekeyCommitted = 3,
    /// CloseRequested 表示 acknowledgement 已排入 listener 且必须终结 session。
    CloseRequested = 4,
    /// Rejected 表示认证后的 envelope、state 或 endpoint 不合法。
    Rejected = 5,
    /// Fatal 表示 output 或 crypto commit 失败，session 必须 fail closed。
    Fatal = 6,
};

/// BattleTransportControlResult 保存 control dispatch 与 terminal 决策。
struct BattleTransportControlResult final {
    /// disposition 是不含 endpoint、nonce 或 secret 的稳定结果。
    BattleTransportControlDisposition disposition;
    /// close_session 表示 runtime 必须在本次 dispatch 后销毁 session。
    bool close_session;
};

/// BattleTransportControl 组合单 session rebind、rekey、close 与同 socket output。
class BattleTransportControl final {
public:
    /// EnvelopeBytes 是所有 control payload 的固定公共 header。
    static constexpr std::size_t EnvelopeBytes = 8;
    /// RebindRequestPayloadBytes 冻结 candidate、port 与 nonce 的总长度。
    static constexpr std::size_t
        RebindRequestPayloadBytes = 34;
    /// RebindChallengePayloadBytes 冻结 generation、nonce、deadline 与 cookie。
    static constexpr std::size_t
        RebindChallengePayloadBytes = 48;
    /// RebindCommittedPayloadBytes 冻结 next generation 与 request nonce。
    static constexpr std::size_t
        RebindCommittedPayloadBytes = 20;
    /// RekeyPayloadBytes 是 rollover nonce 的固定长度。
    static constexpr std::size_t RekeyPayloadBytes = 32;
    /// ClosePayloadBytes 是稳定 close code 的固定长度。
    static constexpr std::size_t ClosePayloadBytes = 1;
    /// CloseAcknowledgementCopies 为 terminal UDP response 提供有界冗余。
    static constexpr std::size_t
        CloseAcknowledgementCopies = 3;

    /// DatagramSender 只能把 secure control 排入同一个 BattleUdpListener。
    using DatagramSender = std::function<BattleUdpSendDisposition(
        std::span<const std::uint8_t>,
        const BattleRemoteEndpoint&)>;

    /// 构造函数创建单 session endpoint owner 并借用唯一 secure channel。
    BattleTransportControl(
        CryptoProvider& crypto,
        BattleSecureChannel& channel,
        BattleRemoteEndpoint initial_endpoint,
        std::uint32_t initial_endpoint_generation,
        DatagramSender sender,
        BattleRuntimeMetrics* runtime_metrics = nullptr);

    /// 析构函数先释放 rebind cookie key，再释放无 secret 的状态。
    ~BattleTransportControl();

    BattleTransportControl(
        const BattleTransportControl&) = delete;
    BattleTransportControl& operator=(
        const BattleTransportControl&) = delete;

    /// Handle 只接收已由 current/previous traffic key 认证的 control plaintext。
    [[nodiscard]] BattleTransportControlResult Handle(
        std::span<const std::uint8_t> plaintext,
        const BattleRemoteEndpoint& remote,
        std::uint64_t now_unix_ms);

    /// ActiveEndpoint 返回 rebind owner 当前唯一 send/receive remote。
    [[nodiscard]] BattleRemoteEndpoint
    ActiveEndpoint() const;

private:
    struct Impl;
    /// impl_ 保存 channel、rebinder、sender 与 pending transition。
    std::unique_ptr<Impl> impl_;
};

}  // namespace ihomeland::sim
