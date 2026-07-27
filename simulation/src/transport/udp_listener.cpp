#include "ihomeland/sim/transport/udp_listener.hpp"

#include <asio.hpp>
#include <mstcpip.h>

#include <algorithm>
#include <array>
#include <atomic>
#include <deque>
#include <mutex>
#include <optional>
#include <ranges>
#include <stdexcept>
#include <string>
#include <thread>
#include <utility>
#include <vector>

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

/// IsIpv4Mapped 判断 canonical endpoint 是否保存 IPv4-mapped IPv6。
[[nodiscard]] bool IsIpv4Mapped(
    const std::array<std::uint8_t, 16>& address) noexcept {
    return std::ranges::all_of(
               address.begin(),
               address.begin() + 10,
               [](const std::uint8_t value) {
                   return value == 0;
               }) &&
        address[10] == 0xff &&
        address[11] == 0xff;
}

/// ParseRemoteEndpoint 从公开 canonical bytes 恢复 closed Asio endpoint。
[[nodiscard]] std::optional<asio::ip::udp::endpoint>
ParseRemoteEndpoint(
    const BattleRemoteEndpoint& remote) noexcept {
    if (remote.port == 0) {
        return std::nullopt;
    }
    if (IsIpv4Mapped(remote.address)) {
        asio::ip::address_v4::bytes_type bytes{};
        std::ranges::copy(
            remote.address.begin() + 12,
            remote.address.end(),
            bytes.begin());
        const asio::ip::address_v4 address(bytes);
        if (address.is_unspecified() ||
            address.is_multicast()) {
            return std::nullopt;
        }
        return asio::ip::udp::endpoint(
            address,
            remote.port);
    }
    asio::ip::address_v6::bytes_type bytes{};
    std::ranges::copy(
        remote.address,
        bytes.begin());
    const asio::ip::address_v6 address(bytes);
    if (address.is_unspecified() ||
        address.is_multicast()) {
        return std::nullopt;
    }
    return asio::ip::udp::endpoint(
        address,
        remote.port);
}

}  // namespace

/// BattleUdpListener::Impl 隔离 Asio socket、worker 与固定 receive storage。
struct BattleUdpListener::Impl final {
    /// PendingSend 拥有 executor 完成前必须保持有效的 output 与 remote。
    struct PendingSend final {
        /// datagram 是不超过 MTU ceiling 的独立副本。
        std::vector<std::uint8_t> datagram;
        /// remote 是已经过 closed public validation 的 numeric endpoint。
        asio::ip::udp::endpoint remote;
    };

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

    /// EnqueueSend 只在 listener executor 上修改 send queue。
    void EnqueueSend(PendingSend request) {
        if (!running.load(std::memory_order_acquire) ||
            !socket.is_open()) {
            pending_send_datagrams.fetch_sub(
                1,
                std::memory_order_acq_rel);
            rejected_send_datagrams.fetch_add(
                1,
                std::memory_order_relaxed);
            return;
        }
        send_queue.push_back(std::move(request));
        if (!send_in_progress) {
            ArmSend();
        }
    }

    /// ArmSend 在同一 socket 上串行发送 queue front。
    void ArmSend() {
        if (send_queue.empty() ||
            !running.load(std::memory_order_acquire) ||
            !socket.is_open()) {
            send_in_progress = false;
            return;
        }
        send_in_progress = true;
        auto& request = send_queue.front();
        socket.async_send_to(
            asio::buffer(request.datagram),
            request.remote,
            [this](
                const asio::error_code& error,
                const std::size_t sent_bytes_value) {
                HandleSend(error, sent_bytes_value);
            });
    }

    /// HandleSend 终结一个 owned output 并继续唯一串行队列。
    void HandleSend(
        const asio::error_code& error,
        const std::size_t sent_bytes_value) {
        if (send_queue.empty()) {
            send_in_progress = false;
            return;
        }
        const auto expected_bytes =
            send_queue.front().datagram.size();
        if (!error && sent_bytes_value == expected_bytes) {
            sent_datagrams.fetch_add(
                1,
                std::memory_order_relaxed);
            sent_bytes.fetch_add(
                sent_bytes_value,
                std::memory_order_relaxed);
        } else if (
            error != asio::error::operation_aborted ||
            running.load(std::memory_order_acquire)) {
            send_failures.fetch_add(
                1,
                std::memory_order_relaxed);
            failed.store(true, std::memory_order_release);
            running.store(false, std::memory_order_release);
            asio::error_code ignored;
            socket.close(ignored);
        }
        send_queue.pop_front();
        pending_send_datagrams.fetch_sub(
            1,
            std::memory_order_acq_rel);
        send_in_progress = false;
        if (running.load(std::memory_order_acquire)) {
            ArmSend();
        } else {
            DiscardPendingSends();
        }
    }

    /// DiscardPendingSends 在 executor 停止或 failure 后释放所有 queue slots。
    void DiscardPendingSends() noexcept {
        const auto discarded = send_queue.size();
        send_queue.clear();
        send_in_progress = false;
        if (discarded != 0) {
            pending_send_datagrams.fetch_sub(
                discarded,
                std::memory_order_acq_rel);
            rejected_send_datagrams.fetch_add(
                discarded,
                std::memory_order_relaxed);
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
    /// send_queue 只由 io_context worker 修改并严格串行发送。
    std::deque<PendingSend> send_queue;
    /// send_in_progress 表示 queue front 已交给 socket。
    bool send_in_progress{false};
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
    /// pending_send_datagrams 是 caller reservation 与 executor queue 的共同 hard budget。
    std::atomic_size_t pending_send_datagrams{0};
    /// queued_send_datagrams 是取得 hard-budget slot 的累计数。
    std::atomic_uint64_t queued_send_datagrams{0};
    /// sent_datagrams 是完整 send completion 累计数。
    std::atomic_uint64_t sent_datagrams{0};
    /// sent_bytes 是成功发送的低敏 byte 累计数。
    std::atomic_uint64_t sent_bytes{0};
    /// rejected_send_datagrams 是 invalid、pressure 或 stop 拒绝累计数。
    std::atomic_uint64_t rejected_send_datagrams{0};
    /// send_failures 是非 shutdown socket failure 累计数。
    std::atomic_uint64_t send_failures{0};
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
    if (error) {
        throw std::runtime_error(
            "battle UDP listener socket open failed: " +
            error.message());
    }
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
        asio::error_code ignored;
        impl_->socket.close(ignored);
        throw std::runtime_error(
            "battle UDP listener ownership policy failed: " +
            error.message());
    }
    // Windows 默认把无连接 UDP 的 ICMP Port Unreachable 映射为下一次
    // receive 的 WSAECONNRESET；该错误只属于单个远端 datagram，不能终止
    // node-global listener。必须在首次 receive 前关闭这一 socket 行为。
    BOOL udp_connection_reset = FALSE;
    DWORD bytes_returned = 0;
    if (::WSAIoctl(
            impl_->socket.native_handle(),
            SIO_UDP_CONNRESET,
            &udp_connection_reset,
            sizeof(udp_connection_reset),
            nullptr,
            0,
            &bytes_returned,
            nullptr,
            nullptr) == SOCKET_ERROR) {
        error.assign(
            WSAGetLastError(),
            asio::error::get_system_category());
        asio::error_code ignored;
        impl_->socket.close(ignored);
        throw std::runtime_error(
            "battle UDP listener ICMP policy failed: " +
            error.message());
    }
    impl_->socket.bind(requested, error);
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
    impl_->io_context.restart();
    while (impl_->io_context.poll() != 0) {
    }
    impl_->DiscardPendingSends();
    impl_->io_context.stop();
}

BattleUdpSendDisposition BattleUdpListener::Send(
    const std::span<const std::uint8_t> datagram,
    const BattleRemoteEndpoint& remote) {
    if (datagram.empty() ||
        datagram.size() > MaximumDatagramBytes) {
        impl_->rejected_send_datagrams.fetch_add(
            1,
            std::memory_order_relaxed);
        return BattleUdpSendDisposition::InvalidDatagram;
    }
    const auto endpoint = ParseRemoteEndpoint(remote);
    if (!endpoint.has_value()) {
        impl_->rejected_send_datagrams.fetch_add(
            1,
            std::memory_order_relaxed);
        return BattleUdpSendDisposition::InvalidRemote;
    }

    std::scoped_lock lock(impl_->lifecycle_mutex);
    if (!impl_->running.load(std::memory_order_acquire) ||
        !impl_->socket.is_open()) {
        impl_->rejected_send_datagrams.fetch_add(
            1,
            std::memory_order_relaxed);
        return BattleUdpSendDisposition::Stopped;
    }
    auto pending = impl_->pending_send_datagrams.load(
        std::memory_order_relaxed);
    while (true) {
        if (pending >= MaximumPendingSendDatagrams) {
            impl_->rejected_send_datagrams.fetch_add(
                1,
                std::memory_order_relaxed);
            return BattleUdpSendDisposition::QueueFull;
        }
        if (impl_->pending_send_datagrams
                .compare_exchange_weak(
                    pending,
                    pending + 1,
                    std::memory_order_acq_rel,
                    std::memory_order_relaxed)) {
            break;
        }
    }
    impl_->queued_send_datagrams.fetch_add(
        1,
        std::memory_order_relaxed);
    asio::post(
        impl_->io_context,
        [state = impl_.get(),
         request = Impl::PendingSend{
             .datagram = std::vector<std::uint8_t>(
                 datagram.begin(),
                 datagram.end()),
             .remote = *endpoint}]() mutable {
            state->EnqueueSend(std::move(request));
        });
    return BattleUdpSendDisposition::Queued;
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
            .queued_send_datagrams =
                impl_->queued_send_datagrams.load(
                    std::memory_order_relaxed),
            .sent_datagrams =
                impl_->sent_datagrams.load(
                    std::memory_order_relaxed),
            .sent_bytes =
                impl_->sent_bytes.load(
                    std::memory_order_relaxed),
            .rejected_send_datagrams =
                impl_->rejected_send_datagrams.load(
                    std::memory_order_relaxed),
            .send_failures =
                impl_->send_failures.load(
                    std::memory_order_relaxed),
        },
    };
}

}  // namespace ihomeland::sim
