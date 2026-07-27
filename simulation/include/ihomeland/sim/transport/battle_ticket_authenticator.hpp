#pragma once

#include <array>
#include <cstdint>
#include <functional>
#include <span>
#include <string>

namespace ihomeland::sim {

/// BattleTicketBinding 是 Go authority facts 在 exact child 的不可变投影。
struct BattleTicketBinding final {
    /// player_id 来自认证 Session，不接受 UDP payload 覆盖。
    std::string player_id;
    /// session_id 绑定账号 session lineage。
    std::string session_id;
    /// session_epoch 是撤销屏障。
    std::uint64_t session_epoch;
    /// role 只允许 owner 或 visitor。
    std::string role;
    /// personal_world_id 绑定持久世界。
    std::string personal_world_id;
    /// visit_session_id 仅 Visitor 非空。
    std::string visit_session_id;
    /// world_instance_id 绑定 placement runtime。
    std::string world_instance_id;
    /// runtime_node_id 必须匹配本 node registration。
    std::string runtime_node_id;
    /// assignment_generation 是 current placement generation。
    std::uint64_t assignment_generation;
    /// fencing_token 绑定完整 AssignmentStamp。
    std::uint64_t fencing_token;
    /// assignment_fingerprint 必须匹配运行 instance。
    std::string assignment_fingerprint;
    /// simulation_node_id 必须匹配本 child incarnation。
    std::string simulation_node_id;
    /// simulation_instance_id 绑定不可复活 worker。
    std::string simulation_instance_id;
    /// mapping_generation 绑定 InputTick timeline。
    std::uint64_t mapping_generation;
    /// target_revision 绑定 Go current target revision。
    std::uint64_t target_revision;
    /// model_identity 绑定冻结 model。
    std::string model_identity;
    /// profile_identity 绑定冻结 network profile。
    std::string profile_identity;
    /// config_identity 绑定 checked runtime config。
    std::string config_identity;
    /// wire_identity 绑定 battle wire corpus。
    std::string wire_identity;
    /// actor_slot 是 0..7 的 exact instance slot。
    std::uint8_t actor_slot;
    /// advertised_host 是 ticket 唯一允许的 UDP host。
    std::string advertised_host;
    /// advertised_port 是 ticket 唯一允许的 UDP port。
    std::uint16_t advertised_port;
    /// issue_id 绑定 HTTP response-loss identity。
    std::string issue_id;
    /// issued_at_unix_ms 是首次冻结签发时刻。
    std::uint64_t issued_at_unix_ms;
    /// expires_at_unix_ms 是等于即失效的绝对 deadline。
    std::uint64_t expires_at_unix_ms;
};

/// BattleTicketAuthenticator 是 handshake 唯一允许调用的 installed-ticket port。
///
/// 实现必须在同一原子边界验证 proof、current target 与 expiry，并至多一次把 ticket
/// 从 Installed 推进为 Consumed；authenticator 不得保存 proof span。
class BattleTicketAuthenticator {
public:
    /// Authenticator 在 registry lock 内同步验证 proof 并构造不可变 session binding。
    using Authenticator = std::function<bool(
        std::span<const std::uint8_t, 32>,
        const BattleTicketBinding&,
        const std::string&)>;

    /// 析构函数允许通过窄 port 安全销毁 concrete owner。
    virtual ~BattleTicketAuthenticator() = default;

    /// AuthenticateAndConsumeBattleTicket 原子认证并消费 exact installed ticket。
    virtual void AuthenticateAndConsumeBattleTicket(
        const std::string& ticket_id,
        const std::string& expected_simulation_node_id,
        const std::string& expected_advertised_host,
        std::uint16_t expected_advertised_port,
        std::uint64_t observed_unix_ms,
        const Authenticator& authenticator) = 0;
};

}  // namespace ihomeland::sim
