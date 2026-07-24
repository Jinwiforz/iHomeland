#pragma once

#include <cstdint>
#include <stdexcept>

namespace ihomeland::sim {

/// MovementErrorCode 是实例启动与 Tick movement 的稳定失败分类。
enum class MovementErrorCode : std::uint8_t {
    /// InvalidConfig 表示单位、速度或 policy 配置不闭合。
    InvalidConfig,
    /// ArithmeticOverflow 表示 checked integer operation 无法表示结果。
    ArithmeticOverflow,
};

/// MovementError 使 movement 配置和算术失败可被确定性 harness 分类。
class MovementError final : public std::runtime_error {
public:
    /// 构造函数绑定稳定错误码和低敏诊断文本。
    MovementError(MovementErrorCode code, const char* message);

    /// Code 返回不依赖文本的稳定错误码。
    [[nodiscard]] MovementErrorCode Code() const noexcept;

private:
    /// code_ 是当前 movement 失败的稳定分类。
    MovementErrorCode code_;
};

/// MovementRejection 是不产生部分 mutation 的稳定 gameplay 拒绝。
enum class MovementRejection : std::uint8_t {
    /// None 表示当前 Tick 没有 movement 拒绝。
    None,
    /// AirborneJump 表示离散 jump edge 到达时 actor 不在合法 ground。
    AirborneJump,
};

/// MovementConfig 固定整数单位、Tick 积分参数与 ground policy。
struct MovementConfig final {
    /// tick_step_ns 是单次积分的固定时长，单位 nanoseconds。
    std::int64_t tick_step_ns;
    /// input_scale 是满幅 movement intent 的正整数 scale。
    std::int64_t input_scale;
    /// maximum_horizontal_speed_mm_per_second 是水平速度硬上限。
    std::int64_t maximum_horizontal_speed_mm_per_second;
    /// acceleration_mm_per_second_squared 是有输入时的速度接近率。
    std::int64_t acceleration_mm_per_second_squared;
    /// deceleration_mm_per_second_squared 是回到 neutral 时的速度接近率。
    std::int64_t deceleration_mm_per_second_squared;
    /// gravity_mm_per_second_squared 是 airborne 每秒向下加速度的正值。
    std::int64_t gravity_mm_per_second_squared;
    /// jump_speed_mm_per_second 是合法 jump 后的向上速度。
    std::int64_t jump_speed_mm_per_second;
    /// maximum_ground_slope_millirad 是仍可视为 ground 的最大坡度。
    std::int64_t maximum_ground_slope_millirad;
    /// maximum_step_height_mm 是可稳定跨越的最大 step。
    std::int64_t maximum_step_height_mm;
};

/// MovementIntent 是 InputIntent/AIIntent 交给 Movement stage 的整数值。
struct MovementIntent final {
    /// x_milli 是局部或已映射 world X 方向，范围由 input_scale clamp。
    std::int64_t x_milli;
    /// z_milli 是局部或已映射 world Z 方向，范围由 input_scale clamp。
    std::int64_t z_milli;
    /// jump_pressed 是当前 Tick 的离散 edge，不允许 hold。
    bool jump_pressed;
};

/// GroundContact 是 PhysicsWorld 量化后交给 movement policy 的项目 value。
struct GroundContact final {
    /// blocking 表示 ground probe 存在可阻挡接触。
    bool blocking;
    /// slope_millirad 是量化后的非负坡度。
    std::int64_t slope_millirad;
    /// step_height_mm 是相对 actor foot 的量化台阶高度。
    std::int64_t step_height_mm;
};

/// MovementState 是 Movement/Physics stage 唯一拥有的整数运动快照。
struct MovementState final {
    /// position_x_mm 是 world X 位置。
    std::int64_t position_x_mm;
    /// position_y_mm 是向上为正的 world Y 位置。
    std::int64_t position_y_mm;
    /// position_z_mm 是 world Z 位置。
    std::int64_t position_z_mm;
    /// velocity_x_mm_per_second 是 world X 速度。
    std::int64_t velocity_x_mm_per_second;
    /// velocity_y_mm_per_second 是 world Y 速度。
    std::int64_t velocity_y_mm_per_second;
    /// velocity_z_mm_per_second 是 world Z 速度。
    std::int64_t velocity_z_mm_per_second;
    /// grounded 是上一 Physics stage 提交的权威接地状态。
    bool grounded;
};

/// MovementStepResult 是单 Tick 原子积分的完整结果。
struct MovementStepResult final {
    /// state 是成功计算后的下一状态；失败抛异常时旧状态不变。
    MovementState state;
    /// delta_x_mm 是本 Tick 请求 PhysicsWorld 移动的 X 位移。
    std::int64_t delta_x_mm;
    /// delta_y_mm 是本 Tick 请求 PhysicsWorld 移动的 Y 位移。
    std::int64_t delta_y_mm;
    /// delta_z_mm 是本 Tick 请求 PhysicsWorld 移动的 Z 位移。
    std::int64_t delta_z_mm;
    /// jump_accepted 只在 grounded jump edge 成功时为 true。
    bool jump_accepted;
    /// rejection 对非法 airborne repeat jump 返回稳定原因。
    MovementRejection rejection;
};

/// ValidateMovementConfig 在实例启动前拒绝非法或必然溢出的参数。
void ValidateMovementConfig(const MovementConfig& config);

/// ClampMovementIntent 按欧氏长度把二维 intent 规范化到 input_scale 圆内。
[[nodiscard]] MovementIntent ClampMovementIntent(
    const MovementConfig& config,
    MovementIntent intent);

/// IntegrateMovement 以 toward-zero 整数积分原子计算下一 movement state。
[[nodiscard]] MovementStepResult IntegrateMovement(
    const MovementConfig& config,
    const MovementState& current,
    MovementIntent intent);

/// ApplyGroundContact 以固定 slope/step policy 提交下一 Tick grounded 状态。
[[nodiscard]] MovementState ApplyGroundContact(
    const MovementConfig& config,
    MovementState state,
    const GroundContact& contact);

}  // namespace ihomeland::sim
