#include "ihomeland/sim/gameplay/pipeline.hpp"

#include <algorithm>
#include <cstdint>
#include <iostream>
#include <stdexcept>
#include <string_view>
#include <vector>

namespace {

/// Require 把 pipeline 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// Registrations 构造与权威顺序一致的完整回调表。
[[nodiscard]] std::vector<ihomeland::sim::GameplaySystemRegistration> Registrations(
    std::vector<ihomeland::sim::GameplayStage>* observed = nullptr) {
    std::vector<ihomeland::sim::GameplaySystemRegistration> registrations;
    for (const auto stage : ihomeland::sim::AuthoritativeGameplayStages()) {
        registrations.push_back({
            .stage = stage,
            .system = [observed, stage](ihomeland::sim::GameplayTickContext&) {
                if (observed != nullptr) {
                    observed->push_back(stage);
                }
            }});
    }
    return registrations;
}

/// ExpectConstructionError 验证 pipeline 构造以预期稳定错误码失败。
void ExpectConstructionError(
    const std::vector<ihomeland::sim::GameplaySystemRegistration>& registrations,
    const ihomeland::sim::GameplayPipelineErrorCode expected) {
    try {
        static_cast<void>(ihomeland::sim::GameplayPipeline(registrations));
    } catch (const ihomeland::sim::GameplayPipelineError& error) {
        Require(error.Code() == expected, "pipeline returned the wrong rejection code");
        return;
    }
    throw std::runtime_error("invalid pipeline registration was accepted");
}

/// TestAuthoritativeRegistry 验证稳定名称、完整执行顺序与 Tick guard。
void TestAuthoritativeRegistry() {
    std::vector<ihomeland::sim::GameplayStage> observed;
    const auto registrations = Registrations(&observed);
    const ihomeland::sim::GameplayPipeline pipeline(registrations);
    const ihomeland::sim::SimulationWorldConfig world_config{
        .entity_capacity = 2,
        .transform_capacity = 2,
        .motion_capacity = 2,
        .actor_capacity = 2,
        .attribute_capacity = 2,
        .ability_capacity = 2,
        .effect_capacity = 2,
        .projectile_capacity = 2,
        .ai_capacity = 2};
    ihomeland::sim::SimulationWorld world(1, world_config);
    ihomeland::sim::StructuralCommandBuffer structural(2);
    ihomeland::sim::GameplayTickContext context{
        .tick = 1,
        .world = world,
        .structural = structural};
    pipeline.Run(context);
    Require(
        observed == std::vector<ihomeland::sim::GameplayStage>(
                        ihomeland::sim::AuthoritativeGameplayStages().begin(),
                        ihomeland::sim::AuthoritativeGameplayStages().end()),
        "pipeline execution order drifted");
    Require(
        ihomeland::sim::GameplayStageName(ihomeland::sim::GameplayStage::DrainInput) ==
            std::string_view("DrainInput"),
        "pipeline stable stage name drifted");
    try {
        context.tick = 0;
        pipeline.Run(context);
    } catch (const std::invalid_argument&) {
        return;
    }
    throw std::runtime_error("zero Tick pipeline run was accepted");
}

/// TestRegistrationGate 验证缺失、重复、重排与空 system 均 fail closed。
void TestRegistrationGate() {
    auto missing = Registrations();
    missing.pop_back();
    ExpectConstructionError(missing, ihomeland::sim::GameplayPipelineErrorCode::MissingStage);

    auto duplicate = Registrations();
    duplicate.back().stage = duplicate.front().stage;
    ExpectConstructionError(duplicate, ihomeland::sim::GameplayPipelineErrorCode::DuplicateStage);

    auto reordered = Registrations();
    std::swap(reordered[0], reordered[1]);
    ExpectConstructionError(reordered, ihomeland::sim::GameplayPipelineErrorCode::ReorderedStage);

    auto empty = Registrations();
    empty[4].system = {};
    ExpectConstructionError(empty, ihomeland::sim::GameplayPipelineErrorCode::InvalidCallback);
}

}  // namespace

/// main 执行固定 pipeline registry 的正向与负向回归。
int main() {
    try {
        TestAuthoritativeRegistry();
        TestRegistrationGate();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
