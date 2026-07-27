#include "ihomeland/sim/simulation/simulation_instance.hpp"

#include "ihomeland/sim/observability/battle_runtime_metrics.hpp"

#include <algorithm>
#include <chrono>
#include <stdexcept>
#include <tuple>
#include <utility>

namespace ihomeland::sim {

InstanceError::InstanceError(const InstanceErrorCode code, const char* message)
    : std::runtime_error(message), code_(code) {}

InstanceErrorCode InstanceError::Code() const noexcept {
    return code_;
}

SimulationInstance::SimulationInstance(
    SimulationInstanceIdentity identity,
    SimulationInstanceConfig config,
    std::shared_ptr<TickClock> clock,
    TickObserver observer,
    BattleRuntimeMetrics* runtime_metrics)
    : identity_(std::move(identity)),
      config_(config),
      clock_(std::move(clock)),
      observer_(std::move(observer)),
      runtime_metrics_(runtime_metrics),
      inbox_(config.inbox_capacity) {
    if (config.tick_step != std::chrono::milliseconds(50) ||
        config.hard_tick_debt == 0 || !clock_ || !observer_) {
        throw std::invalid_argument("SimulationInstance configuration is invalid");
    }
    pending_commands_.reserve(config.inbox_capacity);
}

SimulationInstance::~SimulationInstance() {
    static_cast<void>(Stop(std::chrono::milliseconds(0)));
}

void SimulationInstance::Start(const std::span<const StartupStep> steps) {
    auto expected = SimulationInstanceState::Created;
    if (!state_.compare_exchange_strong(expected, SimulationInstanceState::Starting)) {
        throw InstanceError(InstanceErrorCode::InvalidState, "instance can only start from Created");
    }
    try {
        std::lock_guard lock(lifecycle_mutex_);
        completed_steps_.reserve(steps.size());
        for (const auto& step : steps) {
            if (!step.initialize || !step.rollback) {
                throw InstanceError(InstanceErrorCode::Startup, "startup step callbacks are incomplete");
            }
            step.initialize();
            completed_steps_.push_back(step);
        }
        state_.store(SimulationInstanceState::Running);
        worker_ = std::jthread([this](const std::stop_token token) { WorkerMain(token); });
    } catch (...) {
        state_.store(SimulationInstanceState::Failed);
        RollbackResources();
        throw InstanceError(InstanceErrorCode::Startup, "instance startup failed and rolled back");
    }
}

InboxPushResult SimulationInstance::SubmitValidated(IngressCommand command) {
    if (state_.load() != SimulationInstanceState::Running) {
        return InboxPushResult::Closed;
    }
    std::lock_guard lock(inbox_mutex_);
    if (state_.load() != SimulationInstanceState::Running) {
        return InboxPushResult::Closed;
    }
    if (queued_total_ >= config_.inbox_capacity) {
        return InboxPushResult::Capacity;
    }
    const auto result = inbox_.TryPush(std::move(command));
    if (result == InboxPushResult::Accepted) {
        ++queued_total_;
    }
    return result;
}

void SimulationInstance::BeginDrain() {
    auto expected = SimulationInstanceState::Running;
    if (!state_.compare_exchange_strong(expected, SimulationInstanceState::Draining)) {
        if (expected == SimulationInstanceState::Draining ||
            expected == SimulationInstanceState::Stopped) {
            return;
        }
        throw InstanceError(InstanceErrorCode::InvalidState, "drain requires Running instance");
    }
    std::lock_guard lock(inbox_mutex_);
    inbox_.Close();
}

bool SimulationInstance::Stop(const std::chrono::milliseconds deadline) {
    auto current = state_.load();
    if (current == SimulationInstanceState::Created) {
        state_.store(SimulationInstanceState::Stopped);
        RollbackResources();
        return true;
    }
    if (current == SimulationInstanceState::Running) {
        BeginDrain();
        current = SimulationInstanceState::Draining;
    }
    bool normal = current == SimulationInstanceState::Stopped;
    if (current == SimulationInstanceState::Draining) {
        std::unique_lock lock(lifecycle_mutex_);
        normal = lifecycle_condition_.wait_for(lock, deadline, [&] {
            const auto snapshot = state_.load();
            return snapshot == SimulationInstanceState::Stopped ||
                   snapshot == SimulationInstanceState::Failed;
        });
        normal = normal && state_.load() == SimulationInstanceState::Stopped;
    }
    if (!normal && state_.load() != SimulationInstanceState::Failed) {
        state_.store(SimulationInstanceState::Failed);
    }
    if (worker_.joinable()) {
        worker_.request_stop();
        worker_.join();
    }
    RollbackResources();
    return normal;
}

SimulationInstanceState SimulationInstance::State() const noexcept {
    return state_.load();
}

std::uint64_t SimulationInstance::CommittedTick() const noexcept {
    return committed_tick_.load();
}

const SimulationInstanceIdentity& SimulationInstance::Identity() const noexcept {
    return identity_;
}

std::size_t SimulationInstance::InboxHighWatermark() const {
    std::lock_guard lock(inbox_mutex_);
    return inbox_.HighWatermark();
}

std::size_t SimulationInstance::ReservedBytes() const noexcept {
    return sizeof(*this) +
           inbox_.ReservedBytes() +
           pending_commands_.capacity() * sizeof(IngressCommand) +
           completed_steps_.capacity() * sizeof(StartupStep);
}

void SimulationInstance::WorkerMain(const std::stop_token stop_token) noexcept {
    try {
        while (clock_->WaitNext(stop_token, config_.tick_step)) {
            const auto pending_credits = clock_->PendingCredits();
            if (pending_credits > config_.hard_tick_debt) {
                state_.store(SimulationInstanceState::Failed);
                lifecycle_condition_.notify_all();
                return;
            }
            const auto state = state_.load();
            if (state != SimulationInstanceState::Running &&
                state != SimulationInstanceState::Draining) {
                break;
            }
            {
                std::lock_guard lock(inbox_mutex_);
                inbox_.DrainInto(pending_commands_);
            }
            std::stable_sort(pending_commands_.begin(), pending_commands_.end(), [](const auto& left, const auto& right) {
                return std::tie(
                           left.target_tick,
                           left.actor_id,
                           left.input_tick,
                           left.stable_sequence,
                           left.kind) <
                       std::tie(
                           right.target_tick,
                           right.actor_id,
                           right.input_tick,
                           right.stable_sequence,
                           right.kind);
            });
            const auto tick = committed_tick_.load() + 1;
            const auto ready_end = std::upper_bound(
                pending_commands_.begin(),
                pending_commands_.end(),
                tick,
                [](const std::uint64_t ready_tick, const IngressCommand& command) {
                    return ready_tick < command.target_tick;
                });
            std::vector<IngressCommand> batch;
            batch.reserve(static_cast<std::size_t>(
                std::distance(pending_commands_.begin(), ready_end)));
            for (auto iterator = pending_commands_.begin(); iterator != ready_end; ++iterator) {
                batch.push_back(std::move(*iterator));
            }
            pending_commands_.erase(pending_commands_.begin(), ready_end);
            {
                std::lock_guard lock(inbox_mutex_);
                queued_total_ -= batch.size();
            }
            const auto tick_started = std::chrono::steady_clock::now();
            observer_(TickObservation{.tick = tick, .commands = batch});
            const auto tick_duration =
                std::chrono::duration_cast<std::chrono::nanoseconds>(
                    std::chrono::steady_clock::now() - tick_started);
            if (runtime_metrics_ != nullptr) {
                runtime_metrics_->ObserveTick(
                    static_cast<std::uint64_t>(tick_duration.count()),
                    static_cast<std::uint64_t>(pending_credits));
            }
            committed_tick_.store(tick);
            if (state_.load() == SimulationInstanceState::Draining) {
                std::lock_guard lock(inbox_mutex_);
                if (inbox_.Empty() && pending_commands_.empty()) {
                    state_.store(SimulationInstanceState::Stopped);
                    lifecycle_condition_.notify_all();
                    return;
                }
            }
        }
        if (state_.load() == SimulationInstanceState::Draining) {
            state_.store(SimulationInstanceState::Stopped);
        }
    } catch (...) {
        state_.store(SimulationInstanceState::Failed);
    }
    lifecycle_condition_.notify_all();
}

void SimulationInstance::RollbackResources() noexcept {
    std::vector<StartupStep> steps;
    {
        std::lock_guard lock(lifecycle_mutex_);
        steps.swap(completed_steps_);
    }
    for (auto iterator = steps.rbegin(); iterator != steps.rend(); ++iterator) {
        try {
            iterator->rollback();
        } catch (...) {
            state_.store(SimulationInstanceState::Failed);
        }
    }
}

}  // namespace ihomeland::sim
