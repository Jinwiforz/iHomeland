#pragma once

#include "ihomeland/sim/ecs/simulation_world.hpp"

#include <cstddef>
#include <cstdint>
#include <optional>
#include <variant>
#include <vector>

namespace ihomeland::sim {

/// StructuralCommandKind 的数值顺序属于 canonical barrier tuple，不得重排。
enum class StructuralCommandKind : std::uint8_t {
    /// Create 分配新 EntityID，并在结果中返回 handle。
    Create = 0,
    /// Destroy 先于同 Tick add/remove，使后续冲突稳定得到 stale rejection。
    Destroy = 1,
    /// Add 把一个 typed component 加到存活 entity。
    Add = 2,
    /// Remove 删除一个已存在的 typed component。
    Remove = 3,
};

/// ComponentKind 是 remove command 使用的闭合 component type token。
enum class ComponentKind : std::uint8_t {
    /// Transform 对应 TransformComponent。
    Transform,
    /// Motion 对应 MotionComponent。
    Motion,
    /// Actor 对应 ActorComponent。
    Actor,
    /// Attribute 对应 AttributeComponent。
    Attribute,
    /// Ability 对应 AbilityComponent。
    Ability,
    /// Effect 对应 EffectComponent。
    Effect,
    /// Projectile 对应 ProjectileComponent。
    Projectile,
    /// Ai 对应 AiComponent。
    Ai,
};

/// ComponentPayload 是 structural Add 支持的闭合 typed component 集。
using ComponentPayload = std::variant<
    TransformComponent,
    MotionComponent,
    ActorComponent,
    AttributeComponent,
    AbilityComponent,
    EffectComponent,
    ProjectileComponent,
    AiComponent>;

/// StructuralCommand 是进入唯一 barrier 的规范结构 mutation。
struct StructuralCommand final {
    /// target_tick 绑定允许提交命令的唯一 SimulationTick。
    std::uint64_t target_tick;
    /// kind 参与 canonical tuple 排序。
    StructuralCommandKind kind;
    /// source_entity 标识发起 system/entity；system command 可使用无效零值。
    EntityID source_entity;
    /// stable_sequence 在 source 范围内提供确定 tie-break。
    std::uint64_t stable_sequence;
    /// target_entity 是 destroy/add/remove 的目标；create 使用零值。
    EntityID target_entity;
    /// payload 只允许 Add 携带，其他 command 必须为空。
    std::optional<ComponentPayload> payload;
    /// remove_kind 只允许 Remove 携带，其他 command 必须为空。
    std::optional<ComponentKind> remove_kind;
};

/// StructuralCommandResult 为每条输入命令产生 accepted/rejected 稳定结果。
struct StructuralCommandResult final {
    /// stable_sequence 关联原 command，不包含 pointer 或容器 index。
    std::uint64_t stable_sequence;
    /// accepted 表示 mutation 已完整提交。
    bool accepted;
    /// rejection 在 accepted=false 时提供稳定 EcsErrorCode。
    std::optional<EcsErrorCode> rejection;
    /// created_entity 只在 Create accepted 时返回。
    std::optional<EntityID> created_entity;
};

/// StructuralCapacitySnapshot 是 buffer hard limit 的规范 accounting token。
struct StructuralCapacitySnapshot final {
    /// limit 是构造时固定容量。
    std::uint32_t limit;
    /// queued 是当前尚未提交的命令数。
    std::uint32_t queued;
    /// accepted_total 是生命周期内成功入队数。
    std::uint64_t accepted_total;
    /// rejected_capacity_total 是因 hard limit 拒绝的命令数。
    std::uint64_t rejected_capacity_total;
};

/// StructuralCommandBuffer 有界收集 typed commands，并在唯一 barrier 规范排序后提交。
class StructuralCommandBuffer final {
public:
    /// 构造函数预留 capacity；零容量在实例启动前失败。
    explicit StructuralCommandBuffer(std::uint32_t capacity);

    /// Enqueue 验证 command shape 与 tuple 唯一性；容量失败不修改已排队命令。
    void Enqueue(StructuralCommand command);

    /// Commit 提交 target_tick 命令、拒绝过期命令并保留 future commands。
    ///
    /// 每条 mutation 要么完整成功，要么只产生 rejection；返回顺序与 canonical tuple 一致。
    [[nodiscard]] std::vector<StructuralCommandResult> Commit(
        std::uint64_t target_tick,
        SimulationWorld& world);

    /// Reset 清空排队命令和 accounting，供 SimulationInstance reset 使用。
    void Reset() noexcept;

    /// CapacitySnapshot 返回不暴露 payload 的只读 accounting。
    [[nodiscard]] StructuralCapacitySnapshot CapacitySnapshot() const noexcept;

private:
    /// ValidateShape 拒绝 payload/remove token 与 kind 不一致的 command。
    static void ValidateShape(const StructuralCommand& command);

    /// commands_ 预留到 hard capacity 后不允许扩容。
    std::vector<StructuralCommand> commands_;
    /// capacity_ 是构造后不可变的 hard limit。
    std::uint32_t capacity_;
    /// accepted_total_ 记录成功入队数。
    std::uint64_t accepted_total_{0};
    /// rejected_capacity_total_ 记录 hard limit 拒绝数。
    std::uint64_t rejected_capacity_total_{0};
};

}  // namespace ihomeland::sim
