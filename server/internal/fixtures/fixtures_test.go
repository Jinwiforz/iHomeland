package fixtures

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/account/v1"
	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	_ "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/session/v1"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// TestGoldenPackets 对每个 packet 执行 registry lookup、decode、确定性 re-encode 与摘要验证。
// 动态类型解析确保 fixture 的 Protobuf 全名真实可消费，摘要校验则覆盖 envelope 路由上下文。
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
	// 通过真实基线驱动子测试，防止测试代码复制一份可能漂移的 packet 清单。
	var manifest GoldenManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	seenKinds := make(map[string]bool)
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
			if string(payload) != string(reencoded) {
				t.Fatal("deterministic payload changed after decode")
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
				digestSource = envelopeBytes
			}
			digest := sha256.Sum256(digestSource)
			if hex.EncodeToString(digest[:]) != packet.SHA256 {
				t.Fatal("SHA-256 mismatch")
			}
		})
	}
	for _, required := range []string{"PUSH"} {
		if !seenKinds[required] {
			t.Fatalf("golden packets do not cover message kind %s", required)
		}
	}
}

// TestFixtureFilesMatchDeterministicGeneration 确保 HTTP、golden 与 negative 三类基线都由同一生成器拥有。
func TestFixtureFilesMatchDeterministicGeneration(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(root); err != nil {
		t.Fatal(err)
	}
}

// TestNegativeCasesCoversRequiredBoundaries 保证拒绝用例清单持续覆盖 change 规定的边界。
// 这里只验证清单完整性；各畸形字节的具体拒绝行为由对应 codec 与 contract 测试负责。
func TestNegativeCasesCoversRequiredBoundaries(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "shared", "contracts", "fixtures", "realtime", "negative.json"))
	if err != nil {
		t.Fatal(err)
	}
	// 从版本化清单收集原因，确保新增 case 不会削弱必须长期保留的攻击与边界覆盖。
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
	for _, required := range []string{"unknown_message", "wrong_channel", "invalid_correlation", "unknown_kind", "identity_boundary", "frame_too_large", "truncated_frame"} {
		if !reasons[required] {
			t.Fatalf("missing negative reason %s", required)
		}
	}
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
// 未登记消息返回空字符串并由 LookupRoute 拒绝，helper 不提供任何 fallback channel。
func routeChannel(catalog contract.Catalog, messageID uint32) string {
	for _, route := range catalog.Routes.Routes {
		if route.MessageID == messageID {
			return route.Channel
		}
	}
	return ""
}
