#include "ihomeland/sim/control/control_server.hpp"

#include "ihomeland/sim/control/control_frame.hpp"
#include "ihomeland/sim/control/simulation_node.hpp"

#include <nlohmann/json.hpp>

#include <charconv>
#include <chrono>
#include <cstdint>
#include <istream>
#include <memory>
#include <ostream>
#include <set>
#include <stdexcept>
#include <string>
#include <utility>

namespace ihomeland::sim {
namespace {

using Json = nlohmann::json;

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
    const ControlBuildBinding& build) {
    try {
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
        RequireFields(
            hello,
            {
                "actorCapacity",
                "expectedBuildIdentity",
                "expectedModelManifest",
                "expectedProfileManifest",
                "instanceCapacity",
                "runtimeNodeId",
                "simulationNodeId",
            },
            "hello challenge");
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
        auto node = std::make_unique<SimulationNode>(SimulationNodeConfig{
            .simulation_node_id = hello.at("simulationNodeId").get<std::string>(),
            .runtime_node_id = hello.at("runtimeNodeId").get<std::string>(),
            .build_identity = build.build_identity,
            .model_manifest = build.model_manifest,
            .profile_manifest = build.profile_manifest,
            .instance_capacity =
                hello.at("instanceCapacity").get<std::size_t>(),
            .actor_capacity = hello.at("actorCapacity").get<std::size_t>(),
        });
        const auto hello_receipt = Json{
            {"actorCapacity", node->Config().actor_capacity},
            {"buildIdentity", build.build_identity},
            {"instanceCapacity", node->Config().instance_capacity},
            {"modelManifest", build.model_manifest},
            {"platformQualification", build.platform_qualification},
            {"profileManifest", build.profile_manifest},
            {"runtimeNodeId", node->Config().runtime_node_id},
            {"simulationNodeId", node->Config().simulation_node_id},
        }.dump();
        ControlFrameCodec::Write(
            output,
            sequence.MakeOutbound(
                hello_frame.request_id,
                "node.hello.receipt",
                hello_receipt));

        ControlFrame frame;
        while (ControlFrameCodec::Read(input, frame)) {
            sequence.AcceptInbound(frame);
            const auto payload = ParseObject(frame.payload_json);
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
        node->BeginShutdown(std::chrono::milliseconds(0));
        return 0;
    } catch (const std::exception& error) {
        diagnostics << "simulation control failed: " << error.what() << '\n';
        return 1;
    }
}

}  // namespace ihomeland::sim
