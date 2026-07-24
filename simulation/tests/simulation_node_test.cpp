#include "ihomeland/sim/control/simulation_node.hpp"
#include "ihomeland/sim/core/sha256.hpp"

#include <chrono>
#include <iomanip>
#include <sstream>
#include <stdexcept>
#include <string>

namespace {

using namespace std::chrono_literals;

/// Require 把 node lifecycle 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// RequireFailure 要求动作 fail closed。
template <typename Action>
void RequireFailure(Action action, const char* message) {
    try {
        action();
    } catch (const std::exception&) {
        return;
    }
    throw std::runtime_error(message);
}

/// TestConfig 返回固定 qualification 与容量绑定。
[[nodiscard]] ihomeland::sim::SimulationNodeConfig TestConfig() {
    return {
        .simulation_node_id = "snode_control_test",
        .runtime_node_id = "rnode_control_test",
        .build_identity = std::string(64, 'a'),
        .model_manifest = std::string(64, 'b'),
        .profile_manifest = std::string(64, 'c'),
        .instance_capacity = 2,
        .actor_capacity = 8,
    };
}

/// TestCommand 返回完整 assignment/start binding。
[[nodiscard]] ihomeland::sim::InstanceStartCommand TestCommand(
    const std::string& world_suffix) {
    auto command = ihomeland::sim::InstanceStartCommand{
        .start_request_id = "sctl_start_control_" + world_suffix,
        .assignment =
            {
                .personal_world_id = "pworld_control_" + world_suffix,
                .world_instance_id = "winst_control_" + world_suffix,
                .runtime_node_id = "rnode_control_test",
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
    command.assignment.fingerprint = ihomeland::sim::Sha256Text(material);
    return command;
}

/// TestLifecycle 验证 start replay、capacity、drain、result ack 与 stop replay。
void TestLifecycle() {
    ihomeland::sim::SimulationNode node(TestConfig());
    const auto first_command = TestCommand("instance_0001");
    const auto first = node.Start(first_command);
    Require(!first.replayed, "first start was marked replay");
    const auto repeated = node.Start(first_command);
    Require(repeated.replayed, "matching start did not replay");
    Require(
        repeated.simulation_instance_id == first.simulation_instance_id,
        "start replay created a new runtime identity");

    auto conflict = first_command;
    conflict.config_identity = std::string(64, '2');
    RequireFailure(
        [&] { static_cast<void>(node.Start(conflict)); },
        "WorldInstanceID conflict was accepted");

    const auto second_command = TestCommand("instance_0002");
    static_cast<void>(node.Start(second_command));
    Require(node.RunningInstances() == 2, "instance capacity accounting drifted");
    RequireFailure(
        [&] {
            static_cast<void>(
                node.Start(TestCommand("instance_0003")));
        },
        "node instance capacity was exceeded");

    const auto drained = node.Drain(
        first_command.assignment.world_instance_id,
        first_command.assignment.fingerprint,
        1000ms);
    Require(drained.state == "drained", "instance was not drained");
    const auto proposals = node.PendingResults();
    Require(proposals.size() == 1, "drain did not create one result");
    RequireFailure(
        [&] {
            node.AckResult(
                proposals.front().result_id,
                std::string(64, '9'));
        },
        "result fingerprint drift was accepted");
    node.AckResult(
        proposals.front().result_id,
        proposals.front().proposal_fingerprint);
    node.AckResult(
        proposals.front().result_id,
        proposals.front().proposal_fingerprint);
    Require(node.PendingResults().empty(), "acked result remained pending");
    RequireFailure(
        [&] {
            node.AckResult(
                proposals.front().result_id,
                std::string(64, '8'));
        },
        "replayed result ack fingerprint drift was accepted");

    node.Stop(
        first_command.assignment.world_instance_id,
        first_command.assignment.fingerprint,
        1000ms);
    node.Stop(
        first_command.assignment.world_instance_id,
        first_command.assignment.fingerprint,
        1000ms);
    RequireFailure(
        [&] { static_cast<void>(node.Start(first_command)); },
        "stopped WorldInstanceID was restarted in the same node");
    RequireFailure(
        [&] {
            node.Stop(
                TestCommand("unknown_stop").assignment.world_instance_id,
                first_command.assignment.fingerprint,
                1000ms);
        },
        "stop replay accepted a different WorldInstanceID");
    Require(node.RunningInstances() == 1, "stop replay changed another instance");

    node.BeginShutdown(1000ms);
    Require(!node.Healthy(), "shutdown did not revoke health");
    Require(node.RunningInstances() == 0, "shutdown leaked an instance");
    RequireFailure(
        [&] { static_cast<void>(node.Start(first_command)); },
        "draining node accepted a new start");
}

/// TestActorQualificationCap 验证 8/9 actor hard gate。
void TestActorQualificationCap() {
    ihomeland::sim::SimulationNode node(TestConfig());
    auto command = TestCommand("actor_gate_0001");
    command.actor_capacity = 9;
    RequireFailure(
        [&] { static_cast<void>(node.Start(command)); },
        "nine actors bypassed qualification cap");
}

/// TestResultOutboxCapacity 验证 256 entries hard limit 满后 fail closed 且不丢既有 proposal。
void TestResultOutboxCapacity() {
    auto config = TestConfig();
    config.instance_capacity = 1;
    ihomeland::sim::SimulationNode node(config);
    for (std::size_t index = 0;
         index < ihomeland::sim::SimulationNode::ResultOutboxLimit;
         ++index) {
        std::ostringstream suffix;
        suffix << "outbox_" << std::setw(4) << std::setfill('0') << index;
        auto command = TestCommand(suffix.str());
        static_cast<void>(node.Start(command));
        static_cast<void>(node.Drain(
            command.assignment.world_instance_id,
            command.assignment.fingerprint,
            1000ms));
        node.Stop(
            command.assignment.world_instance_id,
            command.assignment.fingerprint,
            1000ms);
    }
    Require(
        node.PendingResults().size() ==
            ihomeland::sim::SimulationNode::ResultOutboxLimit,
        "result outbox did not reach hard limit");
    auto overflow = TestCommand("outbox_overflow");
    static_cast<void>(node.Start(overflow));
    RequireFailure(
        [&] {
            static_cast<void>(node.Drain(
                overflow.assignment.world_instance_id,
                overflow.assignment.fingerprint,
                1000ms));
        },
        "result outbox overflow was accepted");
    Require(
        node.PendingResults().size() ==
            ihomeland::sim::SimulationNode::ResultOutboxLimit,
        "outbox overflow discarded an existing result");
    const auto first_pending = node.PendingResults().front();
    node.AckResult(
        first_pending.result_id,
        first_pending.proposal_fingerprint);
    const auto recovered = node.Drain(
        overflow.assignment.world_instance_id,
        overflow.assignment.fingerprint,
        1000ms);
    Require(
        recovered.state == "drained" &&
            node.PendingResults().size() ==
                ihomeland::sim::SimulationNode::ResultOutboxLimit,
        "outbox capacity recovery lost or fabricated a result");
}

}  // namespace

/// main 运行多实例 control node lifecycle 回归。
int main() {
    try {
        TestLifecycle();
        TestActorQualificationCap();
        TestResultOutboxCapacity();
        return 0;
    } catch (const std::exception&) {
        return 1;
    }
}
