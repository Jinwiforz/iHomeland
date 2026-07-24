#include "ihomeland/sim/gameplay/movement.hpp"

#include <algorithm>
#include <cstdint>
#include <limits>

namespace ihomeland::sim {
namespace {

/// kNanosecondsPerSecond 是 velocity/acceleration 积分的固定单位换算。
constexpr std::int64_t kNanosecondsPerSecond = 1'000'000'000;

/// CheckedAdd 拒绝 signed 64-bit 加法溢出。
[[nodiscard]] std::int64_t CheckedAdd(const std::int64_t left, const std::int64_t right) {
    if ((right > 0 && left > std::numeric_limits<std::int64_t>::max() - right) ||
        (right < 0 && left < std::numeric_limits<std::int64_t>::min() - right)) {
        throw MovementError(MovementErrorCode::ArithmeticOverflow, "movement addition overflow");
    }
    return left + right;
}

/// CheckedMultiplyDivide 计算 toward-zero 的 value*multiplier/divisor。
[[nodiscard]] std::int64_t CheckedMultiplyDivide(
    const std::int64_t value,
    const std::int64_t multiplier,
    const std::int64_t divisor) {
    if (divisor <= 0 || value < 0 || multiplier < 0) {
        throw MovementError(
            MovementErrorCode::InvalidConfig,
            "movement checked multiplication requires non-negative values");
    }
    if (value != 0 && multiplier > std::numeric_limits<std::int64_t>::max() / value) {
        throw MovementError(
            MovementErrorCode::ArithmeticOverflow,
            "movement multiplication overflow");
    }
    return value * multiplier / divisor;
}

/// ScaleSigned 以 magnitude 计算 signed value*multiplier/divisor。
[[nodiscard]] std::int64_t ScaleSigned(
    const std::int64_t value,
    const std::int64_t multiplier,
    const std::int64_t divisor) {
    if (value == std::numeric_limits<std::int64_t>::min()) {
        throw MovementError(
            MovementErrorCode::ArithmeticOverflow,
            "movement signed magnitude overflow");
    }
    const auto magnitude = value < 0 ? -value : value;
    const auto scaled = CheckedMultiplyDivide(magnitude, multiplier, divisor);
    return value < 0 ? -scaled : scaled;
}

/// IntegerSquareRoot 返回 floor(sqrt(value))，不依赖 floating point 或 locale。
[[nodiscard]] std::uint64_t IntegerSquareRoot(const std::uint64_t value) noexcept {
    std::uint64_t result = 0;
    std::uint64_t bit = std::uint64_t{1} << 62;
    while (bit > value) {
        bit >>= 2;
    }
    auto remainder = value;
    while (bit != 0) {
        if (remainder >= result + bit) {
            remainder -= result + bit;
            result = (result >> 1) + bit;
        } else {
            result >>= 1;
        }
        bit >>= 2;
    }
    return result;
}

/// Approach 以不越过 target 的固定 delta 接近目标速度。
[[nodiscard]] std::int64_t Approach(
    const std::int64_t current,
    const std::int64_t target,
    const std::int64_t maximum_delta) {
    if (current < target) {
        const auto candidate =
            current > std::numeric_limits<std::int64_t>::max() - maximum_delta
                ? std::numeric_limits<std::int64_t>::max()
                : current + maximum_delta;
        return std::min(candidate, target);
    }
    if (current > target) {
        const auto candidate =
            current < std::numeric_limits<std::int64_t>::min() + maximum_delta
                ? std::numeric_limits<std::int64_t>::min()
                : current - maximum_delta;
        return std::max(candidate, target);
    }
    return current;
}

}  // namespace

MovementError::MovementError(const MovementErrorCode code, const char* message)
    : std::runtime_error(message), code_(code) {}

MovementErrorCode MovementError::Code() const noexcept {
    return code_;
}

void ValidateMovementConfig(const MovementConfig& config) {
    if (config.tick_step_ns <= 0 || config.tick_step_ns > kNanosecondsPerSecond ||
        config.input_scale <= 0 || config.input_scale > 3'037'000'499 ||
        config.maximum_horizontal_speed_mm_per_second <= 0 ||
        config.acceleration_mm_per_second_squared <= 0 ||
        config.deceleration_mm_per_second_squared <= 0 ||
        config.gravity_mm_per_second_squared <= 0 ||
        config.jump_speed_mm_per_second <= 0 ||
        config.maximum_ground_slope_millirad < 0 ||
        config.maximum_step_height_mm < 0) {
        throw MovementError(MovementErrorCode::InvalidConfig, "movement configuration is invalid");
    }
    static_cast<void>(CheckedMultiplyDivide(
        config.acceleration_mm_per_second_squared,
        config.tick_step_ns,
        kNanosecondsPerSecond));
    static_cast<void>(CheckedMultiplyDivide(
        config.deceleration_mm_per_second_squared,
        config.tick_step_ns,
        kNanosecondsPerSecond));
    static_cast<void>(CheckedMultiplyDivide(
        config.gravity_mm_per_second_squared,
        config.tick_step_ns,
        kNanosecondsPerSecond));
    static_cast<void>(CheckedMultiplyDivide(
        config.maximum_horizontal_speed_mm_per_second,
        config.tick_step_ns,
        kNanosecondsPerSecond));
}

MovementIntent ClampMovementIntent(
    const MovementConfig& config,
    MovementIntent intent) {
    ValidateMovementConfig(config);
    intent.x_milli = std::clamp(intent.x_milli, -config.input_scale, config.input_scale);
    intent.z_milli = std::clamp(intent.z_milli, -config.input_scale, config.input_scale);
    const auto x_magnitude = static_cast<std::uint64_t>(
        intent.x_milli < 0 ? -intent.x_milli : intent.x_milli);
    const auto z_magnitude = static_cast<std::uint64_t>(
        intent.z_milli < 0 ? -intent.z_milli : intent.z_milli);
    const auto length_squared = x_magnitude * x_magnitude + z_magnitude * z_magnitude;
    const auto scale_squared =
        static_cast<std::uint64_t>(config.input_scale * config.input_scale);
    if (length_squared <= scale_squared) {
        return intent;
    }
    const auto length = static_cast<std::int64_t>(IntegerSquareRoot(length_squared));
    intent.x_milli = ScaleSigned(intent.x_milli, config.input_scale, length);
    intent.z_milli = ScaleSigned(intent.z_milli, config.input_scale, length);
    return intent;
}

MovementStepResult IntegrateMovement(
    const MovementConfig& config,
    const MovementState& current,
    MovementIntent intent) {
    ValidateMovementConfig(config);
    intent = ClampMovementIntent(config, intent);
    auto next = current;
    const auto target_x = ScaleSigned(
        intent.x_milli,
        config.maximum_horizontal_speed_mm_per_second,
        config.input_scale);
    const auto target_z = ScaleSigned(
        intent.z_milli,
        config.maximum_horizontal_speed_mm_per_second,
        config.input_scale);
    const auto acceleration = CheckedMultiplyDivide(
        config.acceleration_mm_per_second_squared,
        config.tick_step_ns,
        kNanosecondsPerSecond);
    const auto deceleration = CheckedMultiplyDivide(
        config.deceleration_mm_per_second_squared,
        config.tick_step_ns,
        kNanosecondsPerSecond);
    next.velocity_x_mm_per_second = Approach(
        current.velocity_x_mm_per_second,
        target_x,
        intent.x_milli == 0 ? deceleration : acceleration);
    next.velocity_z_mm_per_second = Approach(
        current.velocity_z_mm_per_second,
        target_z,
        intent.z_milli == 0 ? deceleration : acceleration);

    bool jump_accepted = false;
    auto rejection = MovementRejection::None;
    if (intent.jump_pressed) {
        if (current.grounded) {
            next.velocity_y_mm_per_second = config.jump_speed_mm_per_second;
            next.grounded = false;
            jump_accepted = true;
        } else {
            rejection = MovementRejection::AirborneJump;
        }
    }
    if (!current.grounded && !jump_accepted) {
        const auto gravity_delta = CheckedMultiplyDivide(
            config.gravity_mm_per_second_squared,
            config.tick_step_ns,
            kNanosecondsPerSecond);
        next.velocity_y_mm_per_second =
            CheckedAdd(current.velocity_y_mm_per_second, -gravity_delta);
    }

    const auto delta_x = ScaleSigned(
        next.velocity_x_mm_per_second,
        config.tick_step_ns,
        kNanosecondsPerSecond);
    const auto delta_y = ScaleSigned(
        next.velocity_y_mm_per_second,
        config.tick_step_ns,
        kNanosecondsPerSecond);
    const auto delta_z = ScaleSigned(
        next.velocity_z_mm_per_second,
        config.tick_step_ns,
        kNanosecondsPerSecond);
    next.position_x_mm = CheckedAdd(current.position_x_mm, delta_x);
    next.position_y_mm = CheckedAdd(current.position_y_mm, delta_y);
    next.position_z_mm = CheckedAdd(current.position_z_mm, delta_z);
    return {
        .state = next,
        .delta_x_mm = delta_x,
        .delta_y_mm = delta_y,
        .delta_z_mm = delta_z,
        .jump_accepted = jump_accepted,
        .rejection = rejection};
}

MovementState ApplyGroundContact(
    const MovementConfig& config,
    MovementState state,
    const GroundContact& contact) {
    ValidateMovementConfig(config);
    if (contact.slope_millirad < 0 || contact.step_height_mm < 0) {
        throw MovementError(MovementErrorCode::InvalidConfig, "ground contact is invalid");
    }
    state.grounded =
        contact.blocking &&
        contact.slope_millirad <= config.maximum_ground_slope_millirad &&
        contact.step_height_mm <= config.maximum_step_height_mm;
    if (state.grounded && state.velocity_y_mm_per_second < 0) {
        state.velocity_y_mm_per_second = 0;
    }
    return state;
}

}  // namespace ihomeland::sim
