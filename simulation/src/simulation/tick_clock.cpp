#include "ihomeland/sim/simulation/tick_clock.hpp"

#include <stdexcept>

namespace ihomeland::sim {

bool SteadyTickClock::WaitNext(
    const std::stop_token stop_token,
    const std::chrono::nanoseconds step) {
    if (step.count() <= 0) {
        throw std::invalid_argument("Tick step must be positive");
    }
    std::unique_lock lock(mutex_);
    std::stop_callback stop_callback(stop_token, [this] { condition_.notify_all(); });
    condition_.wait_for(lock, step, [&] { return stop_token.stop_requested(); });
    return !stop_token.stop_requested();
}

std::uint64_t SteadyTickClock::PendingCredits() const noexcept {
    return 0;
}

bool ManualTickClock::WaitNext(
    const std::stop_token stop_token,
    const std::chrono::nanoseconds step) {
    if (step.count() <= 0) {
        throw std::invalid_argument("Tick step must be positive");
    }
    std::unique_lock lock(mutex_);
    if (!condition_.wait(lock, stop_token, [&] { return credits_ != 0; })) {
        return false;
    }
    --credits_;
    return true;
}

std::uint64_t ManualTickClock::PendingCredits() const noexcept {
    std::lock_guard lock(mutex_);
    return credits_;
}

void ManualTickClock::Advance(const std::uint32_t count) {
    if (count == 0) {
        throw std::invalid_argument("manual Tick credit must be positive");
    }
    {
        std::lock_guard lock(mutex_);
        credits_ += count;
    }
    condition_.notify_all();
}

}  // namespace ihomeland::sim
