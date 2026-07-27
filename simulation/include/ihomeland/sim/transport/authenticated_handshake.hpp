#pragma once

#include "ihomeland/sim/transport/battle_ticket_authenticator.hpp"
#include "ihomeland/sim/transport/crypto_provider.hpp"
#include "ihomeland/sim/transport/handshake_cookie.hpp"

#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <mutex>
#include <span>
#include <string>
#include <vector>

namespace ihomeland::sim {

/// BattleAuthenticatedHandshake 原子完成 ClientAuth、ticket consume 与 AEAD ServerAccept。
class BattleAuthenticatedHandshake final {
public:
    /// ClientAuthBytes 是 repeated hello、cookie 与 transcript proof 的固定 wire 长度。
    static constexpr std::size_t ClientAuthBytes = 140;
    /// ServerAcceptBytes 是 clear key-share、encrypted parameters 与 AEAD tag 的固定长度。
    static constexpr std::size_t ServerAcceptBytes = 156;
    /// ReplayLimit 是 node 内未到期 exact accept replay 的 hard cap。
    static constexpr std::size_t ReplayLimit = 256;

    /// Outcome 是不泄漏 ticket 存在性的 closed handshake disposition。
    enum class Outcome : std::uint8_t {
        /// Dropped 表示无响应拒绝。
        Dropped = 0,
        /// Accepted 表示首次原子消费并建立 session。
        Accepted = 1,
        /// Replayed 表示 exact endpoint/transcript 重放同一 accept。
        Replayed = 2,
    };

    /// SessionBootstrap 是首次 accepted handshake 向唯一 session owner 的一次性转移。
    class SessionBootstrap final {
    public:
        /// SessionSeedConsumer 必须在回调内同步构造唯一 secure channel，不得保存引用。
        using SessionSeedConsumer = std::function<void(
            const CryptoProvider::Key32&)>;

        /// 构造函数锁页复制 seed，并冻结完整 authority/session/endpoint binding。
        SessionBootstrap(
            const CryptoProvider::Key32& session_seed,
            CryptoProvider::Key32 binding_fingerprint,
            std::array<std::uint8_t, 16> session_id,
            std::uint32_t battle_session_generation,
            std::uint32_t key_epoch,
            std::uint32_t endpoint_generation,
            std::uint8_t actor_slot,
            std::uint8_t role,
            BattleTicketBinding binding,
            BattleRemoteEndpoint remote);

        /// 析构函数清零尚未消费的 session seed。
        ~SessionBootstrap();

        SessionBootstrap(const SessionBootstrap&) = delete;
        SessionBootstrap& operator=(const SessionBootstrap&) = delete;

        /// 移动构造函数转移唯一 locked owner。
        SessionBootstrap(SessionBootstrap&& source) noexcept;

        /// 移动赋值先清理当前 owner，再转移唯一 locked owner。
        SessionBootstrap& operator=(
            SessionBootstrap&& source) noexcept;

        /// ConsumeSessionSeed 恰好调用一次 consumer，随后无条件清零 seed。
        void ConsumeSessionSeed(
            const SessionSeedConsumer& consumer);

        /// BindingFingerprint 返回认证 ServerAccept 使用的完整 binding digest。
        [[nodiscard]] const CryptoProvider::Key32&
        BindingFingerprint() const noexcept;

        /// SessionId 返回 ServerAccept 的 16-byte runtime routing identity。
        [[nodiscard]] const std::array<std::uint8_t, 16>&
        SessionId() const noexcept;

        /// BattleSessionGeneration 返回 process-local session incarnation。
        [[nodiscard]] std::uint32_t
        BattleSessionGeneration() const noexcept;

        /// KeyEpoch 返回 secure channel 的初始 key epoch。
        [[nodiscard]] std::uint32_t
        KeyEpoch() const noexcept;

        /// EndpointGeneration 返回 secure AAD 的初始 endpoint generation。
        [[nodiscard]] std::uint32_t
        EndpointGeneration() const noexcept;

        /// ActorSlot 返回 install 冻结的 0..7 actor slot。
        [[nodiscard]] std::uint8_t
        ActorSlot() const noexcept;

        /// Role 返回 owner=1、visitor=2 的 closed projection。
        [[nodiscard]] std::uint8_t
        Role() const noexcept;

        /// Binding 返回 Go 安装的完整 immutable authority facts。
        [[nodiscard]] const BattleTicketBinding&
        Binding() const noexcept;

        /// Remote 返回首次完成地址验证并消费 ticket 的 canonical endpoint。
        [[nodiscard]] const BattleRemoteEndpoint&
        Remote() const noexcept;

    private:
        struct Impl;
        /// impl_ 是 move-only locked secret 与 immutable metadata owner。
        std::unique_ptr<Impl> impl_;
    };

    /// Result 保存公开响应和低敏 session routing projection。
    struct Result final {
        /// outcome 区分首次成功、exact replay 与静默拒绝。
        Outcome outcome{Outcome::Dropped};
        /// response 保存完整或未使用的 ServerAccept bytes。
        std::array<std::uint8_t, ServerAcceptBytes> response{};
        /// response_bytes 只能是零或 ServerAcceptBytes。
        std::size_t response_bytes{};
        /// simulation_instance_id 来自已安装 authority binding。
        std::string simulation_instance_id;
        /// actor_slot 来自 Go 安装时冻结的 actor binding。
        std::uint8_t actor_slot{};
        /// battle_session_generation 在当前 process 内严格递增且不复用。
        std::uint32_t battle_session_generation{};
        /// bootstrap 只在首次 Accepted 存在；exact replay 永远为空。
        std::unique_ptr<SessionBootstrap> bootstrap;
    };

    /// 构造函数绑定 exact node、advertised endpoint、cookie gate 与 ticket registry。
    BattleAuthenticatedHandshake(
        CryptoProvider& crypto,
        BattleHandshakeCookieGate& cookie_gate,
        BattleTicketAuthenticator& ticket_authenticator,
        std::string simulation_node_id,
        std::string advertised_host,
        std::uint16_t advertised_port);

    /// 析构函数释放未到期 exact accept replay metadata。
    ~BattleAuthenticatedHandshake();

    BattleAuthenticatedHandshake(
        const BattleAuthenticatedHandshake&) = delete;
    BattleAuthenticatedHandshake& operator=(
        const BattleAuthenticatedHandshake&) = delete;

    /// HandleClientAuth 验证 cookie/proof 后一次消费 ticket并生成或重放accept。
    [[nodiscard]] Result HandleClientAuth(
        std::span<const std::uint8_t> request,
        const BattleRemoteEndpoint& remote,
        std::uint64_t now_unix_ms);

private:
    struct ParsedClientAuth;
    struct ReplayEntry;

    /// ParseClientAuth 执行 fixed-width closed decode，不查询 ticket。
    [[nodiscard]] static std::unique_ptr<ParsedClientAuth>
    ParseClientAuth(std::span<const std::uint8_t> request);

    /// crypto_ 是唯一密码 primitive adapter。
    CryptoProvider& crypto_;
    /// cookie_gate_ 必须在任何 registry lookup 前运行。
    BattleHandshakeCookieGate& cookie_gate_;
    /// ticket_authenticator_ 拥有 installed ticket 与一次消费状态。
    BattleTicketAuthenticator& ticket_authenticator_;
    /// simulation_node_id_ 必须与 install binding 和 node config 一致。
    std::string simulation_node_id_;
    /// advertised_host_ 是 ticket 必须绑定的服务端 UDP host。
    std::string advertised_host_;
    /// advertised_port_ 是 ticket 必须绑定的服务端 UDP port。
    std::uint16_t advertised_port_;
    /// mutex_ 串行化 consume、generation 与 exact replay decision。
    std::mutex mutex_;
    /// replays_ 是预留容量的有界 accept/session seed owner。
    std::vector<std::unique_ptr<ReplayEntry>> replays_;
    /// next_generation_ 是 process-local 单调 BattleSession generation。
    std::uint32_t next_generation_{1};
};

}  // namespace ihomeland::sim
