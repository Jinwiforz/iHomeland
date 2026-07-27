#include "ihomeland/sim/transport/udp_listener.hpp"
#include "ihomeland/sim/control/simulation_node.hpp"

#include <asio.hpp>

#include <array>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <mutex>
#include <ranges>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <thread>
#include <vector>

namespace {

using namespace std::chrono_literals;

/// Require 把真实 socket contract drift 转换为 test failure。
void Require(
    const bool condition,
    const std::string_view message) {
    if (!condition) {
        throw std::runtime_error(std::string(message));
    }
}

/// ListenerIdentity 返回非零、非秘密测试 identity。
[[nodiscard]] std::array<std::uint8_t, 16>
ListenerIdentity() {
    std::array<std::uint8_t, 16> identity{};
    for (std::size_t index = 0;
         index < identity.size();
         ++index) {
        identity[index] = static_cast<std::uint8_t>(
            index + 1);
    }
    return identity;
}

/// NodeConfig 返回 listener ownership 测试所需的固定 node identity。
[[nodiscard]] ihomeland::sim::SimulationNodeConfig
NodeConfig() {
    return {
        .simulation_node_id = "snode_udp_listener_test",
        .runtime_node_id = "rnode_udp_listener_test",
        .build_identity = std::string(64, 'a'),
        .model_manifest = std::string(64, 'b'),
        .profile_manifest = std::string(64, 'c'),
        .instance_capacity = 1,
        .actor_capacity = 8,
    };
}

/// LoopbackConfig 只使用 contract 允许的 127.0.0.1:0。
[[nodiscard]] ihomeland::sim::BattleUdpListenerConfig
LoopbackConfig() {
    return {
        .bind_endpoint = {
            .host = "127.0.0.1",
            .port = 0,
        },
        .advertised_endpoint = {
            .host = "127.0.0.1",
            .port = 0,
        },
        .listener_identity = ListenerIdentity(),
        .mode =
            ihomeland::sim::BattleUdpListenerMode::
                LoopbackTest,
    };
}

/// ClientHello 返回可通过 listener fast gate 的固定宽度 datagram。
[[nodiscard]] std::array<std::uint8_t, 88>
ClientHello() {
    std::array<std::uint8_t, 88> hello{};
    hello[0] = 'I';
    hello[1] = 'H';
    hello[2] = 'B';
    hello[3] = 'H';
    hello[4] = 1;
    hello[5] = 1;
    return hello;
}

/// SendDatagram 经真实 UDP socket 发送一个完整 datagram。
void SendDatagram(
    asio::ip::udp::socket& socket,
    const asio::ip::udp::endpoint& endpoint,
    const std::span<const std::uint8_t> datagram) {
    const auto sent = socket.send_to(
        asio::buffer(datagram),
        endpoint);
    Require(
        sent == datagram.size(),
        "loopback UDP send was partial");
}

/// CanonicalLoopbackRemote 返回 listener output 所需的 IPv4-mapped identity。
[[nodiscard]] ihomeland::sim::BattleRemoteEndpoint
CanonicalLoopbackRemote(const std::uint16_t port) {
    ihomeland::sim::BattleRemoteEndpoint remote{};
    remote.address[10] = 0xff;
    remote.address[11] = 0xff;
    remote.address[12] = 127;
    remote.address[15] = 1;
    remote.port = port;
    return remote;
}

/// TestValidation 验证 production 与 test endpoint 规则 fail closed。
void TestValidation() {
    auto production = LoopbackConfig();
    production.mode =
        ihomeland::sim::BattleUdpListenerMode::Production;
    bool production_zero_rejected = false;
    try {
        ihomeland::sim::BattleUdpListener listener(
            production,
            [](const auto, const auto&) {});
    } catch (const std::invalid_argument&) {
        production_zero_rejected = true;
    }
    Require(
        production_zero_rejected,
        "production accepted port zero");

    auto implicit_host = LoopbackConfig();
    implicit_host.advertised_endpoint.host = "localhost";
    bool implicit_host_rejected = false;
    try {
        ihomeland::sim::BattleUdpListener listener(
            implicit_host,
            [](const auto, const auto&) {});
    } catch (const std::invalid_argument&) {
        implicit_host_rejected = true;
    }
    Require(
        implicit_host_rejected,
        "listener accepted implicit advertised host");

    auto non_loopback_test = LoopbackConfig();
    non_loopback_test.bind_endpoint.host = "0.0.0.0";
    bool non_loopback_rejected = false;
    try {
        ihomeland::sim::BattleUdpListener listener(
            non_loopback_test,
            [](const auto, const auto&) {});
    } catch (const std::invalid_argument&) {
        non_loopback_rejected = true;
    }
    Require(
        non_loopback_rejected,
        "test listener accepted non-loopback bind");
}

/// TestLoopbackIngress 验证固定 buffer、fast reject 与 source canonicalization。
void TestLoopbackIngress() {
    std::mutex callback_mutex;
    std::condition_variable callback_ready;
    std::size_t callback_count = 0;
    ihomeland::sim::BattleRemoteEndpoint observed_remote{};

    ihomeland::sim::BattleUdpListener listener(
        LoopbackConfig(),
        [&](const std::span<const std::uint8_t> datagram,
            const ihomeland::sim::BattleRemoteEndpoint& remote) {
            std::scoped_lock lock(callback_mutex);
            Require(
                datagram.size() == ClientHello().size(),
                "listener changed accepted datagram length");
            observed_remote = remote;
            ++callback_count;
            callback_ready.notify_one();
        });
    listener.Start();
    const auto started = listener.Status();
    Require(
        started.running && !started.failed,
        "listener did not become ready");
    Require(
        started.bound_endpoint.host == "127.0.0.1" &&
            started.bound_endpoint.port != 0,
        "listener did not expose actual loopback bind");
    Require(
        started.advertised_endpoint.host == "127.0.0.1" &&
            started.advertised_endpoint.port ==
                started.bound_endpoint.port,
        "test advertised endpoint did not resolve exact ephemeral port");

    asio::io_context client_context;
    asio::ip::udp::socket client(
        client_context,
        asio::ip::udp::v4());
    const asio::ip::udp::endpoint target(
        asio::ip::make_address_v4(
            started.bound_endpoint.host),
        started.bound_endpoint.port);

    const std::array<std::uint8_t, 8> malformed{
        'B', 'A', 'D', '!', 1, 1, 0, 0};
    SendDatagram(client, target, malformed);
    std::vector<std::uint8_t> oversized(
        ihomeland::sim::BattleUdpListener::
                MaximumDatagramBytes +
            1,
        0x5a);
    SendDatagram(client, target, oversized);
    const auto hello = ClientHello();
    SendDatagram(client, target, hello);

    {
        std::unique_lock lock(callback_mutex);
        Require(
            callback_ready.wait_for(
                lock,
                2s,
                [&] { return callback_count == 1; }),
            "listener did not deliver accepted loopback datagram");
    }

    const auto status = listener.Status();
    Require(
        status.running && !status.failed,
        "malformed ingress stopped listener");
    Require(
        status.counters.received_datagrams == 3 &&
            status.counters.accepted_datagrams == 1 &&
            status.counters.malformed_datagrams == 1 &&
            status.counters.oversized_datagrams == 1 &&
            status.counters.receive_failures == 0 &&
            status.counters.handler_failures == 0,
        "listener counters do not prove fixed-size fast reject");
    Require(
        observed_remote.port == client.local_endpoint().port() &&
            observed_remote.address[10] == 0xff &&
            observed_remote.address[11] == 0xff &&
            observed_remote.address[12] == 127 &&
            observed_remote.address[15] == 1,
        "listener did not canonicalize IPv4 source endpoint");

    const std::array<std::uint8_t, 4> reply{
        'P', 'O', 'N', 'G'};
    Require(
        listener.Send(reply, observed_remote) ==
            ihomeland::sim::BattleUdpSendDisposition::Queued,
        "listener did not queue same-socket output");
    client.non_blocking(true);
    std::array<std::uint8_t, 16> received{};
    asio::ip::udp::endpoint reply_source;
    std::size_t received_bytes = 0;
    const auto receive_deadline =
        std::chrono::steady_clock::now() + 2s;
    while (std::chrono::steady_clock::now() <
               receive_deadline &&
           received_bytes == 0) {
        asio::error_code error;
        received_bytes = client.receive_from(
            asio::buffer(received),
            reply_source,
            0,
            error);
        if (error != asio::error::would_block &&
            error != asio::error::try_again &&
            error) {
            throw std::runtime_error(
                "same-socket UDP receive failed");
        }
        if (received_bytes == 0) {
            std::this_thread::sleep_for(1ms);
        }
    }
    Require(
        received_bytes == reply.size() &&
            std::ranges::equal(
                reply,
                std::span(received).first(
                    received_bytes)) &&
            reply_source.port() ==
                started.bound_endpoint.port,
        "listener output did not use the bound receive socket");

    ihomeland::sim::BattleRemoteEndpoint invalid_remote{};
    Require(
        listener.Send(reply, invalid_remote) ==
                ihomeland::sim::BattleUdpSendDisposition::
                    InvalidRemote &&
            listener.Send(
                std::vector<std::uint8_t>(
                    ihomeland::sim::BattleUdpListener::
                            MaximumDatagramBytes +
                        1),
                observed_remote) ==
                ihomeland::sim::BattleUdpSendDisposition::
                    InvalidDatagram,
        "listener accepted invalid output");

    auto conflict_config = LoopbackConfig();
    conflict_config.mode =
        ihomeland::sim::BattleUdpListenerMode::Production;
    conflict_config.bind_endpoint =
        started.bound_endpoint;
    conflict_config.advertised_endpoint =
        started.advertised_endpoint;
    ihomeland::sim::BattleUdpListener conflict(
        conflict_config,
        [](const auto, const auto&) {});
    bool conflict_rejected = false;
    try {
        conflict.Start();
    } catch (const std::runtime_error&) {
        conflict_rejected = true;
    }
    Require(
        conflict_rejected,
        "bind conflict selected another UDP port");

    listener.Stop();
    listener.Stop();
    Require(
        !listener.Status().running &&
            listener.Send(reply, observed_remote) ==
                ihomeland::sim::BattleUdpSendDisposition::
                    Stopped,
        "listener did not stop idempotently");
}

/// TestClosedPeerIcmpIsolation 验证旧远端的 ICMP 不会终止 node-global listener。
void TestClosedPeerIcmpIsolation() {
    std::mutex callback_mutex;
    std::condition_variable callback_ready;
    std::size_t callback_count = 0;
    ihomeland::sim::BattleUdpListener listener(
        LoopbackConfig(),
        [&](const auto, const auto&) {
            std::scoped_lock lock(callback_mutex);
            ++callback_count;
            callback_ready.notify_all();
        });
    listener.Start();
    const auto started = listener.Status();
    const asio::ip::udp::endpoint target(
        asio::ip::make_address_v4(
            started.bound_endpoint.host),
        started.bound_endpoint.port);

    asio::io_context doomed_context;
    asio::ip::udp::socket doomed(
        doomed_context,
        asio::ip::udp::v4());
    SendDatagram(doomed, target, ClientHello());
    {
        std::unique_lock lock(callback_mutex);
        Require(
            callback_ready.wait_for(
                lock,
                2s,
                [&] { return callback_count == 1; }),
            "listener did not observe doomed UDP peer");
    }
    const auto doomed_port =
        doomed.local_endpoint().port();
    doomed.close();

    const std::array<std::uint8_t, 1> terminal_reply{
        0x01};
    const auto doomed_remote =
        CanonicalLoopbackRemote(doomed_port);
    for (std::size_t copy = 0; copy < 3; ++copy) {
        Require(
            listener.Send(
                terminal_reply,
                doomed_remote) ==
                ihomeland::sim::BattleUdpSendDisposition::
                    Queued,
            "listener rejected closed-peer regression output");
    }

    const auto output_deadline =
        std::chrono::steady_clock::now() + 2s;
    while (std::chrono::steady_clock::now() <
               output_deadline &&
           listener.Status().counters.sent_datagrams < 3 &&
           !listener.Status().failed) {
        std::this_thread::sleep_for(1ms);
    }
    Require(
        listener.Status().counters.sent_datagrams == 3,
        "listener did not complete closed-peer regression output");
    std::this_thread::sleep_for(100ms);

    asio::io_context survivor_context;
    asio::ip::udp::socket survivor(
        survivor_context,
        asio::ip::udp::v4());
    SendDatagram(survivor, target, ClientHello());
    {
        std::unique_lock lock(callback_mutex);
        Require(
            callback_ready.wait_for(
                lock,
                2s,
                [&] { return callback_count == 2; }),
            "closed peer ICMP stopped later valid UDP ingress");
    }
    const auto survived = listener.Status();
    Require(
        survived.running &&
            !survived.failed &&
            survived.counters.receive_failures == 0,
        "closed peer ICMP escaped datagram-local isolation");
    listener.Stop();
}

/// TestSendPressure 验证 hard queue budget 不会因 executor 被占用而动态扩张。
void TestSendPressure() {
    std::mutex callback_mutex;
    std::condition_variable callback_state;
    bool callback_entered = false;
    bool release_callback = false;
    ihomeland::sim::BattleUdpListener listener(
        LoopbackConfig(),
        [&](const auto, const auto&) {
            std::unique_lock lock(callback_mutex);
            callback_entered = true;
            callback_state.notify_all();
            callback_state.wait(
                lock,
                [&] { return release_callback; });
        });
    listener.Start();
    const auto status = listener.Status();
    asio::io_context client_context;
    asio::ip::udp::socket client(
        client_context,
        asio::ip::udp::v4());
    SendDatagram(
        client,
        asio::ip::udp::endpoint(
            asio::ip::make_address_v4(
                status.bound_endpoint.host),
            status.bound_endpoint.port),
        ClientHello());
    {
        std::unique_lock lock(callback_mutex);
        Require(
            callback_state.wait_for(
                lock,
                2s,
                [&] { return callback_entered; }),
            "listener callback did not enter pressure fixture");
    }

    ihomeland::sim::BattleRemoteEndpoint remote{};
    remote.address[10] = 0xff;
    remote.address[11] = 0xff;
    remote.address[12] = 127;
    remote.address[15] = 1;
    remote.port = 9;
    const std::array<std::uint8_t, 1> payload{1};
    for (std::size_t index = 0;
         index <
         ihomeland::sim::BattleUdpListener::
             MaximumPendingSendDatagrams;
         ++index) {
        Require(
            listener.Send(payload, remote) ==
                ihomeland::sim::BattleUdpSendDisposition::
                    Queued,
            "listener queue reached pressure before hard limit");
    }
    Require(
        listener.Send(payload, remote) ==
            ihomeland::sim::BattleUdpSendDisposition::
                QueueFull,
        "listener queue exceeded hard send budget");
    {
        std::scoped_lock lock(callback_mutex);
        release_callback = true;
    }
    callback_state.notify_all();
    listener.Stop();
    const auto stopped = listener.Status();
    Require(
        !stopped.running &&
            stopped.counters.queued_send_datagrams ==
                ihomeland::sim::BattleUdpListener::
                    MaximumPendingSendDatagrams &&
            stopped.counters.rejected_send_datagrams >= 1,
        "listener pressure counters drifted");
}

/// TestNodeOwnership 验证一个 node 生命周期内不能创建第二 socket。
void TestNodeOwnership() {
    ihomeland::sim::SimulationNode node(NodeConfig());
    node.StartBattleUdpListener(
        LoopbackConfig(),
        [](const auto, const auto&) {});
    const auto status = node.UdpListenerStatus();
    Require(
        status.has_value() &&
            status->running &&
            status->bound_endpoint.port != 0,
        "node-global listener did not become ready");
    bool second_listener_rejected = false;
    try {
        node.StartBattleUdpListener(
            LoopbackConfig(),
            [](const auto, const auto&) {});
    } catch (const std::logic_error&) {
        second_listener_rejected = true;
    }
    Require(
        second_listener_rejected,
        "simulation node accepted a second UDP listener");
    node.BeginShutdown(0ms);
    Require(
        node.UdpListenerStatus().has_value() &&
            !node.UdpListenerStatus()->running,
        "node shutdown left UDP listener running");
}

}  // namespace

int main() {
    TestValidation();
    TestLoopbackIngress();
    TestClosedPeerIcmpIsolation();
    TestSendPressure();
    TestNodeOwnership();
    return 0;
}
