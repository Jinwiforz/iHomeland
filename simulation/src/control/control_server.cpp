#include "ihomeland/sim/control/control_server.hpp"

#include "ihomeland/sim/control/control_frame.hpp"
#include "ihomeland/sim/control/simulation_node.hpp"
#include "ihomeland/sim/transport/battle_transport_runtime.hpp"

#include <nlohmann/json.hpp>

#define NOMINMAX
#include <windows.h>

#include <array>
#include <charconv>
#include <chrono>
#include <cstdint>
#include <istream>
#include <limits>
#include <memory>
#include <ostream>
#include <regex>
#include <set>
#include <stdexcept>
#include <string>
#include <utility>

namespace ihomeland::sim {
namespace {

using Json = nlohmann::json;
constexpr std::uint32_t
    BattleResyncRequestMessageId = 3'006;

/// ParseObject 解析 canonical payload 并要求 object。
[[nodiscard]] Json ParseObject(const std::string& payload) {
    try {
        const auto value = Json::parse(payload, nullptr, true, true);
        if (!value.is_object() || value.dump() != payload) {
            throw std::invalid_argument("control payload is not canonical object");
        }
        return value;
    } catch (const Json::exception&) {
        throw std::invalid_argument("control payload parsing failed");
    }
}

/// RequireFields 验证 payload 的 closed 字段集合。
void RequireFields(
    const Json& value,
    const std::set<std::string, std::less<>>& fields,
    const char* context) {
    if (!value.is_object() || value.size() != fields.size()) {
        throw std::invalid_argument(std::string(context) + " fields are incomplete");
    }
    for (const auto& [name, ignored] : value.items()) {
        static_cast<void>(ignored);
        if (!fields.contains(name)) {
            throw std::invalid_argument(std::string(context) + " contains unknown field");
        }
    }
}

/// ReadDecimal 读取规范 uint64 string。
[[nodiscard]] std::uint64_t ReadDecimal(
    const Json& value,
    const char* context) {
    if (!value.is_string()) {
        throw std::invalid_argument(std::string(context) + " must be decimal string");
    }
    const auto& text = value.get_ref<const std::string&>();
    if (text.empty() || (text.size() > 1 && text.front() == '0')) {
        throw std::invalid_argument(std::string(context) + " is not canonical");
    }
    std::uint64_t result = 0;
    const auto [end, error] =
        std::from_chars(text.data(), text.data() + text.size(), result);
    if (error != std::errc{} || end != text.data() + text.size()) {
        throw std::invalid_argument(std::string(context) + " exceeds uint64");
    }
    return result;
}

/// ReadActorSlot 读取 0..7 的规范 actor slot。
[[nodiscard]] std::uint8_t ReadActorSlot(
    const Json& value,
    const char* context) {
    const auto slot = ReadDecimal(value, context);
    if (slot > 7) {
        throw std::invalid_argument(
            std::string(context) + " exceeds qualified capacity");
    }
    return static_cast<std::uint8_t>(slot);
}

/// ReadProofKey 解码固定 32-byte lowercase hex secret。
[[nodiscard]] std::array<std::uint8_t, 32> ReadProofKey(
    const Json& value) {
    if (!value.is_string()) {
        throw std::invalid_argument("ticket proof key must be string");
    }
    const auto& encoded = value.get_ref<const std::string&>();
    if (encoded.size() != 64) {
        throw std::invalid_argument("ticket proof key size is invalid");
    }
    std::array<std::uint8_t, 32> result{};
    for (std::size_t index = 0; index < result.size(); ++index) {
        const auto high = encoded[index * 2];
        const auto low = encoded[index * 2 + 1];
        const auto nibble = [](const char value) -> std::uint8_t {
            if (value >= '0' && value <= '9') {
                return static_cast<std::uint8_t>(value - '0');
            }
            if (value >= 'a' && value <= 'f') {
                return static_cast<std::uint8_t>(value - 'a' + 10);
            }
            throw std::invalid_argument(
                "ticket proof key is not lowercase hex");
        };
        result[index] = static_cast<std::uint8_t>(
            (nibble(high) << 4U) | nibble(low));
    }
    return result;
}

/// ReadBindingFingerprint 解码 control 已验证的 canonical SHA-256。
[[nodiscard]] std::array<std::uint8_t, 32>
ReadBindingFingerprint(const Json& value) {
    return ReadProofKey(value);
}

/// ObservedUnixMilliseconds 返回 ticket expiry 使用的 UTC 毫秒快照。
[[nodiscard]] std::uint64_t ObservedUnixMilliseconds() {
    const auto value = std::chrono::duration_cast<std::chrono::milliseconds>(
                           std::chrono::system_clock::now()
                               .time_since_epoch())
                           .count();
    if (value <= 0) {
        throw std::runtime_error("system clock is invalid");
    }
    return static_cast<std::uint64_t>(value);
}

/// TicketStateName 返回 control schema 固定的小写 lifecycle。
[[nodiscard]] const char* TicketStateName(
    const BattleTicketState state) {
    switch (state) {
        case BattleTicketState::Installed:
            return "installed";
        case BattleTicketState::Consumed:
            return "consumed";
        case BattleTicketState::Revoked:
            return "revoked";
        case BattleTicketState::Expired:
            return "expired";
    }
    throw std::runtime_error("battle ticket state is invalid");
}

/// TicketReceiptPayload 生成 install/status 共用的 closed receipt。
[[nodiscard]] Json TicketReceiptPayload(
    const BattleTicketReceipt& receipt) {
    return Json{
        {"actorSlot", std::to_string(receipt.actor_slot)},
        {"bindingFingerprint", receipt.binding_fingerprint},
        {"simulationInstanceId", receipt.simulation_instance_id},
        {"state", TicketStateName(receipt.state)},
        {"ticketId", receipt.ticket_id},
    };
}

/// StatusPayload 生成 instance status receipt 的 canonical payload。
[[nodiscard]] std::string StatusPayload(const InstanceStatusReceipt& status) {
    return Json{
        {"assignmentFingerprint", status.assignment_fingerprint},
        {"committedTick", std::to_string(status.committed_tick)},
        {"simulationInstanceId", status.simulation_instance_id},
        {"state", status.state},
    }.dump();
}

/// ResultPayload 生成 immutable result proposal payload。
[[nodiscard]] std::string ResultPayload(const ResultProposal& proposal) {
    return Json{
        {"assignmentFingerprint", proposal.assignment_fingerprint},
        {"evidenceDigest", proposal.evidence_digest},
        {"payloadDigest", proposal.payload_digest},
        {"proposalFingerprint", proposal.proposal_fingerprint},
        {"resultId", proposal.result_id},
        {"resultKind", proposal.result_kind},
        {"simulationInstanceId", proposal.simulation_instance_id},
        {"tickEnd", std::to_string(proposal.tick_end)},
        {"tickStart", std::to_string(proposal.tick_start)},
    }.dump();
}

/// QualificationSnapshotPayload 生成不含动态标签或业务身份的 closed receipt。
[[nodiscard]] std::string QualificationSnapshotPayload(
    const BattleQualificationSnapshotReceipt& snapshot,
    const std::string& run_id,
    const std::uint64_t sample_sequence) {
    const auto& metrics = snapshot.metrics;
    return Json{
        {"activeSessionCount",
         std::to_string(snapshot.active_session_count)},
        {"assignmentFingerprint",
         snapshot.assignment_fingerprint},
        {"committedTick", std::to_string(snapshot.committed_tick)},
        {"installedTicketCount",
         std::to_string(snapshot.installed_ticket_count)},
        {"metrics",
         {
             {"closeAuthentication",
              std::to_string(metrics.close_authentication)},
             {"closeInternal", std::to_string(metrics.close_internal)},
             {"closeLifecycle", std::to_string(metrics.close_lifecycle)},
             {"closeNormal", std::to_string(metrics.close_normal)},
             {"closeResource", std::to_string(metrics.close_resource)},
             {"closeTimeout", std::to_string(metrics.close_timeout)},
             {"closeTransport", std::to_string(metrics.close_transport)},
             {"droppedPackets",
              std::to_string(metrics.dropped_packets)},
             {"egressQueueHighWatermark",
              std::to_string(metrics.egress_queue_high_watermark)},
             {"expiredMessages",
              std::to_string(metrics.expired_messages)},
             {"ingressQueueHighWatermark",
              std::to_string(metrics.ingress_queue_high_watermark)},
             {"instanceMemoryBytes",
              std::to_string(metrics.instance_memory_bytes)},
             {"kcpEgressBytes",
              std::to_string(metrics.kcp_egress_bytes)},
             {"kcpEgressPackets",
              std::to_string(metrics.kcp_egress_packets)},
             {"kcpIngressBytes",
              std::to_string(metrics.kcp_ingress_bytes)},
             {"kcpIngressPackets",
              std::to_string(metrics.kcp_ingress_packets)},
             {"kcpQueueHighWatermark",
              std::to_string(metrics.kcp_queue_high_watermark)},
             {"kcpRetransmits",
              std::to_string(metrics.kcp_retransmits)},
             {"maximumTickDurationNs",
              std::to_string(metrics.maximum_tick_duration_ns)},
             {"historyMemoryBytes",
              std::to_string(metrics.history_memory_bytes)},
             {"rawEgressBytes",
              std::to_string(metrics.raw_egress_bytes)},
             {"rawEgressPackets",
              std::to_string(metrics.raw_egress_packets)},
             {"rawIngressBytes",
              std::to_string(metrics.raw_ingress_bytes)},
             {"rawIngressPackets",
              std::to_string(metrics.raw_ingress_packets)},
             {"rebinds", std::to_string(metrics.rebinds)},
             {"rejectedPackets",
              std::to_string(metrics.rejected_packets)},
             {"rekeys", std::to_string(metrics.rekeys)},
             {"tickDebtHighWatermark",
              std::to_string(metrics.tick_debt_high_watermark)},
         }},
        {"nodeCount", std::to_string(snapshot.node_count)},
        {"qualificationRunId", run_id},
        {"runningInstanceCount",
         std::to_string(snapshot.running_instance_count)},
        {"sampleSequence", std::to_string(sample_sequence)},
        {"simulationInstanceId", snapshot.simulation_instance_id},
        {"simulationNodeId", snapshot.simulation_node_id},
    }.dump();
}

/// EmitPendingResults 顺序输出 node 当前等待 ack 的 proposals。
void EmitPendingResults(
    std::ostream& output,
    ControlSequence& sequence,
    const SimulationNode& node) {
    for (const auto& proposal : node.PendingResults()) {
        const auto request_id =
            "sctl_result_" + proposal.proposal_fingerprint.substr(0, 24);
        ControlFrameCodec::Write(
            output,
            sequence.MakeOutbound(
                request_id,
                "result.proposal",
                ResultPayload(proposal)));
    }
}

}  // namespace

int RunControlStdio(
    std::istream& input,
    std::ostream& output,
    std::ostream& diagnostics,
    const ControlBuildBinding& build,
    const BattleUdpListenerConfig* battle_listener,
    const QualificationControlConfig* qualification,
    const GameplayPackageCatalog* gameplay_package) {
    try {
        static const std::regex qualification_run_pattern{
            "^bqrun_[0-9a-f]{32}$"};
        if (qualification != nullptr &&
            !std::regex_match(
                qualification->run_id,
                qualification_run_pattern)) {
            throw std::invalid_argument(
                "qualification control run identity is invalid");
        }
        ControlFrame hello_frame;
        if (!ControlFrameCodec::Read(input, hello_frame)) {
            throw std::runtime_error("control stdin ended before hello");
        }
        if (hello_frame.kind != "node.hello.challenge") {
            throw std::runtime_error("control first frame must be hello challenge");
        }
        ControlSequence sequence(hello_frame.session_nonce);
        sequence.AcceptInbound(hello_frame);
        const auto hello = ParseObject(hello_frame.payload_json);
        auto hello_fields = std::set<std::string, std::less<>>{
            "actorCapacity",
            "expectedBuildIdentity",
            "expectedModelManifest",
            "expectedProfileManifest",
            "instanceCapacity",
            "runtimeNodeId",
            "simulationNodeId",
        };
        if (gameplay_package != nullptr) {
            hello_fields.insert("expectedConfigIdentity");
            hello_fields.insert("expectedNavigationIdentity");
            hello_fields.insert("expectedPhysicsIdentity");
            hello_fields.insert("expectedWireIdentity");
        }
        RequireFields(hello, hello_fields, "hello challenge");
        if (hello.at("expectedBuildIdentity").get<std::string>() !=
                build.build_identity ||
            hello.at("expectedModelManifest").get<std::string>() !=
                build.model_manifest ||
            hello.at("expectedProfileManifest").get<std::string>() !=
                build.profile_manifest ||
            build.platform_qualification !=
                "implementation-qualified-windows-x64") {
            throw std::runtime_error("control hello build binding drifted");
        }
        if (gameplay_package != nullptr &&
            (hello.at("expectedConfigIdentity").get<std::string>() !=
                 gameplay_package->binding.config_identity ||
             hello.at("expectedNavigationIdentity").get<std::string>() !=
                 gameplay_package->binding.navigation_identity ||
             hello.at("expectedPhysicsIdentity").get<std::string>() !=
                 gameplay_package->binding.physics_identity ||
             hello.at("expectedWireIdentity").get<std::string>() !=
                 gameplay_package->binding.wire_identity)) {
            throw std::runtime_error(
                "control hello gameplay package binding drifted");
        }
        auto node = std::make_unique<SimulationNode>(SimulationNodeConfig{
            .simulation_node_id = hello.at("simulationNodeId").get<std::string>(),
            .runtime_node_id = hello.at("runtimeNodeId").get<std::string>(),
            .build_identity = build.build_identity,
            .model_manifest = build.model_manifest,
            .profile_manifest = build.profile_manifest,
            .instance_capacity =
                hello.at("instanceCapacity").get<std::size_t>(),
            .actor_capacity = hello.at("actorCapacity").get<std::size_t>(),
            .gameplay_catalog = gameplay_package == nullptr
                ? nullptr
                : std::make_shared<const GameplayPackageCatalog>(
                      *gameplay_package),
        });
        std::unique_ptr<BattleTransportRuntime>
            battle_runtime;
        if (battle_listener != nullptr) {
            battle_runtime =
                std::make_unique<
                    BattleTransportRuntime>(
                    BattleTransportRuntimeConfig{
                        .simulation_node_id =
                            node->Config()
                                .simulation_node_id,
                        .advertised_host =
                            battle_listener->
                                advertised_endpoint.host,
                        .advertised_port =
                            battle_listener->
                                advertised_endpoint.port,
                        .listener_identity =
                            battle_listener->
                                listener_identity,
                        .maximum_sessions =
                            node->Config()
                                .actor_capacity,
                    },
                    *node,
                    [owner = node.get()](
                        const std::span<
                            const std::uint8_t>
                            datagram,
                        const BattleRemoteEndpoint&
                            remote) {
                        return owner->
                            SendBattleUdpDatagram(
                                datagram,
                                remote);
                    },
                    [owner = node.get()](
                        const BattleSessionContext&
                            context,
                        const std::uint64_t
                            now_unix_ms) {
                        return owner->
                            BattleRawContext(
                                context,
                                now_unix_ms);
                    },
                    [owner = node.get()](
                        const BattleSessionContext&
                            context) {
                        return owner->
                            ResolveBattleCommandIngress(
                                context);
                    },
                    [owner = node.get()](
                        const BattleSessionContext&
                            context) {
                        return owner->
                            BattleReplicationSnapshot(
                                context);
                    },
                    [](
                        const BattleSessionContext&,
                        const BattleKcpMessageView&
                            message) {
                        if (message.policy == nullptr ||
                            message.policy->
                                    message_id !=
                                BattleResyncRequestMessageId) {
                            throw std::runtime_error(
                                "battle KCP application route is invalid");
                        }
                    },
                    [owner = node.get()](
                        const BattleSessionContext&
                            context) {
                        return owner->
                            BattleSessionCurrent(
                                context);
                    },
                    ObservedUnixMilliseconds(),
                    &node->RuntimeMetrics(),
                    [owner = node.get()](
                        const BattleSessionContext&
                            context,
                        const bool active) {
                        return owner->
                            SetBattleSessionParticipation(
                                context,
                                active);
                    });
            auto* runtime_owner =
                battle_runtime.get();
            node->StartBattleUdpListener(
                *battle_listener,
                [runtime_owner](
                    const std::span<
                        const std::uint8_t>
                        datagram,
                    const BattleRemoteEndpoint&
                        remote) {
                    const auto disposition =
                        runtime_owner->
                            EnqueueDatagram(
                                datagram,
                                remote,
                                ObservedUnixMilliseconds());
                    static_cast<void>(disposition);
                });
        }
        auto hello_receipt = Json{
            {"actorCapacity", node->Config().actor_capacity},
            {"buildIdentity", build.build_identity},
            {"instanceCapacity", node->Config().instance_capacity},
            {"modelManifest", build.model_manifest},
            {"platformQualification", build.platform_qualification},
            {"profileManifest", build.profile_manifest},
            {"runtimeNodeId", node->Config().runtime_node_id},
            {"simulationNodeId", node->Config().simulation_node_id},
        }.dump();
        auto hello_receipt_document = Json::parse(hello_receipt);
        if (gameplay_package != nullptr) {
            hello_receipt_document["configIdentity"] =
                gameplay_package->binding.config_identity;
            hello_receipt_document["gameplayPackageId"] =
                gameplay_package->binding.package_id;
            hello_receipt_document["mappingIdentity"] =
                gameplay_package->binding.mapping_identity;
            hello_receipt_document["navigationIdentity"] =
                gameplay_package->binding.navigation_identity;
            hello_receipt_document["physicsIdentity"] =
                gameplay_package->binding.physics_identity;
            hello_receipt_document["wireIdentity"] =
                gameplay_package->binding.wire_identity;
            hello_receipt = hello_receipt_document.dump();
        }
        ControlFrameCodec::Write(
            output,
            sequence.MakeOutbound(
                hello_frame.request_id,
                "node.hello.receipt",
                hello_receipt));

        std::uint64_t qualification_sample_sequence = 0;
        ControlFrame frame;
        while (ControlFrameCodec::Read(input, frame)) {
            sequence.AcceptInbound(frame);
            auto payload = ParseObject(frame.payload_json);
            if (frame.kind == "node.health.query") {
                RequireFields(payload, {"simulationNodeId"}, "health query");
                if (payload.at("simulationNodeId").get<std::string>() !=
                    node->Config().simulation_node_id) {
                    throw std::runtime_error("health query node identity is stale");
                }
                const auto receipt = Json{
                    {"actorCapacity", node->Config().actor_capacity},
                    {"healthy", node->Healthy()},
                    {"instanceCapacity", node->Config().instance_capacity},
                    {"runningInstances", node->RunningInstances()},
                    {"runtimeNodeId", node->Config().runtime_node_id},
                    {"simulationNodeId", node->Config().simulation_node_id},
                }.dump();
                ControlFrameCodec::Write(
                    output,
                    sequence.MakeOutbound(
                        frame.request_id,
                        "node.health.receipt",
                        receipt));
            } else if (
                frame.kind ==
                "battle_qualification_snapshot_request") {
                if (qualification == nullptr) {
                    throw std::runtime_error(
                        "qualification snapshot is disabled");
                }
                RequireFields(
                    payload,
                    {
                        "assignmentFingerprint",
                        "qualificationRunId",
                        "sampleSequence",
                        "simulationInstanceId",
                        "simulationNodeId",
                    },
                    "qualification snapshot request");
                const auto requested_sequence = ReadDecimal(
                    payload.at("sampleSequence"),
                    "qualification sample sequence");
                if (qualification_sample_sequence ==
                        std::numeric_limits<std::uint64_t>::max() ||
                    requested_sequence !=
                        qualification_sample_sequence + 1 ||
                    payload.at("qualificationRunId")
                            .get<std::string>() !=
                        qualification->run_id ||
                    payload.at("simulationNodeId")
                            .get<std::string>() !=
                        node->Config().simulation_node_id) {
                    throw std::runtime_error(
                        "qualification snapshot identity or sequence is stale");
                }
                const auto snapshot = node->QualificationSnapshot(
                    payload.at("simulationInstanceId")
                        .get<std::string>(),
                    payload.at("assignmentFingerprint")
                        .get<std::string>(),
                    ObservedUnixMilliseconds());
                qualification_sample_sequence = requested_sequence;
                ControlFrameCodec::Write(
                    output,
                    sequence.MakeOutbound(
                        frame.request_id,
                        "battle_qualification_snapshot_receipt",
                        QualificationSnapshotPayload(
                            snapshot,
                            qualification->run_id,
                            requested_sequence)));
            } else if (frame.kind == "instance.start") {
                RequireFields(
                    payload,
                    {
                        "actorCapacity",
                        "assignment",
                        "configIdentity",
                        "mappingGeneration",
                        "navigationIdentity",
                        "physicsIdentity",
                        "seed",
                        "startRequestId",
                    },
                    "instance start");
                const auto& assignment = payload.at("assignment");
                RequireFields(
                    assignment,
                    {
                        "assignmentFingerprint",
                        "fencingToken",
                        "generation",
                        "personalWorldId",
                        "runtimeNodeId",
                        "worldInstanceId",
                    },
                    "instance start assignment");
                const auto command = InstanceStartCommand{
                    .start_request_id =
                        payload.at("startRequestId").get<std::string>(),
                    .assignment =
                        ControlAssignment{
                            .personal_world_id =
                                assignment.at("personalWorldId")
                                    .get<std::string>(),
                            .world_instance_id =
                                assignment.at("worldInstanceId")
                                    .get<std::string>(),
                            .runtime_node_id =
                                assignment.at("runtimeNodeId")
                                    .get<std::string>(),
                            .generation =
                                ReadDecimal(
                                    assignment.at("generation"),
                                    "assignment generation"),
                            .fencing_token =
                                ReadDecimal(
                                    assignment.at("fencingToken"),
                                    "assignment fencing token"),
                            .fingerprint =
                                assignment.at("assignmentFingerprint")
                                    .get<std::string>(),
                        },
                    .mapping_generation =
                        ReadDecimal(
                            payload.at("mappingGeneration"),
                            "mapping generation"),
                    .seed =
                        ReadDecimal(payload.at("seed"), "simulation seed"),
                    .config_identity =
                        payload.at("configIdentity").get<std::string>(),
                    .navigation_identity =
                        payload.at("navigationIdentity").get<std::string>(),
                    .physics_identity =
                        payload.at("physicsIdentity").get<std::string>(),
                    .actor_capacity =
                        payload.at("actorCapacity").get<std::size_t>(),
                };
                if (command.start_request_id != frame.request_id) {
                    throw std::runtime_error("start request identity mismatch");
                }
                const auto ready = node->Start(command);
                const auto receipt = Json{
                    {"assignmentFingerprint", ready.assignment_fingerprint},
                    {"mappingGeneration", std::to_string(ready.mapping_generation)},
                    {"replayed", ready.replayed},
                    {"seed", std::to_string(ready.seed)},
                    {"simulationInstanceId", ready.simulation_instance_id},
                    {"startRequestId", ready.start_request_id},
                }.dump();
                ControlFrameCodec::Write(
                    output,
                    sequence.MakeOutbound(
                        frame.request_id,
                        "instance.ready",
                        receipt));
            } else if (
                frame.kind == "instance.status.query" ||
                frame.kind == "instance.drain" ||
                frame.kind == "instance.stop") {
                const auto fields =
                    frame.kind == "instance.status.query"
                        ? std::set<std::string, std::less<>>{
                              "assignmentFingerprint", "worldInstanceId"}
                        : std::set<std::string, std::less<>>{
                              "assignmentFingerprint",
                              "deadlineMs",
                              "worldInstanceId"};
                RequireFields(payload, fields, "instance lifecycle");
                const auto world_instance_id =
                    payload.at("worldInstanceId").get<std::string>();
                const auto assignment_fingerprint =
                    payload.at("assignmentFingerprint").get<std::string>();
                if (frame.kind == "instance.status.query") {
                    const auto status =
                        node->Status(
                            world_instance_id,
                            assignment_fingerprint);
                    ControlFrameCodec::Write(
                        output,
                        sequence.MakeOutbound(
                            frame.request_id,
                            "instance.status.receipt",
                            StatusPayload(status)));
                } else if (frame.kind == "instance.drain") {
                    const auto deadline = std::chrono::milliseconds(
                        ReadDecimal(payload.at("deadlineMs"), "drain deadline"));
                    const auto current =
                        node->Status(
                            world_instance_id,
                            assignment_fingerprint);
                    if (battle_runtime != nullptr &&
                        !current
                             .simulation_instance_id
                             .empty()) {
                        static_cast<void>(
                            battle_runtime->
                                RevokeInstance(
                                    current
                                        .simulation_instance_id,
                                    BattleSessionInvalidationReason::
                                        Instance));
                    }
                    const auto status = node->Drain(
                        world_instance_id,
                        assignment_fingerprint,
                        deadline);
                    EmitPendingResults(output, sequence, *node);
                    ControlFrameCodec::Write(
                        output,
                        sequence.MakeOutbound(
                            frame.request_id,
                            "instance.drained",
                            StatusPayload(status)));
                } else {
                    const auto deadline = std::chrono::milliseconds(
                        ReadDecimal(payload.at("deadlineMs"), "stop deadline"));
                    const auto current =
                        node->Status(
                            world_instance_id,
                            assignment_fingerprint);
                    if (battle_runtime != nullptr &&
                        !current
                             .simulation_instance_id
                             .empty()) {
                        static_cast<void>(
                            battle_runtime->
                                RevokeInstance(
                                    current
                                        .simulation_instance_id,
                                    BattleSessionInvalidationReason::
                                        Instance));
                    }
                    node->Stop(
                        world_instance_id,
                        assignment_fingerprint,
                        deadline);
                    const auto receipt = Json{
                        {"assignmentFingerprint", assignment_fingerprint},
                        {"state", "stopped"},
                        {"worldInstanceId", world_instance_id},
                    }.dump();
                    ControlFrameCodec::Write(
                        output,
                        sequence.MakeOutbound(
                            frame.request_id,
                            "instance.stopped",
                            receipt));
                }
            } else if (frame.kind == "battle.ticket.install") {
                RequireFields(
                    payload,
                    {
                        "binding",
                        "bindingFingerprint",
                        "installRequestId",
                        "proofKey",
                        "ticketId",
                    },
                    "battle ticket install");
                const auto& binding = payload.at("binding");
                RequireFields(
                    binding,
                    {
                        "actorSlot",
                        "advertisedHost",
                        "advertisedPort",
                        "assignmentFingerprint",
                        "assignmentGeneration",
                        "configIdentity",
                        "expiresAtUnixMs",
                        "fencingToken",
                        "issueId",
                        "issuedAtUnixMs",
                        "mappingGeneration",
                        "modelIdentity",
                        "personalWorldId",
                        "playerId",
                        "profileIdentity",
                        "role",
                        "runtimeNodeId",
                        "sessionEpoch",
                        "sessionId",
                        "simulationInstanceId",
                        "simulationNodeId",
                        "targetRevision",
                        "visitSessionId",
                        "wireIdentity",
                        "worldInstanceId",
                    },
                    "battle ticket binding");
                const auto install_request_id =
                    payload.at("installRequestId").get<std::string>();
                if (install_request_id != frame.request_id) {
                    throw std::runtime_error(
                        "ticket install request identity mismatch");
                }
                auto proof_key = ReadProofKey(payload.at("proofKey"));
                auto& proof_text =
                    payload.at("proofKey")
                        .get_ref<std::string&>();
                SecureZeroMemory(
                    proof_text.data(),
                    proof_text.size());
                proof_text.clear();
                SecureZeroMemory(
                    frame.payload_json.data(),
                    frame.payload_json.size());
                frame.payload_json.clear();
                auto command = BattleTicketInstallCommand{
                    .install_request_id = install_request_id,
                    .ticket_id =
                        payload.at("ticketId").get<std::string>(),
                    .binding_fingerprint =
                        payload.at("bindingFingerprint")
                            .get<std::string>(),
                    .binding =
                        BattleTicketBinding{
                            .player_id =
                                binding.at("playerId")
                                    .get<std::string>(),
                            .session_id =
                                binding.at("sessionId")
                                    .get<std::string>(),
                            .session_epoch =
                                ReadDecimal(
                                    binding.at("sessionEpoch"),
                                    "ticket session epoch"),
                            .role =
                                binding.at("role")
                                    .get<std::string>(),
                            .personal_world_id =
                                binding.at("personalWorldId")
                                    .get<std::string>(),
                            .visit_session_id =
                                binding.at("visitSessionId")
                                    .get<std::string>(),
                            .world_instance_id =
                                binding.at("worldInstanceId")
                                    .get<std::string>(),
                            .runtime_node_id =
                                binding.at("runtimeNodeId")
                                    .get<std::string>(),
                            .assignment_generation =
                                ReadDecimal(
                                    binding.at(
                                        "assignmentGeneration"),
                                    "ticket assignment generation"),
                            .fencing_token =
                                ReadDecimal(
                                    binding.at("fencingToken"),
                                    "ticket fencing token"),
                            .assignment_fingerprint =
                                binding.at(
                                    "assignmentFingerprint")
                                    .get<std::string>(),
                            .simulation_node_id =
                                binding.at("simulationNodeId")
                                    .get<std::string>(),
                            .simulation_instance_id =
                                binding.at(
                                    "simulationInstanceId")
                                    .get<std::string>(),
                            .mapping_generation =
                                ReadDecimal(
                                    binding.at(
                                        "mappingGeneration"),
                                    "ticket mapping generation"),
                            .target_revision =
                                ReadDecimal(
                                    binding.at("targetRevision"),
                                    "ticket target revision"),
                            .model_identity =
                                binding.at("modelIdentity")
                                    .get<std::string>(),
                            .profile_identity =
                                binding.at("profileIdentity")
                                    .get<std::string>(),
                            .config_identity =
                                binding.at("configIdentity")
                                    .get<std::string>(),
                            .wire_identity =
                                binding.at("wireIdentity")
                                    .get<std::string>(),
                            .actor_slot =
                                ReadActorSlot(
                                    binding.at("actorSlot"),
                                    "ticket actor slot"),
                            .advertised_host =
                                binding.at("advertisedHost")
                                    .get<std::string>(),
                            .advertised_port =
                                static_cast<std::uint16_t>(
                                    ReadDecimal(
                                        binding.at(
                                            "advertisedPort"),
                                        "ticket advertised port")),
                            .issue_id =
                                binding.at("issueId")
                                    .get<std::string>(),
                            .issued_at_unix_ms =
                                ReadDecimal(
                                    binding.at(
                                        "issuedAtUnixMs"),
                                    "ticket issued time"),
                            .expires_at_unix_ms =
                                ReadDecimal(
                                    binding.at(
                                        "expiresAtUnixMs"),
                                    "ticket expiry time"),
                        },
                    .proof_key = proof_key,
                };
                SecureZeroMemory(
                    proof_key.data(),
                    proof_key.size());
                BattleTicketReceipt installed;
                try {
                    installed = node->InstallBattleTicket(
                        command,
                        ObservedUnixMilliseconds());
                } catch (...) {
                    SecureZeroMemory(
                        command.proof_key.data(),
                        command.proof_key.size());
                    throw;
                }
                SecureZeroMemory(
                    command.proof_key.data(),
                    command.proof_key.size());
                if (battle_runtime != nullptr &&
                    !installed.superseded_binding_fingerprint.empty()) {
                    auto fingerprint =
                        ReadBindingFingerprint(
                            Json(installed
                                     .superseded_binding_fingerprint));
                    static_cast<void>(
                        battle_runtime->RevokeBinding(
                            fingerprint));
                    SecureZeroMemory(
                        fingerprint.data(),
                        fingerprint.size());
                }
                auto receipt = TicketReceiptPayload(installed);
                receipt["installRequestId"] = install_request_id;
                ControlFrameCodec::Write(
                    output,
                    sequence.MakeOutbound(
                        frame.request_id,
                        "battle.ticket.installed",
                        receipt.dump()));
            } else if (
                frame.kind == "battle.ticket.status.query") {
                RequireFields(
                    payload,
                    {
                        "bindingFingerprint",
                        "simulationInstanceId",
                        "ticketId",
                    },
                    "battle ticket status query");
                const auto ticket_id =
                    payload.at("ticketId").get<std::string>();
                const auto binding_fingerprint =
                    payload.at("bindingFingerprint")
                        .get<std::string>();
                const auto simulation_instance_id =
                    payload.at("simulationInstanceId")
                        .get<std::string>();
                Json receipt;
                try {
                    const auto status =
                        node->BattleTicketStatus(
                            ticket_id,
                            binding_fingerprint,
                            ObservedUnixMilliseconds());
                    if (status.simulation_instance_id !=
                        simulation_instance_id) {
                        throw std::runtime_error(
                            "ticket status instance is stale");
                    }
                    receipt = TicketReceiptPayload(status);
                } catch (const std::runtime_error&) {
                    receipt = Json{
                        {"bindingFingerprint",
                         binding_fingerprint},
                        {"simulationInstanceId",
                         simulation_instance_id},
                        {"state", "missing"},
                        {"ticketId", ticket_id},
                    };
                }
                ControlFrameCodec::Write(
                    output,
                    sequence.MakeOutbound(
                        frame.request_id,
                        "battle.ticket.status.receipt",
                        receipt.dump()));
            } else if (frame.kind == "battle.ticket.revoke") {
                RequireFields(
                    payload,
                    {
                        "actorSlot",
                        "bindingFingerprint",
                        "revokeRequestId",
                        "simulationInstanceId",
                        "ticketId",
                    },
                    "battle ticket revoke");
                const auto revoke_request_id =
                    payload.at("revokeRequestId")
                        .get<std::string>();
                if (revoke_request_id != frame.request_id) {
                    throw std::runtime_error(
                        "ticket revoke request identity mismatch");
                }
                const auto revoked = node->RevokeBattleTicket(
                    revoke_request_id,
                    payload.at("ticketId").get<std::string>(),
                    payload.at("bindingFingerprint")
                        .get<std::string>(),
                    payload.at("simulationInstanceId")
                        .get<std::string>(),
                    ReadActorSlot(
                        payload.at("actorSlot"),
                        "ticket revoke actor slot"));
                if (battle_runtime != nullptr) {
                    auto fingerprint =
                        ReadBindingFingerprint(
                            payload.at(
                                "bindingFingerprint"));
                    static_cast<void>(
                        battle_runtime->RevokeBinding(
                            fingerprint));
                    SecureZeroMemory(
                        fingerprint.data(),
                        fingerprint.size());
                }
                const auto receipt = Json{
                    {"bindingFingerprint",
                     revoked.binding_fingerprint},
                    {"revokeRequestId", revoke_request_id},
                    {"simulationInstanceId",
                     revoked.simulation_instance_id},
                    {"state", "revoked"},
                    {"ticketId", revoked.ticket_id},
                }.dump();
                ControlFrameCodec::Write(
                    output,
                    sequence.MakeOutbound(
                        frame.request_id,
                        "battle.ticket.revoked",
                        receipt));
            } else if (frame.kind == "result.ack") {
                RequireFields(
                    payload,
                    {"disposition", "proposalFingerprint", "resultId"},
                    "result ack");
                const auto disposition =
                    payload.at("disposition").get<std::string>();
                if (disposition != "committed" &&
                    disposition != "rejected" &&
                    disposition != "replayed") {
                    throw std::runtime_error("result ack disposition is invalid");
                }
                node->AckResult(
                    payload.at("resultId").get<std::string>(),
                    payload.at("proposalFingerprint").get<std::string>());
            } else if (frame.kind == "node.shutdown") {
                RequireFields(payload, {"deadlineMs"}, "node shutdown");
                if (battle_runtime != nullptr) {
                    battle_runtime->Stop();
                }
                node->BeginShutdown(std::chrono::milliseconds(
                    ReadDecimal(payload.at("deadlineMs"), "shutdown deadline")));
                const auto receipt = Json{
                    {"pendingResults", node->PendingResults().size()},
                    {"runningInstances", node->RunningInstances()},
                    {"simulationNodeId", node->Config().simulation_node_id},
                    {"state", "stopped"},
                }.dump();
                ControlFrameCodec::Write(
                    output,
                    sequence.MakeOutbound(
                        frame.request_id,
                        "node.stopped",
                        receipt));
                return 0;
            } else {
                throw std::runtime_error("control request kind is not accepted");
            }
        }
        if (battle_runtime != nullptr) {
            battle_runtime->Stop();
        }
        node->BeginShutdown(std::chrono::milliseconds(0));
        return 0;
    } catch (const std::exception& error) {
        diagnostics << "simulation control failed: " << error.what() << '\n';
        return 1;
    }
}

}  // namespace ihomeland::sim
