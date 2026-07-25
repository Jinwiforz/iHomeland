#include "ihomeland/sim/core/sha256.hpp"
#include "ihomeland/sim/transport/authenticated_handshake.hpp"

#include <algorithm>
#include <array>
#include <chrono>
#include <cstdint>
#include <stdexcept>
#include <string>
#include <string_view>
#include <thread>
#include <vector>

namespace {

constexpr std::uint64_t NowUnixMs = 3'000'000;
constexpr std::uint32_t CookieEpoch = 100;
constexpr std::string_view ScheduleDomain =
    "ihomeland/battle/session-schedule/v1";
constexpr std::string_view ProofDomain =
    "ihomeland/battle/client-auth/v1";

/// Require 把 authenticated handshake failure 转换为 test exception。
void Require(
    const bool condition,
    const std::string_view message) {
    if (!condition) {
        throw std::runtime_error(std::string(message));
    }
}

/// Sequence 返回固定公开测试 bytes。
template <std::size_t Size>
[[nodiscard]] std::array<std::uint8_t, Size> Sequence(
    const std::uint8_t start) {
    std::array<std::uint8_t, Size> value{};
    for (std::size_t index = 0; index < Size; ++index) {
        value[index] = static_cast<std::uint8_t>(
            start + index);
    }
    return value;
}

/// NodeConfig 返回 handshake test 的 exact node identity。
[[nodiscard]] ihomeland::sim::SimulationNodeConfig NodeConfig() {
    return {
        .simulation_node_id = "snode_handshake_test",
        .runtime_node_id = "rnode_handshake_test",
        .build_identity = std::string(64, 'a'),
        .model_manifest = std::string(64, 'b'),
        .profile_manifest = std::string(64, 'c'),
        .instance_capacity = 1,
        .actor_capacity = 8,
    };
}

/// StartCommand 返回可安装 ticket 的 current assignment。
[[nodiscard]] ihomeland::sim::InstanceStartCommand StartCommand(
    const std::string& suffix) {
    auto command = ihomeland::sim::InstanceStartCommand{
        .start_request_id = "sctl_handshake_start_" + suffix,
        .assignment =
            {
                .personal_world_id =
                    "pworld_handshake_" + suffix,
                .world_instance_id =
                    "winst_handshake_" + suffix,
                .runtime_node_id = "rnode_handshake_test",
                .generation = 7,
                .fencing_token = 9,
                .fingerprint = "",
            },
        .mapping_generation = 11,
        .seed = 13,
        .config_identity = std::string(64, 'd'),
        .navigation_identity = std::string(64, 'e'),
        .physics_identity = std::string(64, 'f'),
        .actor_capacity = 8,
    };
    std::string material;
    const auto append = [&](const std::string& value) {
        if (!material.empty()) {
            material.push_back('\0');
        }
        material.append(value);
    };
    append(command.assignment.personal_world_id);
    append(command.assignment.world_instance_id);
    append(command.assignment.runtime_node_id);
    append(std::to_string(command.assignment.generation));
    append(std::to_string(command.assignment.fencing_token));
    command.assignment.fingerprint =
        ihomeland::sim::Sha256Text(material);
    return command;
}

/// TicketCommand 返回与 16-byte wire ID 一致的 installed ticket。
[[nodiscard]] ihomeland::sim::BattleTicketInstallCommand
TicketCommand(
    const ihomeland::sim::InstanceStartCommand& start,
    const ihomeland::sim::InstanceReadyReceipt& ready,
    const ihomeland::sim::CryptoProvider::Key32& proof_key,
    const std::uint64_t expires_at_unix_ms) {
    return {
        .install_request_id = "sctl_handshake_install_0001",
        .ticket_id =
            "btk1_AQIDBAUGBwgJCgsMDQ4PEA",
        .binding_fingerprint =
            ihomeland::sim::Sha256Text(
                "authenticated-handshake-binding"),
        .binding =
            {
                .player_id = "ply_handshake_0001",
                .session_id = "ses_handshake_0001",
                .session_epoch = 3,
                .role = "owner",
                .personal_world_id =
                    start.assignment.personal_world_id,
                .visit_session_id = "",
                .world_instance_id =
                    start.assignment.world_instance_id,
                .runtime_node_id =
                    start.assignment.runtime_node_id,
                .assignment_generation =
                    start.assignment.generation,
                .fencing_token =
                    start.assignment.fencing_token,
                .assignment_fingerprint =
                    start.assignment.fingerprint,
                .simulation_node_id =
                    "snode_handshake_test",
                .simulation_instance_id =
                    ready.simulation_instance_id,
                .mapping_generation =
                    start.mapping_generation,
                .target_revision = 5,
                .model_identity = std::string(64, 'b'),
                .profile_identity = std::string(64, 'c'),
                .config_identity = start.config_identity,
                .wire_identity = std::string(64, '1'),
                .actor_slot = 2,
                .advertised_host = "127.0.0.1",
                .advertised_port = 58445,
                .issue_id = "biss_handshake_0001",
                .issued_at_unix_ms = NowUnixMs - 1000,
                .expires_at_unix_ms = expires_at_unix_ms,
            },
        .proof_key = proof_key,
    };
}

/// Endpoint 返回 canonical IPv4-mapped loopback endpoint。
[[nodiscard]] ihomeland::sim::BattleRemoteEndpoint Endpoint() {
    std::array<std::uint8_t, 16> address{};
    address[10] = 0xff;
    address[11] = 0xff;
    address[12] = 127;
    address[15] = 1;
    return {.address = address, .port = 42000};
}

/// ClientFixture 保存客户端 ephemeral key 与 ClientHello bytes。
struct ClientFixture final {
    /// keys 是当前 transcript 的 ephemeral key pair。
    ihomeland::sim::CryptoProvider::X25519KeyPair keys;
    /// hello 是用于取得 cookie 的 canonical ClientHello。
    std::array<std::uint8_t, 88> hello;
};

/// MakeClient 生成与固定 ticket ID 对应的合法 ClientHello。
[[nodiscard]] ClientFixture MakeClient(
    ihomeland::sim::CryptoProvider& crypto) {
    ClientFixture client{
        .keys = crypto.GenerateX25519KeyPair(),
        .hello = {},
    };
    const std::array<std::uint8_t, 4> magic{
        'I', 'H', 'B', 'H'};
    std::ranges::copy(magic, client.hello.begin());
    client.hello[4] = 1;
    client.hello[5] = 1;
    std::ranges::copy(
        Sequence<16>(1),
        client.hello.begin() + 8);
    std::ranges::copy(
        Sequence<32>(0x21),
        client.hello.begin() + 24);
    std::ranges::copy(
        client.keys.public_key,
        client.hello.begin() + 56);
    return client;
}

/// WriteUint32BE 编码 ClientAuth cookie epoch。
void WriteUint32BE(
    const std::span<std::uint8_t, 4> output,
    const std::uint32_t value) {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// ReadUint32BE 解码 ServerAccept session generation。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::span<const std::uint8_t, 4> input) {
    return (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// IssueRetry 为 exact hello/source取得并解析 cookie。
[[nodiscard]] ihomeland::sim::BattleRetry IssueRetry(
    ihomeland::sim::BattleHandshakeCookieGate& gate,
    const ClientFixture& client,
    const ihomeland::sim::BattleRemoteEndpoint& remote) {
    const auto decision = gate.HandleClientHello(
        client.hello,
        remote,
        NowUnixMs);
    Require(decision.response_bytes == 28, "Retry missing");
    const auto retry =
        ihomeland::sim::BattleHandshakeCookieGate::ParseRetry(
            decision.response);
    Require(retry.has_value(), "Retry decode failed");
    return *retry;
}

/// BuildClientAuth 独立编码 transcript 并计算 proof HMAC。
[[nodiscard]] std::array<std::uint8_t, 140>
BuildClientAuth(
    ihomeland::sim::CryptoProvider& crypto,
    const ClientFixture& client,
    const ihomeland::sim::BattleRetry& retry,
    const ihomeland::sim::CryptoProvider::Key32& proof_key) {
    std::array<std::uint8_t, 140> request{};
    const std::array<std::uint8_t, 4> magic{
        'I', 'H', 'B', 'A'};
    std::ranges::copy(magic, request.begin());
    request[4] = 1;
    request[5] = 3;
    std::ranges::copy(
        std::span(client.hello).subspan(8, 80),
        request.begin() + 8);
    WriteUint32BE(
        std::span<std::uint8_t, 4>(
            request.data() + 88,
            4),
        retry.cookie_epoch);
    std::ranges::copy(
        retry.cookie,
        request.begin() + 92);
    std::vector<std::uint8_t> proof_material(
        ProofDomain.begin(),
        ProofDomain.end());
    proof_material.insert(
        proof_material.end(),
        request.begin(),
        request.begin() + 108);
    const auto proof = crypto.HmacSha256(
        proof_key,
        proof_material);
    std::ranges::copy(proof, request.begin() + 108);
    return request;
}

/// VerifyServerAccept 以独立client path解密并核对session参数。
void VerifyServerAccept(
    ihomeland::sim::CryptoProvider& crypto,
    const ClientFixture& client,
    const std::array<std::uint8_t, 140>& request,
    const std::array<std::uint8_t, 156>& response,
    const ihomeland::sim::CryptoProvider::Key32& proof_key,
    const std::string& binding_fingerprint,
    const std::uint32_t expected_generation) {
    Require(
        std::ranges::equal(
            std::array<std::uint8_t, 4>{'I', 'H', 'B', 'S'},
            std::span(response).first(4)),
        "ServerAccept magic drifted");
    ihomeland::sim::CryptoProvider::Key32 server_public{};
    std::ranges::copy(
        std::span(response).subspan(40, 32),
        server_public.begin());
    auto shared = crypto.X25519(
        client.keys.secret_scalar,
        server_public);
    const auto request_digest = crypto.Sha256(request);
    std::vector<std::uint8_t> schedule_info(
        ScheduleDomain.begin(),
        ScheduleDomain.end());
    schedule_info.insert(
        schedule_info.end(),
        request_digest.begin(),
        request_digest.end());
    schedule_info.insert(
        schedule_info.end(),
        response.begin() + 8,
        response.begin() + 72);
    const auto transcript_hash =
        crypto.Sha256(schedule_info);
    std::vector<std::uint8_t> hkdf_info(
        ScheduleDomain.begin(),
        ScheduleDomain.end());
    hkdf_info.insert(
        hkdf_info.end(),
        transcript_hash.begin(),
        transcript_hash.end());
    auto schedule = crypto.HkdfSha256(
        shared,
        proof_key,
        hkdf_info,
        76);
    ihomeland::sim::CryptoProvider::Key32 accept_key{};
    ihomeland::sim::CryptoProvider::Nonce12 accept_nonce{};
    std::ranges::copy_n(
        schedule.begin(),
        32,
        accept_key.begin());
    std::ranges::copy_n(
        schedule.begin() + 32,
        12,
        accept_nonce.begin());
    ihomeland::sim::CryptoProvider::Tag16 tag{};
    std::ranges::copy(
        std::span(response).last(16),
        tag.begin());
    auto plaintext =
        crypto.DecryptChaCha20Poly1305(
            accept_key,
            accept_nonce,
            std::span(response).first(76),
            std::span(response).subspan(76, 64),
            tag);
    Require(plaintext.has_value(), "ServerAccept AEAD failed");
    Require(
        ReadUint32BE(std::span(*plaintext).subspan<16, 4>()) ==
                expected_generation &&
            (*plaintext)[20] == 0 &&
            (*plaintext)[23] == 1 &&
            (*plaintext)[24] == 0 &&
            (*plaintext)[27] == 1 &&
            (*plaintext)[28] == 2 &&
            (*plaintext)[29] == 1,
        "ServerAccept parameters drifted");
    const auto expected_binding =
        ihomeland::sim::Sha256Text(
            "authenticated-handshake-binding");
    std::array<std::uint8_t, 32> expected_binding_bytes{};
    const auto nibble = [](const char value) {
        return static_cast<std::uint8_t>(
            value <= '9' ? value - '0' : value - 'a' + 10);
    };
    for (std::size_t index = 0;
         index < expected_binding_bytes.size();
         ++index) {
        expected_binding_bytes[index] =
            static_cast<std::uint8_t>(
                (nibble(expected_binding[index * 2]) << 4U) |
                nibble(expected_binding[index * 2 + 1]));
    }
    Require(
        binding_fingerprint == expected_binding &&
            std::ranges::equal(
                expected_binding_bytes,
                std::span(*plaintext).subspan(32, 32)),
        "test binding fingerprint drifted");
    crypto.SecureZero(shared);
    crypto.SecureZero(schedule);
    crypto.SecureZero(accept_key);
    crypto.SecureZero(accept_nonce);
    crypto.SecureZero(*plaintext);
}

/// TestProofAcceptReplayAndDrift 验证proof失败不消费、AEAD accept和exact replay。
void TestProofAcceptReplayAndDrift() {
    ihomeland::sim::CryptoProvider crypto;
    ihomeland::sim::SimulationNode node(NodeConfig());
    const auto start = StartCommand("success");
    const auto ready = node.Start(start);
    const auto proof_key = Sequence<32>(0x40);
    const auto ticket = TicketCommand(
        start,
        ready,
        proof_key,
        NowUnixMs + 60'000);
    static_cast<void>(
        node.InstallBattleTicket(ticket, NowUnixMs));
    ihomeland::sim::BattleCookieKeyRing keys(
        crypto,
        CookieEpoch,
        Sequence<32>(0x80));
    ihomeland::sim::BattleHandshakeCookieGate gate(
        keys,
        Sequence<16>(0xa0));
    ihomeland::sim::BattleAuthenticatedHandshake handshake(
        crypto,
        gate,
        node,
        "snode_handshake_test",
        "127.0.0.1",
        58445);
    auto client = MakeClient(crypto);
    const auto remote = Endpoint();
    const auto retry = IssueRetry(gate, client, remote);
    auto request = BuildClientAuth(
        crypto,
        client,
        retry,
        proof_key);
    auto forged = request;
    forged.back() ^= 1;
    Require(
        handshake.HandleClientAuth(
            forged,
            remote,
            NowUnixMs).outcome ==
            ihomeland::sim::BattleAuthenticatedHandshake::
                Outcome::Dropped,
        "forged proof was accepted");
    Require(
        node.BattleTicketStatus(
            ticket.ticket_id,
            ticket.binding_fingerprint,
            NowUnixMs).state ==
            ihomeland::sim::BattleTicketState::Installed,
        "forged proof consumed ticket");
    const auto accepted = handshake.HandleClientAuth(
        request,
        remote,
        NowUnixMs);
    Require(
        accepted.outcome ==
                ihomeland::sim::BattleAuthenticatedHandshake::
                    Outcome::Accepted &&
            accepted.response_bytes == 156,
        "valid ClientAuth was not accepted");
    VerifyServerAccept(
        crypto,
        client,
        request,
        accepted.response,
        proof_key,
        ticket.binding_fingerprint,
        accepted.battle_session_generation);
    const auto replay = handshake.HandleClientAuth(
        request,
        remote,
        NowUnixMs + 1);
    Require(
        replay.outcome ==
                ihomeland::sim::BattleAuthenticatedHandshake::
                    Outcome::Replayed &&
            replay.response == accepted.response &&
            replay.battle_session_generation ==
                accepted.battle_session_generation,
        "exact ClientAuth did not replay same accept");
    auto wrong_remote = remote;
    ++wrong_remote.port;
    Require(
        handshake.HandleClientAuth(
            request,
            wrong_remote,
            NowUnixMs + 1).outcome ==
            ihomeland::sim::BattleAuthenticatedHandshake::
                Outcome::Dropped,
        "ClientAuth replay from a different endpoint was accepted");
    auto drift = request;
    drift.back() ^= 1;
    Require(
        handshake.HandleClientAuth(
            drift,
            remote,
            NowUnixMs + 1).outcome ==
            ihomeland::sim::BattleAuthenticatedHandshake::
                Outcome::Dropped,
        "consumed ticket accepted transcript drift");
    auto successor_client = MakeClient(crypto);
    const auto successor_request = BuildClientAuth(
        crypto,
        successor_client,
        IssueRetry(
            gate,
            successor_client,
            remote),
        proof_key);
    Require(
        handshake.HandleClientAuth(
            successor_request,
            remote,
            NowUnixMs + 1).outcome ==
            ihomeland::sim::BattleAuthenticatedHandshake::
                Outcome::Dropped,
        "consumed ticket accepted a new valid transcript");
    crypto.SecureZero(
        successor_client.keys.secret_scalar);
    crypto.SecureZero(client.keys.secret_scalar);
}

/// TestConcurrentConsume 验证同ticket并发只建立一个generation。
void TestConcurrentConsume() {
    ihomeland::sim::CryptoProvider crypto;
    ihomeland::sim::SimulationNode node(NodeConfig());
    const auto start = StartCommand("race");
    const auto ready = node.Start(start);
    const auto proof_key = Sequence<32>(0x50);
    const auto ticket = TicketCommand(
        start,
        ready,
        proof_key,
        NowUnixMs + 60'000);
    static_cast<void>(
        node.InstallBattleTicket(ticket, NowUnixMs));
    ihomeland::sim::BattleCookieKeyRing keys(
        crypto,
        CookieEpoch,
        Sequence<32>(0x90));
    ihomeland::sim::BattleHandshakeCookieGate gate(
        keys,
        Sequence<16>(0xb0));
    ihomeland::sim::BattleAuthenticatedHandshake handshake(
        crypto,
        gate,
        node,
        "snode_handshake_test",
        "127.0.0.1",
        58445);
    auto client = MakeClient(crypto);
    const auto remote = Endpoint();
    const auto request = BuildClientAuth(
        crypto,
        client,
        IssueRetry(gate, client, remote),
        proof_key);
    std::array<
        ihomeland::sim::BattleAuthenticatedHandshake::Result,
        2> results;
    std::thread first([&] {
        results[0] = handshake.HandleClientAuth(
            request,
            remote,
            NowUnixMs);
    });
    std::thread second([&] {
        results[1] = handshake.HandleClientAuth(
            request,
            remote,
            NowUnixMs);
    });
    first.join();
    second.join();
    const auto accepted = std::ranges::count_if(
        results,
        [](const auto& result) {
            return result.outcome ==
                ihomeland::sim::BattleAuthenticatedHandshake::
                    Outcome::Accepted;
        });
    const auto replayed = std::ranges::count_if(
        results,
        [](const auto& result) {
            return result.outcome ==
                ihomeland::sim::BattleAuthenticatedHandshake::
                    Outcome::Replayed;
        });
    Require(
        accepted == 1 && replayed == 1 &&
            results[0].response == results[1].response,
        "ticket race created multiple sessions");
    crypto.SecureZero(client.keys.secret_scalar);
}

/// TestExpiryAndWrongTarget 验证expiry与错误node/endpoint均不消费。
void TestExpiryAndWrongTarget() {
    ihomeland::sim::CryptoProvider crypto;
    ihomeland::sim::SimulationNode node(NodeConfig());
    const auto start = StartCommand("target");
    const auto ready = node.Start(start);
    const auto proof_key = Sequence<32>(0x60);
    const auto ticket = TicketCommand(
        start,
        ready,
        proof_key,
        NowUnixMs + 10);
    static_cast<void>(
        node.InstallBattleTicket(ticket, NowUnixMs));
    ihomeland::sim::BattleCookieKeyRing keys(
        crypto,
        CookieEpoch,
        Sequence<32>(0xc0));
    ihomeland::sim::BattleHandshakeCookieGate gate(
        keys,
        Sequence<16>(0xd0));
    auto client = MakeClient(crypto);
    const auto remote = Endpoint();
    const auto request = BuildClientAuth(
        crypto,
        client,
        IssueRetry(gate, client, remote),
        proof_key);
    ihomeland::sim::BattleAuthenticatedHandshake wrong_endpoint(
        crypto,
        gate,
        node,
        "snode_handshake_test",
        "battle.invalid",
        58445);
    Require(
        wrong_endpoint.HandleClientAuth(
            request,
            remote,
            NowUnixMs).outcome ==
            ihomeland::sim::BattleAuthenticatedHandshake::
                Outcome::Dropped,
        "wrong advertised endpoint was accepted");
    ihomeland::sim::BattleAuthenticatedHandshake wrong_node(
        crypto,
        gate,
        node,
        "snode_wrong_target",
        "127.0.0.1",
        58445);
    Require(
        wrong_node.HandleClientAuth(
            request,
            remote,
            NowUnixMs).outcome ==
            ihomeland::sim::BattleAuthenticatedHandshake::
                Outcome::Dropped,
        "wrong node was accepted");
    ihomeland::sim::BattleAuthenticatedHandshake correct(
        crypto,
        gate,
        node,
        "snode_handshake_test",
        "127.0.0.1",
        58445);
    Require(
        correct.HandleClientAuth(
            request,
            remote,
            NowUnixMs + 10).outcome ==
            ihomeland::sim::BattleAuthenticatedHandshake::
                Outcome::Dropped,
        "expired ticket was accepted");
    Require(
        node.BattleTicketStatus(
            ticket.ticket_id,
            ticket.binding_fingerprint,
            NowUnixMs + 10).state ==
            ihomeland::sim::BattleTicketState::Expired,
        "expired ticket did not become terminal");

    ihomeland::sim::SimulationNode stopped_node(NodeConfig());
    const auto stopped_start = StartCommand("stopped_instance");
    const auto stopped_ready =
        stopped_node.Start(stopped_start);
    const auto stopped_ticket = TicketCommand(
        stopped_start,
        stopped_ready,
        proof_key,
        NowUnixMs + 60'000);
    static_cast<void>(
        stopped_node.InstallBattleTicket(
            stopped_ticket,
            NowUnixMs));
    stopped_node.Stop(
        stopped_start.assignment.world_instance_id,
        stopped_start.assignment.fingerprint,
        std::chrono::milliseconds(1000));
    ihomeland::sim::BattleAuthenticatedHandshake
        stopped_handshake(
            crypto,
            gate,
            stopped_node,
            "snode_handshake_test",
            "127.0.0.1",
            58445);
    auto stopped_client = MakeClient(crypto);
    const auto stopped_request = BuildClientAuth(
        crypto,
        stopped_client,
        IssueRetry(gate, stopped_client, remote),
        proof_key);
    Require(
        stopped_handshake.HandleClientAuth(
            stopped_request,
            remote,
            NowUnixMs).outcome ==
            ihomeland::sim::BattleAuthenticatedHandshake::
                Outcome::Dropped,
        "stopped SimulationInstance ticket was accepted");
    crypto.SecureZero(stopped_client.keys.secret_scalar);
    crypto.SecureZero(client.keys.secret_scalar);
}

}  // namespace

int main() {
    TestProofAcceptReplayAndDrift();
    TestConcurrentConsume();
    TestExpiryAndWrongTarget();
    return 0;
}
