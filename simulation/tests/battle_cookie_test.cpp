#include "ihomeland/sim/transport/handshake_cookie.hpp"

#include <algorithm>
#include <array>
#include <cstdint>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace {

/// Require 使 cookie contract failure 以稳定非零退出。
void Require(
    const bool condition,
    const std::string_view message) {
    if (!condition) {
        throw std::runtime_error(std::string(message));
    }
}

/// Sequence 返回从 start 开始的固定公开 fixture bytes。
template <std::size_t Size>
[[nodiscard]] std::array<std::uint8_t, Size> Sequence(
    const std::uint8_t start) {
    std::array<std::uint8_t, Size> value{};
    for (std::size_t index = 0; index < value.size(); ++index) {
        value[index] = static_cast<std::uint8_t>(start + index);
    }
    return value;
}

/// HelloBytes 编码 fixed-width ClientHello 和零 padding。
[[nodiscard]] std::vector<std::uint8_t> HelloBytes(
    const std::size_t padding = 0) {
    std::vector<std::uint8_t> bytes(
        ihomeland::sim::BattleHandshakeCookieGate::ClientHelloBytes +
            padding,
        0);
    const std::array<std::uint8_t, 4> magic{'I', 'H', 'B', 'H'};
    std::ranges::copy(magic, bytes.begin());
    bytes[4] = 1;
    bytes[5] = 1;
    const auto ticket = Sequence<16>(1);
    const auto nonce = Sequence<32>(17);
    // small-order public value若误触发 X25519 会失败；cookie gate 只能把它作为 opaque bytes绑定。
    std::array<std::uint8_t, 32> public_value{};
    public_value[0] = 1;
    std::ranges::copy(ticket, bytes.begin() + 8);
    std::ranges::copy(nonce, bytes.begin() + 24);
    std::ranges::copy(public_value, bytes.begin() + 56);
    return bytes;
}

/// Endpoint 返回 canonical IPv4-mapped UDP endpoint。
[[nodiscard]] ihomeland::sim::BattleRemoteEndpoint Endpoint() {
    auto address = std::array<std::uint8_t, 16>{};
    address[10] = 0xff;
    address[11] = 0xff;
    address[12] = 127;
    address[15] = 1;
    return {.address = address, .port = 58445};
}

/// TestRetryAndBinding 验证 stateless Retry、抗放大与全部 cookie binding。
void TestRetryAndBinding() {
    ihomeland::sim::CryptoProvider crypto;
    const auto key = Sequence<32>(0x40);
    ihomeland::sim::BattleCookieKeyRing keys(crypto, 100, key);
    const auto listener = Sequence<16>(0x80);
    ihomeland::sim::BattleHandshakeCookieGate gate(keys, listener);
    const auto hello = HelloBytes(32);
    const auto remote = Endpoint();
    const auto now = 100 *
        ihomeland::sim::BattleCookieKeyRing::RotationMilliseconds;
    const auto decision = gate.HandleClientHello(
        hello,
        remote,
        now);
    Require(
        decision.response_bytes ==
            ihomeland::sim::BattleHandshakeCookieGate::RetryBytes,
        "valid ClientHello did not produce Retry");
    Require(
        decision.response_bytes <= hello.size(),
        "Retry amplified request");
    const auto retry =
        ihomeland::sim::BattleHandshakeCookieGate::ParseRetry(
            std::span(decision.response).first(
                decision.response_bytes));
    Require(retry.has_value(), "Retry closed decode failed");
    Require(
        gate.ValidateCookie(hello, remote, *retry, now),
        "issued cookie did not validate");

    auto changed_endpoint = remote;
    ++changed_endpoint.port;
    Require(
        !gate.ValidateCookie(
            hello,
            changed_endpoint,
            *retry,
            now),
        "cookie ignored remote port");
    auto changed_hello = hello;
    changed_hello[24] ^= 1;
    Require(
        !gate.ValidateCookie(
            changed_hello,
            remote,
            *retry,
            now),
        "cookie ignored ClientHello transcript");
    ihomeland::sim::BattleHandshakeCookieGate other_listener(
        keys,
        Sequence<16>(0x90));
    Require(
        !other_listener.ValidateCookie(
            hello,
            remote,
            *retry,
            now),
        "cookie ignored listener identity");
}

/// TestRotation 验证只接受 current/previous epoch 且跨时间片不恢复旧 key。
void TestRotation() {
    ihomeland::sim::CryptoProvider crypto;
    ihomeland::sim::BattleCookieKeyRing keys(
        crypto,
        200,
        Sequence<32>(0x20));
    ihomeland::sim::BattleHandshakeCookieGate gate(
        keys,
        Sequence<16>(0x60));
    const auto hello = HelloBytes();
    const auto remote = Endpoint();
    const auto issue = [&](const std::uint32_t epoch) {
        const auto decision = gate.HandleClientHello(
            hello,
            remote,
            static_cast<std::uint64_t>(epoch) *
                ihomeland::sim::BattleCookieKeyRing::
                    RotationMilliseconds);
        return *ihomeland::sim::BattleHandshakeCookieGate::ParseRetry(
            decision.response);
    };
    const auto epoch200 = issue(200);
    const auto epoch201 = issue(201);
    Require(
        gate.ValidateCookie(
            hello,
            remote,
            epoch200,
            201 *
                ihomeland::sim::BattleCookieKeyRing::
                    RotationMilliseconds),
        "previous cookie epoch rejected inside overlap");
    const auto epoch202 = issue(202);
    Require(
        !gate.ValidateCookie(
            hello,
            remote,
            epoch200,
            202 *
                ihomeland::sim::BattleCookieKeyRing::
                    RotationMilliseconds),
        "two-epoch-old cookie remained valid");
    Require(
        gate.ValidateCookie(
            hello,
            remote,
            epoch201,
            202 *
                ihomeland::sim::BattleCookieKeyRing::
                    RotationMilliseconds),
        "immediate previous cookie was not retained");
    Require(
        gate.ValidateCookie(
            hello,
            remote,
            epoch202,
            202 *
                ihomeland::sim::BattleCookieKeyRing::
                    RotationMilliseconds),
        "current cookie was not retained");
}

/// TestMalformedDrops 验证 malformed pre-auth input 静默丢弃且不生成放大响应。
void TestMalformedDrops() {
    ihomeland::sim::CryptoProvider crypto;
    ihomeland::sim::BattleCookieKeyRing keys(
        crypto,
        300,
        Sequence<32>(0xa0));
    ihomeland::sim::BattleHandshakeCookieGate gate(
        keys,
        Sequence<16>(0xb0));
    const auto remote = Endpoint();
    const auto now = 300 *
        ihomeland::sim::BattleCookieKeyRing::RotationMilliseconds;
    const auto expect_drop = [&](const auto& request) {
        Require(
            gate.HandleClientHello(request, remote, now)
                    .response_bytes == 0,
            "malformed ClientHello received response");
    };
    auto hello = HelloBytes();
    expect_drop(std::span(hello).first(27));
    hello[6] = 1;
    expect_drop(hello);
    hello = HelloBytes(1);
    hello.back() = 1;
    expect_drop(hello);
    hello = HelloBytes(
        ihomeland::sim::BattleHandshakeCookieGate::
            MaximumDatagramBytes -
        ihomeland::sim::BattleHandshakeCookieGate::
            ClientHelloBytes +
        1);
    expect_drop(hello);
}

}  // namespace

int main() {
    TestRetryAndBinding();
    TestRotation();
    TestMalformedDrops();
    return 0;
}
