#include "ihomeland/sim/config/personal_world_arena.hpp"

#include "ihomeland/sim/core/sha256.hpp"
#include "ihomeland/sim/navigation/detour_navigation_world.hpp"
#include "ihomeland/sim/physics/jolt_physics_world.hpp"

#include <DetourAlloc.h>
#include <DetourNavMeshBuilder.h>
#include <nlohmann/json.hpp>

#include <algorithm>
#include <array>
#include <cstring>
#include <fstream>
#include <iterator>
#include <limits>
#include <map>
#include <set>
#include <stdexcept>
#include <string_view>
#include <tuple>
#include <utility>

namespace ihomeland::sim {
namespace {

using Json = nlohmann::json;
constexpr std::uintmax_t MaximumArenaDocumentBytes = 1U << 20U;
constexpr std::uint64_t FloorColliderId = 1000;
constexpr std::uint64_t StaticSubshapeId = 1;
constexpr std::int64_t NavigationCellMillimeters = 500;

/// ArenaImplementation 保存构造 instance adapters 所需的不可变项目值。
struct ArenaImplementation final {
    /// colliders 保存 floor 与 static blocker 的 Jolt 输入。
    std::vector<JoltBoxCollider> colliders;
    /// physics_config 保存已验证的 Jolt world 边界与容量。
    JoltPhysicsConfig physics_config;
    /// navigation_data 保存编译后的单 tile Detour bytes。
    std::vector<std::byte> navigation_data;
    /// navigation_asset 保存 Detour bytes 的内容 identity。
    NavigationAssetIdentity navigation_asset;
    /// navigation_config 保存已验证的 Detour world 边界与容量。
    DetourNavigationConfig navigation_config;
};

/// RequireKeys 对 arena source 的每层对象执行 closed-field 校验。
void RequireKeys(
    const Json& value,
    const std::set<std::string, std::less<>>& expected,
    const char* context) {
    if (!value.is_object() || value.size() != expected.size()) {
        throw std::runtime_error(std::string("arena ") + context + " fields are invalid");
    }
    for (const auto& [key, ignored] : value.items()) {
        static_cast<void>(ignored);
        if (!expected.contains(key)) {
            throw std::runtime_error(std::string("arena ") + context + " contains an unknown field");
        }
    }
}

/// ReadSource 读取有界 regular file，并把 filesystem 失败收敛为低敏错误。
[[nodiscard]] std::string ReadSource(const std::filesystem::path& path) {
    std::error_code error;
    const auto status = std::filesystem::symlink_status(path, error);
    const auto size = std::filesystem::file_size(path, error);
    if (error || !std::filesystem::is_regular_file(status) || size == 0 ||
        size > MaximumArenaDocumentBytes) {
        throw std::runtime_error("arena source document is invalid");
    }
    std::ifstream stream(path, std::ios::binary);
    if (!stream) {
        throw std::runtime_error("arena source document cannot be read");
    }
    std::string source{
        std::istreambuf_iterator<char>{stream},
        std::istreambuf_iterator<char>{}};
    if (stream.bad()) {
        throw std::runtime_error("arena source document cannot be read");
    }
    return source;
}

/// ParseSource 使用 strict UTF-8 JSON parser，不在异常中保留 source。
[[nodiscard]] Json ParseSource(const std::string& source) {
    try {
        return Json::parse(source, nullptr, true, true);
    } catch (const Json::exception&) {
        throw std::runtime_error("arena JSON is invalid");
    }
}

/// String 返回必需且非空的 string 字段。
[[nodiscard]] const std::string& String(const Json& value, const char* field) {
    if (!value.contains(field) || !value.at(field).is_string() ||
        value.at(field).get_ref<const std::string&>().empty()) {
        throw std::runtime_error("arena string field is invalid");
    }
    return value.at(field).get_ref<const std::string&>();
}

/// Integer 返回能够精确表示为 int64 的 JSON integer。
[[nodiscard]] std::int64_t Integer(const Json& value, const char* field) {
    if (!value.contains(field) || !value.at(field).is_number_integer()) {
        throw std::runtime_error("arena integer field is invalid");
    }
    try {
        return value.at(field).get<std::int64_t>();
    } catch (const Json::exception&) {
        throw std::runtime_error("arena integer field is outside int64");
    }
}

/// Vector 解析 exact 三轴整数 array。
[[nodiscard]] Vector3Mm Vector(const Json& value) {
    if (!value.is_array() || value.size() != 3 ||
        !std::ranges::all_of(value, [](const Json& item) {
            return item.is_number_integer();
        })) {
        throw std::runtime_error("arena vector is invalid");
    }
    try {
        return {
            .x = value.at(0).get<std::int64_t>(),
            .y = value.at(1).get<std::int64_t>(),
            .z = value.at(2).get<std::int64_t>()};
    } catch (const Json::exception&) {
        throw std::runtime_error("arena vector is outside int64");
    }
}

/// RequireDigest 拒绝 source 或 expected identity 漂移。
void RequireDigest(const std::string& source, const std::string& expected) {
    if (expected.size() != 64 || Sha256Text(source) != expected) {
        throw std::runtime_error("arena source identity differs");
    }
}

/// CheckedUnsigned 把正数 int64 收窄到指定 unsigned 类型。
template <typename T>
[[nodiscard]] T CheckedUnsigned(const std::int64_t value, const char* message) {
    if (value <= 0 || static_cast<std::uint64_t>(value) >
                          static_cast<std::uint64_t>(std::numeric_limits<T>::max())) {
        throw std::runtime_error(message);
    }
    return static_cast<T>(value);
}

/// BuildNavigationData 把已验证 versioned polygon source 编译为单 tile Detour bytes。
[[nodiscard]] std::vector<std::byte> BuildNavigationData(
    const Json& navigation,
    const Vector3Mm minimum,
    const Vector3Mm maximum) {
    const auto& polygons_json = navigation.at("walkable_polygons");
    if (!polygons_json.is_array() || polygons_json.empty() || polygons_json.size() > 64) {
        throw std::runtime_error("arena navigation polygon set is invalid");
    }
    using VertexKey = std::tuple<std::int64_t, std::int64_t, std::int64_t>;
    /// SourcePolygon 保存 source polygon 的闭合轴对齐边界。
    struct SourcePolygon final {
        /// id 是 source 中唯一且非零的 polygon identity。
        std::uint64_t id;
        /// minimum_x 是 polygon 的最小 X 坐标。
        std::int64_t minimum_x;
        /// maximum_x 是 polygon 的最大 X 坐标。
        std::int64_t maximum_x;
        /// minimum_z 是 polygon 的最小 Z 坐标。
        std::int64_t minimum_z;
        /// maximum_z 是 polygon 的最大 Z 坐标。
        std::int64_t maximum_z;
    };
    std::map<VertexKey, unsigned short> vertex_indices;
    std::vector<unsigned short> vertices;
    std::vector<std::array<unsigned short, 4>> polygon_vertices;
    std::vector<std::uint64_t> polygon_ids;
    std::vector<SourcePolygon> source_polygons;
    std::set<std::int64_t> x_boundaries;
    std::set<std::int64_t> z_boundaries;
    for (const auto& polygon : polygons_json) {
        RequireKeys(polygon, {"polygon_id", "vertices_mm"}, "navigation polygon");
        const auto polygon_id = CheckedUnsigned<std::uint64_t>(
            Integer(polygon, "polygon_id"), "arena navigation polygon identity is invalid");
        if (std::ranges::find(polygon_ids, polygon_id) != polygon_ids.end() ||
            !polygon.at("vertices_mm").is_array() ||
            polygon.at("vertices_mm").size() != 4) {
            throw std::runtime_error("arena navigation polygon is invalid or duplicated");
        }
        polygon_ids.push_back(polygon_id);
        std::vector<Vector3Mm> points;
        points.reserve(4);
        for (std::size_t index = 0; index < 4; ++index) {
            const auto point = Vector(polygon.at("vertices_mm").at(index));
            if (point.x < minimum.x || point.x > maximum.x || point.y < minimum.y ||
                point.y > maximum.y || point.z < minimum.z || point.z > maximum.z ||
                (point.x - minimum.x) % NavigationCellMillimeters != 0 ||
                (point.y - minimum.y) % NavigationCellMillimeters != 0 ||
                (point.z - minimum.z) % NavigationCellMillimeters != 0) {
                throw std::runtime_error("arena navigation vertex is outside quantized bounds");
            }
            points.push_back(point);
            x_boundaries.insert(point.x);
            z_boundaries.insert(point.z);
        }
        const auto [minimum_x, maximum_x] = std::ranges::minmax_element(
            points, {}, &Vector3Mm::x);
        const auto [minimum_z, maximum_z] = std::ranges::minmax_element(
            points, {}, &Vector3Mm::z);
        std::set<std::pair<std::int64_t, std::int64_t>> rectangle_points;
        for (const auto& point : points) {
            rectangle_points.emplace(point.x, point.z);
        }
        if (std::ranges::any_of(points, [](const Vector3Mm& point) { return point.y != 0; }) ||
            rectangle_points != std::set<std::pair<std::int64_t, std::int64_t>>{
                {minimum_x->x, minimum_z->z}, {maximum_x->x, minimum_z->z},
                {maximum_x->x, maximum_z->z}, {minimum_x->x, maximum_z->z}}) {
            throw std::runtime_error("arena navigation polygon must be an axis-aligned rectangle");
        }
        source_polygons.push_back({
            .id = polygon_id,
            .minimum_x = minimum_x->x,
            .maximum_x = maximum_x->x,
            .minimum_z = minimum_z->z,
            .maximum_z = maximum_z->z});
    }

    const auto append_vertex = [&](const Vector3Mm point) {
        const VertexKey key{point.x, point.y, point.z};
        const auto [iterator, inserted] = vertex_indices.emplace(
            key, static_cast<unsigned short>(vertex_indices.size()));
        if (inserted) {
            const auto append_axis = [&](const std::int64_t coordinate, const std::int64_t origin) {
                const auto quantized = (coordinate - origin) / NavigationCellMillimeters;
                if (quantized < 0 || quantized > std::numeric_limits<unsigned short>::max()) {
                    throw std::runtime_error("arena navigation vertex quantization overflow");
                }
                vertices.push_back(static_cast<unsigned short>(quantized));
            };
            append_axis(point.x, minimum.x);
            append_axis(point.y, minimum.y);
            append_axis(point.z, minimum.z);
        }
        return iterator->second;
    };
    const std::vector<std::int64_t> x_values(x_boundaries.begin(), x_boundaries.end());
    const std::vector<std::int64_t> z_values(z_boundaries.begin(), z_boundaries.end());
    for (std::size_t z = 1; z < z_values.size(); ++z) {
        for (std::size_t x = 1; x < x_values.size(); ++x) {
            const auto min_x = x_values[x - 1];
            const auto max_x = x_values[x];
            const auto min_z = z_values[z - 1];
            const auto max_z = z_values[z];
            const auto center_x = min_x + (max_x - min_x) / 2;
            const auto center_z = min_z + (max_z - min_z) / 2;
            if (std::ranges::none_of(source_polygons, [&](const SourcePolygon& source) {
                    return center_x > source.minimum_x && center_x < source.maximum_x &&
                           center_z > source.minimum_z && center_z < source.maximum_z;
                })) {
                continue;
            }
            polygon_vertices.push_back({
                append_vertex({.x = min_x, .y = 0, .z = min_z}),
                append_vertex({.x = max_x, .y = 0, .z = min_z}),
                append_vertex({.x = max_x, .y = 0, .z = max_z}),
                append_vertex({.x = min_x, .y = 0, .z = max_z})});
        }
    }
    if (polygon_vertices.empty() || polygon_vertices.size() > 64) {
        throw std::runtime_error("arena navigation tessellation exceeds capacity");
    }

    std::set<std::pair<std::uint64_t, std::uint64_t>> declared_links;
    if (!navigation.at("links").is_array()) {
        throw std::runtime_error("arena navigation links are invalid");
    }
    for (const auto& link : navigation.at("links")) {
        RequireKeys(link, {"from_polygon_id", "to_polygon_id"}, "navigation link");
        auto first = CheckedUnsigned<std::uint64_t>(
            Integer(link, "from_polygon_id"), "arena navigation link identity is invalid");
        auto second = CheckedUnsigned<std::uint64_t>(
            Integer(link, "to_polygon_id"), "arena navigation link identity is invalid");
        if (first == second) {
            throw std::runtime_error("arena navigation self link is invalid");
        }
        if (second < first) {
            std::swap(first, second);
        }
        if (!declared_links.emplace(first, second).second) {
            throw std::runtime_error("arena navigation link is duplicated");
        }
        const auto find_source = [&](const std::uint64_t id) {
            return std::ranges::find(source_polygons, id, &SourcePolygon::id);
        };
        const auto left = find_source(first);
        const auto right = find_source(second);
        if (left == source_polygons.end() || right == source_polygons.end()) {
            throw std::runtime_error("arena navigation link target is missing");
        }
        const auto overlaps = [](const std::int64_t left_min, const std::int64_t left_max,
                                 const std::int64_t right_min, const std::int64_t right_max) {
            return std::max(left_min, right_min) < std::min(left_max, right_max);
        };
        const auto touches =
            ((left->maximum_x == right->minimum_x || right->maximum_x == left->minimum_x) &&
             overlaps(left->minimum_z, left->maximum_z, right->minimum_z, right->maximum_z)) ||
            ((left->maximum_z == right->minimum_z || right->maximum_z == left->minimum_z) &&
             overlaps(left->minimum_x, left->maximum_x, right->minimum_x, right->maximum_x));
        if (!touches) {
            throw std::runtime_error("arena navigation link geometry differs");
        }
    }

    std::vector<unsigned short> polygons(polygon_vertices.size() * 8, 0x800f);
    for (std::size_t polygon_index = 0; polygon_index < polygon_vertices.size(); ++polygon_index) {
        for (std::size_t edge = 0; edge < 4; ++edge) {
            polygons[polygon_index * 8 + edge] = polygon_vertices[polygon_index][edge];
            const auto first = polygon_vertices[polygon_index][edge];
            const auto second = polygon_vertices[polygon_index][(edge + 1) % 4];
            for (std::size_t other = 0; other < polygon_vertices.size(); ++other) {
                if (other == polygon_index) {
                    continue;
                }
                for (std::size_t other_edge = 0; other_edge < 4; ++other_edge) {
                    const auto other_first = polygon_vertices[other][other_edge];
                    const auto other_second = polygon_vertices[other][(other_edge + 1) % 4];
                    if ((first == other_second && second == other_first) ||
                        (first == other_first && second == other_second)) {
                        polygons[polygon_index * 8 + 4 + edge] =
                            static_cast<unsigned short>(other + 1);
                    }
                }
            }
        }
    }

    std::vector<unsigned short> flags(polygon_vertices.size(), 1);
    std::vector<unsigned char> areas(polygon_vertices.size(), 0);
    dtNavMeshCreateParams params{};
    params.verts = vertices.data();
    params.vertCount = static_cast<int>(vertices.size() / 3);
    params.polys = polygons.data();
    params.polyFlags = flags.data();
    params.polyAreas = areas.data();
    params.polyCount = static_cast<int>(polygon_vertices.size());
    params.nvp = 4;
    params.bmin[0] = static_cast<float>(minimum.x) / 1000.0F;
    params.bmin[1] = static_cast<float>(minimum.y) / 1000.0F;
    params.bmin[2] = static_cast<float>(minimum.z) / 1000.0F;
    params.bmax[0] = static_cast<float>(maximum.x) / 1000.0F;
    params.bmax[1] = static_cast<float>(maximum.y) / 1000.0F;
    params.bmax[2] = static_cast<float>(maximum.z) / 1000.0F;
    params.walkableHeight = 3.2F;
    params.walkableRadius = 0.9F;
    params.walkableClimb = 0.4F;
    params.cs = static_cast<float>(NavigationCellMillimeters) / 1000.0F;
    params.ch = static_cast<float>(NavigationCellMillimeters) / 1000.0F;
    params.buildBvTree = true;
    unsigned char* raw = nullptr;
    int size = 0;
    if (!dtCreateNavMeshData(&params, &raw, &size) || raw == nullptr || size <= 0) {
        throw std::runtime_error("arena navigation asset build failed");
    }
    std::vector<std::byte> output(static_cast<std::size_t>(size));
    std::memcpy(output.data(), raw, output.size());
    dtFree(raw);
    return output;
}

}  // namespace

PersonalWorldArenaCatalog LoadPersonalWorldArenaCatalog(
    const std::filesystem::path& root,
    const std::string& expected_map_id,
    const std::string& expected_map_content_identity,
    const std::string& expected_navigation_identity,
    const std::string& expected_physics_identity) {
    try {
        if (!root.is_absolute()) {
            throw std::runtime_error("arena selection input is invalid");
        }
        std::error_code error;
        std::set<std::string, std::less<>> entries;
        for (const auto& entry : std::filesystem::directory_iterator(root, error)) {
            if (error || !entry.is_regular_file(error)) {
                throw std::runtime_error("arena root is invalid");
            }
            entries.insert(entry.path().filename().string());
        }
        if (error || entries != std::set<std::string, std::less<>>{
                                  "arena.json", "navigation.json", "physics.json"}) {
            throw std::runtime_error("arena root is not closed");
        }
        const auto arena_source = ReadSource(root / "arena.json");
        const auto navigation_source = ReadSource(root / "navigation.json");
        const auto physics_source = ReadSource(root / "physics.json");
        RequireDigest(arena_source, expected_map_content_identity);
        RequireDigest(navigation_source, expected_navigation_identity);
        RequireDigest(physics_source, expected_physics_identity);
        const auto arena = ParseSource(arena_source);
        const auto navigation = ParseSource(navigation_source);
        const auto physics = ParseSource(physics_source);

        RequireKeys(arena, {"bounds_mm", "floor", "format_version", "map_id", "spawn_points", "static_blockers"}, "document");
        RequireKeys(navigation, {"agent", "coordinate_unit", "format_version", "links", "map_id", "walkable_polygons"}, "navigation document");
        RequireKeys(physics, {"collision_layers", "coordinate_unit", "format_version", "gravity_mm_per_second_squared", "map_id", "query_policy"}, "physics document");
        if (String(arena, "format_version") != "personal-world-arena-v1" ||
            String(navigation, "format_version") != "personal-world-navigation-v1" ||
            String(physics, "format_version") != "personal-world-physics-v1" ||
            String(arena, "map_id") != expected_map_id ||
            String(navigation, "map_id") != expected_map_id ||
            String(physics, "map_id") != expected_map_id ||
            String(navigation, "coordinate_unit") != "millimeters" ||
            String(physics, "coordinate_unit") != "millimeters") {
            throw std::runtime_error("arena document binding differs");
        }

        RequireKeys(arena.at("bounds_mm"), {"maximum", "minimum"}, "bounds");
        const auto minimum = Vector(arena.at("bounds_mm").at("minimum"));
        const auto maximum = Vector(arena.at("bounds_mm").at("maximum"));
        if (minimum.x >= maximum.x || minimum.y >= maximum.y || minimum.z >= maximum.z) {
            throw std::runtime_error("arena bounds are invalid");
        }
        RequireKeys(arena.at("floor"), {"half_extent_x_mm", "half_extent_z_mm", "height_mm"}, "floor");
        const auto floor_height = Integer(arena.at("floor"), "height_mm");
        const auto floor_x = CheckedUnsigned<std::int64_t>(Integer(arena.at("floor"), "half_extent_x_mm"), "arena floor is invalid");
        const auto floor_z = CheckedUnsigned<std::int64_t>(Integer(arena.at("floor"), "half_extent_z_mm"), "arena floor is invalid");
        auto implementation = std::make_shared<ArenaImplementation>();
        implementation->colliders.push_back({
            .collider_id = FloorColliderId,
            .subshape_id = StaticSubshapeId,
            .target_actor_id = 0,
            .center_mm = {.x = 0, .y = floor_height - 500, .z = 0},
            .half_extent_mm = {.x = floor_x, .y = 500, .z = floor_z},
            .blocking = true});
        if (!arena.at("static_blockers").is_array()) {
            throw std::runtime_error("arena blocker set is invalid");
        }
        std::set<std::uint64_t> collider_ids{FloorColliderId};
        for (const auto& blocker : arena.at("static_blockers")) {
            RequireKeys(blocker, {"center_mm", "collider_id", "half_extent_mm"}, "blocker");
            const auto collider_id = CheckedUnsigned<std::uint64_t>(Integer(blocker, "collider_id"), "arena collider identity is invalid");
            const auto center = Vector(blocker.at("center_mm"));
            const auto half_extent = Vector(blocker.at("half_extent_mm"));
            if (!collider_ids.insert(collider_id).second || half_extent.x <= 0 ||
                half_extent.y <= 0 || half_extent.z <= 0) {
                throw std::runtime_error("arena blocker is invalid or duplicated");
            }
            implementation->colliders.push_back({
                .collider_id = collider_id,
                .subshape_id = StaticSubshapeId,
                .target_actor_id = 0,
                .center_mm = center,
                .half_extent_mm = half_extent,
                .blocking = true});
        }

        std::vector<ArenaSpawnPoint> spawn_points;
        std::set<std::string, std::less<>> spawn_ids;
        if (!arena.at("spawn_points").is_array() || arena.at("spawn_points").empty()) {
            throw std::runtime_error("arena spawn set is invalid");
        }
        for (const auto& spawn : arena.at("spawn_points")) {
            RequireKeys(spawn, {"id", "position_mm", "yaw_millidegrees"}, "spawn");
            ArenaSpawnPoint point{
                .id = String(spawn, "id"),
                .position_mm = Vector(spawn.at("position_mm")),
                .yaw_millidegrees = static_cast<std::int32_t>(Integer(spawn, "yaw_millidegrees"))};
            if (!spawn_ids.insert(point.id).second || point.position_mm.x < minimum.x ||
                point.position_mm.x > maximum.x || point.position_mm.y < minimum.y ||
                point.position_mm.y > maximum.y || point.position_mm.z < minimum.z ||
                point.position_mm.z > maximum.z) {
                throw std::runtime_error("arena spawn is invalid or duplicated");
            }
            spawn_points.push_back(std::move(point));
        }

        RequireKeys(physics.at("query_policy"), {"fraction_scale", "maximum_hits", "sort_keys"}, "physics query policy");
        const auto& sort_keys = physics.at("query_policy").at("sort_keys");
        if (!sort_keys.is_array() || sort_keys != Json::array({"fraction", "collider_id", "subshape_id"}) ||
            Integer(physics.at("query_policy"), "fraction_scale") != 1'000'000 ||
            Integer(physics, "gravity_mm_per_second_squared") != -9800 ||
            !physics.at("collision_layers").is_array() || physics.at("collision_layers").size() != 4) {
            throw std::runtime_error("arena physics policy differs");
        }
        implementation->physics_config = {
            .millimeters_per_jolt_unit = 1000,
            .fraction_quantization = 1'000'000,
            .query_capsule_radius_mm = 400,
            .query_capsule_half_height_mm = 500,
            .solver_velocity_steps = 10,
            .solver_position_steps = 2,
            .temporary_allocator_bytes = 8 * 1024 * 1024,
            .maximum_bodies = 128,
            .maximum_body_pairs = 1024,
            .maximum_contact_constraints = 1024,
            .maximum_hits_per_query = CheckedUnsigned<std::uint32_t>(
                Integer(physics.at("query_policy"), "maximum_hits"),
                "arena physics query capacity is invalid")};

        RequireKeys(navigation.at("agent"), {"height_mm", "maximum_slope_millidegrees", "maximum_step_mm", "radius_mm"}, "navigation agent");
        if (Integer(navigation.at("agent"), "radius_mm") != 900 ||
            Integer(navigation.at("agent"), "height_mm") != 3200 ||
            Integer(navigation.at("agent"), "maximum_slope_millidegrees") != 45000 ||
            Integer(navigation.at("agent"), "maximum_step_mm") != 400) {
            throw std::runtime_error("arena navigation agent differs");
        }
        implementation->navigation_data = BuildNavigationData(navigation, minimum, maximum);
        implementation->navigation_asset = {
            .map_id = 1,
            .version = 1,
            .sha256 = Sha256Bytes(implementation->navigation_data),
            .coordinate_scale_mm = 1000};
        implementation->navigation_config = {
            .expected_asset = implementation->navigation_asset,
            .maximum_nodes = 512,
            .maximum_polygons = 64,
            .maximum_points = 32,
            .nearest_half_extent_mm = 3000,
            .maximum_coordinate_mm = std::max({
                std::llabs(minimum.x), std::llabs(minimum.y), std::llabs(minimum.z),
                std::llabs(maximum.x), std::llabs(maximum.y), std::llabs(maximum.z)})};
        return {
            .map_id = expected_map_id,
            .map_content_identity = expected_map_content_identity,
            .navigation_identity = expected_navigation_identity,
            .physics_identity = expected_physics_identity,
            .minimum_mm = minimum,
            .maximum_mm = maximum,
            .spawn_points = std::move(spawn_points),
            .implementation = std::move(implementation)};
    } catch (const std::runtime_error&) {
        throw;
    } catch (...) {
        throw std::runtime_error("arena source validation failed");
    }
}

std::shared_ptr<PhysicsWorld> CreatePersonalWorldPhysicsWorld(
    const PersonalWorldArenaCatalog& catalog) {
    const auto implementation = std::static_pointer_cast<const ArenaImplementation>(catalog.implementation);
    if (!implementation) {
        throw std::runtime_error("arena physics catalog is unavailable");
    }
    return std::make_shared<JoltPhysicsWorld>(
        implementation->physics_config, implementation->colliders);
}

std::shared_ptr<NavigationWorld> CreatePersonalWorldNavigationWorld(
    const PersonalWorldArenaCatalog& catalog) {
    const auto implementation = std::static_pointer_cast<const ArenaImplementation>(catalog.implementation);
    if (!implementation) {
        throw std::runtime_error("arena navigation catalog is unavailable");
    }
    auto world = std::make_shared<DetourNavigationWorld>(
        implementation->navigation_config, implementation->navigation_data);
    world->LoadAsset(implementation->navigation_asset);
    return world;
}

}  // namespace ihomeland::sim
