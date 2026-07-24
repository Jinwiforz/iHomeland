#include "ihomeland/sim/gameplay/movement.hpp"

#include <cstdint>
#include <iostream>
#include <limits>
#include <stdexcept>

namespace {

/// Require 把 movement 回归失败转换为单一 test exception。
void Require(const bool condition, const char* message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

/// FixtureConfig 返回 movement corpus 使用的 16 ms 整数积分参数。
[[nodiscard]] ihomeland::sim::MovementConfig FixtureConfig() {
    return {
        .tick_step_ns = 16'000'000,
        .input_scale = 1000,
        .maximum_horizontal_speed_mm_per_second = 3000,
        .acceleration_mm_per_second_squared = 187'500,
        .deceleration_mm_per_second_squared = 93'750,
        .gravity_mm_per_second_squared = 10'000,
        .jump_speed_mm_per_second = 5000,
        .maximum_ground_slope_millirad = 785,
        .maximum_step_height_mm = 400};
}

/// TestFixtureKinematics 验证 corpus 的 grounded jump、重力积分和 airborne repeat 拒绝。
void TestFixtureKinematics() {
    const auto config = FixtureConfig();
    ihomeland::sim::MovementState state{
        .position_x_mm = 0,
        .position_y_mm = 0,
        .position_z_mm = 0,
        .velocity_x_mm_per_second = 0,
        .velocity_y_mm_per_second = 0,
        .velocity_z_mm_per_second = 0,
        .grounded = true};
    const auto first = ihomeland::sim::IntegrateMovement(
        config,
        state,
        {.x_milli = 1000, .z_milli = 0, .jump_pressed = true});
    Require(
        first.state.position_x_mm == 48 &&
            first.state.position_y_mm == 80 &&
            first.state.velocity_x_mm_per_second == 3000 &&
            first.state.velocity_y_mm_per_second == 5000 &&
            first.jump_accepted,
        "fixture first movement Tick drifted");

    const auto second = ihomeland::sim::IntegrateMovement(
        config,
        first.state,
        {.x_milli = 1000, .z_milli = 0, .jump_pressed = true});
    Require(
        second.state.position_x_mm == 96 &&
            second.state.position_y_mm == 157 &&
            second.state.velocity_y_mm_per_second == 4840 &&
            second.rejection == ihomeland::sim::MovementRejection::AirborneJump,
        "airborne repeat jump changed movement state");
    const auto gravity = ihomeland::sim::IntegrateMovement(
        config,
        first.state,
        {.x_milli = 1000, .z_milli = 0, .jump_pressed = false});
    Require(
        gravity.state.position_y_mm == 157 &&
            gravity.state.velocity_y_mm_per_second == 4840,
        "fixture gravity integration drifted");
}

/// TestClampAndDeceleration 验证 diagonal clamp 不超过满幅且 neutral 使用独立减速度。
void TestClampAndDeceleration() {
    const auto config = FixtureConfig();
    const auto clamped = ihomeland::sim::ClampMovementIntent(
        config,
        {.x_milli = 1200, .z_milli = 1200, .jump_pressed = false});
    Require(
        clamped.x_milli == 707 && clamped.z_milli == 707,
        "diagonal movement clamp drifted");
    const auto result = ihomeland::sim::IntegrateMovement(
        config,
        {
            .position_x_mm = 0,
            .position_y_mm = 0,
            .position_z_mm = 0,
            .velocity_x_mm_per_second = 3000,
            .velocity_y_mm_per_second = 0,
            .velocity_z_mm_per_second = 0,
            .grounded = true},
        {.x_milli = 0, .z_milli = 0, .jump_pressed = false});
    Require(
        result.state.velocity_x_mm_per_second == 1500,
        "movement deceleration policy drifted");
}

/// TestGroundPolicy 验证 slope/step 边界与向下速度清零。
void TestGroundPolicy() {
    const auto config = FixtureConfig();
    ihomeland::sim::MovementState state{
        .position_x_mm = 0,
        .position_y_mm = 0,
        .position_z_mm = 0,
        .velocity_x_mm_per_second = 0,
        .velocity_y_mm_per_second = -160,
        .velocity_z_mm_per_second = 0,
        .grounded = false};
    const auto grounded = ihomeland::sim::ApplyGroundContact(
        config,
        state,
        {.blocking = true, .slope_millirad = 785, .step_height_mm = 400});
    Require(
        grounded.grounded && grounded.velocity_y_mm_per_second == 0,
        "slope/step inclusive boundary drifted");
    const auto steep = ihomeland::sim::ApplyGroundContact(
        config,
        state,
        {.blocking = true, .slope_millirad = 786, .step_height_mm = 0});
    Require(!steep.grounded, "steep slope was accepted as ground");
}

/// TestCheckedArithmetic 验证配置与 Tick 中间值溢出均 fail closed。
void TestCheckedArithmetic() {
    auto invalid = FixtureConfig();
    invalid.tick_step_ns = 0;
    bool invalid_rejected = false;
    try {
        ihomeland::sim::ValidateMovementConfig(invalid);
    } catch (const ihomeland::sim::MovementError& error) {
        Require(
            error.Code() == ihomeland::sim::MovementErrorCode::InvalidConfig,
            "invalid movement config returned wrong code");
        invalid_rejected = true;
    }
    Require(invalid_rejected, "invalid movement config was accepted");

    auto overflow = FixtureConfig();
    overflow.acceleration_mm_per_second_squared =
        std::numeric_limits<std::int64_t>::max();
    try {
        ihomeland::sim::ValidateMovementConfig(overflow);
    } catch (const ihomeland::sim::MovementError& error) {
        Require(
            error.Code() == ihomeland::sim::MovementErrorCode::ArithmeticOverflow,
            "movement overflow returned wrong code");
        return;
    }
    throw std::runtime_error("overflow movement config was accepted");
}

}  // namespace

/// main 执行 movement 整数积分、policy 与负向算术回归。
int main() {
    try {
        TestFixtureKinematics();
        TestClampAndDeceleration();
        TestGroundPolicy();
        TestCheckedArithmetic();
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
