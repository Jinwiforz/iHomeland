#include "ihomeland/sim/transport/udp_listener.hpp"

#include <asio.hpp>

#include <algorithm>
#include <array>
#include <atomic>
#include <mutex>
#include <ranges>
#include <stdexcept>
#include <string>
#include <thread>
#include <utility>

namespace ihomeland::sim {
namespace {

constexpr std::array<std::uint8_t, 4> ClientHelloMagic{
    'I', 'H', 'B', 'H'};
constexpr std::array<std::uint8_t, 4> ClientAuthMagic{
    'I', 'H', 'B', 'A'};
constexpr std::array<std::uint8_t, 4> SecureMagic{
    'I', 'H', 'B', 'T'};
constexpr std::uint8_t WireVersion = 1;
constexpr std::size_t ClientHelloBytes = 88;
constexpr std::size_t ClientAuthBytes = 140;
constexpr std::size_t SecureMinimumBytes = 65;

/// IsAllZero 验证公开 listener identity 不是未配置零值。
template <std::size_t Size>
[[nodiscard]] bool IsAllZero(
    const std::array<std::uint8_t, Size>& value) noexcept {
    return std::ranges::all_of(
        value,
        [](const std::uint8_t item) { return item == 0; });
}

/// ParseNumericAddress 禁止 DNS、隐式 Host 与 wildcard advertised identity。
[[nodiscard]] asio::ip::address ParseNumericAddress(
    const std::string& host,
    const bool advertised) {
    asio::error_code error;
    const auto address = asio::ip::make_address(host, error);
    if (error || host.empty() ||
        (advertised && address.is_unspecified())) {
        throw std::invalid_argument("battle UDP endpoint is invalid");
    }
    return address;
}

/// IsLoopbackV4 判断 endpoint 是否为测试唯一允许的 127.0.0.1。
[[nodiscard]] bool IsLoopbackV4(
    const asio::ip::address& address) noexcept {
    return address.is_v4() &&
           address.to_v4() == asio::ip::address_v4::loopback();
}

/// IsFastAccepted 执行 allocation-free fixed-header ingress 分类。
[[nodiscard]] bool IsFastAccepted(
    const std::span<const std::uint8_t> datagram) noexcept {
    if (datagram.size() < 8 ||
        datagram[4] != WireVersion ||
        datagram[6] != 0 ||
        datagram[7] != 0) {
        return false;
    }
    const auto magic = datagram.first<4>();
    if (std::ranges::equal(ClientHelloMagic, magic)) {
        return datagram[5] == 1 &&
               datagram.size() >= ClientHelloBytes;
    }
    if (std::ranges::equal(ClientAuthMagic, magic)) {
        return datagram[5] == 3 &&
               datagram.size() == ClientAuthBytes;
    }
    if (std::ranges::equal(SecureMagic, magic)) {
        return datagram[5] >= 1 &&
               datagram[5] <= 3 &&
               datagram.size() >= SecureMinimumBytes;
    }
    return false;
}

/// CanonicalRemoteEndpoint 将 IPv4 固定映射为 IPv4-mapped IPv6。
[[nodiscard]] BattleRemoteEndpoint CanonicalRemoteEndpoint(
    const asio::ip::udp::endpoint& endpoint) {
    BattleRemoteEndpoint remote{};
    remote.port = endpoint.port();
    if (endpoint.address().is_v4()) {
        remote.address[10] = 0xff;
        remote.address[11] = 0xff;
        const auto bytes = endpoint.address().to_v4().to_bytes();
        std::ranges::copy(bytes, remote.address.begin() + 12);
    } else {
        const auto bytes = endpoint.address().to_v6().to_bytes();
        std::ranges::copy(bytes, remote.address.begin());
    }
    return remote;
}

}  // namespace

/// BattleUdpListener::Impl 隔离 Asio socket、worker 与固定 receive storage。
struct BattleUdpListener::Impl final {
    /// 构造函数保存已验证配置与唯一上层 multiplexer。
    Impl(
        BattleUdpListenerConfig value,
        DatagramHandler callback)
        : config(std::move(value)),
          handler(std::move(callback)),
          socket(io_context) {}

    /// ArmReceive 复用固定 buffer 和 remote endpoint，禁止 per-packet heap buffer。
    void ArmReceive() {
        socket.async_receive_from(
            asio::buffer(receive_buffer),
            receive_remote,
            [this](
                const asio::error_code& error,
                const std::size_t received_bytes) {
                HandleReceive(error, received_bytes);
            });
    }

    /// HandleReceive 在任何 session/handshake allocation 前完成 MTU 与 header gate。
    void HandleReceive(
        const asio::error_code& error,
        const std::size_t received_bytes) {
        if (error == asio::error::operation_aborted) {
            return;
        }
        received_datagrams.fetch_add(1, std::memory_order_relaxed);
        if (error == asio::error::message_size) {
            oversized_datagrams.fetch_add(1, std::memory_order_relaxed);
            if (running.load(std::memory_order_acquire)) {
                ArmReceive();
            }
            return;
        }
        if (error) {
            receive_failures.fetch_add(1, std::memory_order_relaxed);
            failed.store(true, std::memory_order_release);
            running.store(false, std::memory_order_release);
            asio::error_code ignored;
            socket.close(ignored);
            return;
        }
        if (received_bytes > MaximumDatagramBytes) {
            oversized_datagrams.fetch_add(1, std::memory_order_relaxed);
            if (running.load(std::memory_order_acquire)) {
                ArmReceive();
            }
            return;
        }

        const auto datagram = std::span<const std::uint8_t>(
            receive_buffer.data(),
            received_bytes);
        if (!IsFastAccepted(datagram)) {
            malformed_datagrams.fetch_add(1, std::memory_order_relaxed);
        } else {
            accepted_datagrams.fetch_add(1, std::memory_order_relaxed);
            try {
                handler(
                    datagram,
                    CanonicalRemoteEndpoint(receive_remote));
            } catch (...) {
                handler_failures.fetch_add(1, std::memory_order_relaxed);
                failed.store(true, std::memory_order_release);
                running.store(false, std::memory_order_release);
                asio::error_code ignored;
                socket.close(ignored);
                return;
            }
        }
        if (running.load(std::memory_order_acquire)) {
            ArmReceive();
        }
    }

    /// config 是 immutable bind、advertised 与 cookie identity。
    BattleUdpListenerConfig config;
    /// handler 是 node-global authenticated multiplexer 入口。
    DatagramHandler handler;
    /// io_context 只服务唯一 UDP socket。
    asio::io_context io_context;
    /// socket 是当前 node 唯一网络 owner。
    asio::ip::udp::socket socket;
    /// receive_buffer 用一个固定 sentinel byte 区分 Windows 上的 truncated datagram。
    std::array<std::uint8_t, MaximumDatagramBytes + 1>
        receive_buffer{};
    /// receive_remote 是 Asio 写入的当前 source endpoint。
    asio::ip::udp::endpoint receive_remote;
    /// lifecycle_mutex 串行化 Start、Stop 与 status endpoint snapshot。
    mutable std::mutex lifecycle_mutex;
    /// worker 独占 io_context run loop。
    std::thread worker;
    /// bound_endpoint 是 bind 后由系统确认的 numeric endpoint。
    BattleUdpEndpointConfig bound_endpoint;
    /// advertised_endpoint 可能在 loopback test 中解析 ephemeral port。
    BattleUdpEndpointConfig advertised_endpoint;
    /// running 由 lifecycle 与 receive callback 共同维护。
    std::atomic_bool running{false};
    /// failed 是不可恢复的当前 listener failure。
    std::atomic_bool failed{false};
    /// received_datagrams 是低敏 socket ingress 数。
    std::atomic_uint64_t received_datagrams{0};
    /// accepted_datagrams 是通过 fast reject 的 datagram 数。
    std::atomic_uint64_t accepted_datagrams{0};
    /// malformed_datagrams 是 fixed-header 拒绝数。
    std::atomic_uint64_t malformed_datagrams{0};
    /// oversized_datagrams 是 MTU ceiling 拒绝数。
    std::atomic_uint64_t oversized_datagrams{0};
    /// receive_failures 是非 shutdown socket failure 数。
    std::atomic_uint64_t receive_failures{0};
    /// handler_failures 是上层 callback failure 数。
    std::atomic_uint64_t handler_failures{0};
};

BattleUdpListener::BattleUdpListener(
    BattleUdpListenerConfig config,
    DatagramHandler handler)
    : impl_(std::make_unique<Impl>(
          std::move(config),
          std::move(handler))) {
    if (!impl_->handler ||
        IsAllZero(impl_->config.listener_identity)) {
        throw std::invalid_argument(
            "battle UDP listener identity is invalid");
    }
    const auto bind_address = ParseNumericAddress(
        impl_->config.bind_endpoint.host,
        false);
    const auto advertised_address = ParseNumericAddress(
        impl_->config.advertised_endpoint.host,
        true);
    if (impl_->config.mode == BattleUdpListenerMode::Production) {
        if (impl_->config.bind_endpoint.port == 0 ||
            impl_->config.advertised_endpoint.port == 0) {
            throw std::invalid_argument(
                "production battle UDP port must be non-zero");
        }
    } else if (!IsLoopbackV4(bind_address) ||
               !IsLoopbackV4(advertised_address) ||
               impl_->config.bind_endpoint.port != 0 ||
               impl_->config.advertised_endpoint.port != 0) {
        throw std::invalid_argument(
            "battle UDP loopback test must use 127.0.0.1:0");
    }
}

BattleUdpListener::~BattleUdpListener() {
    Stop();
}

void BattleUdpListener::Start() {
    std::scoped_lock lock(impl_->lifecycle_mutex);
    if (impl_->running.load(std::memory_order_acquire) ||
        impl_->worker.joinable()) {
        throw std::logic_error("battle UDP listener already started");
    }

    const auto bind_address = ParseNumericAddress(
        impl_->config.bind_endpoint.host,
        false);
    const asio::ip::udp::endpoint requested(
        bind_address,
        impl_->config.bind_endpoint.port);
    impl_->io_context.restart();
    impl_->failed.store(false, std::memory_order_release);
    asio::error_code error;
    impl_->socket.open(requested.protocol(), error);
    if (!error) {
        const BOOL exclusive_address_use = TRUE;
        if (::setsockopt(
                impl_->socket.native_handle(),
                SOL_SOCKET,
                SO_EXCLUSIVEADDRUSE,
                reinterpret_cast<const char*>(
                    &exclusive_address_use),
                sizeof(exclusive_address_use)) ==
            SOCKET_ERROR) {
            error.assign(
                WSAGetLastError(),
                asio::error::get_system_category());
        }
    }
    if (!error) {
        impl_->socket.bind(requested, error);
    }
    if (error) {
        asio::error_code ignored;
        impl_->socket.close(ignored);
        throw std::runtime_error(
            "battle UDP listener bind failed: " +
            error.message());
    }

    const auto actual = impl_->socket.local_endpoint(error);
    if (error || actual.port() == 0) {
        asio::error_code ignored;
        impl_->socket.close(ignored);
        throw std::runtime_error(
            "battle UDP listener local endpoint failed");
    }
    impl_->bound_endpoint = BattleUdpEndpointConfig{
        .host = actual.address().to_string(),
        .port = actual.port(),
    };
    impl_->advertised_endpoint =
        impl_->config.advertised_endpoint;
    if (impl_->config.mode ==
        BattleUdpListenerMode::LoopbackTest) {
        impl_->advertised_endpoint.port = actual.port();
    }
    impl_->running.store(true, std::memory_order_release);
    impl_->ArmReceive();
    try {
        impl_->worker = std::thread(
            [state = impl_.get()] {
                state->io_context.run();
            });
    } catch (...) {
        impl_->running.store(false, std::memory_order_release);
        asio::error_code ignored;
        impl_->socket.close(ignored);
        impl_->io_context.stop();
        throw;
    }
}

void BattleUdpListener::Stop() noexcept {
    std::thread worker;
    {
        std::scoped_lock lock(impl_->lifecycle_mutex);
        impl_->running.store(false, std::memory_order_release);
        asio::error_code ignored;
        impl_->socket.cancel(ignored);
        impl_->socket.close(ignored);
        impl_->io_context.stop();
        worker = std::move(impl_->worker);
    }
    if (worker.joinable()) {
        worker.join();
    }
}

BattleUdpListenerStatus BattleUdpListener::Status() const {
    std::scoped_lock lock(impl_->lifecycle_mutex);
    return BattleUdpListenerStatus{
        .running = impl_->running.load(std::memory_order_acquire),
        .failed = impl_->failed.load(std::memory_order_acquire),
        .bound_endpoint = impl_->bound_endpoint,
        .advertised_endpoint = impl_->advertised_endpoint,
        .counters = BattleUdpListenerCounters{
            .received_datagrams =
                impl_->received_datagrams.load(
                    std::memory_order_relaxed),
            .accepted_datagrams =
                impl_->accepted_datagrams.load(
                    std::memory_order_relaxed),
            .malformed_datagrams =
                impl_->malformed_datagrams.load(
                    std::memory_order_relaxed),
            .oversized_datagrams =
                impl_->oversized_datagrams.load(
                    std::memory_order_relaxed),
            .receive_failures =
                impl_->receive_failures.load(
                    std::memory_order_relaxed),
            .handler_failures =
                impl_->handler_failures.load(
                    std::memory_order_relaxed),
        },
    };
}

}  // namespace ihomeland::sim
