#include "ihomeland/qualification/battle/protocol_client.hpp"

#include "ihomeland/battle/v1/battle.pb.h"
#include "ihomeland/sim/control/simulation_node.hpp"
#include "ihomeland/sim/core/sha256.hpp"
#include "ihomeland/sim/observability/battle_runtime_metrics.hpp"
#include "ihomeland/sim/transport/battle_transport_control.hpp"
#include "ihomeland/sim/transport/crypto_provider.hpp"
#include "ihomeland/sim/transport/battle_transport_runtime.hpp"
#include "ihomeland/sim/transport/kcp_adapter.hpp"
#include "ihomeland/sim/transport/secure_datagram.hpp"

#include <algorithm>
#include <array>
#include <chrono>
#include <cstdint>
#include <iostream>
#include <limits>
#include <ranges>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <vector>

namespace {

namespace client =
    ihomeland::qualification::battle;
namespace sim = ihomeland::sim;

constexpr std::uint64_t NowUnixMs = 1'700'000'000'000;
constexpr std::string_view ProofDomain =
    "ihomeland/battle/client-auth/v1";
constexpr std::string_view ProofKeyDerivationDomain =
    "ihomeland/battle-ticket/proof-key/v2";
constexpr std::string_view ScheduleDomain =
    "ihomeland/battle/session-schedule/v1";
constexpr std::size_t AcceptAadBytes = 76;
constexpr std::size_t AcceptPlaintextBytes = 64;
constexpr std::uint32_t Conversation = 0x01020304;
constexpr std::array<std::uint8_t, 4> ControlMagic{
    'I', 'H', 'B', 'C'};
constexpr std::uint8_t ControlVersion = 1;
constexpr std::size_t ControlEnvelopeBytes = 8;
constexpr std::size_t RebindRequestPayloadBytes = 34;
constexpr std::size_t RebindChallengePayloadBytes = 48;
constexpr std::size_t RebindCommittedPayloadBytes = 20;

/// ClientControlKind 独立冻结qualification client使用的control registry。
enum class ClientControlKind : std::uint8_t {
    /// RebindRequest 从current endpoint声明candidate。
    RebindRequest = 1,
    /// RebindChallenge 是server返回的address proof。
    RebindChallenge = 2,
    /// RebindConfirm 从candidate回显challenge。
    RebindConfirm = 3,
    /// RebindCommitted 确认next endpoint generation。
    RebindCommitted = 4,
    /// RekeyProposal 提交authenticated rollover nonce。
    RekeyProposal = 5,
    /// RekeyCommitted 确认next epoch。
    RekeyCommitted = 6,
    /// CloseRequest 请求终结session。
    CloseRequest = 7,
    /// CloseAcknowledged 是terminal response。
    CloseAcknowledged = 8,
};

/// Require 把失败统一转换为稳定 test error。
void Require(
    const bool condition,
    const std::string_view message) {
    if (!condition) {
        throw std::runtime_error(
            std::string(message));
    }
}

/// Sequence 生成公开且可复现的固定宽度 fixture。
template <std::size_t Size>
[[nodiscard]] std::array<std::uint8_t, Size>
Sequence(const std::uint8_t first) {
    std::array<std::uint8_t, Size> output{};
    for (std::size_t index = 0;
         index < output.size();
         ++index) {
        output[index] = static_cast<std::uint8_t>(
            first + index);
    }
    return output;
}

/// Bytes 序列化 lite Protobuf 为 byte vector。
template <typename Message>
[[nodiscard]] std::vector<std::uint8_t> Bytes(
    const Message& message) {
    const auto encoded = message.SerializeAsString();
    return {
        reinterpret_cast<const std::uint8_t*>(
            encoded.data()),
        reinterpret_cast<const std::uint8_t*>(
            encoded.data()) +
            encoded.size(),
    };
}

/// WriteUint16BE 编码 route length。
void WriteUint16BE(
    std::uint8_t* output,
    const std::uint16_t value) {
    output[0] = static_cast<std::uint8_t>(value >> 8U);
    output[1] = static_cast<std::uint8_t>(value);
}

/// WriteUint32BE 编码 handshake/route uint32。
void WriteUint32BE(
    std::uint8_t* output,
    const std::uint32_t value) {
    output[0] = static_cast<std::uint8_t>(value >> 24U);
    output[1] = static_cast<std::uint8_t>(value >> 16U);
    output[2] = static_cast<std::uint8_t>(value >> 8U);
    output[3] = static_cast<std::uint8_t>(value);
}

/// ReadUint32BE 解码control generation。
[[nodiscard]] std::uint32_t ReadUint32BE(
    const std::uint8_t* input) {
    return
        (static_cast<std::uint32_t>(input[0]) << 24U) |
        (static_cast<std::uint32_t>(input[1]) << 16U) |
        (static_cast<std::uint32_t>(input[2]) << 8U) |
        static_cast<std::uint32_t>(input[3]);
}

/// ReadUint64BE 解码 runtime route handle。
[[nodiscard]] std::uint64_t ReadUint64BE(
    const std::uint8_t* input) {
    std::uint64_t value = 0;
    for (std::size_t index = 0; index < 8; ++index) {
        value =
            (value << 8U) |
            static_cast<std::uint64_t>(input[index]);
    }
    return value;
}

/// WriteUint64BE 编码 route application sequence。
void WriteUint64BE(
    std::uint8_t* output,
    const std::uint64_t value) {
    for (std::size_t index = 0; index < 8; ++index) {
        output[index] = static_cast<std::uint8_t>(
            value >> ((7U - index) * 8U));
    }
}

/// EncodeControl 以独立实现构造closed transport-control plaintext。
[[nodiscard]] std::vector<std::uint8_t>
EncodeControl(
    const ClientControlKind kind,
    const std::span<const std::uint8_t> payload) {
    Require(
        !payload.empty() &&
            payload.size() <=
                std::numeric_limits<
                    std::uint16_t>::max(),
        "control fixture payload is invalid");
    std::vector<std::uint8_t> output(
        ControlEnvelopeBytes + payload.size());
    std::ranges::copy(
        ControlMagic,
        output.begin());
    output[4] = ControlVersion;
    output[5] =
        static_cast<std::uint8_t>(kind);
    WriteUint16BE(
        output.data() + 6,
        static_cast<std::uint16_t>(
            payload.size()));
    std::ranges::copy(
        payload,
        output.begin() + ControlEnvelopeBytes);
    return output;
}

/// RetryBytes 构造不依赖服务端 cookie adapter 的 fixed Retry。
[[nodiscard]] std::array<std::uint8_t, 28>
RetryBytes() {
    std::array<std::uint8_t, 28> output{};
    std::ranges::copy(
        std::array<std::uint8_t, 4>{
            'I', 'H', 'B', 'R'},
        output.begin());
    output[4] = 1;
    output[5] = 2;
    WriteUint32BE(output.data() + 8, 56'666'666);
    std::ranges::copy(
        Sequence<16>(0x70),
        output.begin() + 12);
    return output;
}

/// ServerAcceptFixture 保存独立 test server encoder 产生的 accept 与 seed。
struct ServerAcceptFixture final {
    /// bytes 是 canonical ServerAccept datagram。
    std::array<std::uint8_t, 156> bytes;
    /// session_seed 是 secure parity 的共同输入。
    std::array<std::uint8_t, 32> session_seed;
};

/// DeriveProofKey 独立冻结 public ticket material 的 proof-key/v2 派生。
[[nodiscard]] sim::CryptoProvider::Key32
DeriveProofKey(
    sim::CryptoProvider& crypto,
    const std::array<std::uint8_t, 16>& ticket_id,
    const sim::CryptoProvider::Key32& ticket_secret,
    const std::string_view domain =
        ProofKeyDerivationDomain) {
    const std::vector<std::uint8_t> info(
        domain.begin(),
        domain.end());
    auto derived = crypto.HkdfSha256(
        ticket_secret,
        ticket_id,
        info,
        32);
    Require(
        derived.size() == 32,
        "proof-key/v2 derivation length drifted");
    sim::CryptoProvider::Key32 proof_key{};
    std::ranges::copy(
        derived,
        proof_key.begin());
    crypto.SecureZero(derived);
    return proof_key;
}

/// VerifyClientAuthProof 独立验证 ClientAuth 是否由目标 ticket proof key 签发。
[[nodiscard]] bool VerifyClientAuthProof(
    sim::CryptoProvider& crypto,
    const std::span<const std::uint8_t> auth,
    const sim::CryptoProvider::Key32& proof_key) {
    constexpr std::size_t ProofOffset = 108;
    constexpr std::size_t ProofSize = 32;
    if (auth.size() < ProofOffset + ProofSize) {
        return false;
    }
    std::vector<std::uint8_t> proof_material(
        ProofDomain.begin(),
        ProofDomain.end());
    proof_material.insert(
        proof_material.end(),
        auth.begin(),
        auth.begin() + 108);
    auto expected_proof =
        crypto.HmacSha256(
            proof_key,
            proof_material);
    const auto valid = crypto.ConstantTimeEqual(
        expected_proof,
        auth.subspan(ProofOffset, ProofSize));
    crypto.SecureZero(expected_proof);
    crypto.SecureZero(proof_material);
    return valid;
}

/// BuildServerAccept 独立执行 proof verification、X25519/HKDF 与 AEAD seal。
[[nodiscard]] ServerAcceptFixture BuildServerAccept(
    sim::CryptoProvider& crypto,
    const std::span<const std::uint8_t> auth,
    const sim::CryptoProvider::Key32& proof_key,
    const sim::CryptoProvider::Key32& binding) {
    Require(
        VerifyClientAuthProof(
            crypto,
            auth,
            proof_key),
        "client auth proof mismatch");
    sim::CryptoProvider::Key32 client_public{};
    std::ranges::copy(
        auth.subspan(56, 32),
        client_public.begin());
    auto server_keys =
        crypto.GenerateX25519KeyPair();
    auto shared = crypto.X25519(
        server_keys.secret_scalar,
        client_public);
    const auto request_digest =
        crypto.Sha256(auth);
    const auto server_nonce =
        Sequence<32>(0x90);

    ServerAcceptFixture fixture{};
    std::ranges::copy(
        std::array<std::uint8_t, 4>{
            'I', 'H', 'B', 'S'},
        fixture.bytes.begin());
    fixture.bytes[4] = 1;
    fixture.bytes[5] = 4;
    std::ranges::copy(
        server_nonce,
        fixture.bytes.begin() + 8);
    std::ranges::copy(
        server_keys.public_key,
        fixture.bytes.begin() + 40);
    fixture.bytes[72] = 0;
    fixture.bytes[73] =
        static_cast<std::uint8_t>(
            AcceptPlaintextBytes);

    std::vector<std::uint8_t> schedule_material(
        ScheduleDomain.begin(),
        ScheduleDomain.end());
    schedule_material.insert(
        schedule_material.end(),
        request_digest.begin(),
        request_digest.end());
    schedule_material.insert(
        schedule_material.end(),
        fixture.bytes.begin() + 8,
        fixture.bytes.begin() + 72);
    const auto transcript_hash =
        crypto.Sha256(schedule_material);
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
    sim::CryptoProvider::Key32 accept_key{};
    sim::CryptoProvider::Nonce12 accept_nonce{};
    std::ranges::copy_n(
        schedule.begin(),
        accept_key.size(),
        accept_key.begin());
    std::ranges::copy_n(
        schedule.begin() + accept_key.size(),
        accept_nonce.size(),
        accept_nonce.begin());
    std::ranges::copy_n(
        schedule.begin() +
            accept_key.size() +
            accept_nonce.size(),
        fixture.session_seed.size(),
        fixture.session_seed.begin());

    std::array<std::uint8_t, AcceptPlaintextBytes>
        plaintext{};
    std::ranges::copy(
        Sequence<16>(0x30),
        plaintext.begin());
    WriteUint32BE(plaintext.data() + 16, 9);
    WriteUint32BE(plaintext.data() + 20, 1);
    WriteUint32BE(plaintext.data() + 24, 1);
    plaintext[28] = 3;
    plaintext[29] = 2;
    std::ranges::copy(
        binding,
        plaintext.begin() + 32);
    const auto encrypted =
        crypto.EncryptChaCha20Poly1305(
            accept_key,
            accept_nonce,
            std::span(fixture.bytes).first(
                AcceptAadBytes),
            plaintext);
    std::ranges::copy(
        encrypted.bytes,
        fixture.bytes.begin() + AcceptAadBytes);
    std::ranges::copy(
        encrypted.tag,
        fixture.bytes.end() -
            encrypted.tag.size());

    crypto.SecureZero(server_keys.secret_scalar);
    crypto.SecureZero(shared);
    crypto.SecureZero(schedule);
    crypto.SecureZero(accept_key);
    crypto.SecureZero(accept_nonce);
    crypto.SecureZero(plaintext);
    return fixture;
}

/// MakeCredential 生成 move-only stdin secret fixture。
[[nodiscard]] client::TicketCredential
MakeCredential(
    const sim::CryptoProvider::Key32& ticket_secret) {
    client::TicketCredential credential;
    credential.ticket_id = Sequence<16>(0x10);
    credential.ticket_secret = ticket_secret;
    credential.expires_at_unix_ms =
        NowUnixMs + 60'000;
    return credential;
}

/// LowerHex 把固定公开 bytes 编码为 control contract 使用的小写 identity。
template <std::size_t Size>
[[nodiscard]] std::string LowerHex(
    const std::array<std::uint8_t, Size>& bytes) {
    constexpr std::string_view Alphabet =
        "0123456789abcdef";
    std::string output(bytes.size() * 2U, '\0');
    for (std::size_t index = 0;
         index < bytes.size();
         ++index) {
        output[index * 2U] =
            Alphabet[bytes[index] >> 4U];
        output[index * 2U + 1U] =
            Alphabet[bytes[index] & 0x0fU];
    }
    return output;
}

/// RuntimeNodeConfig 冻结真实 runtime integration 使用的 node identity。
[[nodiscard]] sim::SimulationNodeConfig
RuntimeNodeConfig() {
    return {
        .simulation_node_id =
            "snode_protocol_runtime",
        .runtime_node_id =
            "rnode_protocol_runtime",
        .build_identity = std::string(64, 'a'),
        .model_manifest = std::string(64, 'b'),
        .profile_manifest = std::string(64, 'c'),
        .instance_capacity = 1,
        .actor_capacity = 8,
    };
}

/// RuntimeStartCommand 建立可被 runtime session 精确解析的 assignment。
[[nodiscard]] sim::InstanceStartCommand
RuntimeStartCommand() {
    auto command = sim::InstanceStartCommand{
        .start_request_id =
            "sctl_protocol_runtime_start",
        .assignment =
            {
                .personal_world_id =
                    "pworld_protocol_runtime",
                .world_instance_id =
                    "winst_protocol_runtime",
                .runtime_node_id =
                    "rnode_protocol_runtime",
                .generation = 7,
                .fencing_token = 9,
                .fingerprint = "",
            },
        .mapping_generation = 11,
        .seed = 13,
        .config_identity = std::string(64, 'd'),
        .navigation_identity =
            std::string(64, 'e'),
        .physics_identity = std::string(64, 'f'),
        .actor_capacity = 8,
    };
    std::string material;
    const auto append =
        [&](const std::string& value) {
            if (!material.empty()) {
                material.push_back('\0');
            }
            material.append(value);
        };
    append(command.assignment.personal_world_id);
    append(command.assignment.world_instance_id);
    append(command.assignment.runtime_node_id);
    append(std::to_string(
        command.assignment.generation));
    append(std::to_string(
        command.assignment.fencing_token));
    command.assignment.fingerprint =
        sim::Sha256Text(material);
    return command;
}

/// RuntimeTicketCommand 构造与独立 client credential byte-exact 对应的 ticket。
[[nodiscard]] sim::BattleTicketInstallCommand
RuntimeTicketCommand(
    const sim::InstanceStartCommand& start,
    const sim::InstanceReadyReceipt& ready,
    const sim::CryptoProvider::Key32& proof_key) {
    return {
        .install_request_id =
            "sctl_protocol_runtime_install",
        .ticket_id =
            "btk1_EBESExQVFhcYGRobHB0eHw",
        .binding_fingerprint =
            LowerHex(Sequence<32>(0xb0)),
        .binding =
            {
                .player_id =
                    "ply_protocol_runtime",
                .session_id =
                    "ses_protocol_runtime",
                .session_epoch = 3,
                .role = "owner",
                .personal_world_id =
                    start.assignment
                        .personal_world_id,
                .visit_session_id = "",
                .world_instance_id =
                    start.assignment
                        .world_instance_id,
                .runtime_node_id =
                    start.assignment.runtime_node_id,
                .assignment_generation =
                    start.assignment.generation,
                .fencing_token =
                    start.assignment.fencing_token,
                .assignment_fingerprint =
                    start.assignment.fingerprint,
                .simulation_node_id =
                    "snode_protocol_runtime",
                .simulation_instance_id =
                    ready.simulation_instance_id,
                .mapping_generation =
                    start.mapping_generation,
                .target_revision = 5,
                .model_identity =
                    std::string(64, 'b'),
                .profile_identity =
                    std::string(64, 'c'),
                .config_identity =
                    start.config_identity,
                .wire_identity =
                    std::string(64, '1'),
                .actor_slot = 2,
                .advertised_host = "127.0.0.1",
                .advertised_port = 58'445,
                .issue_id =
                    "biss_protocol_runtime",
                .issued_at_unix_ms =
                    NowUnixMs - 1'000,
                .expires_at_unix_ms =
                    NowUnixMs + 60'000,
            },
        .proof_key = proof_key,
    };
}

/// RuntimeEndpoint 返回 listener 已 canonicalize 的 IPv4-mapped source。
[[nodiscard]] sim::BattleRemoteEndpoint
RuntimeEndpoint() {
    std::array<std::uint8_t, 16> address{};
    address[10] = 0xff;
    address[11] = 0xff;
    address[12] = 127;
    address[15] = 1;
    return {
        .address = address,
        .port = 42'000,
    };
}

/// TestHandshake 验证独立 client 与独立 test encoder 的 transcript parity。
[[nodiscard]] ServerAcceptFixture TestHandshake() {
    sim::CryptoProvider crypto;
    constexpr std::array<std::uint8_t, 32>
        ExpectedProofKeyV2{
            0x40, 0xd6, 0x50, 0x68, 0xdd, 0x10, 0x8e, 0x2d,
            0x72, 0x7b, 0xe4, 0x77, 0x52, 0x9e, 0xc5, 0x72,
            0xdb, 0x2b, 0x6d, 0xf9, 0xaa, 0x1e, 0xdc, 0x14,
            0x95, 0xb1, 0xce, 0xfd, 0x72, 0xd9, 0xf5, 0x17};
    Require(
        DeriveProofKey(
            crypto,
            Sequence<16>(0x00),
            Sequence<32>(0x20)) ==
            ExpectedProofKeyV2,
        "proof-key/v2 public vector drifted");
    Require(
        DeriveProofKey(
            crypto,
            Sequence<16>(0x01),
            Sequence<32>(0x20)) !=
            ExpectedProofKeyV2,
        "wrong ticket ID matched proof-key/v2 output");
    Require(
        DeriveProofKey(
            crypto,
            Sequence<16>(0x00),
            Sequence<32>(0x21)) !=
            ExpectedProofKeyV2,
        "wrong ticket secret matched proof-key/v2 output");
    Require(
        DeriveProofKey(
            crypto,
            Sequence<16>(0x00),
            Sequence<32>(0x20),
            "ihomeland/battle-ticket/proof-key/v1") !=
            ExpectedProofKeyV2,
        "legacy proof domain matched proof-key/v2 output");
    const auto ticket_secret = Sequence<32>(0x40);
    const auto ticket_id = Sequence<16>(0x10);
    const auto proof_key = DeriveProofKey(
        crypto,
        ticket_id,
        ticket_secret);
    const auto binding = Sequence<32>(0xb0);
    auto source_credential =
        MakeCredential(ticket_secret);
    client::ProtocolHandshake handshake(
        std::move(source_credential),
        NowUnixMs);
    Require(
        std::ranges::all_of(
            source_credential.ticket_secret,
            [](const std::uint8_t value) {
                return value == 0;
            }),
        "moved credential retained ticket secret");
    const auto hello = handshake.ClientHello();
    Require(
        std::ranges::equal(
            std::array<std::uint8_t, 4>{
                'I', 'H', 'B', 'H'},
            std::span(hello).first(4)) &&
            hello[4] == 1 &&
            hello[5] == 1 &&
            std::ranges::equal(
                Sequence<16>(0x10),
                std::span(hello).subspan(8, 16)),
        "ClientHello layout drifted");
    const auto retry = RetryBytes();
    Require(
        client::ProtocolHandshake::
            MatchesRetryEnvelope(retry) &&
            !client::ProtocolHandshake::
                MatchesServerAcceptEnvelope(retry),
        "Retry envelope classifier drifted");
    auto stale_close_datagram =
        Sequence<73>(0x20);
    std::ranges::copy(
        std::array<std::uint8_t, 4>{
            'I', 'H', 'B', 'T'},
        stale_close_datagram.begin());
    Require(
        !client::ProtocolHandshake::
            MatchesRetryEnvelope(
                stale_close_datagram) &&
            !client::ProtocolHandshake::
                MatchesServerAcceptEnvelope(
                    stale_close_datagram),
        "stale secure close datagram matched handshake envelope");
    const auto auth = handshake.AcceptRetry(
        retry,
        NowUnixMs + 1);
    Require(
        auth.has_value() &&
            std::ranges::equal(
                std::array<std::uint8_t, 4>{
                    'I', 'H', 'B', 'A'},
                std::span(*auth).first(4)),
        "Retry did not produce ClientAuth");
    auto accept = BuildServerAccept(
        crypto,
        *auth,
        proof_key,
        binding);
    Require(
        client::ProtocolHandshake::
            MatchesServerAcceptEnvelope(
                accept.bytes) &&
            !client::ProtocolHandshake::
                MatchesRetryEnvelope(
                    accept.bytes),
        "ServerAccept envelope classifier drifted");
    const auto parameters =
        handshake.AcceptServer(
            accept.bytes,
            NowUnixMs + 2);
    Require(
        parameters.has_value() &&
            parameters->session_seed ==
                accept.session_seed &&
            parameters->battle_session_generation == 9 &&
            parameters->key_epoch == 1 &&
            parameters->endpoint_generation == 1 &&
            parameters->actor_slot == 3 &&
            parameters->role == 2 &&
            parameters->binding_fingerprint == binding,
        "ServerAccept parameters drifted");

    auto wrong_ticket_credential =
        MakeCredential(ticket_secret);
    wrong_ticket_credential.ticket_id[0] ^= 0xff;
    client::ProtocolHandshake wrong_ticket(
        std::move(wrong_ticket_credential),
        NowUnixMs);
    const auto wrong_ticket_auth =
        wrong_ticket.AcceptRetry(
            RetryBytes(),
            NowUnixMs + 1);
    Require(
        wrong_ticket_auth.has_value(),
        "wrong-ticket retry auth should be constructible");
    Require(
        !VerifyClientAuthProof(
            crypto,
            *wrong_ticket_auth,
            proof_key),
        "wrong ticket ID authenticated ClientAuth");

    auto wrong_secret_material = ticket_secret;
    wrong_secret_material[0] ^= 0xff;
    client::ProtocolHandshake wrong_secret(
        MakeCredential(wrong_secret_material),
        NowUnixMs);
    crypto.SecureZero(wrong_secret_material);
    const auto wrong_secret_auth =
        wrong_secret.AcceptRetry(
            RetryBytes(),
            NowUnixMs + 1);
    Require(
        wrong_secret_auth.has_value(),
        "wrong-secret retry auth should be constructible");
    Require(
        !VerifyClientAuthProof(
            crypto,
            *wrong_secret_auth,
            proof_key),
        "wrong ticket secret authenticated ClientAuth");

    client::ProtocolHandshake tampered(
        MakeCredential(ticket_secret),
        NowUnixMs);
    const auto tampered_auth =
        tampered.AcceptRetry(
            RetryBytes(),
            NowUnixMs + 1);
    auto tampered_accept = BuildServerAccept(
        crypto,
        *tampered_auth,
        proof_key,
        binding);
    tampered_accept.bytes.back() ^= 1;
    Require(
        !tampered.AcceptServer(
             tampered_accept.bytes,
             NowUnixMs + 2)
             .has_value(),
        "tampered ServerAccept tag was accepted");

    client::ProtocolHandshake expired(
        MakeCredential(ticket_secret),
        NowUnixMs);
    Require(
        !expired.AcceptRetry(
             RetryBytes(),
             NowUnixMs +
                 client::ProtocolHandshake::
                     HandshakeTimeoutMilliseconds)
             .has_value(),
        "expired Retry was accepted");
    return accept;
}

/// TestRuntimeComposition 验证独立 client 穿过真实 runtime 的 secure raw/KCP/control。
void TestRuntimeComposition() {
    sim::CryptoProvider crypto;
    const auto ticket_secret =
        Sequence<32>(0x40);
    auto proof_key = DeriveProofKey(
        crypto,
        Sequence<16>(0x10),
        ticket_secret);
    sim::SimulationNode node(
        RuntimeNodeConfig());
    const auto start = RuntimeStartCommand();
    const auto ready = node.Start(start);
    const auto ticket = RuntimeTicketCommand(
        start,
        ready,
        proof_key);
    static_cast<void>(
        node.InstallBattleTicket(
            ticket,
            NowUnixMs));
    crypto.SecureZero(proof_key);

    std::vector<std::vector<std::uint8_t>>
        responses;
    std::vector<sim::BattleRemoteEndpoint>
        response_targets;
    bool replication_enabled = false;
    std::uint64_t runtime_kcp_messages = 0;
    sim::BattleRuntimeMetrics runtime_metrics;
    sim::BattleTransportRuntime runtime(
        {
            .simulation_node_id =
                "snode_protocol_runtime",
            .advertised_host = "127.0.0.1",
            .advertised_port = 58'445,
            .listener_identity =
                Sequence<16>(0xa0),
            .maximum_sessions = 8,
        },
        node,
        [&](const std::span<const std::uint8_t>
                datagram,
            const sim::BattleRemoteEndpoint&
                target) {
            responses.emplace_back(
                datagram.begin(),
                datagram.end());
            response_targets.push_back(target);
            return sim::BattleUdpSendDisposition::
                Queued;
        },
        [&](const sim::BattleSessionContext&
                context,
            const std::uint64_t now_unix_ms) {
            return node.BattleRawContext(
                context,
                now_unix_ms);
        },
        [&](const sim::BattleSessionContext&
                context) {
            return node.ResolveBattleCommandIngress(
                context);
        },
        [&](const sim::BattleSessionContext&
                context) {
            if (!replication_enabled) {
                return std::optional<
                    sim::BattleReplicationProjection>{};
            }
            return std::optional{
                sim::BattleReplicationProjection{
                    .server_tick = 20,
                    .acknowledgement = {
                        .actor_id =
                            context.Actor().actor_id,
                        .mapping_generation =
                            context.MappingGeneration(),
                        .last_processed_input_tick = 1,
                    },
                    .states = {{
                        .actor_id =
                            context.Actor().actor_id,
                        .x_mm = 0,
                        .y_mm = 0,
                        .z_mm = 0,
                        .health_scaled = 1,
                        .phase = 1,
                        .alive = true,
                    }},
                },
            };
        },
        [&](const sim::BattleSessionContext&,
            const sim::BattleKcpMessageView&) {
            ++runtime_kcp_messages;
        },
        [&](const sim::BattleSessionContext&
                context) {
            return node.BattleSessionCurrent(
                context);
        },
        NowUnixMs,
        &runtime_metrics);
    client::ProtocolHandshake handshake(
        MakeCredential(ticket_secret),
        NowUnixMs);
    const auto remote = RuntimeEndpoint();
    const auto hello = handshake.ClientHello();
    Require(
        runtime.EnqueueDatagram(
            {},
            remote,
            NowUnixMs) ==
                sim::
                    BattleTransportEnqueueDisposition::
                        Invalid &&
            runtime_metrics.Snapshot()
                    .rejected_packets == 1,
        "runtime enqueue rejection was silent");
    Require(
        runtime.HandleDatagram(
            hello,
            remote,
            NowUnixMs) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        RetryQueued &&
            responses.size() == 1,
        "real runtime did not issue Retry");
    const auto auth = handshake.AcceptRetry(
        responses.back(),
        NowUnixMs + 1);
    Require(
        auth.has_value(),
        "real runtime Retry did not produce ClientAuth");
    auto forged_auth = *auth;
    forged_auth.back() ^= 0x01U;
    Require(
        runtime.HandleDatagram(
                forged_auth,
                remote,
                NowUnixMs + 1) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        Dropped &&
            responses.size() == 1 &&
            runtime.ActiveSessionCount() == 0 &&
            runtime_metrics.Snapshot()
                    .rejected_packets == 2,
        "forged handshake proof rejection was silent");
    Require(
        auth.has_value() &&
            runtime.HandleDatagram(
                *auth,
                remote,
                NowUnixMs + 1) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        SessionAccepted &&
            responses.size() == 2 &&
            runtime.ActiveSessionCount() == 1,
        "real runtime did not publish session");
    auto parameters = handshake.AcceptServer(
        responses.back(),
        NowUnixMs + 2);
    Require(
        parameters.has_value() &&
            parameters->actor_slot == 2 &&
            parameters->role == 1 &&
            parameters->binding_fingerprint ==
                Sequence<32>(0xb0),
        "independent client rejected runtime accept");
    const auto accept_bytes = responses.back();
    Require(
        runtime.HandleDatagram(
            *auth,
            remote,
            NowUnixMs + 2) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        AcceptReplayed &&
            responses.size() == 3 &&
            responses.back() == accept_bytes,
        "runtime did not replay byte-identical accept");

    const auto session_digest =
        crypto.Sha256(parameters->session_id);
    client::SecureIdentity identity{
        .battle_session_generation =
            parameters->
                battle_session_generation,
        .endpoint_generation =
            parameters->endpoint_generation,
    };
    std::ranges::copy_n(
        session_digest.begin(),
        identity.session_id_digest.size(),
        identity.session_id_digest.begin());
    std::ranges::copy_n(
        parameters->binding_fingerprint.begin(),
        identity.binding_discriminator.size(),
        identity.binding_discriminator.begin());
    client::ProtocolSecureChannel channel(
        parameters->session_seed,
        identity,
        parameters->key_epoch,
        NowUnixMs + 2);
    crypto.SecureZero(
        parameters->session_seed);

    ihomeland::battle::v1::BattleInputBundle
        bundle;
    bundle.set_newest_input_tick(1);
    bundle.set_latest_observed_server_tick(1);
    auto* command = bundle.add_commands();
    command->set_command_sequence(1);
    command->set_kind(
        ihomeland::battle::v1::
            BATTLE_INPUT_KIND_JUMP);
    client::ProtocolRawLane raw;
    const auto plaintext =
        raw.EncodeInputBundle(
            1,
            Bytes(bundle));
    const auto sealed = channel.Seal(
        client::PacketKind::Raw,
        plaintext,
        NowUnixMs + 3);
    Require(
        sealed.has_value() &&
            runtime.HandleDatagram(
                *sealed,
                remote,
                NowUnixMs + 3) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        SecureDispatched,
        "authenticated raw input did not reach runtime");

    auto candidate = remote;
    ++candidate.port;
    std::array<
        std::uint8_t,
        RebindRequestPayloadBytes>
        rebind_request{};
    std::ranges::copy(
        candidate.address,
        rebind_request.begin());
    WriteUint16BE(
        rebind_request.data() +
            candidate.address.size(),
        candidate.port);
    const auto rebind_nonce =
        Sequence<16>(0x50);
    std::ranges::copy(
        rebind_nonce,
        rebind_request.begin() +
            candidate.address.size() +
            sizeof(candidate.port));
    const auto rebind_plaintext = EncodeControl(
        ClientControlKind::RebindRequest,
        rebind_request);
    const auto rebind = channel.Seal(
        client::PacketKind::Control,
        rebind_plaintext,
        NowUnixMs + 4);
    Require(
        rebind.has_value() &&
            runtime.HandleDatagram(
                *rebind,
                remote,
                NowUnixMs + 4) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        SecureDispatched &&
            responses.size() == 4 &&
            response_targets.back().address ==
                candidate.address &&
            response_targets.back().port ==
                candidate.port,
        "runtime did not route rebind challenge to candidate");
    const auto challenge = channel.Open(
        responses.back(),
        NowUnixMs + 4);
    Require(
        challenge.disposition ==
                client::OpenDisposition::Accepted &&
            challenge.kind ==
                client::PacketKind::Control &&
            challenge.plaintext.size() ==
                ControlEnvelopeBytes +
                RebindChallengePayloadBytes &&
            challenge.plaintext[5] ==
                static_cast<std::uint8_t>(
                    ClientControlKind::
                        RebindChallenge),
        "independent client rejected runtime rebind challenge");
    auto confirm_plaintext =
        challenge.plaintext;
    confirm_plaintext[5] =
        static_cast<std::uint8_t>(
            ClientControlKind::RebindConfirm);
    const auto confirm = channel.Seal(
        client::PacketKind::Control,
        confirm_plaintext,
        NowUnixMs + 5);
    Require(
        confirm.has_value() &&
            runtime.HandleDatagram(
                *confirm,
                candidate,
                NowUnixMs + 5) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        SecureDispatched &&
            responses.size() == 5,
        "runtime rejected authenticated candidate confirm");
    const auto rebind_committed = channel.Open(
        responses.back(),
        NowUnixMs + 5);
    Require(
        rebind_committed.disposition ==
                client::OpenDisposition::Accepted &&
            rebind_committed.plaintext.size() ==
                ControlEnvelopeBytes +
                RebindCommittedPayloadBytes &&
            rebind_committed.plaintext[5] ==
                static_cast<std::uint8_t>(
                    ClientControlKind::
                        RebindCommitted),
        "independent client rejected rebind commit ack");
    const auto next_endpoint_generation =
        ReadUint32BE(
            rebind_committed.plaintext.data() +
            ControlEnvelopeBytes);
    Require(
        channel.CommitEndpointGeneration(
            parameters->endpoint_generation,
            next_endpoint_generation) &&
            runtime.HandleDatagram(
                *sealed,
                remote,
                NowUnixMs + 6) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        Dropped,
        "rebind did not retire old endpoint");
    ihomeland::battle::v1::BattleProbe
        rebound_probe;
    rebound_probe.set_probe_sequence(1);
    rebound_probe.set_latest_snapshot_sequence(1);
    rebound_probe.set_client_monotonic_time_us(1);
    const auto rebound_plaintext =
        raw.EncodeProbe(
            2,
            Bytes(rebound_probe));
    const auto rebound_raw = channel.Seal(
        client::PacketKind::Raw,
        rebound_plaintext,
        NowUnixMs + 7);
    Require(
        rebound_raw.has_value() &&
            runtime.HandleDatagram(
                *rebound_raw,
                candidate,
                NowUnixMs + 7) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        SecureDispatched,
        "rebind reset raw lane or secure sequence");

    auto rekey_nonce = Sequence<32>(0x70);
    const auto rekey_plaintext = EncodeControl(
        ClientControlKind::RekeyProposal,
        rekey_nonce);
    const auto rekey = channel.Seal(
        client::PacketKind::Control,
        rekey_plaintext,
        NowUnixMs + 8);
    Require(
        rekey.has_value() &&
            runtime.HandleDatagram(
                *rekey,
                candidate,
                NowUnixMs + 8) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        SecureDispatched &&
            responses.size() == 6,
        "runtime did not commit authenticated rekey");
    const auto rekey_committed = channel.Open(
        responses.back(),
        NowUnixMs + 8);
    Require(
        rekey_committed.disposition ==
                client::OpenDisposition::Accepted &&
            rekey_committed.plaintext[5] ==
                static_cast<std::uint8_t>(
                    ClientControlKind::
                        RekeyCommitted) &&
            std::ranges::equal(
                rekey_nonce,
                std::span(
                    rekey_committed.plaintext)
                    .subspan(
                        ControlEnvelopeBytes)) &&
            channel.CommitRollover(
                rekey_nonce,
                parameters->key_epoch + 1U,
                NowUnixMs + 8),
        "client/server rekey acknowledgement diverged");
    crypto.SecureZero(rekey_nonce);
    rebound_probe.set_probe_sequence(2);
    rebound_probe.set_client_monotonic_time_us(2);
    const auto rekeyed_plaintext =
        raw.EncodeProbe(
            3,
            Bytes(rebound_probe));
    const auto rekeyed_raw = channel.Seal(
        client::PacketKind::Raw,
        rekeyed_plaintext,
        NowUnixMs + 9);
    Require(
        rekeyed_raw.has_value() &&
            runtime.HandleDatagram(
                *rekeyed_raw,
                candidate,
                NowUnixMs + 9) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        SecureDispatched,
        "rekey reset raw lane or diverged epoch");

    responses.clear();
    response_targets.clear();
    replication_enabled = true;
    const auto route_handle =
        ReadUint64BE(
            identity.session_id_digest.data());
    const auto conversation =
        static_cast<std::uint32_t>(route_handle) != 0
            ? static_cast<std::uint32_t>(
                  route_handle)
            : static_cast<std::uint32_t>(
                  route_handle >> 32U);
    client::ProtocolKcpLane runtime_kcp(
        conversation);
    ihomeland::battle::v1::BattleResyncRequest
        resync_request;
    resync_request.set_request_sequence(1);
    resync_request.set_latest_server_tick(20);
    resync_request.set_missing_baseline_id(1);
    resync_request.set_latest_snapshot_sequence(1);
    resync_request.set_reason(
        ihomeland::battle::v1::
            BATTLE_RESYNC_REASON_MISSING_BASELINE);
    Require(
        runtime_kcp.QueueResync(
            1,
            Bytes(resync_request),
            NowUnixMs + 10),
        "runtime KCP request did not queue");
    runtime_kcp.Update(NowUnixMs + 10);
    runtime_kcp.Update(
        NowUnixMs +
        10 +
        client::ProtocolKcpLane::
            UpdateIntervalMilliseconds);
    const auto request_segments =
        runtime_kcp.TakeSegments();
    Require(
        !request_segments.empty(),
        "runtime KCP request did not emit");
    for (const auto& segment : request_segments) {
        const auto request_datagram = channel.Seal(
            client::PacketKind::Kcp,
            segment,
            NowUnixMs + 20);
        Require(
            request_datagram.has_value() &&
                runtime.HandleDatagram(
                    *request_datagram,
                    candidate,
                    NowUnixMs + 20) ==
                    sim::
                        BattleTransportRuntimeDisposition::
                            SecureDispatched,
            "runtime rejected secure KCP request");
    }
    std::size_t secure_kcp_responses = 0;
    for (const auto& response : responses) {
        const auto opened = channel.Open(
            response,
            NowUnixMs + 21);
        Require(
            opened.disposition ==
                client::OpenDisposition::Accepted,
            "client rejected runtime secure response");
        if (opened.kind !=
            client::PacketKind::Kcp) {
            continue;
        }
        ++secure_kcp_responses;
        Require(
            runtime_kcp.Input(
                opened.plaintext,
                NowUnixMs + 21),
            "client rejected runtime KCP ACK/response");
    }
    runtime_kcp.Update(NowUnixMs + 30);
    const auto received_messages =
        runtime_kcp.TakeMessages();
    const auto runtime_kcp_status =
        runtime_kcp.Status();
    Require(
        secure_kcp_responses != 0 &&
            runtime_kcp_messages == 1 &&
            runtime_kcp_status.input_ack_commands != 0 &&
            runtime_kcp_status.reconciled_messages == 1 &&
            runtime_kcp_status.inflight_messages == 0 &&
            runtime_kcp_status.waiting_segments == 0 &&
            received_messages.size() == 1 &&
            received_messages.front().message_id ==
                3'007 &&
            !runtime_kcp.Closed(),
        "runtime secure KCP ACK/reconciliation contract drifted");
    for (const auto& segment :
         runtime_kcp.TakeSegments()) {
        const auto acknowledgement = channel.Seal(
            client::PacketKind::Kcp,
            segment,
            NowUnixMs + 30);
        Require(
            acknowledgement.has_value() &&
                runtime.HandleDatagram(
                    *acknowledgement,
                    candidate,
                    NowUnixMs + 30) ==
                    sim::
                        BattleTransportRuntimeDisposition::
                            SecureDispatched,
            "runtime rejected client KCP response ACK");
    }

    ihomeland::battle::v1::BattleResyncRequest
        queued_resync_request;
    queued_resync_request.set_request_sequence(2);
    queued_resync_request.set_latest_server_tick(20);
    queued_resync_request.set_missing_baseline_id(1);
    queued_resync_request.set_latest_snapshot_sequence(1);
    queued_resync_request.set_reason(
        ihomeland::battle::v1::
            BATTLE_RESYNC_REASON_MISSING_BASELINE);
    Require(
        runtime_kcp.QueueResync(
            2,
            Bytes(queued_resync_request),
            NowUnixMs + 40),
        "queued runtime KCP request did not queue");
    runtime_kcp.Update(NowUnixMs + 40);
    runtime_kcp.Update(
        NowUnixMs +
        40 +
        client::ProtocolKcpLane::
            UpdateIntervalMilliseconds);
    std::vector<std::vector<std::uint8_t>>
        queued_kcp_datagrams;
    for (const auto& segment :
         runtime_kcp.TakeSegments()) {
        auto datagram = channel.Seal(
            client::PacketKind::Kcp,
            segment,
            NowUnixMs + 100);
        Require(
            datagram.has_value(),
            "queued runtime KCP request seal failed");
        queued_kcp_datagrams.push_back(
            std::move(*datagram));
    }
    Require(
        !queued_kcp_datagrams.empty(),
        "queued runtime KCP request did not emit");

    rebound_probe.set_probe_sequence(3);
    rebound_probe.set_client_monotonic_time_us(3);
    const auto later_raw_plaintext =
        raw.EncodeProbe(
            4,
            Bytes(rebound_probe));
    const auto later_raw = channel.Seal(
        client::PacketKind::Raw,
        later_raw_plaintext,
        NowUnixMs + 200);
    Require(
        later_raw.has_value() &&
            runtime.HandleDatagram(
                *later_raw,
                candidate,
                NowUnixMs + 200) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        SecureDispatched,
        "runtime clock advance fixture was rejected");
    responses.clear();
    response_targets.clear();
    for (const auto& datagram :
         queued_kcp_datagrams) {
        Require(
            runtime.HandleDatagram(
                datagram,
                candidate,
                NowUnixMs + 100) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        SecureDispatched,
            "queued KCP receive timestamp rolled runtime clock back");
    }
    rebound_probe.set_probe_sequence(4);
    rebound_probe.set_client_monotonic_time_us(4);
    const auto flush_raw_plaintext =
        raw.EncodeProbe(
            5,
            Bytes(rebound_probe));
    const auto flush_raw = channel.Seal(
        client::PacketKind::Raw,
        flush_raw_plaintext,
        NowUnixMs + 210);
    Require(
        flush_raw.has_value() &&
            runtime.HandleDatagram(
                *flush_raw,
                candidate,
                NowUnixMs + 210) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        SecureDispatched,
        "runtime did not flush accepted queued KCP input");
    std::size_t queued_secure_kcp_responses = 0;
    for (const auto& response : responses) {
        const auto opened = channel.Open(
            response,
            NowUnixMs + 211);
        Require(
            opened.disposition ==
                client::OpenDisposition::Accepted,
            "client rejected queued runtime secure response");
        if (opened.kind !=
            client::PacketKind::Kcp) {
            continue;
        }
        ++queued_secure_kcp_responses;
        Require(
            runtime_kcp.Input(
                opened.plaintext,
                NowUnixMs + 211),
            "client rejected queued runtime KCP ACK/response");
    }
    runtime_kcp.Update(NowUnixMs + 211);
    const auto queued_runtime_kcp_status =
        runtime_kcp.Status();
    Require(
        queued_secure_kcp_responses != 0 &&
            runtime_kcp_messages == 2 &&
            queued_runtime_kcp_status
                    .reconciled_messages == 2 &&
            queued_runtime_kcp_status
                    .inflight_messages == 0 &&
            queued_runtime_kcp_status
                    .waiting_segments == 0 &&
            !runtime_kcp.Closed(),
        "queued runtime KCP clock/reconciliation contract drifted");
    replication_enabled = false;
    responses.clear();
    response_targets.clear();

    const std::array<std::uint8_t, 1>
        close_payload{1};
    const auto close_plaintext = EncodeControl(
        ClientControlKind::CloseRequest,
        close_payload);
    const auto close = channel.Seal(
        client::PacketKind::Control,
        close_plaintext,
        NowUnixMs + 220);
    Require(
        close.has_value() &&
            runtime.HandleDatagram(
                *close,
                candidate,
                NowUnixMs + 220) ==
                sim::
                    BattleTransportRuntimeDisposition::
                        SessionClosed &&
            runtime.ActiveSessionCount() == 0 &&
            responses.size() ==
                sim::BattleTransportControl::
                    CloseAcknowledgementCopies &&
            runtime_metrics.Snapshot()
                    .close_normal == 1,
        "authenticated close did not destroy runtime session");
    for (const auto& response : responses) {
        const auto close_acknowledged =
            channel.Open(
                response,
                NowUnixMs + 220);
        Require(
            close_acknowledged.disposition ==
                    client::OpenDisposition::Accepted &&
                close_acknowledged.plaintext[5] ==
                    static_cast<std::uint8_t>(
                        ClientControlKind::
                            CloseAcknowledged),
            "runtime emitted an invalid redundant close acknowledgement");
    }
    Require(
        runtime.HandleDatagram(
            *rekeyed_raw,
            candidate,
            NowUnixMs + 221) ==
            sim::
                BattleTransportRuntimeDisposition::
                    Dropped,
        "closed runtime session remained routable");
    runtime.Stop();
    Require(
        runtime.HandleDatagram(
            hello,
            remote,
            NowUnixMs + 42) ==
            sim::
                BattleTransportRuntimeDisposition::
                    Stopped &&
            runtime.EnqueueDatagram(
                hello,
                remote,
                NowUnixMs + 42) ==
                sim::
                    BattleTransportEnqueueDisposition::
                        Stopped &&
            runtime_metrics.Snapshot()
                    .dropped_packets == 1,
        "stopped runtime accepted ingress");
    node.BeginShutdown(
        std::chrono::milliseconds(1'000));
}

/// TestSnapshotCadence 验证首次、周期与 resync 强制 full baseline。
void TestSnapshotCadence() {
    sim::BattleSnapshotCadence cadence;
    const auto initial = cadence.Next(1, false);
    Require(
        initial.has_value() &&
            initial->snapshot_sequence == 1 &&
            initial->baseline_id == 1 &&
            initial->full,
        "snapshot cadence did not start with full baseline");
    Require(
        !cadence.Next(2, false).has_value(),
        "snapshot cadence ignored the committed Tick interval");
    for (std::uint64_t sequence = 2;
         sequence <=
         sim::BattleSnapshotCadence::
             FullBaselineIntervalSnapshots;
         ++sequence) {
        const auto server_tick =
            1 + (sequence - 1) *
                sim::BattleSnapshotCadence::
                    SnapshotIntervalTicks;
        const auto delta =
            cadence.Next(server_tick, false);
        Require(
            delta.has_value() &&
                delta->snapshot_sequence == sequence &&
                delta->baseline_id == 1 &&
                !delta->full,
            "snapshot cadence emitted an early periodic baseline");
    }
    const auto periodic = cadence.Next(21, false);
    Require(
        periodic.has_value() &&
            periodic->snapshot_sequence == 11 &&
            periodic->baseline_id == 11 &&
            periodic->full,
        "snapshot cadence missed the periodic full baseline");
    const auto forced = cadence.Next(22, true);
    Require(
        forced.has_value() &&
            forced->snapshot_sequence == 12 &&
            forced->baseline_id == 12 &&
            forced->full,
        "snapshot cadence missed the resync full baseline");
    Require(
        cadence.BeginRecovery(22),
        "snapshot cadence rejected a committed recovery window");
    const auto successor = cadence.Next(24, false);
    Require(
        successor.has_value() &&
            successor->snapshot_sequence == 13 &&
            successor->baseline_id == 12 &&
            !successor->full,
        "snapshot cadence did not retain the forced baseline");
    for (std::uint64_t server_tick = 26;
         server_tick < 32;
         server_tick +=
             sim::BattleSnapshotCadence::
                 SnapshotIntervalTicks) {
        const auto recovery_delta =
            cadence.Next(server_tick, false);
        Require(
            recovery_delta.has_value() &&
                !recovery_delta->full,
            "snapshot cadence emitted recovery full before its rate limit");
    }
    const auto recovery_full =
        cadence.Next(32, false);
    Require(
        recovery_full.has_value() &&
            recovery_full->full &&
            recovery_full->baseline_id ==
                recovery_full->snapshot_sequence,
        "snapshot cadence missed bounded recovery redundancy");
    const auto repeated_resync =
        cadence.Next(34, true);
    Require(
        repeated_resync.has_value() &&
            repeated_resync->full &&
            cadence.BeginRecovery(34),
        "snapshot cadence rejected a repeated resync");
    for (std::uint64_t server_tick = 36;
         server_tick <= 52;
         server_tick +=
             sim::BattleSnapshotCadence::
                 SnapshotIntervalTicks) {
        const auto publication =
            cadence.Next(server_tick, false);
        Require(
            publication.has_value() &&
                publication->full ==
                    (server_tick == 44),
            "snapshot cadence extended or violated its bounded recovery schedule");
    }
    constexpr std::uint64_t LateCommittedTick = 101;
    sim::BattleSnapshotCadence delayed;
    const auto delayed_initial =
        delayed.Next(1, false);
    const auto delayed_publication =
        delayed.Next(LateCommittedTick, false);
    Require(
        delayed_initial.has_value() &&
            delayed_publication.has_value() &&
            delayed_publication->snapshot_sequence == 2 &&
            !delayed.Next(
                 LateCommittedTick,
                 false)
                 .has_value(),
        "snapshot cadence emitted a catch-up burst");
    const auto same_tick_resync =
        delayed.Next(LateCommittedTick, true);
    Require(
        same_tick_resync.has_value() &&
            same_tick_resync->snapshot_sequence == 3 &&
            same_tick_resync->baseline_id == 3 &&
            same_tick_resync->full,
        "snapshot cadence rejected a same-Tick resync baseline");
    sim::BattleSnapshotCadence invalid;
    Require(
        invalid.Next(2, false).has_value() &&
            !invalid.Next(1, false).has_value() &&
            invalid.Terminal() &&
            !invalid.Next(3, true).has_value(),
        "snapshot cadence did not fail closed on Tick regression");
}

/// TestSecureParity 验证 client/server direction、replay、rekey 与 rebind parity。
void TestSecureParity(
    const ServerAcceptFixture& accept) {
    sim::CryptoProvider crypto;
    const client::SecureIdentity client_identity{
        .session_id_digest = Sequence<8>(0x30),
        .battle_session_generation = 9,
        .endpoint_generation = 1,
        .binding_discriminator =
            Sequence<8>(0xb0),
    };
    const sim::BattleSecureIdentity server_identity{
        .session_id_digest =
            client_identity.session_id_digest,
        .battle_session_generation =
            client_identity
                .battle_session_generation,
        .endpoint_generation =
            client_identity.endpoint_generation,
        .binding_discriminator =
            client_identity.binding_discriminator,
    };
    client::ProtocolSecureChannel client_channel(
        accept.session_seed,
        client_identity,
        1,
        NowUnixMs);
    sim::BattleSecureChannel server_channel(
        crypto,
        accept.session_seed,
        sim::BattleTransportRole::Server,
        server_identity,
        1,
        1,
        NowUnixMs,
        0);

    const auto c2s = client_channel.Seal(
        client::PacketKind::Raw,
        Sequence<6>(0x10),
        NowUnixMs + 1);
    Require(c2s.has_value(), "client seal failed");
    const auto server_open =
        server_channel.Open(
            *c2s,
            NowUnixMs + 1);
    const auto expected_c2s =
        Sequence<6>(0x10);
    Require(
        server_open.disposition ==
                sim::BattleOpenDisposition::Accepted &&
            server_open.packet_kind ==
                sim::BattlePacketKind::Raw &&
            server_open.plaintext ==
                std::vector<std::uint8_t>(
                    expected_c2s.begin(),
                    expected_c2s.end()),
        "client-to-server secure parity failed");

    const auto s2c = server_channel.Seal(
        sim::BattlePacketKind::Kcp,
        Sequence<7>(0x20),
        NowUnixMs + 2);
    Require(
        s2c.disposition ==
            sim::BattleSealDisposition::Sealed,
        "server seal failed");
    const auto client_open =
        client_channel.Open(
            s2c.datagram,
            NowUnixMs + 2);
    Require(
        client_open.disposition ==
                client::OpenDisposition::Accepted &&
            client_open.kind ==
                client::PacketKind::Kcp,
        "server-to-client secure parity failed");
    Require(
        client_channel.Open(
            s2c.datagram,
            NowUnixMs + 2)
                .disposition ==
            client::OpenDisposition::Duplicate,
        "client replay window accepted duplicate");

    const auto old_epoch_packet =
        server_channel.Seal(
            sim::BattlePacketKind::Raw,
            Sequence<5>(0x50),
            NowUnixMs + 3);
    const auto rekey_nonce =
        Sequence<32>(0xd0);
    Require(
        server_channel.CommitAuthenticatedRollover(
            rekey_nonce,
            NowUnixMs + 4) &&
            client_channel.CommitRollover(
                rekey_nonce,
                2,
                NowUnixMs + 4),
        "client/server rekey parity failed");
    Require(
        client_channel.Open(
            old_epoch_packet.datagram,
            NowUnixMs + 5)
                .disposition ==
            client::OpenDisposition::Accepted,
        "previous epoch overlap rejected valid packet");
    Require(
        server_channel.CommitEndpointGeneration(1, 2) &&
            client_channel.CommitEndpointGeneration(1, 2),
        "client/server endpoint generation drifted");
    const auto rebound = client_channel.Seal(
        client::PacketKind::Raw,
        Sequence<6>(0x60),
        NowUnixMs + 6);
    Require(
        rebound.has_value() &&
            server_channel.Open(
                *rebound,
                NowUnixMs + 6)
                    .disposition ==
                sim::BattleOpenDisposition::Accepted,
        "rebound secure packet was rejected");
}

/// TestSecureNegative 验证 wrong direction、future/old sequence、old endpoint 与 epoch。
void TestSecureNegative(
    const ServerAcceptFixture& accept) {
    sim::CryptoProvider crypto;
    const client::SecureIdentity client_identity{
        .session_id_digest = Sequence<8>(0x30),
        .battle_session_generation = 9,
        .endpoint_generation = 1,
        .binding_discriminator =
            Sequence<8>(0xb0),
    };
    const sim::BattleSecureIdentity server_identity{
        .session_id_digest =
            client_identity.session_id_digest,
        .battle_session_generation = 9,
        .endpoint_generation = 1,
        .binding_discriminator =
            client_identity.binding_discriminator,
    };
    client::ProtocolSecureChannel receiver(
        accept.session_seed,
        client_identity,
        1,
        NowUnixMs);
    client::ProtocolSecureChannel wrong_direction_sender(
        accept.session_seed,
        client_identity,
        1,
        NowUnixMs);
    const auto wrong_direction =
        wrong_direction_sender.Seal(
            client::PacketKind::Raw,
            Sequence<4>(0x10),
            NowUnixMs + 1);
    Require(
        wrong_direction.has_value() &&
            receiver.Open(
                *wrong_direction,
                NowUnixMs + 1)
                    .disposition ==
                client::OpenDisposition::
                    AuthenticationFailed,
        "wrong direction key was accepted");

    client::ProtocolSecureChannel negative_sender(
        accept.session_seed,
        client_identity,
        1,
        NowUnixMs);
    sim::BattleSecureChannel negative_receiver(
        crypto,
        accept.session_seed,
        sim::BattleTransportRole::Server,
        server_identity,
        1,
        1,
        NowUnixMs,
        0);
    const auto too_old =
        negative_sender.SealSecurityNegative(
            client::PacketKind::Raw,
            Sequence<4>(0x11),
            client::SecurityDatagramMutation::
                TooOldSequence,
            NowUnixMs + 1);
    Require(
        too_old.size() == 3 &&
            negative_receiver.Open(
                too_old[0],
                NowUnixMs + 1)
                    .disposition ==
                sim::BattleOpenDisposition::Accepted &&
            negative_receiver.Open(
                too_old[1],
                NowUnixMs + 1)
                    .disposition ==
                sim::BattleOpenDisposition::Accepted &&
            negative_receiver.Open(
                too_old[2],
                NowUnixMs + 1)
                    .disposition ==
                sim::BattleOpenDisposition::TooOld,
        "qualification too-old corpus did not cross replay window");

    client::ProtocolSecureChannel future_negative_sender(
        accept.session_seed,
        client_identity,
        1,
        NowUnixMs);
    sim::BattleSecureChannel future_negative_receiver(
        crypto,
        accept.session_seed,
        sim::BattleTransportRole::Server,
        server_identity,
        1,
        1,
        NowUnixMs,
        0);
    const auto future_negative =
        future_negative_sender.SealSecurityNegative(
            client::PacketKind::Raw,
            Sequence<4>(0x12),
            client::SecurityDatagramMutation::
                FutureSequence,
            NowUnixMs + 1);
    Require(
        future_negative.size() == 1 &&
            future_negative_receiver.Open(
                future_negative.front(),
                NowUnixMs + 1)
                    .disposition ==
                sim::BattleOpenDisposition::FutureJump,
        "qualification future corpus was not authenticated");

    client::ProtocolSecureChannel direction_negative_sender(
        accept.session_seed,
        client_identity,
        1,
        NowUnixMs);
    sim::BattleSecureChannel direction_negative_receiver(
        crypto,
        accept.session_seed,
        sim::BattleTransportRole::Server,
        server_identity,
        1,
        1,
        NowUnixMs,
        0);
    const auto direction_negative =
        direction_negative_sender.SealSecurityNegative(
            client::PacketKind::Raw,
            Sequence<4>(0x13),
            client::SecurityDatagramMutation::
                WrongDirection,
            NowUnixMs + 1);
    Require(
        direction_negative.size() == 1 &&
            direction_negative_receiver.Open(
                direction_negative.front(),
                NowUnixMs + 1)
                    .disposition ==
                sim::BattleOpenDisposition::
                    AuthenticationFailed,
        "qualification wrong-direction corpus used C2S key");

    client::ProtocolSecureChannel future_receiver(
        accept.session_seed,
        client_identity,
        1,
        NowUnixMs);
    sim::BattleSecureChannel future_sender(
        crypto,
        accept.session_seed,
        sim::BattleTransportRole::Server,
        server_identity,
        1,
        257,
        NowUnixMs,
        256);
    const auto future = future_sender.Seal(
        sim::BattlePacketKind::Raw,
        Sequence<4>(0x20),
        NowUnixMs + 1);
    Require(
        future_receiver.Open(
            future.datagram,
            NowUnixMs + 1)
                .disposition ==
            client::OpenDisposition::FutureJump,
        "future sequence jump was accepted");

    client::ProtocolSecureChannel age_receiver(
        accept.session_seed,
        client_identity,
        1,
        NowUnixMs);
    sim::BattleSecureChannel first_sender(
        crypto,
        accept.session_seed,
        sim::BattleTransportRole::Server,
        server_identity,
        1,
        1,
        NowUnixMs,
        0);
    sim::BattleSecureChannel edge_sender(
        crypto,
        accept.session_seed,
        sim::BattleTransportRole::Server,
        server_identity,
        1,
        257,
        NowUnixMs,
        256);
    const auto first = first_sender.Seal(
        sim::BattlePacketKind::Raw,
        Sequence<4>(0x30),
        NowUnixMs + 1);
    const auto edge = edge_sender.Seal(
        sim::BattlePacketKind::Raw,
        Sequence<4>(0x40),
        NowUnixMs + 2);
    Require(
        age_receiver.Open(
            first.datagram,
            NowUnixMs + 1)
                .disposition ==
                client::OpenDisposition::Accepted &&
            age_receiver.Open(
                edge.datagram,
                NowUnixMs + 2)
                    .disposition ==
                client::OpenDisposition::Accepted &&
            age_receiver.Open(
                first.datagram,
                NowUnixMs + 2)
                    .disposition ==
                client::OpenDisposition::TooOld,
        "too-old sequence remained inside replay window");

    client::ProtocolSecureChannel rebound_receiver(
        accept.session_seed,
        client_identity,
        1,
        NowUnixMs);
    sim::BattleSecureChannel rebound_sender(
        crypto,
        accept.session_seed,
        sim::BattleTransportRole::Server,
        server_identity,
        1,
        1,
        NowUnixMs,
        0);
    const auto predecessor = rebound_sender.Seal(
        sim::BattlePacketKind::Raw,
        Sequence<4>(0x50),
        NowUnixMs + 1);
    Require(
        rebound_sender.CommitEndpointGeneration(1, 2) &&
            rebound_receiver.CommitEndpointGeneration(1, 2) &&
            rebound_receiver.Open(
                predecessor.datagram,
                NowUnixMs + 2)
                    .disposition ==
                client::OpenDisposition::InvalidHeader,
        "predecessor endpoint packet survived rebind");
    auto wrong_epoch = predecessor.datagram;
    wrong_epoch[23] = 2;
    Require(
        rebound_receiver.Open(
            wrong_epoch,
            NowUnixMs + 2)
                .disposition ==
            client::OpenDisposition::InvalidHeader,
        "wrong epoch packet was accepted");

    client::ProtocolSecureChannel deadline_channel(
        accept.session_seed,
        client_identity,
        1,
        NowUnixMs);
    const auto trigger_time =
        NowUnixMs +
        client::ProtocolSecureChannel::
            RekeyIntervalMilliseconds;
    Require(
        deadline_channel.RolloverRequired(
            trigger_time) &&
            deadline_channel.RolloverRequired(
                trigger_time +
                client::ProtocolSecureChannel::
                    RolloverDeadlineMilliseconds -
                1),
        "rekey trigger/deadline was not stable");
    Require(
        deadline_channel.RolloverRequired(
            trigger_time +
            client::ProtocolSecureChannel::
                RolloverDeadlineMilliseconds) &&
            !deadline_channel.Seal(
                 client::PacketKind::Control,
                 Sequence<4>(0x70),
                 trigger_time +
                     client::ProtocolSecureChannel::
                         RolloverDeadlineMilliseconds)
                 .has_value(),
        "expired rekey negotiation did not close channel");
}

/// RawEnvelope 构造独立 s2c route fixture。
[[nodiscard]] std::vector<std::uint8_t>
RawEnvelope(
    const std::uint32_t message_id,
    const std::uint64_t sequence,
    const std::uint8_t partition_index,
    const std::uint8_t partition_count,
    const std::string& payload) {
    std::vector<std::uint8_t> output(
        16 + payload.size());
    WriteUint32BE(output.data(), message_id);
    WriteUint16BE(
        output.data() + 4,
        static_cast<std::uint16_t>(payload.size()));
    output[6] = partition_index;
    output[7] = partition_count;
    WriteUint64BE(
        output.data() + 8,
        sequence);
    std::ranges::copy(
        payload,
        output.begin() + 16);
    return output;
}

/// TestRawLane 验证 input/probe 与 full/delta baseline transition。
void TestRawLane() {
    client::ProtocolRawLane lane;
    ihomeland::battle::v1::BattleInputBundle input;
    input.set_newest_input_tick(1);
    input.set_latest_observed_server_tick(1);
    auto* command = input.add_commands();
    command->set_command_sequence(1);
    command->set_kind(
        ihomeland::battle::v1::
            BATTLE_INPUT_KIND_MOVE);
    command->set_move_x_milli(1'000);
    const auto input_bytes = Bytes(input);
    const auto encoded_input = lane.EncodeInputBundle(
        1,
        input_bytes);
    Require(
        encoded_input.size() ==
                input_bytes.size() + 16 &&
            encoded_input[2] == 0x0b &&
            encoded_input[3] == 0xb8,
        "input bundle route encoding drifted");

    ihomeland::battle::v1::BattleProbe probe;
    probe.set_probe_sequence(1);
    probe.set_latest_snapshot_sequence(2);
    probe.set_client_monotonic_time_us(3);
    const auto probe_bytes =
        probe.SerializeAsString();
    const auto encoded_probe = lane.EncodeProbe(
        0x1112131415161718ULL,
        std::span(
            reinterpret_cast<const std::uint8_t*>(
            probe_bytes.data()),
            probe_bytes.size()));
    constexpr std::array<std::uint8_t, 16>
        ExpectedProbeRoute{
            0x00, 0x00, 0x0b, 0xb9,
            0x00, 0x06, 0x00, 0x01,
            0x11, 0x12, 0x13, 0x14,
            0x15, 0x16, 0x17, 0x18};
    constexpr std::array<std::uint8_t, 6>
        ExpectedProbePayload{
            0x08, 0x01, 0x10,
            0x02, 0x18, 0x03};
    Require(
        encoded_probe.size() ==
                probe_bytes.size() + 16 &&
            std::ranges::equal(
                std::span(encoded_probe).first(16),
                ExpectedProbeRoute) &&
            std::ranges::equal(
                std::span(encoded_probe).subspan(16),
                ExpectedProbePayload),
        "canonical probe route/protobuf drifted");

    ihomeland::battle::v1::BattleFullSnapshot full;
    full.set_server_tick(20);
    full.set_snapshot_sequence(1);
    full.set_baseline_id(7);
    full.set_partition_index(0);
    full.set_partition_count(1);
    full.set_last_processed_input_tick(0);
    const auto full_frame = RawEnvelope(
        3'002,
        1,
        0,
        1,
        full.SerializeAsString());
    Require(
        lane.DecodeSnapshot(full_frame)
                .disposition ==
            client::RawDecodeDisposition::Accepted &&
            lane.LatestServerTick() == 20 &&
            lane.LatestSnapshotSequence() == 1 &&
            lane.BaselineID() == 7,
        "full snapshot did not establish baseline");

    ihomeland::battle::v1::BattleDeltaSnapshot delta;
    delta.set_server_tick(21);
    delta.set_snapshot_sequence(2);
    delta.set_baseline_id(7);
    delta.set_partition_index(0);
    delta.set_partition_count(1);
    delta.set_last_processed_input_tick(2);
    const auto delta_frame = RawEnvelope(
        3'003,
        2,
        0,
        1,
        delta.SerializeAsString());
    Require(
        lane.DecodeSnapshot(delta_frame)
                .disposition ==
            client::RawDecodeDisposition::Accepted &&
            lane.LatestServerTick() == 21 &&
            lane.LastProcessedInputTick() == 2,
        "delta snapshot did not advance current baseline");
    Require(
        lane.DecodeSnapshot(full_frame)
                .disposition ==
                client::RawDecodeDisposition::Discarded &&
            lane.LatestServerTick() == 21 &&
            lane.LatestSnapshotSequence() == 2 &&
            lane.LastProcessedInputTick() == 2,
        "reordered stale snapshot was treated as malformed");
    delta.set_baseline_id(8);
    delta.set_server_tick(22);
    delta.set_snapshot_sequence(3);
    Require(
        lane.DecodeSnapshot(
                RawEnvelope(
                    3'003,
                    3,
                    0,
                    1,
                    delta.SerializeAsString()))
                .disposition ==
            client::RawDecodeDisposition::BaselineGap,
        "delta accepted unknown baseline");

    ihomeland::battle::v1::BattleFullSnapshot
        missing_acknowledgement;
    missing_acknowledgement.set_server_tick(30);
    missing_acknowledgement.set_snapshot_sequence(3);
    missing_acknowledgement.set_baseline_id(9);
    missing_acknowledgement.set_partition_index(0);
    missing_acknowledgement.set_partition_count(2);
    Require(
        lane.DecodeSnapshot(
                RawEnvelope(
                    3'002,
                    3,
                    0,
                    2,
                    missing_acknowledgement
                        .SerializeAsString()))
                .disposition ==
                client::RawDecodeDisposition::Rejected &&
            lane.PendingSnapshotSequence() == 0,
        "snapshot accepted missing acknowledgement presence");

    ihomeland::battle::v1::BattleFullSnapshot partial;
    partial.set_server_tick(30);
    partial.set_snapshot_sequence(3);
    partial.set_baseline_id(9);
    partial.set_partition_index(0);
    partial.set_partition_count(2);
    partial.set_last_processed_input_tick(3);
    const auto first_partition = RawEnvelope(
        3'002,
        3,
        0,
        2,
        partial.SerializeAsString());
    Require(
        lane.DecodeSnapshot(first_partition)
                .disposition ==
                client::RawDecodeDisposition::Pending &&
            lane.PendingSnapshotSequence() == 3 &&
            lane.LatestSnapshotSequence() == 2 &&
            lane.DecodeSnapshot(first_partition)
                    .disposition ==
                client::RawDecodeDisposition::Discarded &&
            lane.PendingSnapshotSequence() == 3,
        "duplicate partition changed the pending set");

    lane.ResetPendingSnapshot();
    Require(
        lane.DecodeSnapshot(first_partition)
                .disposition ==
                client::RawDecodeDisposition::Pending &&
            lane.PendingSnapshotSequence() == 3,
        "multipart snapshot did not restart after rejection");
    partial.set_partition_index(1);
    partial.set_last_processed_input_tick(4);
    Require(
        lane.DecodeSnapshot(
                RawEnvelope(
                    3'002,
                    4,
                    1,
                    2,
                    partial.SerializeAsString()))
                .disposition ==
                client::RawDecodeDisposition::Rejected &&
            lane.PendingSnapshotSequence() == 0 &&
            lane.LastProcessedInputTick() == 2,
        "partition acknowledgement drift was published");

    partial.set_partition_index(0);
    partial.set_last_processed_input_tick(3);
    Require(
        lane.DecodeSnapshot(first_partition)
                .disposition ==
                client::RawDecodeDisposition::Pending &&
            lane.PendingSnapshotSequence() == 3,
        "valid multipart snapshot did not begin");
    partial.set_partition_index(1);
    const auto completed = lane.DecodeSnapshot(
        RawEnvelope(
            3'002,
            5,
            1,
            2,
            partial.SerializeAsString()));
    Require(
        completed.disposition ==
                client::RawDecodeDisposition::Accepted &&
            completed.message.has_value() &&
            completed.message->
                    last_processed_input_tick ==
                3 &&
            lane.PendingSnapshotSequence() == 0 &&
            lane.LatestSnapshotSequence() == 3 &&
            lane.BaselineID() == 9 &&
            lane.LastProcessedInputTick() == 3,
        "complete multipart acknowledgement was not published");

    ihomeland::battle::v1::BattleDeltaSnapshot
        regressed_acknowledgement;
    regressed_acknowledgement.set_server_tick(31);
    regressed_acknowledgement.set_snapshot_sequence(4);
    regressed_acknowledgement.set_baseline_id(9);
    regressed_acknowledgement.set_partition_index(0);
    regressed_acknowledgement.set_partition_count(1);
    regressed_acknowledgement.set_last_processed_input_tick(2);
    Require(
        lane.DecodeSnapshot(
                RawEnvelope(
                    3'003,
                    6,
                    0,
                    1,
                    regressed_acknowledgement
                        .SerializeAsString()))
                .disposition ==
                client::RawDecodeDisposition::Rejected &&
            lane.LastProcessedInputTick() == 3,
        "snapshot acknowledgement regressed");
}

/// TestKcpParity 验证 client primitive 与 production server adapter 双向互通。
void TestKcpParity() {
    std::vector<std::vector<std::uint8_t>>
        server_segments;
    std::uint32_t server_received_id = 0;
    std::uint64_t server_received_sequence = 0;
    sim::BattleKcpAdapter server(
        Conversation,
        sim::BattleTransportRole::Server,
        [&](const std::span<const std::uint8_t> segment) {
            server_segments.emplace_back(
                segment.begin(),
                segment.end());
        },
        [&](const sim::BattleKcpMessageView& message) {
            server_received_id =
                message.policy->message_id;
            server_received_sequence =
                message.application_sequence;
        });
    client::ProtocolKcpLane client_lane(
        Conversation);

    ihomeland::battle::v1::BattleResyncRequest request;
    request.set_request_sequence(1);
    request.set_latest_server_tick(20);
    request.set_missing_baseline_id(7);
    request.set_latest_snapshot_sequence(2);
    request.set_reason(
        ihomeland::battle::v1::
            BATTLE_RESYNC_REASON_MISSING_BASELINE);
    const auto request_bytes = Bytes(request);
    Require(
        client_lane.QueueResync(
            1,
            request_bytes,
            NowUnixMs),
        "client KCP did not queue resync");
    client_lane.Update(NowUnixMs);
    if (client_lane.Closed()) {
        throw std::runtime_error(
            "client KCP closed on initial update: " +
            std::to_string(
                static_cast<std::uint8_t>(
                    client_lane.CloseReason())));
    }
    client_lane.Update(
        NowUnixMs +
        client::ProtocolKcpLane::
            UpdateIntervalMilliseconds);
    const auto client_segments =
        client_lane.TakeSegments();
    Require(
        !client_lane.Closed(),
        "client KCP closed before emitting resync");
    Require(
        !client_segments.empty(),
        "client KCP did not emit resync segment");
    for (const auto& segment : client_segments) {
        Require(
            server.Input(
                segment,
                NowUnixMs) ==
                sim::BattleKcpDisposition::Accepted,
            "server rejected client KCP segment");
    }
    Require(
        server_received_id == 3'006 &&
            server_received_sequence == 1,
        "server did not receive client resync");
    static_cast<void>(
        server.Update(NowUnixMs));
    for (const auto& segment : server_segments) {
        Require(
            client_lane.Input(
                segment,
                NowUnixMs + 10),
            "client rejected server KCP ACK");
    }
    client_lane.Update(NowUnixMs + 10);
    static_cast<void>(client_lane.TakeSegments());
    server_segments.clear();

    ihomeland::battle::v1::BattleResyncResponse response;
    response.set_request_sequence(1);
    response.set_server_tick(21);
    response.set_disposition(
        ihomeland::battle::v1::
            BATTLE_RESYNC_DISPOSITION_SCHEDULED);
    response.set_scheduled_baseline_id(8);
    const auto response_bytes = Bytes(response);
    Require(
        server.Queue(
            3'007,
            2,
            response_bytes,
            NowUnixMs + 10) ==
            sim::BattleKcpDisposition::Queued &&
            server.Update(
                NowUnixMs + 10) ==
            sim::BattleKcpDisposition::Accepted &&
            !server_segments.empty(),
        "server KCP did not emit resync response");
    for (const auto& segment : server_segments) {
        if (!client_lane.Input(
                segment,
                NowUnixMs + 10)) {
            throw std::runtime_error(
                "client rejected server response segment: " +
                std::to_string(
                    static_cast<std::uint8_t>(
                        client_lane.CloseReason())));
        }
    }
    client_lane.Update(NowUnixMs + 10);
    const auto messages =
        client_lane.TakeMessages();
    Require(
        messages.size() == 1 &&
            messages.front().message_id == 3'007 &&
            messages.front().application_sequence == 2 &&
            messages.front().server_tick == 21 &&
            !client_lane.Closed(),
        "client KCP did not reassemble response");
    client_lane.Update(
        NowUnixMs +
        client::ProtocolKcpLane::
            ResyncExpiryMilliseconds);
    Require(
        !client_lane.Closed(),
        "acknowledged client KCP request expired");
}

/// TestKcpRetransmitProfile 验证连续丢包时 segment RTO 仍受冻结上限约束。
void TestKcpRetransmitProfile() {
    constexpr std::size_t DroppedAttempts = 5;
    std::vector<std::vector<std::uint8_t>>
        server_segments;
    std::uint64_t received_sequence = 0;
    sim::BattleKcpAdapter server(
        Conversation,
        sim::BattleTransportRole::Server,
        [&](const std::span<const std::uint8_t> segment) {
            server_segments.emplace_back(
                segment.begin(),
                segment.end());
        },
        [&](const sim::BattleKcpMessageView& message) {
            received_sequence =
                message.application_sequence;
        });
    client::ProtocolKcpLane client_lane(
        Conversation);
    ihomeland::battle::v1::BattleResyncRequest request;
    request.set_request_sequence(1);
    request.set_latest_server_tick(20);
    request.set_reason(
        ihomeland::battle::v1::
            BATTLE_RESYNC_REASON_MISSING_BASELINE);
    Require(
        client_lane.QueueResync(
            1,
            Bytes(request),
            NowUnixMs),
        "retransmit fixture did not queue");

    std::size_t attempts = 0;
    for (std::uint64_t elapsed = 0;
         elapsed <
         client::ProtocolKcpLane::
             ResyncExpiryMilliseconds;
         elapsed +=
         client::ProtocolKcpLane::
             UpdateIntervalMilliseconds) {
        const auto now = NowUnixMs + elapsed;
        client_lane.Update(now);
        for (const auto& segment :
             client_lane.TakeSegments()) {
            ++attempts;
            if (attempts <= DroppedAttempts) {
                continue;
            }
            Require(
                server.Input(segment, now) ==
                    sim::BattleKcpDisposition::
                        Accepted,
                "server rejected recovered KCP request");
        }
        if (received_sequence == 0) {
            continue;
        }
        Require(
            server.Update(now) ==
                sim::BattleKcpDisposition::
                    Accepted,
            "server did not emit recovered KCP ACK");
        for (const auto& segment : server_segments) {
            Require(
                client_lane.Input(segment, now),
                "client rejected recovered KCP ACK");
        }
        client_lane.Update(now);
        break;
    }
    Require(
        attempts > DroppedAttempts &&
            received_sequence == 1 &&
            !client_lane.Closed(),
        "bounded KCP RTO did not recover before deadline");
    client_lane.Update(
        NowUnixMs +
        client::ProtocolKcpLane::
            ResyncExpiryMilliseconds);
    Require(
        !client_lane.Closed(),
        "recovered KCP request expired after ACK");
}

/// TestKcpNegative 验证 malformed segment、inflight expiry 与稳定 close reason。
void TestKcpNegative() {
    client::ProtocolKcpLane malformed_lane(
        Conversation);
    std::array<std::uint8_t, 24> malformed{};
    Require(
        !malformed_lane.Input(
            malformed,
            NowUnixMs) &&
            malformed_lane.Closed() &&
            malformed_lane.CloseReason() ==
                client::KcpCloseReason::Protocol,
        "malformed KCP segment did not fail closed");

    client::ProtocolKcpLane expiry_lane(
        Conversation);
    ihomeland::battle::v1::BattleResyncRequest request;
    request.set_request_sequence(1);
    request.set_latest_server_tick(20);
    request.set_reason(
        ihomeland::battle::v1::
            BATTLE_RESYNC_REASON_MISSING_BASELINE);
    const auto bytes = Bytes(request);
    Require(
        expiry_lane.QueueResync(
            1,
            bytes,
            NowUnixMs),
        "expiry fixture did not queue");
    expiry_lane.Update(NowUnixMs);
    expiry_lane.Update(
        NowUnixMs +
        client::ProtocolKcpLane::
            ResyncExpiryMilliseconds);
    Require(
        expiry_lane.Closed() &&
            expiry_lane.CloseReason() ==
                client::KcpCloseReason::InflightExpiry,
        "inflight KCP expiry did not produce stable close");
}

/// TestPollReplayPolicy 验证网络 duplicate/reorder 只被 replay gate 静默抑制。
void TestPollReplayPolicy() {
    for (const auto disposition : {
             client::OpenDisposition::InvalidHeader,
             client::OpenDisposition::AuthenticationFailed,
             client::OpenDisposition::FutureJump,
             client::OpenDisposition::Closed,
             client::OpenDisposition::Accepted}) {
        Require(
            !client::IsPollReplaySuppressed(
                disposition),
            "poll suppressed a non-replay disposition");
    }
    Require(
        client::IsPollReplaySuppressed(
            client::OpenDisposition::Duplicate) &&
            client::IsPollReplaySuppressed(
                client::OpenDisposition::TooOld),
        "poll did not suppress a replay terminal disposition");
}

}  // namespace

int main() {
    try {
        const auto accept = TestHandshake();
        TestSnapshotCadence();
        TestRuntimeComposition();
        TestSecureParity(accept);
        TestSecureNegative(accept);
        TestRawLane();
        TestKcpParity();
        TestKcpRetransmitProfile();
        TestKcpNegative();
        TestPollReplayPolicy();
        std::cout
            << "battle_protocol_client_test: PASS\n";
        return 0;
    } catch (const std::exception& error) {
        std::cerr
            << "battle_protocol_client_test: FAIL: "
            << error.what() << '\n';
        return 1;
    }
}
