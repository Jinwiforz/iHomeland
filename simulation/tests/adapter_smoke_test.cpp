#include "ihomeland/sim/core/adapter_smoke.hpp"

#include <stdexcept>

/// main 验证三个 adapter 实际消费锁定依赖，且以项目 value 返回结果。
int main() {
    const auto fixture = ihomeland::sim::RunFixtureSmoke(R"({"simulation_tick_ms":50})");
    if (fixture.simulation_tick_ms != 50 || !ihomeland::sim::RunJoltSmoke() ||
        !ihomeland::sim::RunDetourSmoke()) {
        return 1;
    }
    try {
        static_cast<void>(ihomeland::sim::RunFixtureSmoke(R"({"simulation_tick_ms":"50"})"));
    } catch (const std::invalid_argument&) {
        return 0;
    }
    return 2;
}
