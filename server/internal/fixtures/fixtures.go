// Package fixtures 生成并验证确定性的公开示例与负向契约用例。
//
// fixture 只使用固定的无效凭据和示例身份，不读取运行环境或真实数据。它们既是 Go/C# 解码
// 兼容性的 golden input，也是协议变更评审材料，因此任何字节变化都必须可重复且经过审查。
package fixtures

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	controlv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	sessionv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/session/v1"
	"github.com/jinwiforz/ihomeland/server/internal/protocol"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// GoldenManifest 在显式 fixture schema 版本下聚合确定性 packet。
type GoldenManifest struct {
	// SchemaVersion 选择 golden.json 自身的结构，不等同于 envelope protocol_version。
	SchemaVersion uint32 `json:"schemaVersion"`
	// Packets 保存按稳定定义顺序生成的代表性 packet，调用方不得重排后覆盖源文件。
	Packets []GoldenPacket `json:"packets"`
}

// GoldenPacket 记录 payload 语义、可选 envelope 字节与可检测篡改的摘要。
type GoldenPacket struct {
	// Name 是测试子用例和跨语言 fixture 查找使用的稳定名称。
	Name string `json:"name"`
	// MessageID 是 registry 编号；零值表示只验证未装入 envelope 的共享 payload。
	MessageID uint32 `json:"messageId"`
	// Protobuf 是用于动态解析 PayloadBase64 的 fully-qualified message name。
	Protobuf string `json:"protobuf"`
	// PayloadBase64 保存确定性 Protobuf payload 的 Base64 表达。
	PayloadBase64 string `json:"payloadBase64"`
	// PayloadJSON 提供可评审语义，不参与 wire 解码或摘要计算。
	PayloadJSON string `json:"payloadJson"`
	// EnvelopeBase64 在 MessageID 非零时保存完整可靠 envelope 的 Base64 表达。
	EnvelopeBase64 string `json:"envelopeBase64,omitempty"`
	// SHA256 校验完整 envelope；没有 envelope 时校验 payload，防止 fixture 被静默修改。
	SHA256 string `json:"sha256"`
}

// NegativeManifest 枚举稳定拒绝用例，且不嵌入可使用的凭据。
type NegativeManifest struct {
	// SchemaVersion 选择 negative.json 的清单结构版本。
	SchemaVersion uint32 `json:"schemaVersion"`
	// Cases 是资格测试必须持续覆盖的拒绝原因集合。
	Cases []NegativeCase `json:"cases"`
}

// NegativeCase 为畸形边界输入提供稳定 reason key，供 contract test 断言。
type NegativeCase struct {
	// Name 是测试和文档引用该畸形输入的稳定名称。
	Name string `json:"name"`
	// ExpectedReason 是 adapter 对该输入必须产生的机器可读拒绝分类。
	ExpectedReason string `json:"expectedReason"`
}

// HTTPManifest 聚合可由客户端、服务端与文档工具共同消费的 HTTPS 示例。
type HTTPManifest struct {
	// SchemaVersion 选择 cases.json 自身的结构版本。
	SchemaVersion uint32 `json:"schemaVersion"`
	// Cases 按稳定顺序保存 request/response 契约示例。
	Cases []HTTPCase `json:"cases"`
}

// HTTPCase 将一个稳定名称绑定到完整 request 与预期 response。
type HTTPCase struct {
	// Name 是跨语言测试引用该示例的稳定名称。
	Name string `json:"name"`
	// Request 是发送给公开 HTTPS operation 的示例输入。
	Request HTTPRequest `json:"request"`
	// Response 是对应状态码与安全公开 body。
	Response HTTPResponse `json:"response"`
}

// HTTPRequest 表达不依赖具体 HTTP client library 的请求语义。
type HTTPRequest struct {
	// Method 是大写标准 HTTP method。
	Method string `json:"method"`
	// Path 是 OpenAPI 中登记的绝对 API path。
	Path string `json:"path"`
	// Body 是可选 UTF-8 JSON object；GET 等无 body 请求省略该字段。
	Body map[string]any `json:"body,omitempty"`
}

// HTTPResponse 表达客户端必须兼容的公开状态与 JSON body。
type HTTPResponse struct {
	// Status 是 OpenAPI operation 声明的 HTTP status code。
	Status int `json:"status"`
	// Body 是可选公开 JSON object；204 等无 body 响应省略该字段。
	Body map[string]any `json:"body,omitempty"`
}

// Generate 使用固定值写入全部公开 fixture，保证重复执行得到完全相同的字节。
//
// root 必须是可写仓库根目录。函数依次写入 HTTP、实时 golden 和负向清单；任一步失败都返回
// 带路径的错误，调用方应视为生成失败并通过版本控制检查可能已写入的文件。
func Generate(root string) error {
	manifest, err := buildGoldenManifest()
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(root, "shared", "contracts", "fixtures", "http", "cases.json"), buildHTTPFixtures()); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(root, "shared", "contracts", "fixtures", "realtime", "golden.json"), manifest); err != nil {
		return err
	}
	return writeJSON(filepath.Join(root, "shared", "contracts", "fixtures", "realtime", "negative.json"), buildNegativeManifest())
}

// buildHTTPFixtures 创建不含可用凭据的 HTTPS 成功、稳定错误与通道边界示例。
func buildHTTPFixtures() HTTPManifest {
	return HTTPManifest{SchemaVersion: 1, Cases: []HTTPCase{
		{Name: "version-success", Request: HTTPRequest{Method: "GET", Path: "/v1/version"}, Response: HTTPResponse{Status: 200, Body: map[string]any{"protocolVersion": 1, "minimumClientVersion": "0.1.0", "serverVersion": "0.1.0"}}},
		{Name: "login-invalid-credentials", Request: HTTPRequest{Method: "POST", Path: "/v1/auth/login", Body: map[string]any{"username": "fixture-user", "password": "fixture-password-not-secret"}}, Response: HTTPResponse{Status: 401, Body: map[string]any{"code": 102, "messageKey": "error.auth.invalid_credentials", "requestId": "fixture-request-id", "retryable": false}}},
		{Name: "register-username-conflict", Request: HTTPRequest{Method: "POST", Path: "/v1/auth/register", Body: map[string]any{"username": "fixture-user", "password": "fixture-password-not-secret", "displayName": "Fixture User"}}, Response: HTTPResponse{Status: 409, Body: map[string]any{"code": 104, "messageKey": "error.account.username_taken", "requestId": "fixture-request-id", "retryable": false}}},
		{Name: "ticket-channel-boundary", Request: HTTPRequest{Method: "POST", Path: "/v1/session/tickets", Body: map[string]any{"channel": "TLS_TCP"}}, Response: HTTPResponse{Status: 201, Body: map[string]any{"ticket": "fixture-ticket-value-that-is-not-valid", "endpoint": map[string]any{"channel": "TLS_TCP", "host": "game.example.invalid", "port": 4433}, "scopes": []string{"GAMEPLAY"}, "expiresAtMs": 1700000030000}}},
	}}
}

// buildNegativeManifest 返回必须由对应 contract/codec 测试持续执行的拒绝原因目录。
func buildNegativeManifest() NegativeManifest {
	return NegativeManifest{SchemaVersion: 1, Cases: []NegativeCase{
		{Name: "unknown-message", ExpectedReason: "unknown_message"},
		{Name: "wrong-channel", ExpectedReason: "wrong_channel"},
		{Name: "invalid-kind-id", ExpectedReason: "invalid_correlation"},
		{Name: "unknown-enum", ExpectedReason: "unknown_kind"},
		{Name: "forbidden-actor-field", ExpectedReason: "identity_boundary"},
		{Name: "oversized-frame", ExpectedReason: "frame_too_large"},
		{Name: "truncated-frame", ExpectedReason: "truncated_frame"},
	}}
}

// Verify 在内存中重新生成全部 fixture，并与版本化文件逐字节比较。
//
// JSON 表示由本包唯一生成，缩进、顺序与 LF 也属于可重复基线。Verify 不修改磁盘，失败时
// 维护者应显式运行 fixtures 并审查 HTTP、golden 或 negative 文件的具体差异。
func Verify(root string) error {
	golden, err := buildGoldenManifest()
	if err != nil {
		return err
	}
	checks := []struct {
		// path 是由 Generate 拥有的版本化 fixture 文件。
		path string
		// expected 是使用固定输入重新构造的内存基线。
		expected any
	}{
		{filepath.Join(root, "shared", "contracts", "fixtures", "http", "cases.json"), buildHTTPFixtures()},
		{filepath.Join(root, "shared", "contracts", "fixtures", "realtime", "golden.json"), golden},
		{filepath.Join(root, "shared", "contracts", "fixtures", "realtime", "negative.json"), buildNegativeManifest()},
	}
	for _, check := range checks {
		if err := verifyJSON(check.path, check.expected); err != nil {
			return err
		}
	}
	catalog, err := contract.Load(root)
	if err != nil {
		return err
	}
	return validateHTTPErrorFixtures(buildHTTPFixtures(), catalog.Errors)
}

// validateHTTPErrorFixtures 确保示例错误不会偏离稳定 error registry 的恢复语义。
func validateHTTPErrorFixtures(manifest HTTPManifest, registry contract.ErrorRegistry) error {
	errorsByCode := make(map[int]contract.ErrorEntry, len(registry.Errors))
	for _, entry := range registry.Errors {
		errorsByCode[int(entry.Code)] = entry
	}
	errorCases := 0
	for _, fixture := range manifest.Cases {
		rawCode, exists := fixture.Response.Body["code"]
		if !exists {
			continue
		}
		code, ok := rawCode.(int)
		if !ok {
			return fmt.Errorf("HTTP fixture %s error code must be an integer", fixture.Name)
		}
		entry, exists := errorsByCode[code]
		if !exists {
			return fmt.Errorf("HTTP fixture %s references unknown error code %d", fixture.Name, code)
		}
		messageKey, messageKeyOK := fixture.Response.Body["messageKey"].(string)
		retryable, retryableOK := fixture.Response.Body["retryable"].(bool)
		if !messageKeyOK || !retryableOK || fixture.Response.Status != entry.HTTPStatus || messageKey != entry.MessageKey || retryable != entry.Retryable {
			return fmt.Errorf("HTTP fixture %s does not match error registry code %d", fixture.Name, code)
		}
		errorCases++
	}
	if errorCases == 0 {
		return fmt.Errorf("HTTP fixtures require at least one stable error case")
	}
	return nil
}

// verifyJSON 比较 generator 的规范字节与磁盘基线，不接受手工重排或格式漂移。
func verifyJSON(path string, expected any) error {
	actual, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read fixture %s: %w", path, err)
	}
	encoded, err := marshalJSON(expected)
	if err != nil {
		return fmt.Errorf("encode fixture %s: %w", path, err)
	}
	if !bytes.Equal(actual, encoded) {
		return fmt.Errorf("fixture differs from deterministic generation: %s", path)
	}
	return nil
}

// buildGoldenManifest 创建具有代表性的 control push 与 gameplay ticket payload。
//
// 所有时间、ID 和顺序均为固定值，禁止使用随机数、当前时间或本机状态。返回的 manifest 由
// 调用方拥有；构建或编码任一 packet 失败时不返回部分清单。
func buildGoldenManifest() (GoldenManifest, error) {
	maintenance := controlv1.MaintenancePush_builder{StartsAtMs: proto.Int64(1_700_000_060_000), ExpectedEndAtMs: proto.Int64(1_700_000_120_000), MessageKey: proto.String("notice.maintenance.fixture")}.Build()
	invalidated := controlv1.SessionInvalidatedPush_builder{SessionEpoch: proto.Uint64(3), ReasonKey: proto.String("session.invalidated.fixture")}.Build()
	endpoint := sessionv1.Endpoint_builder{Channel: enumPointer(sessionv1.TransportChannel_TRANSPORT_CHANNEL_TLS_TCP), Host: proto.String("game.example.invalid"), Port: proto.Uint32(4433)}.Build()
	ticket := sessionv1.ConnectionTicket_builder{SessionId: proto.String("session-fixture-1"), SessionEpoch: proto.Uint64(2), TargetChannel: enumPointer(sessionv1.TransportChannel_TRANSPORT_CHANNEL_TLS_TCP), Endpoint: endpoint, Scopes: []sessionv1.AuthScope{sessionv1.AuthScope_AUTH_SCOPE_GAMEPLAY}, Nonce: bytes.Repeat([]byte{2}, 16), IssuedAtMs: proto.Int64(1_700_000_000_000), ExpiresAtMs: proto.Int64(1_700_000_030_000)}.Build()
	// definitions 是 fixture 的稳定顺序源；调整顺序会改变 envelope sequence 和 golden 摘要。
	definitions := []struct {
		// name 是跨语言测试引用的稳定 fixture 名称。
		name string
		// messageID 为零时仅生成 payload，不创建无法路由的 envelope。
		messageID uint32
		// kind 必须与 registry 登记一致；基础契约只包含服务端 control push。
		kind commonv1.MessageKind
		// message 是待确定性编码的只读 Protobuf 实例。
		message proto.Message
		// typeName 允许消费者通过 descriptor 或全局 registry 动态创建消息。
		typeName string
	}{
		{name: "control-maintenance-push", messageID: 500, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: maintenance, typeName: "ihomeland.control.v1.MaintenancePush"},
		{name: "control-session-invalidated-push", messageID: 504, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: invalidated, typeName: "ihomeland.control.v1.SessionInvalidatedPush"},
		{name: "gameplay-connection-ticket", messageID: 0, kind: commonv1.MessageKind_MESSAGE_KIND_UNSPECIFIED, message: ticket, typeName: "ihomeland.session.v1.ConnectionTicket"},
	}
	manifest := GoldenManifest{SchemaVersion: 1, Packets: make([]GoldenPacket, 0, len(definitions))}
	for index, definition := range definitions {
		payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(definition.message)
		if err != nil {
			return GoldenManifest{}, err
		}
		jsonPayload, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: false}.Marshal(definition.message)
		if err != nil {
			return GoldenManifest{}, err
		}
		// protojson 不承诺空白字符稳定；压缩后再入库，避免可读投影在不同进程间产生无语义漂移。
		canonicalJSON := new(bytes.Buffer)
		if err := json.Compact(canonicalJSON, jsonPayload); err != nil {
			return GoldenManifest{}, fmt.Errorf("canonicalize %s JSON: %w", definition.name, err)
		}
		packet := GoldenPacket{Name: definition.name, MessageID: definition.messageID, Protobuf: definition.typeName, PayloadBase64: base64.StdEncoding.EncodeToString(payload), PayloadJSON: canonicalJSON.String()}
		// 有 envelope 时摘要覆盖完整路由上下文；共享 payload 则直接覆盖 payload 字节。
		digestSource := payload
		if definition.messageID != 0 {
			envelope := envelopeFor(index, definition.messageID, definition.kind, payload)
			encoded, err := protocol.MarshalEnvelope(envelope)
			if err != nil {
				return GoldenManifest{}, err
			}
			packet.EnvelopeBase64 = base64.StdEncoding.EncodeToString(encoded)
			digestSource = encoded
		}
		digest := sha256.Sum256(digestSource)
		packet.SHA256 = hex.EncodeToString(digest[:])
		manifest.Packets = append(manifest.Packets, packet)
	}
	return manifest, nil
}

// envelopeFor 为基础 control push 分配稳定 sequence 与 timestamp。
// builder 会保留 payload slice，因此调用方在 envelope 完成编码前不得修改其内容。
func envelopeFor(index int, messageID uint32, kind commonv1.MessageKind, payload []byte) *commonv1.ReliableEnvelope {
	return commonv1.ReliableEnvelope_builder{ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(messageID), Kind: enumPointer(kind), Sequence: proto.Uint64(uint64(index + 1)), TimestampMs: proto.Int64(1_700_000_000_000 + int64(index)), Payload: payload}.Build()
}

// enumPointer 创建 Edition opaque builder API 所需指针，避免为每种 enum 维护重复 helper。
// 返回指针只交给 builder 立即读取，不作为可变共享状态保存。
func enumPointer[T ~int32](value T) *T { return &value }

// writeJSON 创建 fixture 目录，并使用稳定缩进与 LF 结尾写入文件。
// value 必须可由 encoding/json 完整编码；编码失败时不会触碰目标文件。
func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := marshalJSON(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

// marshalJSON 生成 fixtures 唯一允许的缩进与 LF 结尾表示，供写入和只读验证共同使用。
func marshalJSON(value any) ([]byte, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}
