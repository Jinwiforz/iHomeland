#include "ihomeland/sim/core/adapter_smoke.hpp"
#include "ihomeland/sim/core/fixed_tick.hpp"

#include <iostream>
#include <string_view>

namespace {

/// RunSmoke 执行无 listener 的真实 dependency 与固定 Tick smoke。
[[nodiscard]] int RunSmoke() {
    const auto fixture = ihomeland::sim::RunFixtureSmoke(R"({"simulation_tick_ms":50})");
    ihomeland::sim::FixedTick fixed_tick{{
        .simulation_tick_ms = fixture.simulation_tick_ms,
        .input_tick_hz = 40,
    }};
    const auto state = fixed_tick.Advance();
    if (state.committed_tick != 1 || state.elapsed_ms != 50 || !ihomeland::sim::RunJoltSmoke() ||
        !ihomeland::sim::RunDetourSmoke()) {
        return 1;
    }
    std::cout << R"({"status":"ok","tick":1,"elapsed_ms":50})" << '\n';
    return 0;
}

}  // namespace

/// main 只提供离线 smoke 入口，不创建 socket、listener 或 production port。
int main(const int argument_count, const char* const arguments[]) {
    if (argument_count == 2 && std::string_view{arguments[1]} == "--smoke") {
        return RunSmoke();
    }
    std::cerr << "usage: ihomeland-sim-server --smoke\n";
    return 2;
}
