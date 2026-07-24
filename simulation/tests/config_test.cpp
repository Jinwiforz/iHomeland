#include "ihomeland/sim/config/battle_runtime_config.hpp"
#include "ihomeland/sim/core/sha256.hpp"

#include <filesystem>
#include <iostream>
#include <stdexcept>

namespace {

/// Require 把失败转换为单一 test exception，避免依赖第三方测试框架。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

}  // namespace

/// main 验证 SHA-256 标准 vectors 与冻结 battle profile 的强类型映射。
int main() {
    try {
        Require(
            ihomeland::sim::Sha256Text("") ==
                "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
            "empty SHA-256 vector drifted");
        Require(
            ihomeland::sim::Sha256Text("abc") ==
                "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
            "abc SHA-256 vector drifted");
        Require(
            ihomeland::sim::Sha256Text("abc\n") != ihomeland::sim::Sha256Text("abc"),
            "SHA-256 must preserve line endings");

        const auto config = ihomeland::sim::LoadBattleRuntimeConfig(
            std::filesystem::path(IHOMELAND_PROFILE_ROOT),
            std::filesystem::path(IHOMELAND_MODEL_ROOT));
        Require(config.simulation_tick_ns == 50'000'000, "simulation tick drifted");
        Require(config.simulation_tick_ms == 50, "simulation tick milliseconds drifted");
        Require(config.input_tick_ns == 25'000'000, "input tick drifted");
        Require(config.input_tick_hz == 40, "input tick frequency drifted");
        Require(config.history_window_ticks == 16, "history window drifted");
        Require(config.qualified_actor_cap == 8, "qualified actor cap drifted");
        Require(config.queue_capacity_items == 256, "queue capacity drifted");
        Require(config.cpu_per_tick_target_us == 2'500, "CPU target drifted");
        Require(config.memory_per_instance_target_bytes == 64ULL * 1024ULL * 1024ULL, "memory target drifted");
        Require(config.history_memory_target_bytes == 8ULL * 1024ULL * 1024ULL, "history target drifted");
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
