#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

namespace ihomeland::sim {

/// Vector3Mm 是 PhysicsWorld 边界唯一允许的整数位置/位移类型。
struct Vector3Mm final {
    /// x 是 world X，单位 millimeters。
    std::int64_t x;
    /// y 是 world Y，单位 millimeters。
    std::int64_t y;
    /// z 是 world Z，单位 millimeters。
    std::int64_t z;

    /// operator== 比较规范整数坐标。
    bool operator==(const Vector3Mm&) const = default;
};

/// PhysicsQueryKind 是首版 gameplay 允许的闭合 query 集。
enum class PhysicsQueryKind : std::uint8_t {
    /// GroundProbe 查询 actor foot 下方 ground。
    GroundProbe,
    /// MoveCapsule 查询 kinematic actor capsule movement。
    MoveCapsule,
    /// ShapeCast 查询近战/volume sweep。
    ShapeCast,
    /// RayCast 查询直线遮挡或命中。
    RayCast,
    /// Overlap 查询当前位置 volume overlap。
    Overlap,
    /// ProjectileSweep 查询 projectile 当前 Tick movement。
    ProjectileSweep,
};

/// PhysicsHitValue 是第三方 callback 复制、量化后的项目内命中值。
struct PhysicsHitValue final {
    /// fraction_millionths 是 [0, 1_000_000] 内的 sweep fraction。
    std::uint32_t fraction_millionths;
    /// collider_id 是 map/config 内非零稳定 ColliderID。
    std::uint64_t collider_id;
    /// subshape_id 是 collider 内非零稳定 SubshapeID。
    std::uint64_t subshape_id;
    /// target_actor_id 是可受 gameplay 影响的稳定 ActorID，零表示 world geometry。
    std::uint64_t target_actor_id;
    /// blocking 表示 projectile 遇到该 hit 后必须终止。
    bool blocking;
};

/// PhysicsQuery 是 gameplay system 提交给 adapter 的完整只读 value。
struct PhysicsQuery final {
    /// query_id 是实例/Tick 内非零稳定 identity。
    std::uint64_t query_id;
    /// tick 是当前非零 SimulationTick。
    std::uint64_t tick;
    /// kind 决定 adapter 使用的固定 shape/layer/filter policy。
    PhysicsQueryKind kind;
    /// actor_id 是 query authority owner，system query 可使用零。
    std::uint64_t actor_id;
    /// start_mm 是量化后的 query 起点。
    Vector3Mm start_mm;
    /// end_mm 是量化后的 query 终点；Overlap 可等于 start。
    Vector3Mm end_mm;
    /// maximum_hits 是 gameplay 预先分配的正整数 hard limit。
    std::size_t maximum_hits;

    /// operator== 用于 recorded adapter 的严格 query identity 比较。
    bool operator==(const PhysicsQuery&) const = default;
};

/// PhysicsQueryResult 是 callback 已复制并规范排序的 adapter 输出。
struct PhysicsQueryResult final {
    /// query_id 必须与输入 query 完全一致。
    std::uint64_t query_id;
    /// hits 只包含项目 values，不暴露第三方 handle/type。
    std::vector<PhysicsHitValue> hits;
};

/// PhysicsWorld 是 gameplay systems 可依赖的唯一物理查询 port。
class PhysicsWorld {
public:
    /// 虚析构函数允许实例 owner 经窄接口释放 adapter。
    virtual ~PhysicsWorld() = default;

    /// Query 同步返回规范 result，不允许 callback 在返回后修改结果。
    [[nodiscard]] virtual PhysicsQueryResult Query(const PhysicsQuery& query) = 0;
};

}  // namespace ihomeland::sim
