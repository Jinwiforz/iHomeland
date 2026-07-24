#include "ihomeland/sim/gameplay/pipeline.hpp"

#include <algorithm>
#include <array>
#include <cstddef>

namespace ihomeland::sim {
namespace {

/// kAuthoritativeStages 是禁止运行时重排的唯一 pipeline source。
constexpr std::array<GameplayStage, kGameplayStageCount> kAuthoritativeStages{
    GameplayStage::DrainInput,
    GameplayStage::InputIntent,
    GameplayStage::AIIntent,
    GameplayStage::AbilityActivation,
    GameplayStage::Movement,
    GameplayStage::Physics,
    GameplayStage::HitDetection,
    GameplayStage::Effect,
    GameplayStage::Attribute,
    GameplayStage::Death,
    GameplayStage::Replication,
    GameplayStage::CommitDeferredStructuralChanges,
};

}  // namespace

GameplayPipelineError::GameplayPipelineError(
    const GameplayPipelineErrorCode code,
    const char* message)
    : std::runtime_error(message), code_(code) {}

GameplayPipelineErrorCode GameplayPipelineError::Code() const noexcept {
    return code_;
}

std::span<const GameplayStage, kGameplayStageCount>
AuthoritativeGameplayStages() noexcept {
    return kAuthoritativeStages;
}

std::string_view GameplayStageName(const GameplayStage stage) {
    constexpr std::array<std::string_view, kGameplayStageCount> names{
        "DrainInput",
        "InputIntent",
        "AIIntent",
        "AbilityActivation",
        "Movement",
        "Physics",
        "HitDetection",
        "Effect",
        "Attribute",
        "Death",
        "Replication",
        "CommitDeferredStructuralChanges",
    };
    const auto index = static_cast<std::size_t>(stage);
    if (index >= names.size()) {
        throw GameplayPipelineError(
            GameplayPipelineErrorCode::MissingStage,
            "gameplay stage is outside the authoritative registry");
    }
    return names[index];
}

GameplayPipeline::GameplayPipeline(
    const std::span<const GameplaySystemRegistration> registrations) {
    if (registrations.size() < kGameplayStageCount) {
        throw GameplayPipelineError(
            GameplayPipelineErrorCode::MissingStage,
            "gameplay pipeline is missing an authoritative stage");
    }

    std::array<bool, kGameplayStageCount> seen{};
    for (const auto& registration : registrations) {
        const auto index = static_cast<std::size_t>(registration.stage);
        if (index >= kGameplayStageCount) {
            throw GameplayPipelineError(
                GameplayPipelineErrorCode::MissingStage,
                "gameplay pipeline contains an unknown stage");
        }
        if (seen[index]) {
            throw GameplayPipelineError(
                GameplayPipelineErrorCode::DuplicateStage,
                "gameplay pipeline contains a duplicate stage");
        }
        seen[index] = true;
    }

    if (registrations.size() > kGameplayStageCount) {
        throw GameplayPipelineError(
            GameplayPipelineErrorCode::DuplicateStage,
            "gameplay pipeline contains more than one registration per stage");
    }
    for (std::size_t index = 0; index < kGameplayStageCount; ++index) {
        if (registrations[index].stage != kAuthoritativeStages[index]) {
            throw GameplayPipelineError(
                GameplayPipelineErrorCode::ReorderedStage,
                "gameplay pipeline order differs from the authoritative registry");
        }
        if (!registrations[index].system) {
            throw GameplayPipelineError(
                GameplayPipelineErrorCode::InvalidCallback,
                "gameplay pipeline stage callback is empty");
        }
        systems_[index] = registrations[index].system;
    }
}

void GameplayPipeline::Run(GameplayTickContext& context) const {
    if (context.tick == 0) {
        throw std::invalid_argument("gameplay pipeline Tick must be non-zero");
    }
    for (const auto& system : systems_) {
        system(context);
    }
}

}  // namespace ihomeland::sim
