#pragma once

#include "ihomeland/sim/simulation/command_ingress.hpp"
#include "ihomeland/sim/simulation/simulation_instance.hpp"

#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <string>
#include <vector>

namespace ihomeland::sim {

/// ActorInputResolution 是 InputIntent stage 对单个 actor 的确定输入投影。
struct ActorInputResolution final {
    /// actor_id 是实例绑定且规范排序的 ActorID。
    std::uint64_t actor_id;
    /// continuous_payload 是最后有效样本或 neutral token。
    std::string continuous_payload;
    /// held 表示当前 Tick 沿用了更早的连续样本。
    bool held;
    /// discrete_sequences 保存当前 Tick 合法离散边沿的规范顺序。
    std::vector<std::uint64_t> discrete_sequences;
    /// last_processed_input_tick 只越过 received 或已按 gap expiry 终结的连续区间。
    std::uint64_t last_processed_input_tick;
};

/// InputTimeline 解析 continuous hold、离散边沿与有界 gap confirmation。
///
/// 该类型只由 simulation worker 调用；所有 vectors 在构造时按 actor/capacity 预留，
/// pending identity 达到 hard limit 时抛出 Capacity，不进行无界增长。
class InputTimeline final {
public:
    /// 构造函数冻结 mapping、actor binding、hold/gap 与每 actor pending hard capacity。
    InputTimeline(
        InputMappingConfig mapping,
        std::vector<std::uint64_t> actor_ids,
        std::uint32_t continuous_hold_ticks,
        std::uint32_t gap_expiry_ticks,
        std::size_t pending_capacity_per_actor);

    /// Resolve 对一个 SimulationTick 的完整 batch 生成按 ActorID 排序的投影。
    [[nodiscard]] std::vector<ActorInputResolution> Resolve(
        std::uint64_t simulation_tick,
        std::span<const IngressCommand> commands);

    /// Reset 清除 hold/gap 状态，禁止跨 assignment/mapping generation 继续确认。
    void Reset();

private:
    /// ActorState 保存单个 actor 的 bounded timeline 状态。
    struct ActorState final {
        /// actor_id 是构造时冻结的非零 identity。
        std::uint64_t actor_id;
        /// last_continuous_payload 保存最近合法连续样本。
        std::optional<std::string> last_continuous_payload;
        /// last_continuous_tick 是该样本被应用的 SimulationTick。
        std::uint64_t last_continuous_tick{0};
        /// last_processed_input_tick 是已连续终结的最大 InputTick。
        std::uint64_t last_processed_input_tick{0};
        /// received_input_ticks 保存尚未越过 confirmation frontier 的唯一 identities。
        std::vector<std::uint64_t> received_input_ticks;
    };

    /// MapInputTick 使用与 ingress 相同的 checked integer mapping。
    [[nodiscard]] std::uint64_t MapInputTick(std::uint64_t input_tick) const;

    /// FindActor 返回排序 states_ 中的 actor；未绑定 actor 表示内部 invariant 失败。
    [[nodiscard]] ActorState& FindActor(std::uint64_t actor_id);

    /// mapping_ 是构造后不可变的 mapping epoch。
    InputMappingConfig mapping_;
    /// continuous_hold_ticks_ 冻结连续样本最大复用 Tick 数。
    std::uint32_t continuous_hold_ticks_;
    /// gap_expiry_ticks_ 冻结 missing InputTick 终结窗口。
    std::uint32_t gap_expiry_ticks_;
    /// pending_capacity_per_actor_ 限制 confirmation frontier 前的 identities。
    std::size_t pending_capacity_per_actor_;
    /// states_ 按 ActorID 排序并固定长度。
    std::vector<ActorState> states_;
};

}  // namespace ihomeland::sim
