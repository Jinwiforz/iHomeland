#pragma once

#include "ihomeland/sim/simulation/command_ingress.hpp"
#include "ihomeland/sim/simulation/simulation_instance.hpp"

#include <array>
#include <cstddef>
#include <cstdint>
#include <mutex>
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

/// InputAcknowledgementProjection 是 replication 冻结点的不可变 actor 确认。
struct InputAcknowledgementProjection final {
    /// actor_id 是当前 BattleSession 绑定的稳定 ActorID。
    std::uint64_t actor_id;
    /// mapping_generation 防止 reconnect 后继承 predecessor 确认。
    std::uint64_t mapping_generation;
    /// last_processed_input_tick 是已连续稳定终结的最大 InputTick。
    std::uint64_t last_processed_input_tick;
};

/// InputAcknowledgementSnapshot 把 committed ServerTick 与 actor 确认冻结为一个副本。
struct InputAcknowledgementSnapshot final {
    /// server_tick 是产生 acknowledgement 的同一次 simulation commit。
    std::uint64_t server_tick;
    /// acknowledgement 是该 commit 中 exact actor/generation 的连续确认前沿。
    InputAcknowledgementProjection acknowledgement;
};

/// InputAcknowledgementStore 在线程边界发布 generation-scoped actor 确认。
///
/// simulation worker 是唯一 publisher；replication worker 只读取同一 commit 的不可变副本。
/// actor binding 在构造后不可变，禁止网络线程接触可变 InputTimeline。
class InputAcknowledgementStore final {
public:
    /// 构造函数冻结 mapping generation 与最多八个规范排序 ActorID。
    InputAcknowledgementStore(
        std::uint64_t mapping_generation,
        std::vector<std::uint64_t> actor_ids);

    /// Publish 原子发布一个 committed Tick 的完整 actor acknowledgement set。
    void Publish(
        std::uint64_t server_tick,
        std::span<
            const InputAcknowledgementProjection>
            projections);

    /// Freeze 返回 exact actor/generation 的 point-in-time commit snapshot。
    [[nodiscard]] std::optional<
        InputAcknowledgementSnapshot>
    Freeze(
        std::uint64_t actor_id,
        std::uint64_t mapping_generation) const;

private:
    /// MaximumActors 与已资格的单 instance actor capacity 一致。
    static constexpr std::size_t MaximumActors = 8;

    /// mapping_generation_ 是该 store 唯一允许的 InputTick epoch。
    std::uint64_t mapping_generation_;
    /// actor_ids_ 前 actor_count_ 项按升序保存不可变 binding。
    std::array<std::uint64_t, MaximumActors>
        actor_ids_{};
    /// frontiers_ 与 actor_ids_ 同 index，只在 snapshot_mutex_ 下读写。
    std::array<std::uint64_t, MaximumActors>
        frontiers_{};
    /// actor_count_ 是 actor_ids_ 的有效前缀长度。
    std::size_t actor_count_;
    /// published_server_tick_ 与 frontiers_ 共同组成唯一 replication commit。
    std::uint64_t published_server_tick_{0};
    /// snapshot_mutex_ 防止 reader 组合不同 Tick 的 server_tick 与 acknowledgement。
    mutable std::mutex snapshot_mutex_;
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

    /// FreezeAcknowledgements 复制当前全部 actor 的 immutable replication projection。
    [[nodiscard]] std::vector<
        InputAcknowledgementProjection>
    FreezeAcknowledgements() const;

    /// ReplaceMapping 只接受 successor generation，并把全部 actor 前沿重置为零。
    void ReplaceMapping(InputMappingConfig successor);

    /// Reset 清除 hold/gap 状态，禁止跨 assignment/mapping generation 继续确认。
    void Reset();

private:
    /// FirstInputTick 冻结每个 mapping generation 的首个可确认 InputTick。
    static constexpr std::uint64_t FirstInputTick = 1;

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
