#pragma once

#include "ihomeland/sim/transport/handshake_cookie.hpp"

#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <span>
#include <string>

namespace ihomeland::sim {

/// BattleUdpEndpointConfig 是显式配置的 numeric UDP endpoint。
struct BattleUdpEndpointConfig final {
    /// host 必须是 numeric IPv4 或 IPv6，不执行隐式 DNS/Host 推导。
    std::string host;
    /// port 在 production 必须非零；loopback test 可显式使用零。
    std::uint16_t port;
};

/// BattleUdpListenerMode 区分 production 约束与唯一允许的 loopback ephemeral 测试。
enum class BattleUdpListenerMode : std::uint8_t {
    /// Production 拒绝 port 0、wildcard advertised host 与自动换端口。
    Production = 1,
    /// LoopbackTest 只允许 bind/advertised 使用 127.0.0.1:0。
    LoopbackTest = 2,
};

/// BattleUdpListenerConfig 绑定 node-global socket、ticket endpoint 与 cookie identity。
struct BattleUdpListenerConfig final {
    /// bind_endpoint 是本机实际 bind identity。
    BattleUdpEndpointConfig bind_endpoint;
    /// advertised_endpoint 是 ticket 下发且握手必须匹配的 endpoint。
    BattleUdpEndpointConfig advertised_endpoint;
    /// listener_identity 防止 cookie 跨 node/listener 复用，必须非零。
    std::array<std::uint8_t, 16> listener_identity;
    /// mode 决定 production 与 loopback test 的端口规则。
    BattleUdpListenerMode mode{BattleUdpListenerMode::Production};
};

/// BattleUdpListenerCounters 是不包含 endpoint、ticket 或 payload 的低敏累计计数。
struct BattleUdpListenerCounters final {
    /// received_datagrams 统计 socket 已完成的 datagram。
    std::uint64_t received_datagrams;
    /// accepted_datagrams 统计通过固定头 fast reject 并交给 multiplexer 的 datagram。
    std::uint64_t accepted_datagrams;
    /// malformed_datagrams 统计短包、未知 magic/version/kind 或 reserved 非零。
    std::uint64_t malformed_datagrams;
    /// oversized_datagrams 统计超过 1200-byte ceiling 的 datagram。
    std::uint64_t oversized_datagrams;
    /// receive_failures 统计非 shutdown 的 socket receive failure。
    std::uint64_t receive_failures;
    /// handler_failures 统计 multiplexer callback 抛出的异常。
    std::uint64_t handler_failures;
};

/// BattleUdpListenerStatus 暴露 readiness 与 bind/advertised identity。
struct BattleUdpListenerStatus final {
    /// running 表示 socket 已 bind 且 receive loop 正常。
    bool running;
    /// failed 表示 receive loop 或 callback 已 fail closed。
    bool failed;
    /// bound_endpoint 是系统确认的实际本地 endpoint。
    BattleUdpEndpointConfig bound_endpoint;
    /// advertised_endpoint 是 ticket 唯一可下发的 endpoint。
    BattleUdpEndpointConfig advertised_endpoint;
    /// counters 是低敏累计计数快照。
    BattleUdpListenerCounters counters;
};

/// BattleUdpListener 拥有一个 SimulationNode 的唯一 Asio UDP socket。
///
/// handler 在 listener worker 上同步调用，datagram span 只在该调用期间有效；
/// handler 不得保存 span，后续 authenticated multiplexer 必须保持有界处理。
class BattleUdpListener final {
public:
    /// MaximumDatagramBytes 固定 receive buffer 与 battle MTU ceiling。
    static constexpr std::size_t MaximumDatagramBytes = 1200;

    /// DatagramHandler 接收已通过廉价 fixed-header 分类的 datagram。
    using DatagramHandler = std::function<void(
        std::span<const std::uint8_t>,
        const BattleRemoteEndpoint&)>;

    /// 构造函数验证完整 bind/advertised/listener identity，但尚不 bind。
    BattleUdpListener(
        BattleUdpListenerConfig config,
        DatagramHandler handler);

    /// 析构函数停止 receive loop 并释放 socket。
    ~BattleUdpListener();

    BattleUdpListener(const BattleUdpListener&) = delete;
    BattleUdpListener& operator=(const BattleUdpListener&) = delete;
    BattleUdpListener(BattleUdpListener&&) = delete;
    BattleUdpListener& operator=(BattleUdpListener&&) = delete;

    /// Start 精确 bind 配置地址；冲突时失败且绝不自动改端口。
    void Start();

    /// Stop 幂等关闭 socket 并等待 worker 退出。
    void Stop() noexcept;

    /// Status 返回不含 remote endpoint 与 payload 的线程安全快照。
    [[nodiscard]] BattleUdpListenerStatus Status() const;

private:
    struct Impl;
    /// impl_ 隐藏 Asio 类型，防止第三方网络类型扩散到公开契约。
    std::unique_ptr<Impl> impl_;
};

}  // namespace ihomeland::sim
