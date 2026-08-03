#pragma once

#include "ihomeland/sim/physics/physics_world.hpp"

#include <cstdint>
#include <stdexcept>

namespace ihomeland::sim {

/// FlatGroundPhysicsErrorCode 是 current PersonalWorld 过渡 adapter 的闭合失败分类。
enum class FlatGroundPhysicsErrorCode : std::uint8_t {
    /// InvalidQuery 表示 identity、方向、坐标或容量违反固定 contract。
    InvalidQuery,
    /// UnsupportedQuery 表示调用方越过 ground/capsule movement 子集。
    UnsupportedQuery,
};

/// FlatGroundPhysicsError 携带稳定且低敏的 PhysicsWorld adapter failure。
class FlatGroundPhysicsError final : public std::runtime_error {
public:
    /// 构造函数绑定稳定错误码。
    FlatGroundPhysicsError(
        FlatGroundPhysicsErrorCode code,
        const char* message);

    /// Code 返回可机器判断的失败分类。
    [[nodiscard]] FlatGroundPhysicsErrorCode Code() const noexcept;

private:
    /// code_ 在异常构造后不可变。
    FlatGroundPhysicsErrorCode code_;
};

/// FlatGroundPhysicsWorld 为尚未绑定正式地图碰撞的 current PersonalWorld 提供 Y=0 平地。
///
/// 该 adapter 只接受单 actor 的 GroundProbe 与 MoveCapsule value query，不读取 Unity
/// Scene、客户端 Transform 或 wall clock。正式地图 physics 必须通过同一 PhysicsWorld
/// port 在后续独立 change 中替换。
class FlatGroundPhysicsWorld final : public PhysicsWorld {
public:
    /// GroundColliderID 是当前过渡 adapter 唯一稳定 world geometry identity。
    static constexpr std::uint64_t GroundColliderID = 1;
    /// GroundSubshapeID 是平面 primitive 的唯一稳定 subshape identity。
    static constexpr std::uint64_t GroundSubshapeID = 1;
    /// GroundLevelMillimeters 固定 current PersonalWorld 的服务器地面高度。
    static constexpr std::int64_t GroundLevelMillimeters = 0;
    /// MaximumCoordinateMagnitudeMillimeters 防止 fraction 量化中间值溢出。
    static constexpr std::int64_t MaximumCoordinateMagnitudeMillimeters =
        1'000'000'000;
    /// MaximumHitsPerQuery 与 current actor movement 的单平面结果一致。
    static constexpr std::size_t MaximumHitsPerQuery = 1;

    /// Query 返回与 Y=0 首次交点对应的规范 blocking hit，未跨越地面时为空。
    [[nodiscard]] PhysicsQueryResult Query(
        const PhysicsQuery& query) override;
};

}  // namespace ihomeland::sim
