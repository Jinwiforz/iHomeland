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

/// LockedProofKey 只在已 VirtualLock 的 32-byte buffer 中保存握手 proof key。
class LockedProofKey final {
public:
    /// 构造函数先锁页再复制，并拒绝全零 secret。
    explicit LockedProofKey(const std::array<std::uint8_t, 32>& source) {
        if (std::all_of(source.begin(), source.end(), [](const auto value) {
                return value == 0;
            })) {
            throw std::invalid_argument("battle proof key is empty");
        }
        if (VirtualLock(material_.data(), material_.size()) == 0) {
            throw std::runtime_error("battle proof key memory lock failed");
        }
        locked_ = true;
        std::copy(source.begin(), source.end(), material_.begin());
    }

    /// 析构函数先清零再解锁物理页。
    ~LockedProofKey() {
        SecureZeroMemory(material_.data(), material_.size());
        if (locked_) {
            static_cast<void>(VirtualUnlock(material_.data(), material_.size()));
        }
    }

    LockedProofKey(const LockedProofKey&) = delete;
    LockedProofKey& operator=(const LockedProofKey&) = delete;

    /// Matches 使用固定 32-byte loop 比较重放输入，不提前退出。
    [[nodiscard]] bool Matches(
        const std::array<std::uint8_t, 32>& candidate) const noexcept {
        std::uint8_t difference = 0;
        for (std::size_t index = 0; index < material_.size(); ++index) {
            difference = static_cast<std::uint8_t>(
                difference | (material_[index] ^ candidate[index]));
        }
        return difference == 0;
    }

    /// View 只在 registry lock 内借出固定长度只读 secret view。
    [[nodiscard]] std::span<const std::uint8_t, 32> View() const noexcept {
        return material_;
    }

private:
    /// material_ 是唯一 proof key resident copy。
    std::array<std::uint8_t, 32> material_{};
    /// locked_ 防止构造失败路径执行无效 unlock。
    bool locked_{false};
};

/// SameTicketBinding 比较所有 Go authority facts，禁止局部 ID replay。
[[nodiscard]] bool SameTicketBinding(
    const BattleTicketBinding& first,
    const BattleTicketBinding& second) {
    return first.player_id == second.player_id &&
           first.session_id == second.session_id &&
           first.session_epoch == second.session_epoch &&
           first.role == second.role &&
           first.personal_world_id == second.personal_world_id &&
           first.visit_session_id == second.visit_session_id &&
           first.world_instance_id == second.world_instance_id &&
           first.runtime_node_id == second.runtime_node_id &&
           first.assignment_generation == second.assignment_generation &&
           first.fencing_token == second.fencing_token &&
           first.assignment_fingerprint == second.assignment_fingerprint &&
           first.simulation_node_id == second.simulation_node_id &&
           first.simulation_instance_id == second.simulation_instance_id &&
           first.mapping_generation == second.mapping_generation &&
           first.target_revision == second.target_revision &&
           first.model_identity == second.model_identity &&
           first.profile_identity == second.profile_identity &&
           first.config_identity == second.config_identity &&
           first.wire_identity == second.wire_identity &&
           first.actor_slot == second.actor_slot &&
           first.advertised_host == second.advertised_host &&
           first.advertised_port == second.advertised_port &&
           first.issue_id == second.issue_id &&
           first.issued_at_unix_ms == second.issued_at_unix_ms &&
           first.expires_at_unix_ms == second.expires_at_unix_ms;
}

/// ProofDigest 只保留 secret 的不可逆 replay comparison 摘要。
[[nodiscard]] std::string ProofDigest(
    const std::array<std::uint8_t, 32>& proof_key) {
    return Sha256Text(std::string(
        reinterpret_cast<const char*>(proof_key.data()),
        proof_key.size()));
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

/// TicketEntry 保存首次 install identity、locked proof 与不可逆 lifecycle。
struct SimulationNode::TicketEntry final {
    /// install_request_id 是首次 control request identity。
    std::string install_request_id;
    /// ticket_id 是 opaque UDP lookup identity。
    std::string ticket_id;
    /// binding_fingerprint 绑定完整 Go facts。
    std::string binding_fingerprint;
    /// binding 保存不含 secret 的 immutable facts。
    BattleTicketBinding binding;
    /// proof_key 只在 Installed/Consumed 生命周期存在于 locked memory。
    std::unique_ptr<LockedProofKey> proof_key;
    /// proof_digest 支持 secret 清零后的 exact install replay 比较。
    std::string proof_digest;
    /// state 只允许向 terminal 推进。
    BattleTicketState state{BattleTicketState::Installed};
    /// revoke_request_id 冻结首次 revoke replay identity。
    std::string revoke_request_id;
};

/// RevokeTombstone 阻止先于迟到 install 到达的 exact cancel 恢复资格。
struct SimulationNode::RevokeTombstone final {
    /// revoke_request_id 冻结首次 cancel identity。
    std::string revoke_request_id;
    /// ticket_id 绑定不可复用 lookup identity。
    std::string ticket_id;
    /// binding_fingerprint 以 canonical digest 绑定完整 Go facts。
    std::string binding_fingerprint;
    /// simulation_instance_id 防止撤销命中 successor worker。
    std::string simulation_instance_id;
    /// actor_slot 使 status/replay 保持 closed receipt。
    std::uint8_t actor_slot;
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
        {
            std::scoped_lock lock(ticket_mutex_);
            for (auto& ticket : tickets_) {
                if (ticket->binding.simulation_instance_id ==
                        entry.simulation_instance_id &&
                    (ticket->state == BattleTicketState::Installed ||
                     ticket->state == BattleTicketState::Consumed)) {
                    ticket->state = BattleTicketState::Revoked;
                    ticket->proof_key.reset();
                }
            }
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
    {
        std::scoped_lock lock(ticket_mutex_);
        for (auto& ticket : tickets_) {
            if (ticket->binding.simulation_instance_id ==
                    entry.simulation_instance_id &&
                (ticket->state == BattleTicketState::Installed ||
                 ticket->state == BattleTicketState::Consumed)) {
                ticket->state = BattleTicketState::Revoked;
                ticket->proof_key.reset();
            }
        }
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

BattleTicketReceipt SimulationNode::InstallBattleTicket(
    const BattleTicketInstallCommand& command,
    const std::uint64_t observed_unix_ms) {
    RequireIdentity(
        command.install_request_id,
        "sctl_",
        "ticket install request identity");
    RequireIdentity(command.ticket_id, "btk1_", "BattleTicketID");
    RequireDigest(command.binding_fingerprint, "ticket binding fingerprint");
    const auto& binding = command.binding;
    RequireIdentity(binding.player_id, "ply_", "PlayerID");
    RequireIdentity(binding.session_id, "ses_", "SessionID");
    RequireIdentity(
        binding.personal_world_id,
        "pworld_",
        "PersonalWorldID");
    RequireIdentity(
        binding.world_instance_id,
        "winst_",
        "WorldInstanceID");
    RequireIdentity(binding.runtime_node_id, "rnode_", "RuntimeNodeID");
    RequireDigest(binding.assignment_fingerprint, "assignment fingerprint");
    RequireIdentity(
        binding.simulation_node_id,
        "snode_",
        "SimulationNodeID");
    RequireIdentity(
        binding.simulation_instance_id,
        "sinst_",
        "SimulationInstanceID");
    RequireDigest(binding.model_identity, "model identity");
    RequireDigest(binding.profile_identity, "profile identity");
    RequireDigest(binding.config_identity, "config identity");
    RequireDigest(binding.wire_identity, "wire identity");
    RequireIdentity(binding.issue_id, "biss_", "ticket issue identity");
    if (binding.role == "owner") {
        if (!binding.visit_session_id.empty()) {
            throw std::invalid_argument("owner ticket contains VisitSessionID");
        }
    } else if (binding.role == "visitor") {
        RequireIdentity(
            binding.visit_session_id,
            "vses_",
            "VisitSessionID");
    } else {
        throw std::invalid_argument("battle ticket role is invalid");
    }
    if (observed_unix_ms == 0 || binding.session_epoch == 0 ||
        binding.assignment_generation == 0 || binding.fencing_token == 0 ||
        binding.mapping_generation == 0 || binding.target_revision == 0 ||
        binding.actor_slot >= config_.actor_capacity ||
        binding.advertised_host.empty() ||
        binding.advertised_host.size() > 253 ||
        binding.advertised_port == 0 || binding.issued_at_unix_ms == 0 ||
        binding.issued_at_unix_ms > observed_unix_ms ||
        binding.expires_at_unix_ms <= binding.issued_at_unix_ms ||
        binding.runtime_node_id != config_.runtime_node_id ||
        binding.simulation_node_id != config_.simulation_node_id ||
        binding.model_identity != config_.model_manifest ||
        binding.profile_identity != config_.profile_manifest) {
        throw std::invalid_argument("battle ticket binding is invalid");
    }
    std::scoped_lock lock(ticket_mutex_);
    const auto revoked_before_install = std::find_if(
        revoke_tombstones_.begin(),
        revoke_tombstones_.end(),
        [&](const auto& tombstone) {
            return tombstone->ticket_id == command.ticket_id;
        });
    if (revoked_before_install != revoke_tombstones_.end()) {
        const auto& tombstone = **revoked_before_install;
        if (tombstone.binding_fingerprint != command.binding_fingerprint ||
            tombstone.simulation_instance_id !=
                binding.simulation_instance_id ||
            tombstone.actor_slot != binding.actor_slot) {
            throw std::runtime_error(
                "battle ticket revoked tombstone conflicts");
        }
        return BattleTicketReceipt{
            .ticket_id = tombstone.ticket_id,
            .binding_fingerprint = tombstone.binding_fingerprint,
            .simulation_instance_id =
                tombstone.simulation_instance_id,
            .actor_slot = tombstone.actor_slot,
            .state = BattleTicketState::Revoked,
            .replayed = true,
        };
    }
    for (auto& existing : tickets_) {
        if ((existing->state == BattleTicketState::Installed ||
             existing->state == BattleTicketState::Consumed) &&
            observed_unix_ms >= existing->binding.expires_at_unix_ms) {
            existing->state = BattleTicketState::Expired;
            existing->proof_key.reset();
        }
        if (existing->install_request_id == command.install_request_id ||
            existing->ticket_id == command.ticket_id) {
            const auto proof_matches =
                existing->proof_key != nullptr
                    ? existing->proof_key->Matches(command.proof_key)
                    : existing->proof_digest ==
                          ProofDigest(command.proof_key);
            if (existing->install_request_id != command.install_request_id ||
                existing->ticket_id != command.ticket_id ||
                existing->binding_fingerprint !=
                    command.binding_fingerprint ||
                !SameTicketBinding(existing->binding, binding) ||
                !proof_matches) {
                throw std::runtime_error(
                    "battle ticket install replay conflicts");
            }
            return BattleTicketReceipt{
                .ticket_id = existing->ticket_id,
                .binding_fingerprint = existing->binding_fingerprint,
                .simulation_instance_id =
                    existing->binding.simulation_instance_id,
                .actor_slot = existing->binding.actor_slot,
                .state = existing->state,
                .replayed = true,
            };
        }
    }
    if (binding.expires_at_unix_ms <= observed_unix_ms) {
        throw std::invalid_argument("battle ticket is already expired");
    }
    const auto target = std::find_if(
        entries_.begin(),
        entries_.end(),
        [&](const auto& entry) {
            return entry->command.assignment.world_instance_id ==
                   binding.world_instance_id;
        });
    if (target == entries_.end() || (*target)->drained ||
        (*target)->simulation_instance_id !=
            binding.simulation_instance_id ||
        (*target)->command.assignment.fingerprint !=
            binding.assignment_fingerprint ||
        (*target)->command.assignment.generation !=
            binding.assignment_generation ||
        (*target)->command.assignment.fencing_token !=
            binding.fencing_token ||
        (*target)->command.mapping_generation !=
            binding.mapping_generation ||
        (*target)->command.config_identity != binding.config_identity) {
        throw std::runtime_error("battle ticket target is stale");
    }
    if (tickets_.size() + revoke_tombstones_.size() >=
        BattleTicketRegistryLimit) {
        throw std::length_error("battle ticket registry capacity exhausted");
    }
    const auto occupied = std::any_of(
        tickets_.begin(),
        tickets_.end(),
        [&](const auto& existing) {
            return (existing->state == BattleTicketState::Installed ||
                    existing->state == BattleTicketState::Consumed) &&
                   existing->binding.simulation_instance_id ==
                       binding.simulation_instance_id &&
                   (existing->binding.actor_slot == binding.actor_slot ||
                    existing->binding.player_id == binding.player_id);
        });
    if (occupied) {
        throw std::length_error(
            "battle installed and active actor capacity exhausted");
    }
    auto entry = std::make_unique<TicketEntry>();
    entry->install_request_id = command.install_request_id;
    entry->ticket_id = command.ticket_id;
    entry->binding_fingerprint = command.binding_fingerprint;
    entry->binding = binding;
    entry->proof_key = std::make_unique<LockedProofKey>(command.proof_key);
    entry->proof_digest = ProofDigest(command.proof_key);
    const auto receipt = BattleTicketReceipt{
        .ticket_id = entry->ticket_id,
        .binding_fingerprint = entry->binding_fingerprint,
        .simulation_instance_id = entry->binding.simulation_instance_id,
        .actor_slot = entry->binding.actor_slot,
        .state = entry->state,
        .replayed = false,
    };
    tickets_.push_back(std::move(entry));
    return receipt;
}

BattleTicketReceipt SimulationNode::BattleTicketStatus(
    const std::string& ticket_id,
    const std::string& binding_fingerprint,
    const std::uint64_t observed_unix_ms) {
    RequireIdentity(ticket_id, "btk1_", "BattleTicketID");
    RequireDigest(binding_fingerprint, "ticket binding fingerprint");
    if (observed_unix_ms == 0) {
        throw std::invalid_argument("ticket status time is invalid");
    }
    std::scoped_lock lock(ticket_mutex_);
    const auto tombstone = std::find_if(
        revoke_tombstones_.begin(),
        revoke_tombstones_.end(),
        [&](const auto& entry) { return entry->ticket_id == ticket_id; });
    if (tombstone != revoke_tombstones_.end()) {
        if ((*tombstone)->binding_fingerprint != binding_fingerprint) {
            throw std::runtime_error(
                "battle ticket status target is missing");
        }
        return BattleTicketReceipt{
            .ticket_id = (*tombstone)->ticket_id,
            .binding_fingerprint =
                (*tombstone)->binding_fingerprint,
            .simulation_instance_id =
                (*tombstone)->simulation_instance_id,
            .actor_slot = (*tombstone)->actor_slot,
            .state = BattleTicketState::Revoked,
            .replayed = true,
        };
    }
    const auto iterator = std::find_if(
        tickets_.begin(),
        tickets_.end(),
        [&](const auto& entry) { return entry->ticket_id == ticket_id; });
    if (iterator == tickets_.end() ||
        (*iterator)->binding_fingerprint != binding_fingerprint) {
        throw std::runtime_error("battle ticket status target is missing");
    }
    auto& entry = **iterator;
    if ((entry.state == BattleTicketState::Installed ||
         entry.state == BattleTicketState::Consumed) &&
        observed_unix_ms >= entry.binding.expires_at_unix_ms) {
        entry.state = BattleTicketState::Expired;
        entry.proof_key.reset();
    }
    return BattleTicketReceipt{
        .ticket_id = entry.ticket_id,
        .binding_fingerprint = entry.binding_fingerprint,
        .simulation_instance_id = entry.binding.simulation_instance_id,
        .actor_slot = entry.binding.actor_slot,
        .state = entry.state,
        .replayed = true,
    };
}

BattleTicketReceipt SimulationNode::ConsumeBattleTicket(
    const std::string& ticket_id,
    const std::string& binding_fingerprint,
    const std::uint64_t observed_unix_ms) {
    auto receipt =
        BattleTicketStatus(ticket_id, binding_fingerprint, observed_unix_ms);
    std::scoped_lock lock(ticket_mutex_);
    auto& entry = **std::find_if(
        tickets_.begin(),
        tickets_.end(),
        [&](const auto& candidate) {
            return candidate->ticket_id == ticket_id;
        });
    if (entry.state == BattleTicketState::Consumed) {
        receipt.replayed = true;
        return receipt;
    }
    if (entry.state != BattleTicketState::Installed ||
        entry.proof_key == nullptr) {
        throw std::runtime_error("battle ticket cannot be consumed");
    }
    entry.state = BattleTicketState::Consumed;
    entry.proof_key.reset();
    receipt.state = entry.state;
    receipt.replayed = false;
    return receipt;
}

void SimulationNode::AuthenticateAndConsumeBattleTicket(
    const std::string& ticket_id,
    const std::string& expected_simulation_node_id,
    const std::string& expected_advertised_host,
    const std::uint16_t expected_advertised_port,
    const std::uint64_t observed_unix_ms,
    const std::function<bool(
        std::span<const std::uint8_t, 32>,
        const BattleTicketBinding&,
        const std::string&)>& authenticator) {
    RequireIdentity(ticket_id, "btk1_", "BattleTicketID");
    RequireIdentity(
        expected_simulation_node_id,
        "snode_",
        "expected SimulationNodeID");
    if (expected_advertised_host.empty() ||
        expected_advertised_port == 0 ||
        observed_unix_ms == 0 ||
        !authenticator) {
        throw std::invalid_argument(
            "battle ticket authentication input is invalid");
    }
    std::scoped_lock lock(ticket_mutex_);
    const auto iterator = std::find_if(
        tickets_.begin(),
        tickets_.end(),
        [&](const auto& entry) {
            return entry->ticket_id == ticket_id;
        });
    if (iterator == tickets_.end()) {
        throw std::runtime_error(
            "battle ticket authentication failed");
    }
    auto& entry = **iterator;
    if ((entry.state == BattleTicketState::Installed ||
         entry.state == BattleTicketState::Consumed) &&
        observed_unix_ms >= entry.binding.expires_at_unix_ms) {
        entry.state = BattleTicketState::Expired;
        entry.proof_key.reset();
    }
    if (entry.state != BattleTicketState::Installed ||
        entry.proof_key == nullptr ||
        expected_simulation_node_id != config_.simulation_node_id ||
        entry.binding.simulation_node_id !=
            expected_simulation_node_id ||
        entry.binding.runtime_node_id != config_.runtime_node_id ||
        entry.binding.advertised_host !=
            expected_advertised_host ||
        entry.binding.advertised_port !=
            expected_advertised_port) {
        throw std::runtime_error(
            "battle ticket authentication failed");
    }
    if (!authenticator(
            entry.proof_key->View(),
            entry.binding,
            entry.binding_fingerprint)) {
        throw std::runtime_error(
            "battle ticket authentication failed");
    }
    entry.state = BattleTicketState::Consumed;
    entry.proof_key.reset();
}

BattleTicketReceipt SimulationNode::RevokeBattleTicket(
    const std::string& revoke_request_id,
    const std::string& ticket_id,
    const std::string& binding_fingerprint,
    const std::string& simulation_instance_id,
    const std::uint8_t actor_slot) {
    RequireIdentity(
        revoke_request_id,
        "sctl_",
        "ticket revoke request identity");
    RequireIdentity(ticket_id, "btk1_", "BattleTicketID");
    RequireDigest(binding_fingerprint, "ticket binding fingerprint");
    RequireIdentity(
        simulation_instance_id,
        "sinst_",
        "SimulationInstanceID");
    if (actor_slot >= config_.actor_capacity) {
        throw std::invalid_argument("battle ticket revoke slot is invalid");
    }
    std::scoped_lock lock(ticket_mutex_);
    const auto tombstone = std::find_if(
        revoke_tombstones_.begin(),
        revoke_tombstones_.end(),
        [&](const auto& entry) {
            return entry->ticket_id == ticket_id ||
                   entry->revoke_request_id == revoke_request_id;
        });
    if (tombstone != revoke_tombstones_.end()) {
        if ((*tombstone)->revoke_request_id != revoke_request_id ||
            (*tombstone)->ticket_id != ticket_id ||
            (*tombstone)->binding_fingerprint != binding_fingerprint ||
            (*tombstone)->simulation_instance_id !=
                simulation_instance_id ||
            (*tombstone)->actor_slot != actor_slot) {
            throw std::runtime_error(
                "battle ticket revoke replay conflicts");
        }
        return BattleTicketReceipt{
            .ticket_id = ticket_id,
            .binding_fingerprint = binding_fingerprint,
            .simulation_instance_id = simulation_instance_id,
            .actor_slot = actor_slot,
            .state = BattleTicketState::Revoked,
            .replayed = true,
        };
    }
    const auto iterator = std::find_if(
        tickets_.begin(),
        tickets_.end(),
        [&](const auto& entry) { return entry->ticket_id == ticket_id; });
    if (iterator == tickets_.end()) {
        if (tickets_.size() + revoke_tombstones_.size() >=
            BattleTicketRegistryLimit) {
            throw std::length_error(
                "battle ticket registry capacity exhausted");
        }
        auto created = std::make_unique<RevokeTombstone>();
        created->revoke_request_id = revoke_request_id;
        created->ticket_id = ticket_id;
        created->binding_fingerprint = binding_fingerprint;
        created->simulation_instance_id = simulation_instance_id;
        created->actor_slot = actor_slot;
        revoke_tombstones_.push_back(std::move(created));
        return BattleTicketReceipt{
            .ticket_id = ticket_id,
            .binding_fingerprint = binding_fingerprint,
            .simulation_instance_id = simulation_instance_id,
            .actor_slot = actor_slot,
            .state = BattleTicketState::Revoked,
            .replayed = false,
        };
    }
    auto& entry = **iterator;
    if (entry.binding_fingerprint != binding_fingerprint ||
        entry.binding.simulation_instance_id !=
            simulation_instance_id ||
        entry.binding.actor_slot != actor_slot) {
        throw std::runtime_error("battle ticket revoke target is stale");
    }
    if (!entry.revoke_request_id.empty() &&
        entry.revoke_request_id != revoke_request_id) {
        throw std::runtime_error("battle ticket revoke replay conflicts");
    }
    const auto replayed = !entry.revoke_request_id.empty();
    entry.revoke_request_id = revoke_request_id;
    if (entry.state == BattleTicketState::Installed ||
        entry.state == BattleTicketState::Consumed) {
        entry.state = BattleTicketState::Revoked;
        entry.proof_key.reset();
    }
    return BattleTicketReceipt{
        .ticket_id = entry.ticket_id,
        .binding_fingerprint = entry.binding_fingerprint,
        .simulation_instance_id = entry.binding.simulation_instance_id,
        .actor_slot = entry.binding.actor_slot,
        .state = entry.state,
        .replayed = replayed,
    };
}

std::size_t SimulationNode::InstalledOrActiveActors(
    const std::string& simulation_instance_id,
    const std::uint64_t observed_unix_ms) {
    RequireIdentity(
        simulation_instance_id,
        "sinst_",
        "SimulationInstanceID");
    if (observed_unix_ms == 0) {
        throw std::invalid_argument("actor capacity observation is invalid");
    }
    std::scoped_lock lock(ticket_mutex_);
    std::size_t actors = 0;
    for (auto& entry : tickets_) {
        if ((entry->state == BattleTicketState::Installed ||
             entry->state == BattleTicketState::Consumed) &&
            observed_unix_ms >= entry->binding.expires_at_unix_ms) {
            entry->state = BattleTicketState::Expired;
            entry->proof_key.reset();
        }
        if ((entry->state == BattleTicketState::Installed ||
             entry->state == BattleTicketState::Consumed) &&
            entry->binding.simulation_instance_id ==
                simulation_instance_id) {
            ++actors;
        }
    }
    return actors;
}

void SimulationNode::StartBattleUdpListener(
    BattleUdpListenerConfig config,
    BattleUdpListener::DatagramHandler handler) {
    if (!healthy_) {
        throw std::runtime_error("simulation node is draining");
    }
    if (battle_udp_listener_created_) {
        throw std::logic_error(
            "simulation node UDP listener already created");
    }
    battle_udp_listener_created_ = true;
    try {
        auto listener = std::make_unique<BattleUdpListener>(
            std::move(config),
            std::move(handler));
        listener->Start();
        battle_udp_listener_ = std::move(listener);
    } catch (...) {
        healthy_ = false;
        throw;
    }
}

void SimulationNode::StopBattleUdpListener() noexcept {
    if (battle_udp_listener_) {
        battle_udp_listener_->Stop();
    }
}

std::optional<BattleUdpListenerStatus>
SimulationNode::UdpListenerStatus() const {
    if (!battle_udp_listener_) {
        return std::nullopt;
    }
    return battle_udp_listener_->Status();
}

void SimulationNode::BeginShutdown(const std::chrono::milliseconds deadline) {
    if (!healthy_ && entries_.empty()) {
        StopBattleUdpListener();
        return;
    }
    healthy_ = false;
    {
        std::scoped_lock lock(ticket_mutex_);
        for (auto& ticket : tickets_) {
            if (ticket->state == BattleTicketState::Installed ||
                ticket->state == BattleTicketState::Consumed) {
                ticket->state = BattleTicketState::Revoked;
                ticket->proof_key.reset();
            }
        }
    }
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
    StopBattleUdpListener();
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
