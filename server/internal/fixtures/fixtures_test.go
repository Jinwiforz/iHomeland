package fixtures

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/account/v1"
	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/session/v1"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// TestGoldenPackets 对每个 packet 执行 registry lookup、decode、canonical re-encode 与摘要验证。
// 动态类型解析确保 fixture 的 Protobuf 全名真实可消费，覆盖检查则防止新增登记遗漏 golden。
func TestGoldenPackets(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := contract.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "shared", "contracts", "fixtures", "realtime", "golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	// 通过真实基线驱动子测试，避免测试代码复制一份可能漂移的 packet 清单。
	var manifest GoldenManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	seenKinds := make(map[string]bool)
	seenMessageIDs := make(map[uint32]bool)
	for _, packet := range manifest.Packets {
		t.Run(packet.Name, func(t *testing.T) {
			payload, err := base64.StdEncoding.DecodeString(packet.PayloadBase64)
			if err != nil {
				t.Fatal(err)
			}
			messageType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(packet.Protobuf))
			if err != nil {
				t.Fatal(err)
			}
			message := messageType.New().Interface()
			if err := proto.Unmarshal(payload, message); err != nil {
				t.Fatal(err)
			}
			reencoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(payload, reencoded) {
				t.Fatal("deterministic payload changed after decode")
			}
			jsonPayload, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: false}).Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			canonicalJSON := new(bytes.Buffer)
			if err := json.Compact(canonicalJSON, jsonPayload); err != nil {
				t.Fatal(err)
			}
			if canonicalJSON.String() != packet.PayloadJSON {
				t.Fatal("canonical JSON changed after descriptor decode")
			}
			digestSource := payload
			if packet.MessageID != 0 {
				messageEntry, route, err := catalog.LookupRoute(packet.MessageID, routeChannel(catalog, packet.MessageID))
				if err != nil {
					t.Fatal(err)
				}
				if messageEntry.Protobuf != packet.Protobuf {
					t.Fatalf("registry type %s does not match fixture type %s", messageEntry.Protobuf, packet.Protobuf)
				}
				envelopeBytes, err := base64.StdEncoding.DecodeString(packet.EnvelopeBase64)
				if err != nil {
					t.Fatal(err)
				}
				if len(envelopeBytes) > int(route.MaxSize) {
					t.Fatalf("envelope size %d exceeds route maxSize %d", len(envelopeBytes), route.MaxSize)
				}
				envelope, err := protocol.UnmarshalEnvelope(envelopeBytes)
				if err != nil {
					t.Fatal(err)
				}
				if envelope.GetMessageId() != packet.MessageID || !bytes.Equal(envelope.GetPayload(), payload) {
					t.Fatal("envelope routing identity or payload differs from fixture")
				}
				kind := messageKindName(envelope.GetKind())
				if kind != messageEntry.Kind {
					t.Fatalf("registry kind %s does not match envelope kind %s", messageEntry.Kind, kind)
				}
				assertEnvelopeCorrelation(t, route.Idempotency, envelope)
				seenKinds[kind] = true
				seenMessageIDs[packet.MessageID] = true
				digestSource = envelopeBytes
			}
			digest := sha256.Sum256(digestSource)
			if hex.EncodeToString(digest[:]) != packet.SHA256 {
				t.Fatal("SHA-256 mismatch")
			}
		})
	}
	for _, required := range []string{"REQUEST", "RESPONSE", "COMMAND", "PUSH"} {
		if !seenKinds[required] {
			t.Fatalf("golden packets do not cover message kind %s", required)
		}
	}
	for _, message := range catalog.Messages.Messages {
		isGameplay := message.ID == 1 || message.ID == 2 ||
			(message.ID >= 2000 && message.ID <= 2122)
		if isGameplay && !seenMessageIDs[message.ID] {
			t.Fatalf("golden packets do not cover message id %d", message.ID)
		}
	}
}

// TestFixtureFilesMatchDeterministicGeneration 确保所有版本化 fixture 都由同一生成器拥有。
func TestFixtureFilesMatchDeterministicGeneration(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(root); err != nil {
		t.Fatal(err)
	}
}

// TestBattleTicketHTTPFixturesCoverAdmissionBoundary 验证公开样例同时冻结 own/visit、8/9 actor、
// stale target、response-loss 与 credential redaction，且成功响应不泄漏内部 binding。
func TestBattleTicketHTTPFixturesCoverAdmissionBoundary(t *testing.T) {
	manifest := buildHTTPFixtures()
	cases := make(map[string]HTTPCase, len(manifest.Cases))
	for _, testCase := range manifest.Cases {
		cases[testCase.Name] = testCase
	}
	required := []string{
		"battle-ticket-own-world-success",
		"battle-ticket-eighth-visit-actor-success",
		"battle-ticket-ninth-actor-capacity",
		"battle-ticket-stale-target",
		"battle-ticket-response-loss-replay",
		"battle-ticket-credential-field-rejected",
	}
	for _, name := range required {
		if _, ok := cases[name]; !ok {
			t.Fatalf("missing BattleTicket HTTP fixture %s", name)
		}
	}
	own := cases["battle-ticket-own-world-success"]
	replay := cases["battle-ticket-response-loss-replay"]
	ownResponse, err := json.Marshal(own.Response)
	if err != nil {
		t.Fatal(err)
	}
	replayResponse, err := json.Marshal(replay.Response)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ownResponse, replayResponse) {
		t.Fatal("BattleTicket response-loss replay must preserve the first credential and binding projection")
	}
	for _, name := range []string{"battle-ticket-own-world-success", "battle-ticket-eighth-visit-actor-success"} {
		response, err := json.Marshal(cases[name].Response)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			`"assignmentStamp"`, `"runtimeNodeId"`, `"simulationNodeId"`,
			`"simulationInstanceId"`, `"actorSlot"`, `"proofKey"`,
		} {
			if bytes.Contains(response, []byte(forbidden)) {
				t.Fatalf("BattleTicket fixture %s leaked internal field %s", name, forbidden)
			}
		}
	}
	redacted, err := json.Marshal(cases["battle-ticket-credential-field-rejected"].Response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(redacted, []byte("fixture_secret_must_not_be_echoed")) {
		t.Fatal("BattleTicket validation error echoed credential-like request material")
	}
}

// TestNegativeCasesCoversRequiredBoundaries 保证拒绝用例持续覆盖协议规定的边界。
// 这里验证清单完整性；各畸形字节的具体拒绝行为由对应 codec 与 contract 测试负责。
func TestNegativeCasesCoversRequiredBoundaries(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "shared", "contracts", "fixtures", "realtime", "negative.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest NegativeManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	reasons := make(map[string]bool, len(manifest.Cases))
	names := make(map[string]bool, len(manifest.Cases))
	for _, testCase := range manifest.Cases {
		if names[testCase.Name] {
			t.Fatalf("duplicate negative fixture name %s", testCase.Name)
		}
		if reasons[testCase.ExpectedReason] {
			t.Fatalf("duplicate negative fixture reason %s", testCase.ExpectedReason)
		}
		names[testCase.Name] = true
		reasons[testCase.ExpectedReason] = true
	}
	for _, required := range []string{
		"unknown_message", "wrong_channel", "wrong_direction", "invalid_correlation",
		"invalid_response_correlation", "unknown_kind", "identity_boundary",
		"internal_assignment_boundary", "frame_too_large", "truncated_frame", "unregistered_interaction",
	} {
		if !reasons[required] {
			t.Fatalf("missing negative reason %s", required)
		}
	}
}

// TestAdmissionSemanticCorpusIsOpaque 验证语义 corpus 只描述受信绑定结果，不泄露 credential claims 布局。
func TestAdmissionSemanticCorpusIsOpaque(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "shared", "contracts", "fixtures", "admission", "semantic.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest AdmissionSemanticManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.RuntimeImplemented {
		t.Fatal("semantic corpus must identify the implemented runtime")
	}
	for _, forbiddenKey := range []string{`"credential":`, `"claims":`, `"nonce":`, `"consumeId":`, `"sessionEpoch":`, `"assignmentStamp":`, `"signature":`} {
		if strings.Contains(string(contents), forbiddenKey) {
			t.Fatalf("semantic corpus exposes forbidden credential detail %s", forbiddenKey)
		}
	}
	accepted := make(map[string]bool)
	for _, testCase := range manifest.Cases {
		if testCase.ExpectedOutcome == "ACCEPT" {
			accepted[testCase.MembershipState+":"+testCase.Purpose] = true
		}
	}
	if !accepted["RESERVED:JOIN"] || !accepted["RECONNECTING:RECONNECT"] {
		t.Fatal("semantic corpus lacks valid JOIN or RECONNECT binding")
	}
}

// FuzzGoldenPayloadRoundTrip 验证已登记类型在未知字段、未知 enum 与任意字段顺序下仍可 canonical re-encode。
func FuzzGoldenPayloadRoundTrip(f *testing.F) {
	manifest, err := buildGoldenManifest()
	if err != nil {
		f.Fatal(err)
	}
	for _, packet := range manifest.Packets {
		payload, err := base64.StdEncoding.DecodeString(packet.PayloadBase64)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(packet.Protobuf, payload)
	}
	f.Fuzz(func(t *testing.T, typeName string, payload []byte) {
		messageType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(typeName))
		if err != nil {
			return
		}
		first := messageType.New().Interface()
		if err := proto.Unmarshal(payload, first); err != nil {
			return
		}
		canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(first)
		if err != nil {
			t.Fatal(err)
		}
		second := messageType.New().Interface()
		if err := proto.Unmarshal(canonical, second); err != nil {
			t.Fatal(err)
		}
		reencoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(second)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(canonical, reencoded) || !proto.Equal(first, second) {
			t.Fatal("payload canonical form is not stable")
		}
	})
}

// messageKindName 将 wire enum 转换为 registry 使用的稳定 kind symbol。
func messageKindName(kind commonv1.MessageKind) string {
	switch kind {
	case commonv1.MessageKind_MESSAGE_KIND_REQUEST:
		return "REQUEST"
	case commonv1.MessageKind_MESSAGE_KIND_RESPONSE:
		return "RESPONSE"
	case commonv1.MessageKind_MESSAGE_KIND_COMMAND:
		return "COMMAND"
	case commonv1.MessageKind_MESSAGE_KIND_PUSH:
		return "PUSH"
	case commonv1.MessageKind_MESSAGE_KIND_ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// assertEnvelopeCorrelation 验证 golden envelope 与 route idempotency 使用相同关联策略。
func assertEnvelopeCorrelation(t *testing.T, strategy string, envelope *commonv1.ReliableEnvelope) {
	t.Helper()
	requestLength := len(envelope.GetRequestId())
	commandLength := len(envelope.GetCommandId())
	switch strategy {
	case "REQUEST_ID":
		if requestLength != 16 || commandLength != 0 {
			t.Fatal("REQUEST_ID route has invalid envelope correlation")
		}
	case "COMMAND_ID":
		if commandLength != 16 || requestLength != 0 {
			t.Fatal("COMMAND_ID route has invalid envelope correlation")
		}
	case "CORRELATION_ID":
		if (requestLength == 16) == (commandLength == 16) {
			t.Fatal("CORRELATION_ID route requires exactly one identifier")
		}
	case "NONE":
		if requestLength != 0 || commandLength != 0 {
			t.Fatal("NONE route must not carry correlation identifiers")
		}
	default:
		t.Fatalf("unknown route idempotency strategy %s", strategy)
	}
}

// routeChannel 返回已登记 channel，使 LookupRoute 仍能验证路由唯一存在。
// 未登记消息返回空字符串并由 LookupRoute 拒绝，helper 不提供 fallback channel。
func routeChannel(catalog contract.Catalog, messageID uint32) string {
	for _, route := range catalog.Routes.Routes {
		if route.MessageID == messageID {
			return route.Channel
		}
	}
	return ""
}
