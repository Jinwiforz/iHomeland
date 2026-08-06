#pragma once

#include <cstddef>
#include <cstdint>
#include <mutex>
#include <string>
#include <variant>
#include <vector>

namespace ihomeland::sim {

class SimulationInstance;

/// GameplayCommandKind 的数值顺序属于 canonical command tuple。
enum class GameplayCommandKind : std::uint8_t {
    /// ContinuousIntentSample 提供归一化移动 intent。
    ContinuousIntentSample = 0,
    /// JumpPressed 是不允许推测或 hold 的离散边沿。
    JumpPressed = 1,
    /// SwitchWeapon 请求切换到登记 weapon。
    SwitchWeapon = 2,
    /// ActivateAbility 请求激活已 grant ability。
    ActivateAbility = 3,
    /// LifecycleDirective 只允许受信离线 harness/control owner 提交。
    LifecycleDirective = 4,
    /// AimIntent 请求更新量化瞄准方向，不携带最终Transform。
    AimIntent = 5,
    /// InteractSlot 请求使用受限交互槽，不携带entity identity。
    InteractSlot = 6,
};

/// ContinuousIntentPayload 使用整数千分比轴值，不携带最终 Transform。
struct ContinuousIntentPayload final {
    /// move_x_permille 是横向 intent，范围 [-1000, 1000]。
    std::int16_t move_x_permille;
    /// move_y_permille 是纵向 intent，范围 [-1000, 1000]。
    std::int16_t move_y_permille;
};

/// JumpPressedPayload 不携带 grounded、velocity 或最终位置。
struct JumpPressedPayload final {};

/// SwitchWeaponPayload 是切换当前武器的无字段离散 edge。
/// 目标 weapon 只能由当前权威装备与 production grants 决定。
struct SwitchWeaponPayload final {};

/// ActivateAbilityPayload 只携带请求的冻结 Ability ID。
struct ActivateAbilityPayload final {
    /// ability_id 必须为非零且最终 grant/cost/cooldown 由 server 决议。
    std::uint32_t ability_id;
};

/// AimIntentPayload 只携带量化yaw/pitch请求。
struct AimIntentPayload final {
    /// yaw_millidegrees 是[-180000, 180000]范围内请求值。
    std::int32_t yaw_millidegrees;
    /// pitch_millidegrees 是[-90000, 90000]范围内请求值。
    std::int32_t pitch_millidegrees;
};

/// InteractSlotPayload 只选择1..16受限槽位。
struct InteractSlotPayload final {
    /// interaction_slot 由simulation映射合法目标，不是entity identity。
    std::uint8_t interaction_slot;
};

/// LifecycleDirectivePayload 只允许受信 source 表达登记 directive token。
struct LifecycleDirectivePayload final {
    /// directive 是闭合 lifecycle token，零值无效。
    std::uint8_t directive;
    /// trusted_source 由 harness/control composition 设置，不能来自客户端 payload。
    bool trusted_source;
};

/// GameplayCommandPayload 是 intent-only 的闭合 payload 集。
using GameplayCommandPayload = std::variant<
    ContinuousIntentPayload,
    JumpPressedPayload,
    SwitchWeaponPayload,
    ActivateAbilityPayload,
    LifecycleDirectivePayload,
    AimIntentPayload,
    InteractSlotPayload>;

/// GameplayCommand 绑定 assignment/mapping/actor/Tick/sequence 与 intent payload。
struct GameplayCommand final {
    /// assignment_fingerprint 必须等于实例完整 AssignmentStamp fingerprint。
    std::string assignment_fingerprint;
    /// mapping_generation 必须等于当前 mapping epoch。
    std::uint64_t mapping_generation;
    /// actor_id 必须来自实例启动时冻结的 actor binding。
    std::uint64_t actor_id;
    /// input_tick 是 session generation 内从 1 开始的采样索引。
    std::uint64_t input_tick;
    /// sequence 是 actor/session 内非零稳定 command sequence。
    std::uint64_t sequence;
    /// expires_at_tick 是 producer 声明的最后可接受 SimulationTick。
    std::uint64_t expires_at_tick;
    /// kind 参与 canonical tuple 并必须与 payload variant 一致。
    GameplayCommandKind kind;
    /// payload 只表达 intent，不包含 hit/damage/death/reward/identity override。
    GameplayCommandPayload payload;
};

/// InputMappingConfig 固定 InputTick 到 SimulationTick 的 checked integer mapping。
struct InputMappingConfig final {
    /// generation 是当前 mapping epoch identity。
    std::uint64_t generation;
    /// base_input_tick 是 epoch anchor 的 InputTick。
    std::uint64_t base_input_tick;
    /// base_simulation_tick 是同一 anchor 的 SimulationTick。
    std::uint64_t base_simulation_tick;
    /// input_step_ns 是 40 Hz 对应的 25,000,000 ns。
    std::uint64_t input_step_ns;
    /// simulation_step_ns 是 20 Hz 对应的 50,000,000 ns。
    std::uint64_t simulation_step_ns;
    /// early_window_ticks 是相对 committed Tick 的最大提前窗口。
    std::uint32_t early_window_ticks;
    /// late_window_ticks 是相对 mapped Tick 的最大迟到窗口。
    std::uint32_t late_window_ticks;
};

/// CommandRejection 是输入边界对外输出的稳定拒绝原因。
enum class CommandRejection : std::uint8_t {
    /// None 表示 command 已通过边界并进入 inbox。
    None,
    /// StaleAssignment 表示 assignment fingerprint 不匹配。
    StaleAssignment,
    /// StaleMapping 表示 mapping generation 不匹配。
    StaleMapping,
    /// ActorBinding 表示 actor 不属于实例。
    ActorBinding,
    /// InvalidTick 表示 InputTick/expiry 为零或早于 epoch anchor。
    InvalidTick,
    /// InvalidSequence 表示 sequence 为零。
    InvalidSequence,
    /// TooEarly 表示 mapped Tick 超出提前窗口。
    TooEarly,
    /// Expired 表示 mapped Tick 或显式 expiry 已过期。
    Expired,
    /// Duplicate 表示 actor/input/sequence identity 已终结。
    Duplicate,
    /// UnsafePayload 表示 kind/payload/range 或 trusted directive 无效。
    UnsafePayload,
    /// ArithmeticOverflow 表示 mapping 的 checked arithmetic 无法表达。
    ArithmeticOverflow,
    /// Capacity 表示 dedupe 或实例 inbox hard limit 已耗尽。
    Capacity,
    /// InstanceClosed 表示实例不处于 Running。
    InstanceClosed,
};

/// CommandSubmitResult 返回 accepted/rejected 与唯一 mapped target Tick。
struct CommandSubmitResult final {
    /// accepted 表示 command 已进入实例 inbox。
    bool accepted;
    /// rejection 在 accepted=false 时提供稳定原因。
    CommandRejection rejection;
    /// target_tick 是 checked mapping 结果；mapping 前失败时为零。
    std::uint64_t target_tick;
};

/// CommandIngress 是 GameplayCommand 进入 SimulationInstance inbox 的唯一验证 owner。
///
/// Submit 可被多个 producer 并发调用；mutex 只保护有界 dedupe set，实例状态和 queue 由
/// SimulationInstance 自己线性化。该类型不暴露 ECS、adapter 或 history。
class CommandIngress final {
public:
    /// 构造函数冻结 assignment、mapping、actor binding 与 dedupe hard capacity。
    CommandIngress(
        SimulationInstance& instance,
        InputMappingConfig mapping,
        std::vector<std::uint64_t> actor_ids,
        std::size_t dedupe_capacity,
        std::string assignment_fingerprint = {});

    /// Submit 在进入 inbox 前完成全部 binding/window/sequence/payload 检查。
    [[nodiscard]] CommandSubmitResult Submit(GameplayCommand command);

private:
    /// DedupeEntry 绑定 actor/command sequence，并保存淘汰所需 target Tick。
    struct DedupeEntry final {
        /// actor_id 是实例内稳定 ActorID。
        std::uint64_t actor_id;
        /// sequence 是同一 actor/session command identity。
        std::uint64_t sequence;
        /// target_tick 用于晚到窗口结束后有界淘汰。
        std::uint64_t target_tick;
    };

    /// ValidatePayload 验证 kind/variant 对应、整数范围和 trusted lifecycle source。
    [[nodiscard]] static bool ValidatePayload(const GameplayCommand& command);

    /// CanonicalPayload 生成不包含 assignment fingerprint 的稳定 intent token。
    [[nodiscard]] static std::string CanonicalPayload(const GameplayCommand& command);

    /// instance_ 是被验证 command 的唯一 inbox owner，必须长于 ingress。
    SimulationInstance* instance_;
    /// assignment_fingerprint_ 冻结构造方唯一允许的外部 placement stamp。
    std::string assignment_fingerprint_;
    /// mapping_ 是构造后不可变的 mapping epoch。
    InputMappingConfig mapping_;
    /// actor_ids_ 保持排序且无重复，用于确定查找。
    std::vector<std::uint64_t> actor_ids_;
    /// dedupe_capacity_ 是 entries_ 的 hard limit。
    std::size_t dedupe_capacity_;
    /// mutex_ 线性化 concurrent duplicate check/prune/insert。
    std::mutex mutex_;
    /// entries_ 预留到 hard capacity 后不扩容。
    std::vector<DedupeEntry> entries_;
};

}  // namespace ihomeland::sim
