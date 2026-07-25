#include "ihomeland/sim/control/simulation_node.hpp"
#include "ihomeland/sim/core/sha256.hpp"

#include <chrono>
#include <array>
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

/// TestTicketCommand 返回指向 exact runtime 的完整 ticket install 输入。
[[nodiscard]] ihomeland::sim::BattleTicketInstallCommand TestTicketCommand(
    const ihomeland::sim::InstanceStartCommand& start,
    const ihomeland::sim::InstanceReadyReceipt& ready,
    const std::size_t index,
    const std::uint64_t expires_at_unix_ms = 4000) {
    std::ostringstream suffix;
    suffix << std::setw(4) << std::setfill('0') << index;
    auto command = ihomeland::sim::BattleTicketInstallCommand{
        .install_request_id = "sctl_install_ticket_" + suffix.str(),
        .ticket_id = "btk1_ticket_" + suffix.str(),
        .binding_fingerprint =
            ihomeland::sim::Sha256Text("ticket-binding|" + suffix.str()),
        .binding =
            {
                .player_id = "ply_ticket_" + suffix.str(),
                .session_id = "ses_ticket_" + suffix.str(),
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
                .simulation_node_id = "snode_control_test",
                .simulation_instance_id =
                    ready.simulation_instance_id,
                .mapping_generation = start.mapping_generation,
                .target_revision = 5,
                .model_identity = std::string(64, 'b'),
                .profile_identity = std::string(64, 'c'),
                .config_identity = start.config_identity,
                .wire_identity = std::string(64, '1'),
                .actor_slot = static_cast<std::uint8_t>(index),
                .advertised_host = "127.0.0.1",
                .advertised_port = 58445,
                .issue_id = "biss_ticket_" + suffix.str(),
                .issued_at_unix_ms = 1000,
                .expires_at_unix_ms = expires_at_unix_ms,
            },
        .proof_key = {},
    };
    command.proof_key.fill(static_cast<std::uint8_t>(index + 1));
    return command;
}

/// TestBattleTicketRegistry 验证 exact replay、不可逆状态与 instance 8/9 hard cap。
void TestBattleTicketRegistry() {
    constexpr std::uint64_t observed_unix_ms = 2000;
    ihomeland::sim::SimulationNode node(TestConfig());
    const auto start = TestCommand("ticket_registry_0001");
    const auto ready = node.Start(start);
    std::array<ihomeland::sim::BattleTicketInstallCommand, 8> commands;
    for (std::size_t index = 0; index < commands.size(); ++index) {
        commands[index] = TestTicketCommand(start, ready, index);
        const auto receipt =
            node.InstallBattleTicket(commands[index], observed_unix_ms);
        Require(
            receipt.state ==
                    ihomeland::sim::BattleTicketState::Installed &&
                receipt.actor_slot == index && !receipt.replayed,
            "ticket install receipt drifted");
    }
    Require(
        node.InstalledOrActiveActors(
            ready.simulation_instance_id,
            observed_unix_ms) == 8,
        "eight installed actors were not reserved");

    const auto replay =
        node.InstallBattleTicket(commands.front(), observed_unix_ms);
    Require(
        replay.replayed &&
            replay.state == ihomeland::sim::BattleTicketState::Installed,
        "exact ticket install did not replay");
    auto binding_drift = commands.front();
    ++binding_drift.binding.target_revision;
    RequireFailure(
        [&] {
            static_cast<void>(
                node.InstallBattleTicket(binding_drift, observed_unix_ms));
        },
        "ticket install binding drift was accepted");
    auto proof_drift = commands.front();
    ++proof_drift.proof_key.front();
    RequireFailure(
        [&] {
            static_cast<void>(
                node.InstallBattleTicket(proof_drift, observed_unix_ms));
        },
        "ticket install proof drift was accepted");

    auto ninth = TestTicketCommand(start, ready, 8);
    ninth.binding.actor_slot = 7;
    RequireFailure(
        [&] {
            static_cast<void>(
                node.InstallBattleTicket(ninth, observed_unix_ms));
        },
        "ninth installed actor bypassed instance hard cap");
    Require(
        node.InstalledOrActiveActors(
            ready.simulation_instance_id,
            observed_unix_ms) == 8,
        "rejected ninth actor changed capacity");

    const auto consumed = node.ConsumeBattleTicket(
        commands[0].ticket_id,
        commands[0].binding_fingerprint,
        observed_unix_ms);
    Require(
        consumed.state == ihomeland::sim::BattleTicketState::Consumed &&
            !consumed.replayed,
        "installed ticket was not consumed");
    const auto consume_replay = node.ConsumeBattleTicket(
        commands[0].ticket_id,
        commands[0].binding_fingerprint,
        observed_unix_ms);
    Require(
        consume_replay.state ==
                ihomeland::sim::BattleTicketState::Consumed &&
            consume_replay.replayed,
        "ticket consume was not idempotent");

    const auto revoked = node.RevokeBattleTicket(
        "sctl_revoke_ticket_0000",
        commands[0].ticket_id,
        commands[0].binding_fingerprint,
        commands[0].binding.simulation_instance_id,
        commands[0].binding.actor_slot);
    Require(
        revoked.state == ihomeland::sim::BattleTicketState::Revoked &&
            !revoked.replayed,
        "consumed ticket was not revoked");
    const auto revoke_replay = node.RevokeBattleTicket(
        "sctl_revoke_ticket_0000",
        commands[0].ticket_id,
        commands[0].binding_fingerprint,
        commands[0].binding.simulation_instance_id,
        commands[0].binding.actor_slot);
    Require(revoke_replay.replayed, "ticket revoke was not idempotent");
    RequireFailure(
        [&] {
            static_cast<void>(node.RevokeBattleTicket(
                "sctl_revoke_ticket_conflict",
                commands[0].ticket_id,
                commands[0].binding_fingerprint,
                commands[0].binding.simulation_instance_id,
                commands[0].binding.actor_slot));
        },
        "ticket revoke request drift was accepted");
    const auto terminal_install_replay =
        node.InstallBattleTicket(commands[0], observed_unix_ms);
    Require(
        terminal_install_replay.replayed &&
            terminal_install_replay.state ==
                ihomeland::sim::BattleTicketState::Revoked,
        "terminal ticket install replay lost its tombstone");

    auto replacement = TestTicketCommand(start, ready, 8, 5000);
    replacement.binding.actor_slot = 0;
    static_cast<void>(
        node.InstallBattleTicket(replacement, observed_unix_ms));
    Require(
        node.InstalledOrActiveActors(
            ready.simulation_instance_id,
            observed_unix_ms) == 8,
        "revoked actor slot was not reusable");

    const auto expired = node.BattleTicketStatus(
        commands[1].ticket_id,
        commands[1].binding_fingerprint,
        commands[1].binding.expires_at_unix_ms);
    Require(
        expired.state == ihomeland::sim::BattleTicketState::Expired,
        "ticket did not expire at its absolute deadline");
    RequireFailure(
        [&] {
            static_cast<void>(node.ConsumeBattleTicket(
                commands[1].ticket_id,
                commands[1].binding_fingerprint,
                commands[1].binding.expires_at_unix_ms));
        },
        "expired ticket was consumed");
    auto expiry_replacement = TestTicketCommand(start, ready, 9, 5000);
    expiry_replacement.binding.actor_slot = 1;
    static_cast<void>(
        node.InstallBattleTicket(expiry_replacement, observed_unix_ms));

    auto stale_target = TestTicketCommand(start, ready, 10, 5000);
    ++stale_target.binding.mapping_generation;
    RequireFailure(
        [&] {
            static_cast<void>(
                node.InstallBattleTicket(stale_target, observed_unix_ms));
        },
        "stale ticket target was accepted");

    auto cancelled_before_install =
        TestTicketCommand(start, ready, 11, 5000);
    cancelled_before_install.binding.actor_slot = 3;
    static_cast<void>(node.RevokeBattleTicket(
        "sctl_revoke_ticket_0011",
        cancelled_before_install.ticket_id,
        cancelled_before_install.binding_fingerprint,
        cancelled_before_install.binding.simulation_instance_id,
        cancelled_before_install.binding.actor_slot));
    const auto cancelled_replay =
        node.InstallBattleTicket(
            cancelled_before_install,
            observed_unix_ms);
    Require(
        cancelled_replay.state ==
                ihomeland::sim::BattleTicketState::Revoked &&
            cancelled_replay.replayed,
        "pre-install revoke allowed delayed install to restore eligibility");

    node.Stop(
        start.assignment.world_instance_id,
        start.assignment.fingerprint,
        1000ms);
    const auto stopped = node.BattleTicketStatus(
        commands[2].ticket_id,
        commands[2].binding_fingerprint,
        observed_unix_ms);
    Require(
        stopped.state == ihomeland::sim::BattleTicketState::Revoked,
        "instance stop did not revoke installed ticket");
    const auto replay_after_stop =
        node.InstallBattleTicket(commands[2], 5000);
    Require(
        replay_after_stop.replayed &&
            replay_after_stop.state ==
                ihomeland::sim::BattleTicketState::Revoked,
        "stopped target lost exact terminal install replay");
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
        TestBattleTicketRegistry();
        TestResultOutboxCapacity();
        return 0;
    } catch (const std::exception&) {
        return 1;
    }
}
