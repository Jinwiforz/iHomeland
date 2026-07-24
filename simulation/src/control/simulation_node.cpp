#include "ihomeland/sim/control/simulation_node.hpp"

#include "ihomeland/sim/core/adapter_smoke.hpp"
#include "ihomeland/sim/core/sha256.hpp"
#include "ihomeland/sim/simulation/identity.hpp"
#include "ihomeland/sim/simulation/simulation_instance.hpp"
#include "ihomeland/sim/simulation/tick_clock.hpp"

#include <windows.h>
#include <bcrypt.h>

#include <algorithm>
#include <array>
#include <chrono>
#include <memory>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace ihomeland::sim {
namespace {

using namespace std::chrono_literals;

/// RequireIdentity 验证 control identity 只含安全 ASCII 且有固定前缀。
void RequireIdentity(
    const std::string& value,
    const std::string_view prefix,
    const char* context) {
    if (!value.starts_with(prefix) || value.size() < prefix.size() + 4 ||
        value.size() > 96 ||
        !std::all_of(value.begin(), value.end(), [](const char character) {
            return (character >= 'a' && character <= 'z') ||
                   (character >= 'A' && character <= 'Z') ||
                   (character >= '0' && character <= '9') ||
                   character == '_' || character == '-';
        })) {
        throw std::invalid_argument(std::string(context) + " is invalid");
    }
}

/// RequireDigest 验证 canonical lowercase SHA-256。
void RequireDigest(const std::string& digest, const char* context) {
    if (digest.size() != 64 ||
        !std::all_of(digest.begin(), digest.end(), [](const char value) {
            return (value >= '0' && value <= '9') ||
                   (value >= 'a' && value <= 'f');
        })) {
        throw std::invalid_argument(std::string(context) + " is not SHA-256");
    }
}

/// RandomIdentity128 使用 Windows CSPRNG 生成非零 runtime identity。
[[nodiscard]] Identity128 RandomIdentity128() {
    std::array<unsigned char, 16> bytes{};
    const auto status = BCryptGenRandom(
        nullptr,
        bytes.data(),
        static_cast<ULONG>(bytes.size()),
        BCRYPT_USE_SYSTEM_PREFERRED_RNG);
    if (status < 0) {
        throw std::runtime_error("simulation instance CSPRNG failed");
    }
    bool any_nonzero = false;
    constexpr char digits[] = "0123456789abcdef";
    std::string text(bytes.size() * 2, '0');
    for (std::size_t index = 0; index < bytes.size(); ++index) {
        any_nonzero = any_nonzero || bytes[index] != 0;
        text[index * 2] = digits[bytes[index] >> 4U];
        text[index * 2 + 1] = digits[bytes[index] & 0x0fU];
    }
    if (!any_nonzero) {
        throw std::runtime_error("simulation instance CSPRNG returned zero");
    }
    return Identity128::ParseLowerHex(text);
}

/// DerivedIdentity128 将 Go string identity 映射为 C++ core 的固定 128-bit value。
[[nodiscard]] Identity128 DerivedIdentity128(const std::string& value) {
    return Identity128::ParseLowerHex(Sha256Text(value).substr(0, 32));
}

/// ProposalFingerprint 绑定 lifecycle result 的完整不可变字段。
[[nodiscard]] std::string ProposalFingerprint(const ResultProposal& proposal) {
    return Sha256Text(
        proposal.result_id + "|" + proposal.result_kind + "|" +
        proposal.assignment_fingerprint + "|" +
        proposal.simulation_instance_id + "|" +
        std::to_string(proposal.tick_start) + "|" +
        std::to_string(proposal.tick_end) + "|" +
        proposal.payload_digest + "|" + proposal.evidence_digest);
}

/// AssignmentFingerprint 重算 Go 定义的完整 placement stamp 摘要。
[[nodiscard]] std::string AssignmentFingerprint(
    const ControlAssignment& assignment) {
    std::string material;
    const auto append = [&](const std::string& value) {
        if (!material.empty()) {
            material.push_back('\0');
        }
        material.append(value);
    };
    append(assignment.personal_world_id);
    append(assignment.world_instance_id);
    append(assignment.runtime_node_id);
    append(std::to_string(assignment.generation));
    append(std::to_string(assignment.fencing_token));
    return Sha256Text(material);
}

/// SameStartCommand 比较可影响 runtime identity 的全部 immutable start 输入。
[[nodiscard]] bool SameStartCommand(
    const InstanceStartCommand& first,
    const InstanceStartCommand& second) {
    return first.start_request_id == second.start_request_id &&
           first.assignment.personal_world_id ==
               second.assignment.personal_world_id &&
           first.assignment.world_instance_id ==
               second.assignment.world_instance_id &&
           first.assignment.runtime_node_id ==
               second.assignment.runtime_node_id &&
           first.assignment.generation == second.assignment.generation &&
           first.assignment.fencing_token ==
               second.assignment.fencing_token &&
           first.assignment.fingerprint == second.assignment.fingerprint &&
           first.mapping_generation == second.mapping_generation &&
           first.seed == second.seed &&
           first.config_identity == second.config_identity &&
           first.navigation_identity == second.navigation_identity &&
           first.physics_identity == second.physics_identity &&
           first.actor_capacity == second.actor_capacity;
}

}  // namespace

/// Entry 绑定 control assignment、core worker 与 lifecycle replay identity。
struct SimulationNode::Entry final {
    /// command 保存首次 start 的 immutable input。
    InstanceStartCommand command;
    /// simulation_instance_id 是对外 C++ runtime identity。
    std::string simulation_instance_id;
    /// clock 由 instance worker 使用 monotonic cadence。
    std::shared_ptr<SteadyTickClock> clock;
    /// instance 是唯一 core lifecycle/Tick owner。
    std::unique_ptr<SimulationInstance> instance;
    /// drained 标记 ingress 已关闭且 lifecycle result 已生成。
    bool drained{false};
};

SimulationNode::SimulationNode(SimulationNodeConfig config)
    : config_(std::move(config)) {
    RequireIdentity(config_.simulation_node_id, "snode_", "SimulationNodeID");
    RequireIdentity(config_.runtime_node_id, "rnode_", "RuntimeNodeID");
    RequireDigest(config_.build_identity, "build identity");
    RequireDigest(config_.model_manifest, "model manifest");
    RequireDigest(config_.profile_manifest, "profile manifest");
    if (config_.instance_capacity == 0 ||
        config_.instance_capacity > ResultOutboxLimit ||
        config_.actor_capacity == 0 || config_.actor_capacity > 8) {
        throw std::invalid_argument("simulation node capacity is invalid");
    }
    entries_.reserve(config_.instance_capacity);
    retired_bindings_.reserve(config_.instance_capacity);
    outbox_.reserve(ResultOutboxLimit);
    acked_results_.reserve(ResultOutboxLimit);
}

SimulationNode::~SimulationNode() {
    try {
        BeginShutdown(0ms);
    } catch (...) {
    }
}

InstanceReadyReceipt SimulationNode::Start(const InstanceStartCommand& command) {
    if (!healthy_) {
        throw std::runtime_error("simulation node is draining");
    }
    if (std::any_of(
            retired_bindings_.begin(),
            retired_bindings_.end(),
            [&](const auto& retired) {
                return retired.world_instance_id ==
                       command.assignment.world_instance_id;
            })) {
        throw std::runtime_error("WorldInstanceID is retired in this node");
    }
    RequireIdentity(command.start_request_id, "sctl_", "start request identity");
    RequireIdentity(command.assignment.personal_world_id, "pworld_", "PersonalWorldID");
    RequireIdentity(command.assignment.world_instance_id, "winst_", "WorldInstanceID");
    RequireIdentity(command.assignment.runtime_node_id, "rnode_", "RuntimeNodeID");
    RequireDigest(command.assignment.fingerprint, "assignment fingerprint");
    RequireDigest(command.config_identity, "config identity");
    RequireDigest(command.navigation_identity, "navigation identity");
    RequireDigest(command.physics_identity, "physics identity");
    if (command.assignment.runtime_node_id != config_.runtime_node_id ||
        command.assignment.fingerprint !=
            AssignmentFingerprint(command.assignment) ||
        command.assignment.generation == 0 ||
        command.assignment.fencing_token == 0 ||
        command.mapping_generation == 0 ||
        command.seed == 0 ||
        command.actor_capacity == 0 ||
        command.actor_capacity > config_.actor_capacity) {
        throw std::invalid_argument("simulation start binding is invalid");
    }
    const auto existing = std::find_if(
        entries_.begin(),
        entries_.end(),
        [&](const auto& entry) {
            return entry->command.assignment.world_instance_id ==
                   command.assignment.world_instance_id;
        });
    if (existing != entries_.end()) {
        const auto& entry = **existing;
        if (!SameStartCommand(entry.command, command)) {
            throw std::runtime_error(
                "WorldInstanceID was reused with another assignment");
        }
        return InstanceReadyReceipt{
            .start_request_id = entry.command.start_request_id,
            .assignment_fingerprint = entry.command.assignment.fingerprint,
            .simulation_instance_id = entry.simulation_instance_id,
            .mapping_generation = entry.command.mapping_generation,
            .seed = entry.command.seed,
            .replayed = true,
        };
    }
    if (entries_.size() >= config_.instance_capacity) {
        throw std::length_error("simulation node instance capacity exhausted");
    }

    auto core_id = RandomIdentity128();
    auto clock = std::make_shared<SteadyTickClock>();
    auto identity = SimulationInstanceIdentity(
        core_id,
        AssignmentStamp(
            DerivedIdentity128(command.assignment.world_instance_id),
            command.assignment.generation,
            command.assignment.fencing_token,
            DerivedIdentity128(command.assignment.runtime_node_id)),
        command.mapping_generation,
        BuildConfigIdentity(
            config_.build_identity,
            config_.model_manifest,
            config_.profile_manifest,
            command.config_identity,
            command.navigation_identity,
            command.physics_identity));
    auto instance = std::make_unique<SimulationInstance>(
        std::move(identity),
        SimulationInstanceConfig{
            .tick_step = 50ms,
            .inbox_capacity = 256,
            .hard_tick_debt = 4,
        },
        clock,
        [](const TickObservation&) {});
    const std::vector<StartupStep> startup{
        {
            .stage = StartupStage::Fixture,
            .initialize = [] {},
            .rollback = [] {},
        },
        {
            .stage = StartupStage::Physics,
            .initialize = [] {
                if (!RunJoltSmoke()) {
                    throw std::runtime_error("Jolt adapter startup failed");
                }
            },
            .rollback = [] {},
        },
        {
            .stage = StartupStage::Navigation,
            .initialize = [] {
                if (!RunDetourSmoke()) {
                    throw std::runtime_error("Detour adapter startup failed");
                }
            },
            .rollback = [] {},
        },
    };
    instance->Start(startup);
    auto entry = std::make_unique<Entry>();
    entry->command = command;
    entry->simulation_instance_id = "sinst_" + core_id.ToLowerHex();
    entry->clock = std::move(clock);
    entry->instance = std::move(instance);
    const auto receipt = InstanceReadyReceipt{
        .start_request_id = entry->command.start_request_id,
        .assignment_fingerprint = entry->command.assignment.fingerprint,
        .simulation_instance_id = entry->simulation_instance_id,
        .mapping_generation = entry->command.mapping_generation,
        .seed = entry->command.seed,
        .replayed = false,
    };
    entries_.push_back(std::move(entry));
    return receipt;
}

InstanceStatusReceipt SimulationNode::Status(
    const std::string& world_instance_id,
    const std::string& assignment_fingerprint) const {
    const auto iterator = std::find_if(
        entries_.begin(),
        entries_.end(),
        [&](const auto& entry) {
            return entry->command.assignment.world_instance_id == world_instance_id;
        });
    if (iterator == entries_.end() ||
        (*iterator)->command.assignment.fingerprint != assignment_fingerprint) {
        if (std::find_if(
                retired_bindings_.begin(),
                retired_bindings_.end(),
                [&](const auto& retired) {
                    return retired.world_instance_id == world_instance_id &&
                           retired.assignment_fingerprint ==
                               assignment_fingerprint;
                }) != retired_bindings_.end()) {
            return InstanceStatusReceipt{
                .assignment_fingerprint = assignment_fingerprint,
                .simulation_instance_id = "",
                .state = "stopped",
                .committed_tick = 0,
            };
        }
        throw std::runtime_error("simulation instance status binding is stale");
    }
    const auto& entry = **iterator;
    return InstanceStatusReceipt{
        .assignment_fingerprint = entry.command.assignment.fingerprint,
        .simulation_instance_id = entry.simulation_instance_id,
        .state = entry.drained ? "drained" : "running",
        .committed_tick = entry.instance->CommittedTick(),
    };
}

InstanceStatusReceipt SimulationNode::Drain(
    const std::string& world_instance_id,
    const std::string& assignment_fingerprint,
    const std::chrono::milliseconds deadline) {
    const auto iterator = std::find_if(
        entries_.begin(),
        entries_.end(),
        [&](const auto& entry) {
            return entry->command.assignment.world_instance_id == world_instance_id;
        });
    if (iterator == entries_.end() ||
        (*iterator)->command.assignment.fingerprint != assignment_fingerprint) {
        throw std::runtime_error("simulation instance drain binding is stale");
    }
    auto& entry = **iterator;
    if (!entry.drained) {
        if (outbox_.size() >= ResultOutboxLimit) {
            throw std::length_error("simulation result outbox capacity exhausted");
        }
        entry.instance->BeginDrain();
        if (!entry.instance->Stop(deadline)) {
            throw std::runtime_error("simulation instance drain deadline exceeded");
        }
        const auto tick_end = entry.instance->CommittedTick();
        const auto payload_digest = Sha256Text(
            entry.simulation_instance_id + "|" + std::to_string(tick_end));
        ResultProposal proposal{
            .result_id =
                "sresult_" +
                Sha256Text(
                    entry.command.assignment.fingerprint + "|" +
                    entry.simulation_instance_id + "|" +
                    std::to_string(tick_end))
                    .substr(0, 32),
            .result_kind = "simulation.lifecycle.summary.v1",
            .assignment_fingerprint = entry.command.assignment.fingerprint,
            .simulation_instance_id = entry.simulation_instance_id,
            .tick_start = tick_end == 0 ? 0ULL : 1ULL,
            .tick_end = tick_end,
            .payload_digest = payload_digest,
            .evidence_digest = Sha256Text(
                config_.build_identity + "|" + config_.model_manifest + "|" +
                config_.profile_manifest + "|" +
                entry.command.config_identity + "|" +
                entry.command.navigation_identity + "|" +
                entry.command.physics_identity + "|" +
                std::to_string(entry.command.mapping_generation) + "|" +
                std::to_string(entry.command.seed) + "|" +
                std::to_string(entry.command.actor_capacity) + "|" +
                std::to_string(tick_end)),
            .proposal_fingerprint = "",
        };
        proposal.proposal_fingerprint = ProposalFingerprint(proposal);
        outbox_.push_back(std::move(proposal));
        entry.drained = true;
    }
    return Status(world_instance_id, assignment_fingerprint);
}

void SimulationNode::Stop(
    const std::string& world_instance_id,
    const std::string& assignment_fingerprint,
    const std::chrono::milliseconds deadline) {
    const auto iterator = std::find_if(
        entries_.begin(),
        entries_.end(),
        [&](const auto& entry) {
            return entry->command.assignment.world_instance_id == world_instance_id;
        });
    if (iterator == entries_.end()) {
        if (std::find_if(
                retired_bindings_.begin(),
                retired_bindings_.end(),
                [&](const auto& retired) {
                    return retired.world_instance_id == world_instance_id &&
                           retired.assignment_fingerprint ==
                               assignment_fingerprint;
                }) != retired_bindings_.end()) {
            return;
        }
        throw std::runtime_error("simulation instance stop target is missing");
    }
    auto& entry = **iterator;
    if (entry.command.assignment.fingerprint != assignment_fingerprint) {
        throw std::runtime_error("simulation instance stop binding is stale");
    }
    if (!entry.drained && !entry.instance->Stop(deadline)) {
        throw std::runtime_error("simulation instance stop deadline exceeded");
    }
    retired_bindings_.push_back(RetiredBinding{
        .world_instance_id = world_instance_id,
        .assignment_fingerprint = assignment_fingerprint,
    });
    entries_.erase(iterator);
}

std::vector<ResultProposal> SimulationNode::PendingResults() const {
    return outbox_;
}

void SimulationNode::AckResult(
    const std::string& result_id,
    const std::string& proposal_fingerprint) {
    const auto iterator = std::find_if(
        outbox_.begin(),
        outbox_.end(),
        [&](const auto& proposal) {
            return proposal.result_id == result_id;
        });
    if (iterator == outbox_.end()) {
        const auto acknowledged = std::find_if(
            acked_results_.begin(),
            acked_results_.end(),
            [&](const auto& proposal) {
                return proposal.result_id == result_id;
            });
        if (acknowledged != acked_results_.end()) {
            if (acknowledged->proposal_fingerprint != proposal_fingerprint) {
                throw std::runtime_error("simulation result ack fingerprint conflicts");
            }
            return;
        }
        throw std::runtime_error("simulation result ack target is missing");
    }
    if (iterator->proposal_fingerprint != proposal_fingerprint) {
        throw std::runtime_error("simulation result ack fingerprint conflicts");
    }
    if (acked_results_.size() >= ResultOutboxLimit) {
        acked_results_.erase(acked_results_.begin());
    }
    acked_results_.push_back(*iterator);
    outbox_.erase(iterator);
}

void SimulationNode::BeginShutdown(const std::chrono::milliseconds deadline) {
    if (!healthy_ && entries_.empty()) {
        return;
    }
    healthy_ = false;
    while (!entries_.empty()) {
        const auto world_instance_id =
            entries_.back()->command.assignment.world_instance_id;
        const auto assignment_fingerprint =
            entries_.back()->command.assignment.fingerprint;
        try {
            if (!entries_.back()->drained) {
                static_cast<void>(
                    Drain(world_instance_id, assignment_fingerprint, deadline));
            }
        } catch (...) {
        }
        try {
            Stop(world_instance_id, assignment_fingerprint, deadline);
        } catch (...) {
            entries_.pop_back();
        }
    }
}

bool SimulationNode::Healthy() const noexcept {
    return healthy_;
}

std::size_t SimulationNode::RunningInstances() const noexcept {
    return entries_.size();
}

const SimulationNodeConfig& SimulationNode::Config() const noexcept {
    return config_;
}

}  // namespace ihomeland::sim
