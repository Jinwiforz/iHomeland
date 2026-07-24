#include "ihomeland/sim/core/fixed_tick.hpp"

#include <limits>
#include <stdexcept>

namespace ihomeland::sim {

FixedTick::FixedTick(const FixedTickConfig config)
    : config_(config), state_{.committed_tick = 0, .elapsed_ms = 0} {
    if (config_.simulation_tick_ms == 0 || config_.input_tick_hz == 0 ||
        1000U % config_.input_tick_hz != 0U) {
        throw std::invalid_argument("fixed tick config is invalid");
    }
}

FixedTickState FixedTick::Advance() {
    if (state_.committed_tick == std::numeric_limits<std::uint64_t>::max() ||
        state_.elapsed_ms > std::numeric_limits<std::uint64_t>::max() - config_.simulation_tick_ms) {
        throw std::overflow_error("fixed tick state overflow");
    }
    ++state_.committed_tick;
    state_.elapsed_ms += config_.simulation_tick_ms;
    return state_;
}

FixedTickState FixedTick::State() const noexcept {
    return state_;
}

}  // namespace ihomeland::sim
