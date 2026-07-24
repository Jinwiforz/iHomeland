#include "ihomeland/sim/navigation/navigation_world.hpp"

#include "ihomeland/sim/core/sha256.hpp"

#include <array>
#include <charconv>
#include <stdexcept>
#include <string>

namespace ihomeland::sim {
namespace {

/// AppendInteger 以 locale-independent decimal 写 path identity source。
template <typename Integer>
void AppendInteger(std::string& output, const Integer value) {
    std::array<char, 32> buffer{};
    const auto result =
        std::to_chars(buffer.data(), buffer.data() + buffer.size(), value);
    if (result.ec != std::errc{}) {
        throw std::runtime_error(
            "navigation path identity integer encoding failed");
    }
    output.append(buffer.data(), result.ptr);
}

}  // namespace

std::string CanonicalNavigationPathIdentity(
    const NavigationAssetIdentity& asset,
    const NavigationQuery& query,
    const std::vector<NavigationPointMm>& points) {
    std::string source = asset.sha256;
    source.push_back('|');
    AppendInteger(source, asset.map_id);
    source.push_back('|');
    AppendInteger(source, asset.version);
    source.push_back('|');
    AppendInteger(source, asset.coordinate_scale_mm);
    source.push_back('|');
    AppendInteger(source, query.query_id);
    source.push_back('|');
    AppendInteger(source, query.tick);
    source.push_back('|');
    AppendInteger(source, static_cast<std::uint8_t>(query.kind));
    source.push_back('|');
    AppendInteger(source, query.actor_id);
    source.push_back('|');
    AppendInteger(source, query.start_mm.x);
    source.push_back(',');
    AppendInteger(source, query.start_mm.y);
    source.push_back(',');
    AppendInteger(source, query.start_mm.z);
    source.push_back('|');
    AppendInteger(source, query.end_mm.x);
    source.push_back(',');
    AppendInteger(source, query.end_mm.y);
    source.push_back(',');
    AppendInteger(source, query.end_mm.z);
    for (const auto& point : points) {
        source.push_back('|');
        AppendInteger(source, point.x);
        source.push_back(',');
        AppendInteger(source, point.y);
        source.push_back(',');
        AppendInteger(source, point.z);
    }
    return Sha256Text(source);
}

}  // namespace ihomeland::sim
