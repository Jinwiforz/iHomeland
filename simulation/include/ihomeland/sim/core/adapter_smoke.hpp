#pragma once

#include <cstdint>
#include <string_view>

namespace ihomeland::sim {

/// FixtureSmokeResult 是 JSON adapter 向 smoke harness 返回的项目值。
struct FixtureSmokeResult final {
    /// simulation_tick_ms 是 fixture 中经过类型校验的固定步长。
    std::uint32_t simulation_tick_ms;
};

/// RunFixtureSmoke 解析闭合 JSON schema，并拒绝隐式类型转换。
[[nodiscard]] FixtureSmokeResult RunFixtureSmoke(std::string_view json_text);

/// RunJoltSmoke 调用真实 Jolt 数学实现并验证固定向量长度。
[[nodiscard]] bool RunJoltSmoke();

/// RunDetourSmoke 分配并释放真实 Detour nav mesh owner。
[[nodiscard]] bool RunDetourSmoke();

}  // namespace ihomeland::sim
