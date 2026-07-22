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
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	controlv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	sessionv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/session/v1"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
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

// goldenDefinition 是生成器内部的稳定消息定义，不进入版本化 JSON。
type goldenDefinition struct {
	// name 是跨语言测试引用的稳定 fixture 名称。
	name string
	// messageID 为零时只生成共享 payload，不创建无法路由的 envelope。
	messageID uint32
	// kind 必须与 registry 登记一致。
	kind commonv1.MessageKind
	// message 是待确定性编码的只读 Protobuf 实例。
	message proto.Message
	// typeName 允许消费者通过 descriptor 动态创建消息。
	typeName string
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
	// Gate 标识必须在 registry、descriptor、envelope、payload 或 frame 哪一层拒绝。
	Gate string `json:"gate"`
	// ExpectedErrorCode 引用 errors.json 中允许跨网络边界返回的稳定错误。
	ExpectedErrorCode uint32 `json:"expectedErrorCode"`
}

// AdmissionSemanticManifest 保存 admission issuer/verifier 必须消费的抽象验收场景。
// 它不描述 token claims 或密码学布局，因此不能被当作可解析 credential schema。
type AdmissionSemanticManifest struct {
	// SchemaVersion 选择 semantic.json 自身的结构版本。
	SchemaVersion uint32 `json:"schemaVersion"`
	// RuntimeImplemented 表示 production issuer/verifier 与 credential consume 是否已由测试验收。
	RuntimeImplemented bool `json:"runtimeImplemented"`
	// Cases 按稳定顺序保存 membership、purpose 与 binding condition 组合。
	Cases []AdmissionSemanticCase `json:"cases"`
}

// AdmissionSemanticCase 只表达验收语义，不携带可伪造的完整 claims。
type AdmissionSemanticCase struct {
	// Name 是 verifier contract test 引用的稳定名称。
	Name string `json:"name"`
	// MembershipState 是签发/消费时的受信 aggregate 状态。
	MembershipState string `json:"membershipState"`
	// Purpose 是 Visitor credential 绑定的 JOIN 或 RECONNECT 动作。
	Purpose string `json:"purpose"`
	// Condition 表达其余完整 binding 是匹配、过期、重放或被替换。
	Condition string `json:"condition"`
	// ExpectedOutcome 只允许 ACCEPT 或 REJECT。
	ExpectedOutcome string `json:"expectedOutcome"`
	// ExpectedErrorCode 在 REJECT 时引用 stable error；ACCEPT 时为零且省略。
	ExpectedErrorCode uint32 `json:"expectedErrorCode,omitempty"`
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
	// Headers 只保存契约要求的安全示例 header，不包含 bearer 或真实 credential。
	Headers map[string]string `json:"headers,omitempty"`
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
	if err := writeJSON(filepath.Join(root, "shared", "contracts", "fixtures", "realtime", "negative.json"), buildNegativeManifest()); err != nil {
		return err
	}
	return writeJSON(filepath.Join(root, "shared", "contracts", "fixtures", "admission", "semantic.json"), buildAdmissionSemanticManifest())
}

// buildHTTPFixtures 创建不含可用凭据的 HTTPS 成功、稳定错误与通道边界示例。
func buildHTTPFixtures() HTTPManifest {
	return HTTPManifest{SchemaVersion: 1, Cases: []HTTPCase{
		{Name: "version-success", Request: HTTPRequest{Method: "GET", Path: "/v1/version"}, Response: HTTPResponse{Status: 200, Body: map[string]any{"protocolVersion": 1, "minimumClientVersion": "0.1.0", "serverVersion": "0.1.0"}}},
		{Name: "login-invalid-credentials", Request: HTTPRequest{Method: "POST", Path: "/v1/auth/login", Body: map[string]any{"username": "fixture-user", "password": "fixture-password-not-secret"}}, Response: HTTPResponse{Status: 401, Body: map[string]any{"code": 102, "messageKey": "error.auth.invalid_credentials", "requestId": "fixture-request-id", "retryable": false}}},
		{Name: "register-username-conflict", Request: HTTPRequest{Method: "POST", Path: "/v1/auth/register", Body: map[string]any{"username": "fixture-user", "password": "fixture-password-not-secret", "displayName": "Fixture User"}}, Response: HTTPResponse{Status: 409, Body: map[string]any{"code": 104, "messageKey": "error.account.username_taken", "requestId": "fixture-request-id", "retryable": false}}},
		{Name: "ticket-channel-boundary", Request: HTTPRequest{Method: "POST", Path: "/v1/session/tickets", Body: map[string]any{"channel": "TLS_TCP"}}, Response: HTTPResponse{Status: 201, Body: map[string]any{"ticket": "fixture-ticket-value-that-is-not-valid", "endpoint": map[string]any{"channel": "TLS_TCP", "host": "game.example.invalid", "port": 4433}, "scopes": []string{"GAMEPLAY"}, "expiresAtMs": 1700000030000}}},
		{Name: "world-bootstrap-success", Request: HTTPRequest{Method: "GET", Path: "/v1/world/bootstrap"}, Response: HTTPResponse{Status: 200, Body: map[string]any{"world": map[string]any{"personalWorldId": "pworld_fixture_owner", "ownerPlayerId": "player_fixture_owner", "lifecycle": "ACTIVE", "revision": 3, "createdAtMs": 1_700_000_000_000}, "assignment": map[string]any{"personalWorldId": "pworld_fixture_owner", "worldInstanceId": "winst_fixture_current", "endpoint": map[string]any{"channel": "TLS_TCP", "host": "game.example.invalid", "port": 4433}, "generation": 2, "leaseExpiresAtMs": 1_700_000_120_000}}}},
		{Name: "visit-accept-success", Request: HTTPRequest{Method: "POST", Path: "/v1/visits/{visitSessionId}/invites/{inviteId}/accept", Headers: map[string]string{"Idempotency-Key": "fixture-accept-key-0001"}, Body: map[string]any{"expectedRevision": 4}}, Response: HTTPResponse{Status: 200, Body: map[string]any{"reservation": map[string]any{"visitSessionId": "visit_fixture_one", "revision": 5, "reservationExpiresAtMs": 1_700_000_060_000}}}},
		{Name: "visit-accept-same-key-replay", Request: HTTPRequest{Method: "POST", Path: "/v1/visits/{visitSessionId}/invites/{inviteId}/accept", Headers: map[string]string{"Idempotency-Key": "fixture-accept-key-0001"}, Body: map[string]any{"expectedRevision": 4}}, Response: HTTPResponse{Status: 200, Body: map[string]any{"reservation": map[string]any{"visitSessionId": "visit_fixture_one", "revision": 5, "reservationExpiresAtMs": 1_700_000_060_000}}}},
		{Name: "own-world-admission-success", Request: HTTPRequest{Method: "POST", Path: "/v1/world/admissions", Headers: map[string]string{"Idempotency-Key": "fixture-own-admission-01"}, Body: map[string]any{"kind": "OWN_WORLD"}}, Response: HTTPResponse{Status: 201, Body: map[string]any{"credential": "fixture-own-world-admission-value-not-valid", "endpoint": map[string]any{"channel": "TLS_TCP", "host": "game.example.invalid", "port": 4433}, "role": "OWNER", "purpose": "OWN_WORLD", "visitRevision": 0, "expiresAtMs": 1_700_000_030_000}}},
		{Name: "visit-world-admission-success", Request: HTTPRequest{Method: "POST", Path: "/v1/world/admissions", Headers: map[string]string{"Idempotency-Key": "fixture-visit-admission-1"}, Body: map[string]any{"kind": "VISIT_WORLD", "visitSessionId": "visit_fixture_one"}}, Response: HTTPResponse{Status: 201, Body: map[string]any{"credential": "fixture-visit-world-admission-value-not-valid", "endpoint": map[string]any{"channel": "TLS_TCP", "host": "game.example.invalid", "port": 4433}, "role": "VISITOR", "purpose": "JOIN", "visitRevision": 5, "expiresAtMs": 1_700_000_030_000}}},
		{Name: "world-admission-idempotency-conflict", Request: HTTPRequest{Method: "POST", Path: "/v1/world/admissions", Headers: map[string]string{"Idempotency-Key": "fixture-own-admission-01"}, Body: map[string]any{"kind": "VISIT_WORLD", "visitSessionId": "visit_fixture_two"}}, Response: HTTPResponse{Status: 409, Body: map[string]any{"code": 2006, "messageKey": "error.world.idempotency_conflict", "requestId": "fixture-request-id", "retryable": false}}},
		{Name: "visit-accept-stale-revision", Request: HTTPRequest{Method: "POST", Path: "/v1/visits/{visitSessionId}/invites/{inviteId}/accept", Headers: map[string]string{"Idempotency-Key": "fixture-accept-stale-01"}, Body: map[string]any{"expectedRevision": 3}}, Response: HTTPResponse{Status: 409, Body: map[string]any{"code": 2105, "messageKey": "error.visit.revision_conflict", "requestId": "fixture-request-id", "retryable": false}}},
		{Name: "world-admission-forbidden-actor-field", Request: HTTPRequest{Method: "POST", Path: "/v1/world/admissions", Headers: map[string]string{"Idempotency-Key": "fixture-forbidden-actor1"}, Body: map[string]any{"kind": "OWN_WORLD", "playerId": "player_attacker"}}, Response: HTTPResponse{Status: 400, Body: map[string]any{"code": 200, "messageKey": "error.validation.failed", "requestId": "fixture-request-id", "retryable": false}}},
		{Name: "visit-admission-membership-required", Request: HTTPRequest{Method: "POST", Path: "/v1/world/admissions", Headers: map[string]string{"Idempotency-Key": "fixture-membership-none1"}, Body: map[string]any{"kind": "VISIT_WORLD", "visitSessionId": "visit_fixture_missing"}}, Response: HTTPResponse{Status: 403, Body: map[string]any{"code": 2107, "messageKey": "error.visit.membership_required", "requestId": "fixture-request-id", "retryable": false}}},
		{Name: "world-bootstrap-dependency-unavailable", Request: HTTPRequest{Method: "GET", Path: "/v1/world/bootstrap"}, Response: HTTPResponse{Status: 503, Body: map[string]any{"code": 500, "messageKey": "error.dependency.unavailable", "requestId": "fixture-request-id", "retryable": true}}},
	}}
}

// buildNegativeManifest 返回必须由对应 contract/codec 测试持续执行的拒绝原因目录。
func buildNegativeManifest() NegativeManifest {
	return NegativeManifest{SchemaVersion: 1, Cases: []NegativeCase{
		{Name: "unknown-message", ExpectedReason: "unknown_message", Gate: "registry", ExpectedErrorCode: 1},
		{Name: "wrong-channel", ExpectedReason: "wrong_channel", Gate: "registry", ExpectedErrorCode: 1},
		{Name: "wrong-direction", ExpectedReason: "wrong_direction", Gate: "registry", ExpectedErrorCode: 1},
		{Name: "invalid-kind-id", ExpectedReason: "invalid_correlation", Gate: "envelope", ExpectedErrorCode: 1},
		{Name: "invalid-response-correlation", ExpectedReason: "invalid_response_correlation", Gate: "envelope", ExpectedErrorCode: 1},
		{Name: "unknown-envelope-enum", ExpectedReason: "unknown_kind", Gate: "envelope", ExpectedErrorCode: 1},
		{Name: "forbidden-actor-field", ExpectedReason: "identity_boundary", Gate: "descriptor", ExpectedErrorCode: 200},
		{Name: "forbidden-internal-assignment", ExpectedReason: "internal_assignment_boundary", Gate: "descriptor", ExpectedErrorCode: 200},
		{Name: "oversized-frame", ExpectedReason: "frame_too_large", Gate: "frame", ExpectedErrorCode: 1},
		{Name: "truncated-frame", ExpectedReason: "truncated_frame", Gate: "frame", ExpectedErrorCode: 1},
		{Name: "unregistered-world-interaction", ExpectedReason: "unregistered_interaction", Gate: "registry", ExpectedErrorCode: 1},
	}}
}

// buildAdmissionSemanticManifest 冻结 admission verifier 的 state/purpose/binding 验收矩阵。
func buildAdmissionSemanticManifest() AdmissionSemanticManifest {
	return AdmissionSemanticManifest{SchemaVersion: 1, RuntimeImplemented: true, Cases: []AdmissionSemanticCase{
		{Name: "reserved-join-valid", MembershipState: "RESERVED", Purpose: "JOIN", Condition: "MATCHING_BINDING", ExpectedOutcome: "ACCEPT"},
		{Name: "reconnecting-reconnect-valid", MembershipState: "RECONNECTING", Purpose: "RECONNECT", Condition: "MATCHING_BINDING", ExpectedOutcome: "ACCEPT"},
		{Name: "reserved-reconnect-purpose-mismatch", MembershipState: "RESERVED", Purpose: "RECONNECT", Condition: "PURPOSE_MISMATCH", ExpectedOutcome: "REJECT", ExpectedErrorCode: 2003},
		{Name: "reconnecting-join-purpose-mismatch", MembershipState: "RECONNECTING", Purpose: "JOIN", Condition: "PURPOSE_MISMATCH", ExpectedOutcome: "REJECT", ExpectedErrorCode: 2003},
		{Name: "expired", MembershipState: "RESERVED", Purpose: "JOIN", Condition: "EXPIRED", ExpectedOutcome: "REJECT", ExpectedErrorCode: 2004},
		{Name: "replayed-credential", MembershipState: "RESERVED", Purpose: "JOIN", Condition: "CREDENTIAL_REPLAYED", ExpectedOutcome: "REJECT", ExpectedErrorCode: 2005},
		{Name: "stale-session-epoch", MembershipState: "RESERVED", Purpose: "JOIN", Condition: "STALE_SESSION_EPOCH", ExpectedOutcome: "REJECT", ExpectedErrorCode: 2003},
		{Name: "stale-full-assignment", MembershipState: "RESERVED", Purpose: "JOIN", Condition: "STALE_ASSIGNMENT", ExpectedOutcome: "REJECT", ExpectedErrorCode: 2002},
		{Name: "wrong-endpoint", MembershipState: "RESERVED", Purpose: "JOIN", Condition: "WRONG_ENDPOINT", ExpectedOutcome: "REJECT", ExpectedErrorCode: 2003},
		{Name: "wrong-channel", MembershipState: "RESERVED", Purpose: "JOIN", Condition: "WRONG_CHANNEL", ExpectedOutcome: "REJECT", ExpectedErrorCode: 2003},
		{Name: "missing-membership", MembershipState: "NONE", Purpose: "JOIN", Condition: "MEMBERSHIP_MISSING", ExpectedOutcome: "REJECT", ExpectedErrorCode: 2107},
	}}
}

// Verify 在内存中重新生成全部 fixture，并与版本化文件逐字节比较。
//
// JSON 表示由本包唯一生成，缩进、顺序与 LF 也属于可重复基线。Verify 不修改磁盘，失败时
// 维护者应显式运行 fixtures 并审查 HTTP、golden、negative 或 admission 文件的具体差异。
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
		{filepath.Join(root, "shared", "contracts", "fixtures", "admission", "semantic.json"), buildAdmissionSemanticManifest()},
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
	if err := validateHTTPErrorFixtures(buildHTTPFixtures(), catalog.Errors); err != nil {
		return err
	}
	if err := validateNegativeFixtures(buildNegativeManifest(), catalog.Errors); err != nil {
		return err
	}
	return validateAdmissionSemanticFixtures(buildAdmissionSemanticManifest(), catalog.Errors)
}

// validateNegativeFixtures 确保每个拒绝用例绑定已知 gate 与 stable error。
func validateNegativeFixtures(manifest NegativeManifest, registry contract.ErrorRegistry) error {
	errorsByCode := make(map[uint32]struct{}, len(registry.Errors))
	for _, entry := range registry.Errors {
		errorsByCode[entry.Code] = struct{}{}
	}
	names := make(map[string]struct{}, len(manifest.Cases))
	reasons := make(map[string]struct{}, len(manifest.Cases))
	for _, fixture := range manifest.Cases {
		if fixture.Name == "" || fixture.ExpectedReason == "" || fixture.ExpectedErrorCode == 0 {
			return errors.New("negative fixture requires name, reason, and stable error")
		}
		if _, exists := names[fixture.Name]; exists {
			return fmt.Errorf("duplicate negative fixture name %s", fixture.Name)
		}
		if _, exists := reasons[fixture.ExpectedReason]; exists {
			return fmt.Errorf("duplicate negative fixture reason %s", fixture.ExpectedReason)
		}
		names[fixture.Name] = struct{}{}
		reasons[fixture.ExpectedReason] = struct{}{}
		switch fixture.Gate {
		case "registry", "descriptor", "envelope", "payload", "frame":
		default:
			return fmt.Errorf("negative fixture %s has unknown gate %s", fixture.Name, fixture.Gate)
		}
		if _, exists := errorsByCode[fixture.ExpectedErrorCode]; !exists {
			return fmt.Errorf("negative fixture %s references unknown error %d", fixture.Name, fixture.ExpectedErrorCode)
		}
	}
	return nil
}

// validateAdmissionSemanticFixtures 校验已实现 runtime 的抽象 corpus 完整性且不冻结 credential 布局。
func validateAdmissionSemanticFixtures(manifest AdmissionSemanticManifest, registry contract.ErrorRegistry) error {
	if manifest.SchemaVersion != 1 || !manifest.RuntimeImplemented || len(manifest.Cases) == 0 {
		return errors.New("admission semantic fixtures require the production version 1 runtime")
	}
	errorsByCode := make(map[uint32]struct{}, len(registry.Errors))
	for _, entry := range registry.Errors {
		errorsByCode[entry.Code] = struct{}{}
	}
	seen := make(map[string]struct{}, len(manifest.Cases))
	accepted := make(map[string]bool)
	expectedErrors := map[string]uint32{
		"PURPOSE_MISMATCH":    2003,
		"EXPIRED":             2004,
		"CREDENTIAL_REPLAYED": 2005,
		"STALE_SESSION_EPOCH": 2003,
		"STALE_ASSIGNMENT":    2002,
		"WRONG_ENDPOINT":      2003,
		"WRONG_CHANNEL":       2003,
		"MEMBERSHIP_MISSING":  2107,
	}
	for _, fixture := range manifest.Cases {
		if fixture.Name == "" || fixture.MembershipState == "" || fixture.Purpose == "" || fixture.Condition == "" {
			return errors.New("admission semantic fixture is incomplete")
		}
		if _, exists := seen[fixture.Name]; exists {
			return fmt.Errorf("duplicate admission semantic fixture %s", fixture.Name)
		}
		seen[fixture.Name] = struct{}{}
		key := fixture.MembershipState + ":" + fixture.Purpose
		switch fixture.ExpectedOutcome {
		case "ACCEPT":
			if fixture.ExpectedErrorCode != 0 || fixture.Condition != "MATCHING_BINDING" || (key != "RESERVED:JOIN" && key != "RECONNECTING:RECONNECT") {
				return fmt.Errorf("admission semantic fixture %s accepts an invalid state/purpose binding", fixture.Name)
			}
			accepted[key] = true
		case "REJECT":
			if fixture.ExpectedErrorCode == 0 {
				return fmt.Errorf("admission semantic fixture %s lacks stable error", fixture.Name)
			}
			expectedCode, knownCondition := expectedErrors[fixture.Condition]
			if !knownCondition || fixture.ExpectedErrorCode != expectedCode {
				return fmt.Errorf("admission semantic fixture %s has unmapped condition or error", fixture.Name)
			}
			if _, exists := errorsByCode[fixture.ExpectedErrorCode]; !exists {
				return fmt.Errorf("admission semantic fixture %s references unknown error %d", fixture.Name, fixture.ExpectedErrorCode)
			}
		default:
			return fmt.Errorf("admission semantic fixture %s has invalid outcome", fixture.Name)
		}
	}
	if !accepted["RESERVED:JOIN"] || !accepted["RECONNECTING:RECONNECT"] {
		return errors.New("admission semantic fixtures require both valid JOIN and RECONNECT paths")
	}
	return nil
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

// buildGoldenManifest 创建已登记实时消息与共享 credential 的确定性 payload。
//
// 所有时间、ID 和顺序均为固定值，禁止使用随机数、当前时间或本机状态。返回的 manifest 由
// 调用方拥有；构建或编码任一 packet 失败时不返回部分清单。
func buildGoldenManifest() (GoldenManifest, error) {
	maintenance := controlv1.MaintenancePush_builder{StartsAtMs: proto.Int64(1_700_000_060_000), ExpectedEndAtMs: proto.Int64(1_700_000_120_000), MessageKey: proto.String("notice.maintenance.fixture")}.Build()
	invalidated := controlv1.SessionInvalidatedPush_builder{SessionEpoch: proto.Uint64(3), ReasonKey: proto.String("session.invalidated.fixture")}.Build()
	endpoint := sessionv1.Endpoint_builder{Channel: enumPointer(sessionv1.TransportChannel_TRANSPORT_CHANNEL_TLS_TCP), Host: proto.String("game.example.invalid"), Port: proto.Uint32(4433)}.Build()
	ticket := sessionv1.ConnectionTicket_builder{SessionId: proto.String("session-fixture-1"), SessionEpoch: proto.Uint64(2), TargetChannel: enumPointer(sessionv1.TransportChannel_TRANSPORT_CHANNEL_TLS_TCP), Endpoint: endpoint, Scopes: []sessionv1.AuthScope{sessionv1.AuthScope_AUTH_SCOPE_GAMEPLAY}, Nonce: bytes.Repeat([]byte{2}, 16), IssuedAtMs: proto.Int64(1_700_000_000_000), ExpiresAtMs: proto.Int64(1_700_000_030_000)}.Build()
	// definitions 是 fixture 的稳定顺序源；调整顺序会改变 envelope sequence 和 golden 摘要。
	definitions := []goldenDefinition{
		{name: "control-maintenance-push", messageID: 500, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: maintenance, typeName: "ihomeland.control.v1.MaintenancePush"},
		{name: "control-session-invalidated-push", messageID: 504, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: invalidated, typeName: "ihomeland.control.v1.SessionInvalidatedPush"},
		{name: "gameplay-connection-ticket", messageID: 0, kind: commonv1.MessageKind_MESSAGE_KIND_UNSPECIFIED, message: ticket, typeName: "ihomeland.session.v1.ConnectionTicket"},
	}
	definitions = append(definitions, buildWorldVisitGoldenDefinitions(endpoint)...)
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

// buildWorldVisitGoldenDefinitions 为每条新增 registry message 构造一个有意义的固定 payload。
// 共享 snapshot 只读复用，所有 identity、时间和 opaque credential 都是不可使用的示例值。
func buildWorldVisitGoldenDefinitions(endpoint *sessionv1.Endpoint) []goldenDefinition {
	assignment := worldv1.WorldAssignment_builder{
		PersonalWorldId:  proto.String("pworld_fixture_owner"),
		WorldInstanceId:  proto.String("winst_fixture_current"),
		Endpoint:         endpoint,
		Generation:       proto.Uint64(2),
		LeaseExpiresAtMs: proto.Int64(1_700_000_120_000),
	}.Build()
	world := worldv1.PersonalWorldSnapshot_builder{
		PersonalWorldId: proto.String("pworld_fixture_owner"),
		OwnerPlayerId:   proto.String("player_fixture_owner"),
		Lifecycle:       enumPointer(worldv1.PersonalWorldLifecycle_PERSONAL_WORLD_LIFECYCLE_ACTIVE),
		Revision:        proto.Uint64(3),
		CreatedAtMs:     proto.Int64(1_700_000_000_000),
	}.Build()
	worldSnapshot := worldv1.WorldSnapshot_builder{World: world, Assignment: assignment}.Build()
	visitor := visitv1.VisitVisitorSummary_builder{
		PlayerId: proto.String("player_fixture_visitor"),
		State:    enumPointer(visitv1.VisitMembershipState_VISIT_MEMBERSHIP_STATE_JOINED),
	}.Build()
	visitSnapshot := visitv1.VisitSessionSnapshot_builder{
		VisitSessionId: proto.String("visit_fixture_one"),
		OwnerPlayerId:  proto.String("player_fixture_owner"),
		Assignment:     assignment,
		Lifecycle:      enumPointer(visitv1.VisitLifecycle_VISIT_LIFECYCLE_OPEN),
		Revision:       proto.Uint64(5),
		Capacity:       proto.Uint32(4),
		Visitors:       []*visitv1.VisitVisitorSummary{visitor},
		CreatedAtMs:    proto.Int64(1_700_000_000_000),
		ExpiresAtMs:    proto.Int64(1_700_003_600_000),
	}.Build()
	invite := visitv1.VisitInviteSummary_builder{
		InviteId:        proto.String("invite_fixture_one"),
		VisitSessionId:  proto.String("visit_fixture_one"),
		TargetVisitorId: proto.String("player_fixture_visitor"),
		State:           enumPointer(visitv1.VisitInviteState_VISIT_INVITE_STATE_PENDING),
		CreatedRevision: proto.Uint64(4),
		ExpiresAtMs:     proto.Int64(1_700_000_060_000),
	}.Build()
	retiredInvite := visitv1.VisitInviteSummary_builder{
		InviteId:        proto.String("invite_fixture_retired"),
		VisitSessionId:  proto.String("visit_fixture_one"),
		TargetVisitorId: proto.String("player_fixture_visitor"),
		State:           enumPointer(visitv1.VisitInviteState_VISIT_INVITE_STATE_RETIRED),
		CreatedRevision: proto.Uint64(4),
		ExpiresAtMs:     proto.Int64(1_700_000_060_000),
	}.Build()
	directive := visitv1.SafeReturnDirective_builder{
		VisitSessionId: proto.String("visit_fixture_one"),
		VisitorId:      proto.String("player_fixture_visitor"),
		Reason:         enumPointer(visitv1.SafeReturnReason_SAFE_RETURN_REASON_OWNER_CLOSED),
		Preferred:      enumPointer(visitv1.SafeReturnDestination_SAFE_RETURN_DESTINATION_OWN_PERSONAL_WORLD),
		Fallback:       enumPointer(visitv1.SafeReturnDestination_SAFE_RETURN_DESTINATION_SAFE_ENTRY),
		Revision:       proto.Uint64(5),
	}.Build()
	mutation := visitv1.VisitMutationResult_builder{Snapshot: visitSnapshot, SafeReturns: []*visitv1.SafeReturnDirective{directive}}.Build()
	joinCredential := "fixture-join-admission-not-valid"
	reconnectCredential := "fixture-reconnect-admission-not-valid"
	return []goldenDefinition{
		{name: "gameplay-heartbeat-request", messageID: 1, kind: commonv1.MessageKind_MESSAGE_KIND_REQUEST, message: commonv1.GameplayHeartbeatRequest_builder{}.Build(), typeName: "ihomeland.common.v1.GameplayHeartbeatRequest"},
		{name: "gameplay-heartbeat-response", messageID: 2, kind: commonv1.MessageKind_MESSAGE_KIND_RESPONSE, message: commonv1.GameplayHeartbeatResponse_builder{}.Build(), typeName: "ihomeland.common.v1.GameplayHeartbeatResponse"},
		{name: "world-snapshot-request", messageID: 2000, kind: commonv1.MessageKind_MESSAGE_KIND_REQUEST, message: worldv1.WorldSnapshotRequest_builder{}.Build(), typeName: "ihomeland.world.v1.WorldSnapshotRequest"},
		{name: "world-snapshot-response", messageID: 2001, kind: commonv1.MessageKind_MESSAGE_KIND_RESPONSE, message: worldv1.WorldSnapshotResponse_builder{Snapshot: worldSnapshot}.Build(), typeName: "ihomeland.world.v1.WorldSnapshotResponse"},
		{name: "world-snapshot-push", messageID: 2002, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: worldv1.WorldSnapshotPush_builder{Snapshot: worldSnapshot}.Build(), typeName: "ihomeland.world.v1.WorldSnapshotPush"},
		{name: "world-assignment-changed-push", messageID: 2003, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: worldv1.WorldAssignmentChangedPush_builder{PersonalWorldId: proto.String("pworld_fixture_owner"), Assignment: assignment, ReasonKey: proto.String("world.assignment.changed.fixture")}.Build(), typeName: "ihomeland.world.v1.WorldAssignmentChangedPush"},
		{name: "visit-invite-push", messageID: 2100, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: visitv1.VisitInvitePush_builder{Invite: invite, OwnerPlayerId: proto.String("player_fixture_owner")}.Build(), typeName: "ihomeland.visit.v1.VisitInvitePush"},
		{name: "visit-invite-retired-push", messageID: 2100, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: visitv1.VisitInvitePush_builder{Invite: retiredInvite, OwnerPlayerId: proto.String("player_fixture_owner")}.Build(), typeName: "ihomeland.visit.v1.VisitInvitePush"},
		{name: "visit-owner-availability-push", messageID: 2101, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: visitv1.VisitOwnerAvailabilityPush_builder{VisitSessionId: proto.String("visit_fixture_one"), Available: proto.Bool(false), GraceExpiresAtMs: proto.Int64(1_700_000_030_000), Revision: proto.Uint64(6)}.Build(), typeName: "ihomeland.visit.v1.VisitOwnerAvailabilityPush"},
		{name: "visit-closed-notice-push", messageID: 2102, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: visitv1.VisitClosedNoticePush_builder{VisitSessionId: proto.String("visit_fixture_one"), Reason: enumPointer(visitv1.SafeReturnReason_SAFE_RETURN_REASON_OWNER_CLOSED), Revision: proto.Uint64(7)}.Build(), typeName: "ihomeland.visit.v1.VisitClosedNoticePush"},
		{name: "visit-open-command", messageID: 2103, kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, message: visitv1.VisitOpenCommand_builder{}.Build(), typeName: "ihomeland.visit.v1.VisitOpenCommand"},
		{name: "visit-open-response", messageID: 2104, kind: commonv1.MessageKind_MESSAGE_KIND_RESPONSE, message: visitv1.VisitOpenResponse_builder{Snapshot: visitSnapshot}.Build(), typeName: "ihomeland.visit.v1.VisitOpenResponse"},
		{name: "visit-create-invite-command", messageID: 2105, kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, message: visitv1.VisitCreateInviteCommand_builder{TargetVisitorId: proto.String("player_fixture_visitor"), InviteLifetimeMs: proto.Uint32(60_000), ExpectedRevision: proto.Uint64(3)}.Build(), typeName: "ihomeland.visit.v1.VisitCreateInviteCommand"},
		{name: "visit-create-invite-response", messageID: 2106, kind: commonv1.MessageKind_MESSAGE_KIND_RESPONSE, message: visitv1.VisitCreateInviteResponse_builder{Result: mutation, Invite: invite}.Build(), typeName: "ihomeland.visit.v1.VisitCreateInviteResponse"},
		{name: "visit-revoke-invite-command", messageID: 2107, kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, message: visitv1.VisitRevokeInviteCommand_builder{InviteId: proto.String("invite_fixture_one"), ExpectedRevision: proto.Uint64(4)}.Build(), typeName: "ihomeland.visit.v1.VisitRevokeInviteCommand"},
		{name: "visit-revoke-invite-response", messageID: 2108, kind: commonv1.MessageKind_MESSAGE_KIND_RESPONSE, message: visitv1.VisitRevokeInviteResponse_builder{Result: mutation}.Build(), typeName: "ihomeland.visit.v1.VisitRevokeInviteResponse"},
		{name: "visit-join-command", messageID: 2109, kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, message: visitv1.VisitJoinCommand_builder{AdmissionCredential: proto.String(joinCredential), ExpectedRevision: proto.Uint64(5)}.Build(), typeName: "ihomeland.visit.v1.VisitJoinCommand"},
		{name: "visit-join-response", messageID: 2110, kind: commonv1.MessageKind_MESSAGE_KIND_RESPONSE, message: visitv1.VisitJoinResponse_builder{Result: mutation}.Build(), typeName: "ihomeland.visit.v1.VisitJoinResponse"},
		{name: "visit-leave-command", messageID: 2111, kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, message: visitv1.VisitLeaveCommand_builder{ExpectedRevision: proto.Uint64(5)}.Build(), typeName: "ihomeland.visit.v1.VisitLeaveCommand"},
		{name: "visit-leave-response", messageID: 2112, kind: commonv1.MessageKind_MESSAGE_KIND_RESPONSE, message: visitv1.VisitLeaveResponse_builder{Result: mutation}.Build(), typeName: "ihomeland.visit.v1.VisitLeaveResponse"},
		{name: "visit-kick-command", messageID: 2113, kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, message: visitv1.VisitKickCommand_builder{TargetVisitorId: proto.String("player_fixture_visitor"), ExpectedRevision: proto.Uint64(5)}.Build(), typeName: "ihomeland.visit.v1.VisitKickCommand"},
		{name: "visit-kick-response", messageID: 2114, kind: commonv1.MessageKind_MESSAGE_KIND_RESPONSE, message: visitv1.VisitKickResponse_builder{Result: mutation}.Build(), typeName: "ihomeland.visit.v1.VisitKickResponse"},
		{name: "visit-reconnect-command", messageID: 2115, kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, message: visitv1.VisitReconnectCommand_builder{AdmissionCredential: proto.String(reconnectCredential), ExpectedRevision: proto.Uint64(6)}.Build(), typeName: "ihomeland.visit.v1.VisitReconnectCommand"},
		{name: "visit-reconnect-response", messageID: 2116, kind: commonv1.MessageKind_MESSAGE_KIND_RESPONSE, message: visitv1.VisitReconnectResponse_builder{Result: mutation}.Build(), typeName: "ihomeland.visit.v1.VisitReconnectResponse"},
		{name: "visit-close-command", messageID: 2117, kind: commonv1.MessageKind_MESSAGE_KIND_COMMAND, message: visitv1.VisitCloseCommand_builder{ExpectedRevision: proto.Uint64(6)}.Build(), typeName: "ihomeland.visit.v1.VisitCloseCommand"},
		{name: "visit-close-response", messageID: 2118, kind: commonv1.MessageKind_MESSAGE_KIND_RESPONSE, message: visitv1.VisitCloseResponse_builder{Result: mutation}.Build(), typeName: "ihomeland.visit.v1.VisitCloseResponse"},
		{name: "visit-snapshot-request", messageID: 2119, kind: commonv1.MessageKind_MESSAGE_KIND_REQUEST, message: visitv1.VisitSnapshotRequest_builder{}.Build(), typeName: "ihomeland.visit.v1.VisitSnapshotRequest"},
		{name: "visit-snapshot-response", messageID: 2120, kind: commonv1.MessageKind_MESSAGE_KIND_RESPONSE, message: visitv1.VisitSnapshotResponse_builder{Snapshot: visitSnapshot}.Build(), typeName: "ihomeland.visit.v1.VisitSnapshotResponse"},
		{name: "visit-snapshot-push", messageID: 2121, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: visitv1.VisitSnapshotPush_builder{Snapshot: visitSnapshot}.Build(), typeName: "ihomeland.visit.v1.VisitSnapshotPush"},
		{name: "visit-safe-return-push", messageID: 2122, kind: commonv1.MessageKind_MESSAGE_KIND_PUSH, message: visitv1.VisitSafeReturnPush_builder{Directive: directive}.Build(), typeName: "ihomeland.visit.v1.VisitSafeReturnPush"},
	}
}

// envelopeFor 为已登记实时消息分配稳定 correlation、sequence 与 timestamp。
// builder 会保留 payload slice，因此调用方在 envelope 完成编码前不得修改其内容。
func envelopeFor(index int, messageID uint32, kind commonv1.MessageKind, payload []byte) *commonv1.ReliableEnvelope {
	builder := commonv1.ReliableEnvelope_builder{ProtocolVersion: proto.Uint32(1), MessageId: proto.Uint32(messageID), Kind: enumPointer(kind), Sequence: proto.Uint64(uint64(index + 1)), TimestampMs: proto.Int64(1_700_000_000_000 + int64(index)), Payload: payload}
	switch kind {
	case commonv1.MessageKind_MESSAGE_KIND_REQUEST:
		builder.RequestId = bytes.Repeat([]byte{1}, 16)
	case commonv1.MessageKind_MESSAGE_KIND_COMMAND:
		builder.CommandId = bytes.Repeat([]byte{2}, 16)
	case commonv1.MessageKind_MESSAGE_KIND_RESPONSE:
		// Snapshot response 关联 request，其余首批 response 均关联 mutation command。
		if messageID == 2001 || messageID == 2120 {
			builder.RequestId = bytes.Repeat([]byte{1}, 16)
		} else {
			builder.CommandId = bytes.Repeat([]byte{2}, 16)
		}
	}
	return builder.Build()
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
