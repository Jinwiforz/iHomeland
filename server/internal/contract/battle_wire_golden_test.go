package contract

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	battlev1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/battle/v1"
	"google.golang.org/protobuf/proto"
)

// battleWireGoldenDocument 是 Go parity test 消费的只读 canonical fixture 投影。
type battleWireGoldenDocument struct {
	// Vectors 保存所有 header 与 Protobuf byte-exact case。
	Vectors []battleWireVector `json:"vectors"`
}

// battleWireVector 保存单个 canonical byte sequence 及其预期解码字段。
type battleWireVector struct {
	// VectorID 是跨语言共享的稳定 case identity。
	VectorID string `json:"vector_id"`
	// Layer 选择 secure/raw/KCP/Protobuf 解码规则。
	Layer string `json:"layer"`
	// Protobuf 是 payload case 的 fully-qualified generated type。
	Protobuf string `json:"protobuf"`
	// BytesHex 是必须按字节精确匹配的小写十六进制。
	BytesHex string `json:"bytes_hex"`
	// ByteCount 是 wire sequence 的冻结长度。
	ByteCount int `json:"byte_count"`
	// SHA256 是 wire sequence 的冻结内容摘要。
	SHA256 string `json:"sha256"`
	// Decoded 保存当前 layer 必须恢复的字段。
	Decoded battleWireDecoded `json:"decoded"`
}

// battleWireDecoded 聚合各 layer 的数值字段；未参与当前 vector 的字段保持零值。
type battleWireDecoded struct {
	// PacketKind 区分 raw、KCP 与 control packet。
	PacketKind uint8 `json:"packet_kind"`
	// BattleSessionGeneration 防止旧 network session generation 复活。
	BattleSessionGeneration uint32 `json:"battle_session_generation"`
	// KeyEpoch 与 PacketSequence 共同构成 nonce。
	KeyEpoch uint32 `json:"key_epoch"`
	// PacketSequence 是 epoch 内严格单调的 64-bit sequence。
	PacketSequence uint64 `json:"packet_sequence"`
	// ProtectedPayloadLength 不包含 secure header 与 AEAD tag。
	ProtectedPayloadLength uint16 `json:"protected_payload_length"`
	// EndpointGeneration 绑定 authenticated rebind generation。
	EndpointGeneration uint32 `json:"endpoint_generation"`
	// MessageID 是 registry 分配的 numeric route。
	MessageID uint32 `json:"message_id"`
	// PayloadLength 是 route 内 Protobuf bytes 的精确长度。
	PayloadLength uint16 `json:"payload_length"`
	// PartitionIndex 是 raw state/input 分区的零基索引。
	PartitionIndex uint8 `json:"partition_index"`
	// PartitionCount 是 raw logical message 的有界分区数。
	PartitionCount uint8 `json:"partition_count"`
	// RouteFlags 必须为零直到新 wire version 显式登记。
	RouteFlags uint16 `json:"route_flags"`
	// ApplicationSequence 是 route-specific monotonic identity。
	ApplicationSequence uint64 `json:"application_sequence"`
	// Conv 是 KCP conversation identity。
	Conv uint32 `json:"conv"`
	// Cmd 是 KCP segment command。
	Cmd uint8 `json:"cmd"`
	// Frg 是 KCP fragment countdown。
	Frg uint8 `json:"frg"`
	// Wnd 是 KCP advertised receive window。
	Wnd uint16 `json:"wnd"`
	// TS 是 KCP millisecond timestamp。
	TS uint32 `json:"ts"`
	// SN 是 KCP segment sequence。
	SN uint32 `json:"sn"`
	// UNA 是 KCP unacknowledged sequence。
	UNA uint32 `json:"una"`
	// Len 是 KCP segment payload bytes。
	Len uint32 `json:"len"`
	// ProbeSequence 是 BattleProbe 的 monotonic identity。
	ProbeSequence uint64 `json:"probe_sequence"`
	// LatestSnapshotSequence 是 BattleProbe 已应用的 snapshot sequence。
	LatestSnapshotSequence uint64 `json:"latest_snapshot_sequence"`
	// ClientMonotonicTimeUS 是 BattleProbe 的本地单调时钟采样。
	ClientMonotonicTimeUS uint64 `json:"client_monotonic_time_us"`
	// EventID 是 reliable ability event identity。
	EventID uint64 `json:"event_id"`
	// ServerTick 是 ability event 的权威 simulation tick。
	ServerTick uint64 `json:"server_tick"`
	// SourceEntityID 是 ability source entity。
	SourceEntityID uint64 `json:"source_entity_id"`
	// SourceEntityGeneration 是 ability source 的精确 generation。
	SourceEntityGeneration uint32 `json:"source_entity_generation"`
	// AbilityID 是版本化 content identity。
	AbilityID uint32 `json:"ability_id"`
	// Phase 是 ability lifecycle phase 数值。
	Phase int32 `json:"phase"`
}

// loadBattleWireGolden 每次从 source corpus 读取 fixture，避免测试 mutation 污染其他用例。
func loadBattleWireGolden(t *testing.T) battleWireGoldenDocument {
	t.Helper()
	path := filepath.Join("..", "..", "..", "shared", "contracts", "fixtures", "battle", "wire", "canonical-golden.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document battleWireGoldenDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Vectors) != 7 {
		t.Fatalf("canonical battle wire vector count = %d, want 7", len(document.Vectors))
	}
	return document
}

// findBattleWireVector 返回唯一 vector；缺失 identity 立即终止当前测试。
func findBattleWireVector(t *testing.T, document battleWireGoldenDocument, id string) battleWireVector {
	t.Helper()
	for _, vector := range document.Vectors {
		if vector.VectorID == id {
			return vector
		}
	}
	t.Fatalf("canonical battle wire vector %s is missing", id)
	return battleWireVector{}
}

// decodeBattleWireBytes 验证 hex、length 与 SHA-256 后返回由测试拥有的 bytes。
func decodeBattleWireBytes(t *testing.T, vector battleWireVector) []byte {
	t.Helper()
	data, err := hex.DecodeString(vector.BytesHex)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != vector.ByteCount {
		t.Fatalf("%s byte count = %d, want %d", vector.VectorID, len(data), vector.ByteCount)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != vector.SHA256 {
		t.Fatalf("%s SHA-256 drifted", vector.VectorID)
	}
	return data
}

// TestBattleWireHeaderGoldenParity 验证 Go 对 secure/raw/KCP headers 的双向 endian 与宽度。
func TestBattleWireHeaderGoldenParity(t *testing.T) {
	document := loadBattleWireGolden(t)

	for _, id := range []string{"secure-raw-aad-v1", "secure-kcp-aad-v1"} {
		vector := findBattleWireVector(t, document, id)
		data := decodeBattleWireBytes(t, vector)
		if string(data[0:4]) != "IHBT" || data[4] != 1 || data[5] != vector.Decoded.PacketKind ||
			binary.BigEndian.Uint32(data[16:20]) != vector.Decoded.BattleSessionGeneration ||
			binary.BigEndian.Uint32(data[20:24]) != vector.Decoded.KeyEpoch ||
			binary.BigEndian.Uint64(data[24:32]) != vector.Decoded.PacketSequence ||
			binary.BigEndian.Uint16(data[32:34]) != vector.Decoded.ProtectedPayloadLength ||
			binary.BigEndian.Uint32(data[34:38]) != vector.Decoded.EndpointGeneration {
			t.Fatalf("%s secure header decode drifted", id)
		}
		encoded := make([]byte, 48)
		copy(encoded[0:4], "IHBT")
		encoded[4] = 1
		encoded[5] = vector.Decoded.PacketKind
		copy(encoded[8:16], data[8:16])
		binary.BigEndian.PutUint32(encoded[16:20], vector.Decoded.BattleSessionGeneration)
		binary.BigEndian.PutUint32(encoded[20:24], vector.Decoded.KeyEpoch)
		binary.BigEndian.PutUint64(encoded[24:32], vector.Decoded.PacketSequence)
		binary.BigEndian.PutUint16(encoded[32:34], vector.Decoded.ProtectedPayloadLength)
		binary.BigEndian.PutUint32(encoded[34:38], vector.Decoded.EndpointGeneration)
		copy(encoded[38:46], data[38:46])
		if hex.EncodeToString(encoded) != vector.BytesHex {
			t.Fatalf("%s secure header encode drifted", id)
		}
	}

	raw := findBattleWireVector(t, document, "raw-probe-route-v1")
	rawBytes := decodeBattleWireBytes(t, raw)
	if binary.BigEndian.Uint32(rawBytes[0:4]) != raw.Decoded.MessageID ||
		binary.BigEndian.Uint16(rawBytes[4:6]) != raw.Decoded.PayloadLength ||
		rawBytes[6] != raw.Decoded.PartitionIndex ||
		rawBytes[7] != raw.Decoded.PartitionCount ||
		binary.BigEndian.Uint64(rawBytes[8:16]) != raw.Decoded.ApplicationSequence {
		t.Fatal("raw route header decode drifted")
	}

	segment := findBattleWireVector(t, document, "kcp-push-segment-v1")
	segmentBytes := decodeBattleWireBytes(t, segment)
	if binary.LittleEndian.Uint32(segmentBytes[0:4]) != segment.Decoded.Conv ||
		segmentBytes[4] != segment.Decoded.Cmd ||
		segmentBytes[5] != segment.Decoded.Frg ||
		binary.LittleEndian.Uint16(segmentBytes[6:8]) != segment.Decoded.Wnd ||
		binary.LittleEndian.Uint32(segmentBytes[8:12]) != segment.Decoded.TS ||
		binary.LittleEndian.Uint32(segmentBytes[12:16]) != segment.Decoded.SN ||
		binary.LittleEndian.Uint32(segmentBytes[16:20]) != segment.Decoded.UNA ||
		binary.LittleEndian.Uint32(segmentBytes[20:24]) != segment.Decoded.Len {
		t.Fatal("KCP segment header decode drifted")
	}

	route := findBattleWireVector(t, document, "kcp-ability-route-v1")
	routeBytes := decodeBattleWireBytes(t, route)
	if binary.BigEndian.Uint32(routeBytes[0:4]) != route.Decoded.MessageID ||
		binary.BigEndian.Uint16(routeBytes[4:6]) != route.Decoded.PayloadLength ||
		binary.BigEndian.Uint16(routeBytes[6:8]) != route.Decoded.RouteFlags ||
		binary.BigEndian.Uint64(routeBytes[8:16]) != route.Decoded.ApplicationSequence {
		t.Fatal("KCP route envelope decode drifted")
	}
}

// TestBattleWireProtobufGoldenParity 验证 Go generated battle payload 的 deterministic bytes 与反向解析。
func TestBattleWireProtobufGoldenParity(t *testing.T) {
	document := loadBattleWireGolden(t)
	options := proto.MarshalOptions{Deterministic: true}

	probeVector := findBattleWireVector(t, document, "battle-probe-protobuf-v1")
	probe := &battlev1.BattleProbe{}
	probe.SetProbeSequence(probeVector.Decoded.ProbeSequence)
	probe.SetLatestSnapshotSequence(probeVector.Decoded.LatestSnapshotSequence)
	probe.SetClientMonotonicTimeUs(probeVector.Decoded.ClientMonotonicTimeUS)
	probeBytes, err := options.Marshal(probe)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(probeBytes) != probeVector.BytesHex {
		t.Fatal("BattleProbe canonical encode drifted")
	}
	var decodedProbe battlev1.BattleProbe
	if err := proto.Unmarshal(decodeBattleWireBytes(t, probeVector), &decodedProbe); err != nil ||
		!proto.Equal(probe, &decodedProbe) {
		t.Fatal("BattleProbe canonical decode drifted")
	}

	abilityVector := findBattleWireVector(t, document, "battle-ability-event-protobuf-v1")
	ability := &battlev1.BattleAbilityReliableEvent{}
	ability.SetEventId(abilityVector.Decoded.EventID)
	ability.SetServerTick(abilityVector.Decoded.ServerTick)
	ability.SetSourceEntityId(abilityVector.Decoded.SourceEntityID)
	ability.SetSourceEntityGeneration(abilityVector.Decoded.SourceEntityGeneration)
	ability.SetAbilityId(abilityVector.Decoded.AbilityID)
	ability.SetPhase(battlev1.BattleAbilityPhase(abilityVector.Decoded.Phase))
	abilityBytes, err := options.Marshal(ability)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(abilityBytes) != abilityVector.BytesHex {
		t.Fatal("BattleAbilityReliableEvent canonical encode drifted")
	}
	var decodedAbility battlev1.BattleAbilityReliableEvent
	if err := proto.Unmarshal(decodeBattleWireBytes(t, abilityVector), &decodedAbility); err != nil ||
		!proto.Equal(ability, &decodedAbility) {
		t.Fatal("BattleAbilityReliableEvent canonical decode drifted")
	}
}
