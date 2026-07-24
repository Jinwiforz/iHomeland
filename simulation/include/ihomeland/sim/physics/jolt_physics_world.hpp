#pragma once

#include "ihomeland/sim/physics/physics_world.hpp"

#include <cstddef>
#include <cstdint>
#include <memory>
#include <span>
#include <stdexcept>

namespace ihomeland::sim {

/// JoltPhysicsErrorCode 是 live adapter 初始化/query 的稳定失败分类。
enum class JoltPhysicsErrorCode : std::uint8_t {
    /// InvalidConfig 表示单位/layer/shape/solver/allocator 配置漂移。
    InvalidConfig,
    /// InvalidCollider 表示 scene collider identity/shape 不合法或重复。
    InvalidCollider,
    /// NonFinite 表示第三方转换或 callback 返回 NaN/Inf/范围外值。
    NonFinite,
    /// Capacity 表示 body/query/hit hard limit 被超过。
    Capacity,
    /// Runtime 表示 Jolt body/query 初始化失败。
    Runtime,
};

/// JoltPhysicsError 保留可机器判断的 live adapter failure。
class JoltPhysicsError final : public std::runtime_error {
public:
    /// 构造函数绑定稳定错误码与低敏诊断文本。
    JoltPhysicsError(JoltPhysicsErrorCode code, const char* message);

    /// Code 返回不依赖文本的稳定错误码。
    [[nodiscard]] JoltPhysicsErrorCode Code() const noexcept;

private:
    /// code_ 是当前 Jolt adapter 失败分类。
    JoltPhysicsErrorCode code_;
};

/// JoltPhysicsConfig 冻结坐标、量化、shape、solver、allocator 与容量。
struct JoltPhysicsConfig final {
    /// millimeters_per_jolt_unit 固定为 1000。
    std::uint32_t millimeters_per_jolt_unit;
    /// fraction_quantization 固定为 1_000_000。
    std::uint32_t fraction_quantization;
    /// query_capsule_radius_mm 是 capsule/shape/projectile query 半径。
    std::uint32_t query_capsule_radius_mm;
    /// query_capsule_half_height_mm 是 capsule 圆柱部分半高。
    std::uint32_t query_capsule_half_height_mm;
    /// solver_velocity_steps 固定 Jolt velocity iterations。
    std::uint32_t solver_velocity_steps;
    /// solver_position_steps 固定 Jolt position iterations。
    std::uint32_t solver_position_steps;
    /// temporary_allocator_bytes 是实例预分配 Jolt temp allocator 大小。
    std::uint32_t temporary_allocator_bytes;
    /// maximum_bodies 是 PhysicsSystem body hard limit。
    std::uint32_t maximum_bodies;
    /// maximum_body_pairs 是 broadphase pair hard limit。
    std::uint32_t maximum_body_pairs;
    /// maximum_contact_constraints 是 contact buffer hard limit。
    std::uint32_t maximum_contact_constraints;
    /// maximum_hits_per_query 是复制 callback 的 hard limit。
    std::uint32_t maximum_hits_per_query;
};

/// JoltBoxCollider 是 live parity scene 可加载的固定静态 box 项目 value。
struct JoltBoxCollider final {
    /// collider_id 是 scene 内非零唯一 ColliderID。
    std::uint64_t collider_id;
    /// subshape_id 是 primitive box 的非零稳定项目 SubshapeID。
    std::uint64_t subshape_id;
    /// target_actor_id 是 gameplay actor，world geometry 使用零。
    std::uint64_t target_actor_id;
    /// center_mm 是 box 中心整数坐标。
    Vector3Mm center_mm;
    /// half_extent_mm 是每轴正整数半长。
    Vector3Mm half_extent_mm;
    /// blocking 是 projectile first-blocking policy 值。
    bool blocking;
};

/// JoltPhysicsWorld 以 PIMPL 隔离全部 Jolt type/allocator/handle。
class JoltPhysicsWorld final : public PhysicsWorld {
public:
    /// 构造函数验证冻结配置、初始化单 layer world 并批量加载静态 boxes。
    JoltPhysicsWorld(
        const JoltPhysicsConfig& config,
        std::span<const JoltBoxCollider> colliders);

    /// 析构函数先移除/销毁 bodies，再释放 PhysicsSystem 和 allocator。
    ~JoltPhysicsWorld() override;

    JoltPhysicsWorld(const JoltPhysicsWorld&) = delete;
    JoltPhysicsWorld& operator=(const JoltPhysicsWorld&) = delete;
    JoltPhysicsWorld(JoltPhysicsWorld&&) noexcept;
    JoltPhysicsWorld& operator=(JoltPhysicsWorld&&) noexcept;

    /// Query 执行固定 ray/capsule/overlap policy并返回规范项目 hit values。
    [[nodiscard]] PhysicsQueryResult Query(const PhysicsQuery& query) override;

private:
    /// Impl 是唯一允许出现 Jolt types 的 translation-unit-local owner。
    class Impl;
    /// impl_ 唯一拥有 Jolt world、allocator、bodies 与 metadata。
    std::unique_ptr<Impl> impl_;
};

}  // namespace ihomeland::sim
