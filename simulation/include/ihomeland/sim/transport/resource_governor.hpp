#pragma once

#include "ihomeland/sim/transport/handshake_cookie.hpp"

#include <array>
#include <cstddef>
#include <cstdint>
#include <memory>

namespace ihomeland::sim {

class BattleRuntimeMetrics;
class BattleSessionResourceGovernor;

/// BattleResourceRejection 是低基数资源拒绝与终结原因。
enum class BattleResourceRejection : std::uint8_t {
    /// None 表示本次资源申请成功。
    None = 0,
    /// IpRate 表示pre-auth IP token bucket耗尽。
    IpRate = 1,
    /// TicketRate 表示ticket lookup/auth bucket耗尽。
    TicketRate = 2,
    /// SessionRate 表示session aggregate bucket耗尽。
    SessionRate = 3,
    /// MessageRate 表示numeric route bucket耗尽。
    MessageRate = 4,
    /// InstanceRate 表示instance aggregate bucket耗尽。
    InstanceRate = 5,
    /// IngressBudget 表示session或node ingress 256-item hard budget耗尽。
    IngressBudget = 6,
    /// EgressBudget 表示session或node egress 256-item hard budget耗尽。
    EgressBudget = 7,
    /// RegistryCapacity 表示固定bucket registry已满，禁止动态扩容。
    RegistryCapacity = 8,
};

/// BattleResourceDecision 返回是否允许以及稳定低基数原因。
struct BattleResourceDecision final {
    /// allowed 表示调用方可以继续下一层处理。
    bool allowed;
    /// rejection 在allowed=false时解释唯一资源gate。
    BattleResourceRejection rejection;
};

/// BattleResourceMetrics 是不含IP、ticket、session或payload的累计计数。
struct BattleResourceMetrics final {
    /// accepted 是成功token/budget申请次数。
    std::uint64_t accepted;
    /// rejected 按BattleResourceRejection数值索引累计。
    std::array<std::uint64_t, 9> rejected;
    /// ingress_high_watermark 是node ingress预算峰值。
    std::size_t ingress_high_watermark;
    /// egress_high_watermark 是node egress预算峰值。
    std::size_t egress_high_watermark;
};

/// BattleResourceGovernor 使用固定内存治理pre-auth与authenticated资源。
///
/// 该owner不创建BattleSession、KCP或payload queue；pre-auth路径最多占用固定256个
/// IP bucket。ticket/session/message/instance使用调用方提供的低敏非零handle。
class BattleResourceGovernor final {
public:
    /// RegistryItems 是每一类bucket的固定hard cap。
    static constexpr std::size_t RegistryItems = 256;
    /// NodeItems 是node ingress与egress各自的固定hard budget。
    static constexpr std::size_t NodeItems = 256;
    /// SessionItems 是单session ingress与egress各自的固定hard budget。
    static constexpr std::size_t SessionItems = 256;

    /// 构造函数初始化固定bucket与低敏metrics，不分配session状态。
    explicit BattleResourceGovernor(
        BattleRuntimeMetrics* runtime_metrics = nullptr);

    /// 析构函数释放固定资源表。
    ~BattleResourceGovernor();

    BattleResourceGovernor(
        const BattleResourceGovernor&) = delete;
    BattleResourceGovernor& operator=(
        const BattleResourceGovernor&) = delete;

    /// AllowPreAuthIp 对canonical remote IP执行20/s、burst 40 gate。
    [[nodiscard]] BattleResourceDecision AllowPreAuthIp(
        const BattleRemoteEndpoint& remote,
        std::uint64_t now_unix_ms);

    /// AllowTicket 对ticket lookup handle执行4/s、burst 8 gate。
    [[nodiscard]] BattleResourceDecision AllowTicket(
        std::uint64_t ticket_handle,
        std::uint64_t now_unix_ms);

    /// AllowMessage 同时执行session、message与instance token bucket。
    [[nodiscard]] BattleResourceDecision AllowMessage(
        std::uint64_t session_handle,
        std::uint64_t instance_handle,
        std::uint32_t message_id,
        std::uint16_t message_rate_per_second,
        std::uint64_t now_unix_ms);

    /// ReserveIngress 原子申请session与node ingress items。
    [[nodiscard]] BattleResourceDecision ReserveIngress(
        std::uint64_t session_handle,
        std::size_t items);

    /// ReleaseIngress 释放已成功申请的session与node ingress items。
    void ReleaseIngress(
        std::uint64_t session_handle,
        std::size_t items) noexcept;

    /// ReserveEgress 原子申请session与node egress items。
    [[nodiscard]] BattleResourceDecision ReserveEgress(
        std::uint64_t session_handle,
        std::size_t items);

    /// ReleaseEgress 释放已成功申请的session与node egress items。
    void ReleaseEgress(
        std::uint64_t session_handle,
        std::size_t items) noexcept;

    /// Metrics 返回不含identity的低敏快照。
    [[nodiscard]] BattleResourceMetrics Metrics() const noexcept;

private:
    friend class BattleSessionResourceGovernor;

    /// ReleaseSession 清除terminal session bucket并归还未释放的node budget。
    void ReleaseSession(
        std::uint64_t session_handle) noexcept;

    struct Impl;
    /// impl_ 保存固定array、计数与mutex。
    std::unique_ptr<Impl> impl_;
};

/// BattleSessionResourceGovernor 是单session拥有的node governor有界视图。
///
/// 该owner冻结session/instance低敏handle；析构时回收session/message registry与任何
/// 遗留budget，同时保留node级IP、ticket与instance抗滥用历史。
class BattleSessionResourceGovernor final {
public:
    /// 构造函数绑定唯一session与instance handle。
    BattleSessionResourceGovernor(
        BattleResourceGovernor& node_governor,
        std::uint64_t session_handle,
        std::uint64_t instance_handle);

    /// 析构函数终结该session全部bucket与未释放budget。
    ~BattleSessionResourceGovernor();

    BattleSessionResourceGovernor(
        const BattleSessionResourceGovernor&) = delete;
    BattleSessionResourceGovernor& operator=(
        const BattleSessionResourceGovernor&) = delete;

    /// AllowMessage 执行session/message/instance三级rate gate。
    [[nodiscard]] BattleResourceDecision AllowMessage(
        std::uint32_t message_id,
        std::uint16_t message_rate_per_second,
        std::uint64_t now_unix_ms);

    /// ReserveIngress 申请当前session与node ingress budget。
    [[nodiscard]] BattleResourceDecision ReserveIngress(
        std::size_t items);

    /// ReleaseIngress 归还当前session与node ingress budget。
    void ReleaseIngress(
        std::size_t items) noexcept;

    /// ReserveEgress 申请当前session与node egress budget。
    [[nodiscard]] BattleResourceDecision ReserveEgress(
        std::size_t items);

    /// ReleaseEgress 归还当前session与node egress budget。
    void ReleaseEgress(
        std::size_t items) noexcept;

private:
    /// node_governor_ 是node-global hard budget与registry owner。
    BattleResourceGovernor* node_governor_;
    /// session_handle_ 是构造时冻结的低敏session identity。
    std::uint64_t session_handle_;
    /// instance_handle_ 是构造时冻结的低敏instance identity。
    std::uint64_t instance_handle_;
};

}  // namespace ihomeland::sim
