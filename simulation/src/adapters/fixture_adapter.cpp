#include "ihomeland/sim/core/adapter_smoke.hpp"

#include <nlohmann/json.hpp>

#include <stdexcept>
#include <string>

namespace ihomeland::sim {

FixtureSmokeResult RunFixtureSmoke(const std::string_view json_text) {
    const auto document = nlohmann::json::parse(json_text);
    if (!document.is_object() || document.size() != 1 || !document.contains("simulation_tick_ms") ||
        !document.at("simulation_tick_ms").is_number_unsigned()) {
        throw std::invalid_argument("fixture smoke schema is invalid");
    }
    return FixtureSmokeResult{
        .simulation_tick_ms = document.at("simulation_tick_ms").get<std::uint32_t>(),
    };
}

}  // namespace ihomeland::sim
