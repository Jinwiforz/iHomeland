#pragma once

#include <cstdint>
#include <filesystem>
#include <stdexcept>
#include <string>

namespace ihomeland::sim {

/// BattleConfigErrorCode 为 fixture/config 边界提供稳定、可测试的失败分类。
enum class BattleConfigErrorCode : std::uint8_t {
    /// Io 表示 source corpus 无法安全读取。
    Io,
    /// Json 表示 JSON 语法或字段类型无效。
    Json,
    /// Schema 表示闭合字段、枚举、单位或范围违反 consumer contract。
    Schema,
    /// Digest 表示 manifest、binding、case 或 report 摘要漂移。
    Digest,
    /// Qualification 表示 profile 尚未达到 C++ runtime 的进入条件。
    Qualification,
};

/// BattleConfigError 携带稳定错误码，message 不包含 fixture 内容或敏感值。
class BattleConfigError final : public std::runtime_error {
public:
    /// 构造函数保存稳定 code 与低敏诊断。
    BattleConfigError(BattleConfigErrorCode code, const std::string& message);

    /// Code 返回机器可判定的失败类别。
    [[nodiscard]] BattleConfigErrorCode Code() const noexcept;

private:
    /// code_ 是构造后不可变的失败类别。
    BattleConfigErrorCode code_;
};

/// BattleRuntimeConfig 是冻结 profile 向 SimulationInstance 提供的强类型 checked 配置。
struct BattleRuntimeConfig final {
    /// simulation_tick_ns 是固定权威模拟步长，单位 nanoseconds。
    std::uint64_t simulation_tick_ns;
    /// input_tick_ns 是输入采样步长，单位 nanoseconds。
    std::uint64_t input_tick_ns;
    /// simulation_tick_ms 是便于调度和展示的精确整数毫秒。
    std::uint32_t simulation_tick_ms;
    /// input_tick_hz 是由 input_tick_ns 精确推导的采样频率。
    std::uint32_t input_tick_hz;
    /// history_window_ticks 是服务器历史环的固定 Tick 容量。
    std::uint32_t history_window_ticks;
    /// qualified_actor_cap 是已完成 profile 资格验证的 actor/player 上限。
    std::uint32_t qualified_actor_cap;
    /// queue_capacity_items 是实例 inbox 的 hard capacity。
    std::uint32_t queue_capacity_items;
    /// cpu_per_tick_target_us 是 reference environment 的单 Tick CPU target。
    std::uint32_t cpu_per_tick_target_us;
    /// memory_per_instance_target_bytes 是实例内存 target。
    std::uint64_t memory_per_instance_target_bytes;
    /// history_memory_target_bytes 是 history hard accounting target。
    std::uint64_t history_memory_target_bytes;
    /// continuous_hold_ticks 是连续输入缺帧时允许保持最后样本的 Tick 数。
    std::uint32_t continuous_hold_ticks;
    /// input_early_window_ticks 是 mapping 接受的最远提前窗口。
    std::uint32_t input_early_window_ticks;
    /// input_late_window_ticks 是 mapping 接受的最远迟到窗口。
    std::uint32_t input_late_window_ticks;
    /// input_gap_expiry_ticks 是未决 InputTick gap 的终结窗口。
    std::uint32_t input_gap_expiry_ticks;
    /// model_manifest_sha256 绑定被 profile 消费的 model manifest。
    std::string model_manifest_sha256;
    /// profile_manifest_sha256 绑定 runtime 实际读取的 profile manifest。
    std::string profile_manifest_sha256;
    /// qualification_report_sha256 绑定 profile qualification report。
    std::string qualification_report_sha256;
};

/// LoadBattleRuntimeConfig 校验 manifest/binding/report 后返回无隐藏默认值的 checked 配置。
[[nodiscard]] BattleRuntimeConfig LoadBattleRuntimeConfig(
    const std::filesystem::path& profile_root,
    const std::filesystem::path& model_root);

}  // namespace ihomeland::sim
