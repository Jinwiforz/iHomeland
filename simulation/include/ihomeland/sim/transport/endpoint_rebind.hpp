#pragma once

#include "ihomeland/sim/transport/handshake_cookie.hpp"
#include "ihomeland/sim/transport/secure_datagram.hpp"

#include <array>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>

namespace ihomeland::sim {

class BattleRuntimeMetrics;

/// BattleRebindChallenge 是绑定candidate endpoint与generation的短期cookie。
struct BattleRebindChallenge final {
    /// candidate 是唯一允许confirm的新remote。
    BattleRemoteEndpoint candidate;
    /// current_generation 必须匹配发起时active generation。
    std::uint32_t current_generation;
    /// next_generation 必须精确为current+1。
    std::uint32_t next_generation;
    /// nonce 是authenticated request提供的非零唯一值。
    std::array<std::uint8_t, 16> nonce;
    /// expires_at_unix_ms 等于即失效。
    std::uint64_t expires_at_unix_ms;
    /// cookie 是process-local HMAC-SHA-256截断值。
    std::array<std::uint8_t, 16> cookie;
};

/// BattleRebindDisposition 是rebind request/confirm闭合结果。
enum class BattleRebindDisposition : std::uint8_t {
    /// ChallengeIssued 表示candidate已获得有界pending challenge。
    ChallengeIssued = 1,
    /// Committed 表示active endpoint与generation已原子切换。
    Committed = 2,
    /// AuthenticationRequired 表示control packet未由current traffic key认证。
    AuthenticationRequired = 3,
    /// EndpointMismatch 表示request不来自current或confirm不来自candidate。
    EndpointMismatch = 4,
    /// Concurrent 表示已有pending rebind，禁止双active竞争。
    Concurrent = 5,
    /// RateLimited 表示每session每秒2次request额度耗尽。
    RateLimited = 6,
    /// InvalidChallenge 表示cookie、nonce、generation或candidate不匹配。
    InvalidChallenge = 7,
    /// Expired 表示3秒challenge deadline已到。
    Expired = 8,
    /// GenerationExhausted 表示endpoint generation不能再推进。
    GenerationExhausted = 9,
    /// OutputUnavailable 表示commit acknowledgement未能排入唯一listener。
    OutputUnavailable = 10,
};

/// BattleRebindResult 保存稳定decision与可选challenge。
struct BattleRebindResult final {
    /// disposition 是闭合处理结果。
    BattleRebindDisposition disposition;
    /// challenge 仅ChallengeIssued时存在。
    std::optional<BattleRebindChallenge> challenge;
};

/// BattleEndpointRebinder 管理单session唯一active endpoint与stateless-address proof。
///
/// 该owner借用既有BattleSecureChannel；成功rebind只更新channel AAD generation，不重建
/// key、packet sequence、replay window或KCP。
class BattleEndpointRebinder final {
public:
    /// CommitBarrier 在endpoint generation切换前排队旧generation acknowledgement。
    using CommitBarrier = std::function<bool(
        const BattleRebindChallenge&)>;

    /// ChallengeLifetimeMilliseconds 固定candidate confirm deadline。
    static constexpr std::uint64_t
        ChallengeLifetimeMilliseconds = 3'000;

    /// 构造函数生成process-local cookie key并冻结initial endpoint/generation。
    BattleEndpointRebinder(
        CryptoProvider& crypto,
        BattleSecureChannel& channel,
        BattleRemoteEndpoint initial_endpoint,
        std::uint32_t initial_generation,
        BattleRuntimeMetrics* runtime_metrics = nullptr);

    /// fixture构造函数接受公开测试key。
    BattleEndpointRebinder(
        CryptoProvider& crypto,
        BattleSecureChannel& channel,
        BattleRemoteEndpoint initial_endpoint,
        std::uint32_t initial_generation,
        const CryptoProvider::Key32& fixture_key,
        BattleRuntimeMetrics* runtime_metrics = nullptr);

    /// 析构函数清零cookie key。
    ~BattleEndpointRebinder();

    BattleEndpointRebinder(
        const BattleEndpointRebinder&) = delete;
    BattleEndpointRebinder& operator=(
        const BattleEndpointRebinder&) = delete;

    /// Begin 验证current authenticated request并签发candidate challenge。
    [[nodiscard]] BattleRebindResult Begin(
        const BattleRemoteEndpoint& current_remote,
        const BattleRemoteEndpoint& candidate,
        const std::array<std::uint8_t, 16>& nonce,
        bool authenticated,
        std::uint64_t now_unix_ms);

    /// Confirm 从candidate验证exact challenge并原子推进active endpoint。
    [[nodiscard]] BattleRebindDisposition Confirm(
        const BattleRemoteEndpoint& candidate_remote,
        const BattleRebindChallenge& challenge,
        bool authenticated,
        std::uint64_t now_unix_ms,
        CommitBarrier before_commit = {});

    /// ActiveEndpoint 返回当前唯一remote快照。
    [[nodiscard]] BattleRemoteEndpoint
    ActiveEndpoint() const;

    /// EndpointGeneration 返回当前AAD generation。
    [[nodiscard]] std::uint32_t
    EndpointGeneration() const;

private:
    struct Impl;
    /// impl_ 保存secret、pending、rate与mutex。
    std::unique_ptr<Impl> impl_;
};

}  // namespace ihomeland::sim
