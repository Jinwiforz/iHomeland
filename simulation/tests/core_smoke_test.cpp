#include "ihomeland/sim/core/fixed_tick.hpp"

#include <stdexcept>

/// main 验证固定 Tick 只按显式步长推进，不读取墙钟。
int main() {
    ihomeland::sim::FixedTick fixed_tick{{
        .simulation_tick_ms = 50,
        .input_tick_hz = 40,
    }};
    const auto first = fixed_tick.Advance();
    const auto second = fixed_tick.Advance();
    if (first.committed_tick != 1 || first.elapsed_ms != 50 || second.committed_tick != 2 ||
        second.elapsed_ms != 100) {
        return 1;
    }
    try {
        const ihomeland::sim::FixedTick invalid{{.simulation_tick_ms = 0, .input_tick_hz = 40}};
        static_cast<void>(invalid);
    } catch (const std::invalid_argument&) {
        return 0;
    }
    return 2;
}
