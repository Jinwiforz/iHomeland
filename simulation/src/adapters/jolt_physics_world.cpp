#include "ihomeland/sim/physics/jolt_physics_world.hpp"

#include "ihomeland/sim/gameplay/hit_detection.hpp"

#include <Jolt/Jolt.h>
#include <Jolt/RegisterTypes.h>
#include <Jolt/Core/Factory.h>
#include <Jolt/Core/TempAllocator.h>
#include <Jolt/Physics/Body/BodyCreationSettings.h>
#include <Jolt/Physics/Collision/CastResult.h>
#include <Jolt/Physics/Collision/CollisionCollectorImpl.h>
#include <Jolt/Physics/Collision/NarrowPhaseQuery.h>
#include <Jolt/Physics/Collision/RayCast.h>
#include <Jolt/Physics/Collision/Shape/BoxShape.h>
#include <Jolt/Physics/Collision/Shape/CapsuleShape.h>
#include <Jolt/Physics/Collision/ShapeCast.h>
#include <Jolt/Physics/PhysicsSettings.h>
#include <Jolt/Physics/PhysicsSystem.h>

#include <algorithm>
#include <cmath>
#include <limits>
#include <unordered_map>
#include <utility>

namespace ihomeland::sim {
namespace {

/// kStaticObjectLayer 是 B0.3 scene 唯一 object layer。
constexpr JPH::ObjectLayer kStaticObjectLayer = 0;
/// kStaticBroadPhaseLayer 是 B0.3 scene 唯一 broadphase layer。
constexpr JPH::BroadPhaseLayer kStaticBroadPhaseLayer{0};

/// JoltRuntime 在进程内唯一注册 allocator/factory/types 并在退出时逆序释放。
class JoltRuntime final {
public:
    /// 构造函数按 Jolt 要求的全局顺序初始化 runtime。
    JoltRuntime() {
        JPH::RegisterDefaultAllocator();
        if (JPH::Factory::sInstance != nullptr) {
            throw JoltPhysicsError(
                JoltPhysicsErrorCode::Runtime,
                "Jolt Factory is already owned by another runtime");
        }
        JPH::Factory::sInstance = new JPH::Factory();
        JPH::RegisterTypes();
    }

    /// 析构函数在所有 function-local dependent objects 之后卸载 types/factory。
    ~JoltRuntime() {
        JPH::UnregisterTypes();
        delete JPH::Factory::sInstance;
        JPH::Factory::sInstance = nullptr;
    }

    JoltRuntime(const JoltRuntime&) = delete;
    JoltRuntime& operator=(const JoltRuntime&) = delete;
};

/// Runtime 返回线程安全初始化的进程唯一 Jolt runtime。
[[nodiscard]] JoltRuntime& Runtime() {
    static JoltRuntime runtime;
    return runtime;
}

/// SingleBroadPhaseLayer 固定 object layer 到唯一 broadphase layer 的映射。
class SingleBroadPhaseLayer final : public JPH::BroadPhaseLayerInterface {
public:
    /// GetNumBroadPhaseLayers 返回固定单 layer 数。
    [[nodiscard]] JPH::uint GetNumBroadPhaseLayers() const override {
        return 1;
    }

    /// GetBroadPhaseLayer 拒绝配置外 object layer。
    [[nodiscard]] JPH::BroadPhaseLayer GetBroadPhaseLayer(
        const JPH::ObjectLayer layer) const override {
        if (layer != kStaticObjectLayer) {
            throw JoltPhysicsError(
                JoltPhysicsErrorCode::InvalidConfig,
                "Jolt object layer is outside fixed registry");
        }
        return kStaticBroadPhaseLayer;
    }
};

/// AllowSingleBroadPhase 只允许固定 object/broadphase layer pair。
class AllowSingleBroadPhase final : public JPH::ObjectVsBroadPhaseLayerFilter {
public:
    /// ShouldCollide 对唯一合法 layer pair 返回 true。
    [[nodiscard]] bool ShouldCollide(
        const JPH::ObjectLayer object_layer,
        const JPH::BroadPhaseLayer broadphase_layer) const override {
        return object_layer == kStaticObjectLayer &&
               broadphase_layer == kStaticBroadPhaseLayer;
    }
};

/// AllowSingleObjectPair 只允许固定 object layer 自碰撞。
class AllowSingleObjectPair final : public JPH::ObjectLayerPairFilter {
public:
    /// ShouldCollide 对唯一合法 object pair 返回 true。
    [[nodiscard]] bool ShouldCollide(
        const JPH::ObjectLayer first,
        const JPH::ObjectLayer second) const override {
        return first == kStaticObjectLayer && second == kStaticObjectLayer;
    }
};

/// ValidateConfig 拒绝任何单位、量化、solver、allocator 或容量漂移。
void ValidateConfig(const JoltPhysicsConfig& config) {
    if (config.millimeters_per_jolt_unit != 1000 ||
        config.fraction_quantization != 1'000'000 ||
        config.query_capsule_radius_mm == 0 ||
        config.query_capsule_half_height_mm == 0 ||
        config.solver_velocity_steps == 0 ||
        config.solver_position_steps == 0 ||
        config.temporary_allocator_bytes < 1024 * 1024 ||
        config.maximum_bodies == 0 ||
        config.maximum_body_pairs == 0 ||
        config.maximum_contact_constraints == 0 ||
        config.maximum_hits_per_query == 0) {
        throw JoltPhysicsError(
            JoltPhysicsErrorCode::InvalidConfig,
            "Jolt physics configuration differs from fixed contract");
    }
}

/// ValidatedAllocatorBytes 在分配前验证配置，避免非法大小先触发第三方分配。
[[nodiscard]] std::uint32_t ValidatedAllocatorBytes(
    const JoltPhysicsConfig& config) {
    ValidateConfig(config);
    return config.temporary_allocator_bytes;
}

/// ToJoltCoordinate 把 millimeters 转为有限 float Jolt units。
[[nodiscard]] float ToJoltCoordinate(
    const std::int64_t millimeters,
    const JoltPhysicsConfig& config) {
    const auto value =
        static_cast<double>(millimeters) / config.millimeters_per_jolt_unit;
    if (!std::isfinite(value) ||
        value > std::numeric_limits<float>::max() ||
        value < -std::numeric_limits<float>::max()) {
        throw JoltPhysicsError(
            JoltPhysicsErrorCode::NonFinite,
            "project coordinate cannot be represented by Jolt");
    }
    return static_cast<float>(value);
}

/// ToJoltPosition 转换 integer point，不保留项目类型引用。
[[nodiscard]] JPH::RVec3 ToJoltPosition(
    const Vector3Mm& value,
    const JoltPhysicsConfig& config) {
    return JPH::RVec3(
        ToJoltCoordinate(value.x, config),
        ToJoltCoordinate(value.y, config),
        ToJoltCoordinate(value.z, config));
}

/// ToJoltDirection 转换 end-start，并用 checked integer subtraction 拒绝 wrap。
[[nodiscard]] JPH::Vec3 ToJoltDirection(
    const Vector3Mm& start,
    const Vector3Mm& end,
    const JoltPhysicsConfig& config) {
    const auto checked_delta = [](const std::int64_t left, const std::int64_t right) {
        if ((right > 0 && left < std::numeric_limits<std::int64_t>::min() + right) ||
            (right < 0 && left > std::numeric_limits<std::int64_t>::max() + right)) {
            throw JoltPhysicsError(
                JoltPhysicsErrorCode::NonFinite,
                "physics query direction integer subtraction overflow");
        }
        return left - right;
    };
    return JPH::Vec3(
        ToJoltCoordinate(checked_delta(end.x, start.x), config),
        ToJoltCoordinate(checked_delta(end.y, start.y), config),
        ToJoltCoordinate(checked_delta(end.z, start.z), config));
}

}  // namespace

/// JoltPhysicsWorld::Impl 是 Jolt world 与 metadata 的唯一 translation-unit owner。
class JoltPhysicsWorld::Impl final {
public:
    /// ColliderMetadata 把 Jolt Body user data 映射回稳定项目值。
    struct ColliderMetadata final {
        /// subshape_id 是项目稳定 SubshapeID。
        std::uint64_t subshape_id;
        /// target_actor_id 是 gameplay target，world geometry 为零。
        std::uint64_t target_actor_id;
        /// blocking 是 projectile policy。
        bool blocking;
    };

    /// 构造函数初始化 PhysicsSystem、固定 solver/temp allocator 和静态 box scene。
    Impl(
        const JoltPhysicsConfig& config,
        const std::span<const JoltBoxCollider> colliders)
        : config_(config),
          runtime_(Runtime()),
          temporary_allocator_(ValidatedAllocatorBytes(config)) {
        if (colliders.size() > config_.maximum_bodies) {
            throw JoltPhysicsError(
                JoltPhysicsErrorCode::Capacity,
                "Jolt collider set exceeds body capacity");
        }
        physics_.Init(
            config_.maximum_bodies,
            0,
            config_.maximum_body_pairs,
            config_.maximum_contact_constraints,
            broadphase_layers_,
            object_vs_broadphase_,
            object_pairs_);
        auto settings = physics_.GetPhysicsSettings();
        settings.mNumVelocitySteps = config_.solver_velocity_steps;
        settings.mNumPositionSteps = config_.solver_position_steps;
        physics_.SetPhysicsSettings(settings);

        auto& bodies = physics_.GetBodyInterface();
        body_ids_.reserve(colliders.size());
        metadata_.reserve(colliders.size());
        for (const auto& collider : colliders) {
            if (collider.collider_id == 0 || collider.subshape_id == 0 ||
                collider.half_extent_mm.x <= 0 ||
                collider.half_extent_mm.y <= 0 ||
                collider.half_extent_mm.z <= 0 ||
                metadata_.contains(collider.collider_id)) {
                throw JoltPhysicsError(
                    JoltPhysicsErrorCode::InvalidCollider,
                    "Jolt box collider is invalid or duplicated");
            }
            const JPH::Vec3 half_extent(
                ToJoltCoordinate(collider.half_extent_mm.x, config_),
                ToJoltCoordinate(collider.half_extent_mm.y, config_),
                ToJoltCoordinate(collider.half_extent_mm.z, config_));
            JPH::BodyCreationSettings body_settings(
                new JPH::BoxShape(half_extent),
                ToJoltPosition(collider.center_mm, config_),
                JPH::Quat::sIdentity(),
                JPH::EMotionType::Static,
                kStaticObjectLayer);
            body_settings.mUserData = collider.collider_id;
            const auto body_id = bodies.CreateAndAddBody(
                body_settings,
                JPH::EActivation::DontActivate);
            if (body_id.IsInvalid()) {
                throw JoltPhysicsError(
                    JoltPhysicsErrorCode::Runtime,
                    "Jolt failed to create a fixed scene body");
            }
            body_ids_.push_back(body_id);
            metadata_.emplace(
                collider.collider_id,
                ColliderMetadata{
                    .subshape_id = collider.subshape_id,
                    .target_actor_id = collider.target_actor_id,
                    .blocking = collider.blocking});
        }
        physics_.OptimizeBroadPhase();
    }

    /// 析构函数逆序移除并销毁全部 static bodies。
    ~Impl() {
        auto& bodies = physics_.GetBodyInterface();
        for (auto iterator = body_ids_.rbegin(); iterator != body_ids_.rend(); ++iterator) {
            bodies.RemoveBody(*iterator);
            bodies.DestroyBody(*iterator);
        }
    }

    Impl(const Impl&) = delete;
    Impl& operator=(const Impl&) = delete;

    /// Query 执行闭合 query kind，并在第三方边界内复制/量化 hits。
    [[nodiscard]] PhysicsQueryResult Query(const PhysicsQuery& query) {
        if (query.query_id == 0 || query.tick == 0 || query.maximum_hits == 0 ||
            query.maximum_hits > config_.maximum_hits_per_query) {
            throw JoltPhysicsError(
                JoltPhysicsErrorCode::Capacity,
                "Jolt PhysicsQuery identity or capacity is invalid");
        }
        std::vector<PhysicsHitValue> hits;
        hits.reserve(query.maximum_hits);
        const auto start = ToJoltPosition(query.start_mm, config_);
        const auto direction = ToJoltDirection(query.start_mm, query.end_mm, config_);
        switch (query.kind) {
            case PhysicsQueryKind::GroundProbe:
            case PhysicsQueryKind::RayCast: {
                JPH::AllHitCollisionCollector<JPH::CastRayCollector> collector;
                physics_.GetNarrowPhaseQuery().CastRay(
                    JPH::RRayCast(start, direction),
                    JPH::RayCastSettings{},
                    collector);
                for (const auto& hit : collector.mHits) {
                    AppendHit(hits, hit.mFraction, hit.mBodyID);
                }
                break;
            }
            case PhysicsQueryKind::MoveCapsule:
            case PhysicsQueryKind::ShapeCast:
            case PhysicsQueryKind::ProjectileSweep: {
                const JPH::RefConst<JPH::Shape> shape = new JPH::CapsuleShape(
                    ToJoltCoordinate(config_.query_capsule_half_height_mm, config_),
                    ToJoltCoordinate(config_.query_capsule_radius_mm, config_));
                const auto cast = JPH::RShapeCast::sFromWorldTransform(
                    shape,
                    JPH::Vec3::sOne(),
                    JPH::RMat44::sTranslation(start),
                    direction);
                JPH::AllHitCollisionCollector<JPH::CastShapeCollector> collector;
                physics_.GetNarrowPhaseQuery().CastShape(
                    cast,
                    JPH::ShapeCastSettings{},
                    JPH::RVec3::sZero(),
                    collector);
                for (const auto& hit : collector.mHits) {
                    AppendHit(hits, hit.mFraction, hit.mBodyID2);
                }
                break;
            }
            case PhysicsQueryKind::Overlap: {
                const JPH::RefConst<JPH::Shape> shape = new JPH::CapsuleShape(
                    ToJoltCoordinate(config_.query_capsule_half_height_mm, config_),
                    ToJoltCoordinate(config_.query_capsule_radius_mm, config_));
                JPH::AllHitCollisionCollector<JPH::CollideShapeCollector> collector;
                physics_.GetNarrowPhaseQuery().CollideShape(
                    shape,
                    JPH::Vec3::sOne(),
                    JPH::RMat44::sTranslation(start),
                    JPH::CollideShapeSettings{},
                    JPH::RVec3::sZero(),
                    collector);
                for (const auto& hit : collector.mHits) {
                    AppendHit(hits, 0.0F, hit.mBodyID2);
                }
                break;
            }
            default:
                throw JoltPhysicsError(
                    JoltPhysicsErrorCode::InvalidConfig,
                    "Jolt PhysicsQuery kind is outside fixed registry");
        }
        hits = CanonicalizePhysicsHits(hits);
        if (hits.size() > query.maximum_hits ||
            hits.size() > config_.maximum_hits_per_query) {
            throw JoltPhysicsError(
                JoltPhysicsErrorCode::Capacity,
                "Jolt callback set exceeds query hit capacity");
        }
        return {.query_id = query.query_id, .hits = std::move(hits)};
    }

private:
    /// AppendHit 把 Jolt callback value 复制、量化并映射到稳定 metadata。
    void AppendHit(
        std::vector<PhysicsHitValue>& output,
        const float fraction,
        const JPH::BodyID body_id) const {
        if (!std::isfinite(fraction) || fraction < 0.0F || fraction > 1.0F) {
            throw JoltPhysicsError(
                JoltPhysicsErrorCode::NonFinite,
                "Jolt callback fraction is non-finite or outside range");
        }
        const auto collider_id =
            physics_.GetBodyInterface().GetUserData(body_id);
        const auto metadata = metadata_.find(collider_id);
        if (collider_id == 0 || metadata == metadata_.end()) {
            throw JoltPhysicsError(
                JoltPhysicsErrorCode::Runtime,
                "Jolt callback references unknown body metadata");
        }
        const auto quantized = std::llround(
            static_cast<double>(fraction) * config_.fraction_quantization);
        if (quantized < 0 ||
            quantized > static_cast<std::int64_t>(config_.fraction_quantization)) {
            throw JoltPhysicsError(
                JoltPhysicsErrorCode::NonFinite,
                "Jolt callback fraction quantization overflow");
        }
        output.push_back({
            .fraction_millionths = static_cast<std::uint32_t>(quantized),
            .collider_id = collider_id,
            .subshape_id = metadata->second.subshape_id,
            .target_actor_id = metadata->second.target_actor_id,
            .blocking = metadata->second.blocking});
    }

    /// config_ 是启动后不可变的固定 adapter 配置。
    JoltPhysicsConfig config_;
    /// runtime_ 强制 Jolt 全局 allocator/factory/types 先于任何 Jolt state 初始化。
    JoltRuntime& runtime_;
    /// broadphase_layers_ 在 PhysicsSystem 生命周期内保持有效。
    SingleBroadPhaseLayer broadphase_layers_;
    /// object_vs_broadphase_ 在 PhysicsSystem 生命周期内保持有效。
    AllowSingleBroadPhase object_vs_broadphase_;
    /// object_pairs_ 在 PhysicsSystem 生命周期内保持有效。
    AllowSingleObjectPair object_pairs_;
    /// temporary_allocator_ 预分配固定 bytes，Tick 内禁止增长。
    JPH::TempAllocatorImpl temporary_allocator_;
    /// physics_ 唯一拥有 Jolt BodyManager/query state。
    JPH::PhysicsSystem physics_;
    /// body_ids_ 保存析构时需要逆序释放的 Jolt handles，不越过 adapter。
    std::vector<JPH::BodyID> body_ids_;
    /// metadata_ 只按项目 ColliderID 查找，不参与 gameplay 排序。
    std::unordered_map<std::uint64_t, ColliderMetadata> metadata_;
};

JoltPhysicsError::JoltPhysicsError(
    const JoltPhysicsErrorCode code,
    const char* message)
    : std::runtime_error(message), code_(code) {}

JoltPhysicsErrorCode JoltPhysicsError::Code() const noexcept {
    return code_;
}

JoltPhysicsWorld::JoltPhysicsWorld(
    const JoltPhysicsConfig& config,
    const std::span<const JoltBoxCollider> colliders)
    : impl_(std::make_unique<Impl>(config, colliders)) {}

JoltPhysicsWorld::~JoltPhysicsWorld() = default;
JoltPhysicsWorld::JoltPhysicsWorld(JoltPhysicsWorld&&) noexcept = default;
JoltPhysicsWorld& JoltPhysicsWorld::operator=(JoltPhysicsWorld&&) noexcept = default;

PhysicsQueryResult JoltPhysicsWorld::Query(const PhysicsQuery& query) {
    if (!impl_) {
        throw JoltPhysicsError(
            JoltPhysicsErrorCode::Runtime,
            "Jolt PhysicsWorld was moved from");
    }
    return impl_->Query(query);
}

}  // namespace ihomeland::sim
