#include "ihomeland/battle/v1/battle.pb.h"

#include <nlohmann/json.hpp>

#include <cstddef>
#include <cstdint>
#include <fstream>
#include <iterator>
#include <string>
#include <string_view>
#include <vector>

/// 匿名 namespace 保存 test-only wire codec，防止 fixture helper 形成可链接 production API。
namespace {

/// ByteVector 是 canonical wire bytes 的测试所有权容器。
using ByteVector = std::vector<std::uint8_t>;

/// DecodeHex 将 source corpus 的 lowercase hex 严格解码为独立 bytes。
/// 非法字符或奇数长度返回空结果，使主测试 fail closed。
ByteVector DecodeHex(const std::string_view value) {
    if (value.empty() || value.size() % 2U != 0U) {
        return {};
    }
    ByteVector result(value.size() / 2U);
    for (std::size_t index = 0; index < result.size(); ++index) {
        const auto high = value[index * 2U];
        const auto low = value[index * 2U + 1U];
        const auto nibble = [](const char character) -> int {
            if (character >= '0' && character <= '9') {
                return character - '0';
            }
            if (character >= 'a' && character <= 'f') {
                return character - 'a' + 10;
            }
            return -1;
        };
        const auto high_value = nibble(high);
        const auto low_value = nibble(low);
        if (high_value < 0 || low_value < 0) {
            return {};
        }
        result[index] = static_cast<std::uint8_t>((high_value << 4) | low_value);
    }
    return result;
}

/// PutBig16 写入 network-order uint16，调用方必须提供至少两个可写 bytes。
void PutBig16(ByteVector& destination, const std::size_t offset, const std::uint16_t value) {
    destination[offset] = static_cast<std::uint8_t>(value >> 8U);
    destination[offset + 1U] = static_cast<std::uint8_t>(value);
}

/// PutBig32 写入 network-order uint32，调用方必须提供至少四个可写 bytes。
void PutBig32(ByteVector& destination, const std::size_t offset, const std::uint32_t value) {
    destination[offset] = static_cast<std::uint8_t>(value >> 24U);
    destination[offset + 1U] = static_cast<std::uint8_t>(value >> 16U);
    destination[offset + 2U] = static_cast<std::uint8_t>(value >> 8U);
    destination[offset + 3U] = static_cast<std::uint8_t>(value);
}

/// PutBig64 写入 network-order uint64，调用方必须提供至少八个可写 bytes。
void PutBig64(ByteVector& destination, const std::size_t offset, const std::uint64_t value) {
    for (std::size_t index = 0; index < 8U; ++index) {
        destination[offset + index] =
            static_cast<std::uint8_t>(value >> ((7U - index) * 8U));
    }
}

/// PutLittle16 写入 KCP little-endian uint16，不能用于其他 wire layer。
void PutLittle16(ByteVector& destination, const std::size_t offset, const std::uint16_t value) {
    destination[offset] = static_cast<std::uint8_t>(value);
    destination[offset + 1U] = static_cast<std::uint8_t>(value >> 8U);
}

/// PutLittle32 写入 KCP little-endian uint32，不能用于 secure 或 route header。
void PutLittle32(ByteVector& destination, const std::size_t offset, const std::uint32_t value) {
    destination[offset] = static_cast<std::uint8_t>(value);
    destination[offset + 1U] = static_cast<std::uint8_t>(value >> 8U);
    destination[offset + 2U] = static_cast<std::uint8_t>(value >> 16U);
    destination[offset + 3U] = static_cast<std::uint8_t>(value >> 24U);
}

/// LoadGolden 只读解析 canonical corpus；文件缺失或 JSON 非法由调用方转为测试失败。
nlohmann::json LoadGolden() {
    std::ifstream stream(
        std::string(IHOMELAND_BATTLE_WIRE_ROOT) + "/canonical-golden.json",
        std::ios::binary);
    if (!stream) {
        return {};
    }
    return nlohmann::json::parse(
        std::istreambuf_iterator<char>(stream),
        std::istreambuf_iterator<char>(),
        nullptr,
        false);
}

/// FindVector 返回指定 fixture vector 的只读引用；缺失 identity 返回 JSON null。
const nlohmann::json& FindVector(const nlohmann::json& golden, const std::string_view id) {
    static const nlohmann::json null_vector;
    if (!golden.contains("vectors") || !golden["vectors"].is_array()) {
        return null_vector;
    }
    for (const auto& vector : golden["vectors"]) {
        if (vector.value("vector_id", "") == id) {
            return vector;
        }
    }
    return null_vector;
}

/// VerifySecureHeader 由 decoded fields 重编码 48-byte AAD 并与 fixture 精确比较。
bool VerifySecureHeader(const nlohmann::json& vector) {
    if (vector.is_null()) {
        return false;
    }
    const auto expected = DecodeHex(vector.value("bytes_hex", ""));
    if (expected.size() != 48U) {
        return false;
    }
    const auto& decoded = vector["decoded"];
    ByteVector encoded(48U);
    encoded[0] = 'I';
    encoded[1] = 'H';
    encoded[2] = 'B';
    encoded[3] = 'T';
    encoded[4] = 1U;
    encoded[5] = decoded["packet_kind"].get<std::uint8_t>();
    for (std::size_t index = 8U; index < 16U; ++index) {
        encoded[index] = expected[index];
    }
    PutBig32(encoded, 16U, decoded["battle_session_generation"].get<std::uint32_t>());
    PutBig32(encoded, 20U, decoded["key_epoch"].get<std::uint32_t>());
    PutBig64(encoded, 24U, decoded["packet_sequence"].get<std::uint64_t>());
    PutBig16(encoded, 32U, decoded["protected_payload_length"].get<std::uint16_t>());
    PutBig32(encoded, 34U, decoded["endpoint_generation"].get<std::uint32_t>());
    for (std::size_t index = 38U; index < 46U; ++index) {
        encoded[index] = expected[index];
    }
    return encoded == expected;
}

/// VerifyRouteHeaders 重编码 raw、KCP segment 与 KCP logical envelope。
bool VerifyRouteHeaders(const nlohmann::json& golden) {
    const auto& raw = FindVector(golden, "raw-probe-route-v1");
    const auto& segment = FindVector(golden, "kcp-push-segment-v1");
    const auto& route = FindVector(golden, "kcp-ability-route-v1");
    if (raw.is_null() || segment.is_null() || route.is_null()) {
        return false;
    }

    ByteVector raw_encoded(16U);
    const auto& raw_fields = raw["decoded"];
    PutBig32(raw_encoded, 0U, raw_fields["message_id"].get<std::uint32_t>());
    PutBig16(raw_encoded, 4U, raw_fields["payload_length"].get<std::uint16_t>());
    raw_encoded[6] = raw_fields["partition_index"].get<std::uint8_t>();
    raw_encoded[7] = raw_fields["partition_count"].get<std::uint8_t>();
    PutBig64(raw_encoded, 8U, raw_fields["application_sequence"].get<std::uint64_t>());

    ByteVector segment_encoded(24U);
    const auto& segment_fields = segment["decoded"];
    PutLittle32(segment_encoded, 0U, segment_fields["conv"].get<std::uint32_t>());
    segment_encoded[4] = segment_fields["cmd"].get<std::uint8_t>();
    segment_encoded[5] = segment_fields["frg"].get<std::uint8_t>();
    PutLittle16(segment_encoded, 6U, segment_fields["wnd"].get<std::uint16_t>());
    PutLittle32(segment_encoded, 8U, segment_fields["ts"].get<std::uint32_t>());
    PutLittle32(segment_encoded, 12U, segment_fields["sn"].get<std::uint32_t>());
    PutLittle32(segment_encoded, 16U, segment_fields["una"].get<std::uint32_t>());
    PutLittle32(segment_encoded, 20U, segment_fields["len"].get<std::uint32_t>());

    ByteVector route_encoded(16U);
    const auto& route_fields = route["decoded"];
    PutBig32(route_encoded, 0U, route_fields["message_id"].get<std::uint32_t>());
    PutBig16(route_encoded, 4U, route_fields["payload_length"].get<std::uint16_t>());
    PutBig16(route_encoded, 6U, route_fields["route_flags"].get<std::uint16_t>());
    PutBig64(route_encoded, 8U, route_fields["application_sequence"].get<std::uint64_t>());

    return raw_encoded == DecodeHex(raw.value("bytes_hex", "")) &&
        segment_encoded == DecodeHex(segment.value("bytes_hex", "")) &&
        route_encoded == DecodeHex(route.value("bytes_hex", ""));
}

/// PopulateSnapshotIdentity 从 canonical decoded 投影设置 full/delta 共享身份字段。
template <typename Snapshot>
void PopulateSnapshotIdentity(
    Snapshot& snapshot,
    const nlohmann::json& decoded) {
    snapshot.set_server_tick(decoded["server_tick"].get<std::uint64_t>());
    snapshot.set_snapshot_sequence(
        decoded["snapshot_sequence"].get<std::uint64_t>());
    snapshot.set_baseline_id(decoded["baseline_id"].get<std::uint64_t>());
    snapshot.set_partition_index(
        decoded["partition_index"].get<std::uint32_t>());
    snapshot.set_partition_count(
        decoded["partition_count"].get<std::uint32_t>());
    snapshot.set_last_processed_input_tick(
        decoded["last_processed_input_tick"].get<std::uint64_t>());
}

/// VerifySnapshotVector 验证 snapshot 的显式 ack presence、值与 canonical bytes。
template <typename Snapshot>
bool VerifySnapshotVector(const nlohmann::json& vector) {
    if (vector.is_null()) {
        return false;
    }
    const auto& decoded = vector["decoded"];
    Snapshot snapshot;
    PopulateSnapshotIdentity(snapshot, decoded);
    std::string encoded;
    if (!snapshot.SerializeToString(&encoded) ||
        ByteVector(encoded.begin(), encoded.end()) !=
            DecodeHex(vector.value("bytes_hex", ""))) {
        return false;
    }
    Snapshot parsed;
    return parsed.ParseFromString(encoded) &&
        parsed.has_last_processed_input_tick() &&
        parsed.last_processed_input_tick() ==
            decoded["last_processed_input_tick"].get<std::uint64_t>() &&
        parsed.partition_index() ==
            decoded["partition_index"].get<std::uint32_t>() &&
        parsed.partition_count() ==
            decoded["partition_count"].get<std::uint32_t>();
}

/// VerifySnapshotAcknowledgements 覆盖 explicit zero、常规值、最大 varint 与 multipart。
bool VerifySnapshotAcknowledgements(const nlohmann::json& golden) {
    using ihomeland::battle::v1::BattleDeltaSnapshot;
    using ihomeland::battle::v1::BattleFullSnapshot;
    return VerifySnapshotVector<BattleFullSnapshot>(
               FindVector(golden, "battle-full-snapshot-ack-zero-v1")) &&
        VerifySnapshotVector<BattleDeltaSnapshot>(
               FindVector(golden, "battle-delta-snapshot-ack-zero-v1")) &&
        VerifySnapshotVector<BattleDeltaSnapshot>(
               FindVector(golden, "battle-delta-snapshot-ack-normal-v1")) &&
        VerifySnapshotVector<BattleDeltaSnapshot>(
               FindVector(golden, "battle-delta-snapshot-ack-large-v1")) &&
        VerifySnapshotVector<BattleFullSnapshot>(
               FindVector(golden, "battle-full-snapshot-ack-multipart-0-v1")) &&
        VerifySnapshotVector<BattleFullSnapshot>(
               FindVector(golden, "battle-full-snapshot-ack-multipart-1-v1"));
}

/// VerifyProtobuf 使用本次生成的 lite messages 双向消费 canonical payload。
bool VerifyProtobuf(const nlohmann::json& golden) {
    const auto& probe_vector = FindVector(golden, "battle-probe-protobuf-v1");
    const auto& ability_vector = FindVector(golden, "battle-ability-event-protobuf-v1");
    if (probe_vector.is_null() || ability_vector.is_null()) {
        return false;
    }

    const auto& probe_fields = probe_vector["decoded"];
    ihomeland::battle::v1::BattleProbe probe;
    probe.set_probe_sequence(probe_fields["probe_sequence"].get<std::uint64_t>());
    probe.set_latest_snapshot_sequence(
        probe_fields["latest_snapshot_sequence"].get<std::uint64_t>());
    probe.set_client_monotonic_time_us(
        probe_fields["client_monotonic_time_us"].get<std::uint64_t>());
    std::string probe_encoded;
    if (!probe.SerializeToString(&probe_encoded) ||
        ByteVector(probe_encoded.begin(), probe_encoded.end()) !=
            DecodeHex(probe_vector.value("bytes_hex", ""))) {
        return false;
    }
    ihomeland::battle::v1::BattleProbe probe_decoded;
    if (!probe_decoded.ParseFromString(probe_encoded) ||
        probe_decoded.probe_sequence() != probe.probe_sequence()) {
        return false;
    }

    const auto& ability_fields = ability_vector["decoded"];
    ihomeland::battle::v1::BattleAbilityReliableEvent ability;
    ability.set_event_id(ability_fields["event_id"].get<std::uint64_t>());
    ability.set_server_tick(ability_fields["server_tick"].get<std::uint64_t>());
    ability.set_source_entity_id(ability_fields["source_entity_id"].get<std::uint64_t>());
    ability.set_source_entity_generation(
        ability_fields["source_entity_generation"].get<std::uint32_t>());
    ability.set_ability_id(ability_fields["ability_id"].get<std::uint32_t>());
    ability.set_phase(static_cast<ihomeland::battle::v1::BattleAbilityPhase>(
        ability_fields["phase"].get<std::int32_t>()));
    std::string ability_encoded;
    if (!ability.SerializeToString(&ability_encoded) ||
        ByteVector(ability_encoded.begin(), ability_encoded.end()) !=
            DecodeHex(ability_vector.value("bytes_hex", ""))) {
        return false;
    }
    ihomeland::battle::v1::BattleAbilityReliableEvent ability_decoded;
    return ability_decoded.ParseFromString(ability_encoded) &&
        ability_decoded.event_id() == ability.event_id() &&
        ability_decoded.phase() == ability.phase();
}

/// PopulateContentState 从 canonical decoded 投影设置 full state 的全部显式字段。
void PopulateContentState(
    ihomeland::battle::v1::BattleEntityState& state,
    const nlohmann::json& decoded) {
    state.set_entity_id(decoded["entity_id"].get<std::uint64_t>());
    state.set_entity_generation(decoded["entity_generation"].get<std::uint32_t>());
    auto* transform = state.mutable_transform();
    transform->set_position_x_mm(0);
    transform->set_position_y_mm(0);
    transform->set_position_z_mm(0);
    transform->set_yaw_millidegrees(0);
    transform->set_velocity_x_mm_per_second(0);
    transform->set_velocity_y_mm_per_second(0);
    transform->set_velocity_z_mm_per_second(0);
    state.set_health_milli(decoded["health_milli"].get<std::uint32_t>());
    state.set_state_flags(decoded["state_flags"].get<std::uint32_t>());
    state.set_archetype_id(decoded["archetype_id"].get<std::uint32_t>());
    state.set_equipped_weapon_id(
        decoded["equipped_weapon_id"].get<std::uint32_t>());
    state.set_max_health_milli(decoded["max_health_milli"].get<std::uint32_t>());
}

/// VerifyContentProjection 验证 player/Boss full state 与 Boss lifecycle 的 canonical parity。
bool VerifyContentProjection(const nlohmann::json& golden) {
    const auto& player_vector = FindVector(
        golden,
        "battle-player-entity-state-protobuf-v1");
    const auto& boss_vector = FindVector(
        golden,
        "battle-boss-entity-state-protobuf-v1");
    const auto& lifecycle_vector = FindVector(
        golden,
        "battle-boss-lifecycle-protobuf-v1");
    if (player_vector.is_null() || boss_vector.is_null() || lifecycle_vector.is_null()) {
        return false;
    }

    ihomeland::battle::v1::BattleEntityState player;
    PopulateContentState(player, player_vector["decoded"]);
    std::string player_encoded;
    if (!player.SerializeToString(&player_encoded) ||
        ByteVector(player_encoded.begin(), player_encoded.end()) !=
            DecodeHex(player_vector.value("bytes_hex", "")) ||
        player.health_milli() > player.max_health_milli() ||
        player.archetype_id() == 0U || player.equipped_weapon_id() == 0U) {
        return false;
    }

    ihomeland::battle::v1::BattleEntityState boss;
    PopulateContentState(boss, boss_vector["decoded"]);
    std::string boss_encoded;
    if (!boss.SerializeToString(&boss_encoded) ||
        ByteVector(boss_encoded.begin(), boss_encoded.end()) !=
            DecodeHex(boss_vector.value("bytes_hex", ""))) {
        return false;
    }

    const auto& decoded = lifecycle_vector["decoded"];
    ihomeland::battle::v1::BattleEntityLifecycle lifecycle;
    lifecycle.set_event_id(decoded["event_id"].get<std::uint64_t>());
    lifecycle.set_server_tick(decoded["server_tick"].get<std::uint64_t>());
    lifecycle.set_entity_id(decoded["entity_id"].get<std::uint64_t>());
    lifecycle.set_entity_generation(decoded["entity_generation"].get<std::uint32_t>());
    lifecycle.set_kind(static_cast<ihomeland::battle::v1::BattleEntityLifecycleKind>(
        decoded["lifecycle_kind"].get<std::int32_t>()));
    lifecycle.set_archetype_id(decoded["archetype_id"].get<std::uint32_t>());
    *lifecycle.mutable_initial_state() = boss;
    std::string lifecycle_encoded;
    return lifecycle.SerializeToString(&lifecycle_encoded) &&
        ByteVector(lifecycle_encoded.begin(), lifecycle_encoded.end()) ==
            DecodeHex(lifecycle_vector.value("bytes_hex", "")) &&
        lifecycle.archetype_id() == lifecycle.initial_state().archetype_id();
}

}  // namespace

/// main 验证 C++ 对共享 secure/raw/KCP/Protobuf golden 的双向 byte-exact parity。
/// 返回非零表示 source corpus、endian、generated source 或 lite linkage 已经漂移。
int main() {
    const auto golden = LoadGolden();
    if (golden.is_discarded() || golden.is_null()) {
        return 1;
    }
    if (!VerifySecureHeader(FindVector(golden, "secure-raw-aad-v1")) ||
        !VerifySecureHeader(FindVector(golden, "secure-kcp-aad-v1"))) {
        return 2;
    }
    if (!VerifyRouteHeaders(golden)) {
        return 3;
    }
    if (!VerifyProtobuf(golden)) {
        return 4;
    }
    if (!VerifySnapshotAcknowledgements(golden)) {
        return 5;
    }
    if (!VerifyContentProjection(golden)) {
        return 6;
    }
    return 0;
}
