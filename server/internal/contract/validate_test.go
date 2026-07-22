package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"google.golang.org/protobuf/reflect/protoregistry"
)

// TestValidateRepository 保护 schema、registry 与版本声明始终一致的仓库级契约。
// 测试不传官方 OpenAPI schema，避免单元测试依赖本机缓存；完整 schema 校验由 proto verify 门禁覆盖。
func TestValidateRepository(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(root, ""); err != nil {
		t.Fatal(err)
	}
}

// TestValidateCSharpProtobufRuntimeVersion 覆盖官方包锁定、发布线配对、摘要和程序集身份门禁。
func TestValidateCSharpProtobufRuntimeVersion(t *testing.T) {
	valid := csharpProtobufVersionValue{
		Version:         "3.35.0",
		Source:          "https://www.nuget.org/packages/Google.Protobuf/3.35.0",
		PackageURL:      "https://api.nuget.org/v3-flatcontainer/google.protobuf/3.35.0/google.protobuf.3.35.0.nupkg",
		PackageSHA256:   "7dbfa99660caf55c915acf305e091f7107d4c62b338d9a56e19b1ad9077359d1",
		TargetFramework: "netstandard2.0",
		DLLPath:         "lib/netstandard2.0/Google.Protobuf.dll",
		AssemblyName:    "Google.Protobuf",
		AssemblyVersion: "3.35.0.0",
		PublicKeyToken:  "a7d26565bac4d604",
	}
	if err := validateCSharpProtobufRuntimeVersion("35.0", valid); err != nil {
		t.Fatalf("valid C# runtime lock rejected: %v", err)
	}
	tests := []struct {
		// name 描述被破坏的单项供应链事实。
		name string
		// mutate 只改变一个字段，使失败原因保持可定位。
		mutate func(*csharpProtobufVersionValue)
	}{
		{name: "release-line", mutate: func(value *csharpProtobufVersionValue) { value.Version = "3.34.1" }},
		{name: "package-url", mutate: func(value *csharpProtobufVersionValue) { value.PackageURL = "https://example.invalid/package.nupkg" }},
		{name: "checksum", mutate: func(value *csharpProtobufVersionValue) { value.PackageSHA256 = "invalid" }},
		{name: "target", mutate: func(value *csharpProtobufVersionValue) { value.TargetFramework = "net5.0" }},
		{name: "identity", mutate: func(value *csharpProtobufVersionValue) { value.PublicKeyToken = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := validateCSharpProtobufRuntimeVersion("35.0", candidate); err == nil {
				t.Fatal("invalid C# runtime lock unexpectedly accepted")
			}
		})
	}
}

// TestBuildProjectionRejectsDuplicateRoute 防止单条消息获得第二个 transport 入口。
// 该回归会让 runtime 根据遍历顺序选择路由，因此必须在生成 projection 前确定性失败。
func TestBuildProjectionRejectsDuplicateRoute(t *testing.T) {
	catalog := Catalog{
		Messages: MessageRegistry{SchemaVersion: 1, Messages: []MessageEntry{{ID: 1, Name: "TEST"}}},
		Routes:   RouteRegistry{SchemaVersion: 1, Routes: []RouteEntry{{MessageID: 1}, {MessageID: 1}}},
	}
	if _, err := BuildProjection(catalog); err == nil {
		t.Fatal("expected duplicate route rejection")
	}
}

// TestBuildProjectionRejectsOrphanRoute 防止 projection 静默丢弃不存在于消息目录的 route。
func TestBuildProjectionRejectsOrphanRoute(t *testing.T) {
	catalog := Catalog{
		Messages: MessageRegistry{SchemaVersion: 1, Messages: []MessageEntry{{ID: 1, Name: "TEST"}}},
		Routes:   RouteRegistry{SchemaVersion: 1, Routes: []RouteEntry{{MessageID: 1}, {MessageID: 2}}},
	}
	if _, err := BuildProjection(catalog); err == nil {
		t.Fatal("expected orphan route rejection")
	}
}

// TestLookupRouteRejectsUnknownAndWrongChannel 验证 runtime lookup 不能按调用方偏好改变路由。
// 两个断言分别保护未知编号拒绝和“同一消息禁止跨 WSS/TLS_TCP 双入口”的核心边界。
func TestLookupRouteRejectsUnknownAndWrongChannel(t *testing.T) {
	catalog := Catalog{
		Messages: MessageRegistry{Messages: []MessageEntry{{ID: 500, Name: "CONTROL_MAINTENANCE_PUSH"}}},
		Routes:   RouteRegistry{Routes: []RouteEntry{{MessageID: 500, Channel: "WSS"}}},
	}
	if _, _, err := catalog.LookupRoute(9999, "WSS"); err == nil {
		t.Fatal("expected unknown message rejection")
	}
	if _, _, err := catalog.LookupRoute(500, "TLS_TCP"); err == nil {
		t.Fatal("expected wrong channel rejection")
	}
	catalog.Routes.Routes = append(catalog.Routes.Routes, RouteEntry{MessageID: 500, Channel: "WSS"})
	if _, _, err := catalog.LookupRoute(500, "WSS"); err == nil {
		t.Fatal("expected duplicate route rejection")
	}
}

// TestValidateErrorsRejectsDuplicateAndReservedCodes 保护稳定错误编号不会被重复或重新解释。
// 重复当前条目与复用退役编号都会使旧客户端产生歧义，因此两种来源必须分别覆盖。
func TestValidateErrorsRejectsDuplicateAndReservedCodes(t *testing.T) {
	entry := ErrorEntry{Code: 1, Name: "ONE", Owner: "common", Category: "PROTOCOL", MessageKey: "error.one", HTTPStatus: 400}
	if err := validateErrors(ErrorRegistry{SchemaVersion: 1, Errors: []ErrorEntry{entry, entry}}); err == nil {
		t.Fatal("expected duplicate error rejection")
	}
	if err := validateErrors(ErrorRegistry{SchemaVersion: 1, Reserved: []uint32{1}, Errors: []ErrorEntry{entry}}); err == nil {
		t.Fatal("expected reserved error rejection")
	}
	entry.Owner = "unknown"
	if err := validateErrors(ErrorRegistry{SchemaVersion: 1, Errors: []ErrorEntry{entry}}); err == nil {
		t.Fatal("expected unknown error owner rejection")
	}
}

// TestValidateMessagesRejectsInvalidGovernance 覆盖 registry 结构与跨字段策略的失败门禁。
// 每个子用例从真实 catalog 重新加载，避免前一项 mutation 污染后续断言。
func TestValidateMessagesRejectsInvalidGovernance(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		// name 描述被破坏的治理不变量。
		name string
		// mutate 只修改当前子用例私有的 catalog 快照。
		mutate func(*Catalog)
	}{
		{name: "unknown schema version", mutate: func(catalog *Catalog) { catalog.Messages.SchemaVersion = 2 }},
		{name: "overlapping owner ranges", mutate: func(catalog *Catalog) {
			catalog.Messages.OwnerRanges = append(catalog.Messages.OwnerRanges, OwnerRange{Owner: "overlap", Start: 900, End: 1100})
		}},
		{name: "invalid direction", mutate: func(catalog *Catalog) { catalog.Messages.Messages[0].Direction = "BOTH" }},
		{name: "missing route policy", mutate: func(catalog *Catalog) { catalog.Routes.Routes[0].QoS = "" }},
		{name: "push timeout", mutate: func(catalog *Catalog) { catalog.Routes.Routes[0].TimeoutMS = 1 }},
		{name: "push correlation strategy", mutate: func(catalog *Catalog) {
			catalog.Routes.Routes[len(catalog.Routes.Routes)-1].Idempotency = "REQUEST_ID"
		}},
		{name: "response correlation strategy", mutate: func(catalog *Catalog) {
			for index := range catalog.Messages.Messages {
				if catalog.Messages.Messages[index].Kind == "RESPONSE" {
					for routeIndex := range catalog.Routes.Routes {
						if catalog.Routes.Routes[routeIndex].MessageID == catalog.Messages.Messages[index].ID {
							catalog.Routes.Routes[routeIndex].Idempotency = "COMMAND_ID"
							return
						}
					}
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&catalog)
			if err := validateMessages(catalog, &protoregistryFiles{files: protoregistry.GlobalFiles}); err == nil {
				t.Fatal("expected invalid governance rejection")
			}
		})
	}
}

// TestReadJSONRejectsTrailingValue 防止合法 registry 后追加第二个 JSON 值绕过严格解码。
func TestReadJSONRejectsTrailingValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1}{"schemaVersion":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var registry MessageRegistry
	if err := readJSON(path, &registry); err == nil {
		t.Fatal("expected trailing JSON value rejection")
	}
}

// TestValidateOperationPolicyRejectsMissingOrContradictoryLimits 保护 HTTP adapter 所需的资源元数据。
// 测试直接构造 YAML 解码后的 map 形态，避免依赖官方 OpenAPI schema 是否理解项目扩展。
func TestValidateOperationPolicyRejectsMissingOrContradictoryLimits(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{
			"requestBody":                  map[string]any{},
			"x-ihomeland-body-limit-bytes": 4096,
			"x-ihomeland-timeout-ms":       5000,
			"x-ihomeland-idempotency":      "NON_IDEMPOTENT",
		}
	}
	tests := []struct {
		// name 标识被破坏的 operation policy。
		name string
		// mutate 从合法基线删除或改写一个约束。
		mutate func(map[string]any)
	}{
		{name: "missing timeout", mutate: func(operation map[string]any) { delete(operation, "x-ihomeland-timeout-ms") }},
		{name: "body without limit", mutate: func(operation map[string]any) { operation["x-ihomeland-body-limit-bytes"] = 0 }},
		{name: "unsafe get", mutate: func(operation map[string]any) { operation["x-ihomeland-idempotency"] = "NON_IDEMPOTENT" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation := valid()
			test.mutate(operation)
			method := "post"
			if test.name == "unsafe get" {
				method = "get"
			}
			if err := validateOperationPolicy(method, "testOperation", operation); err == nil {
				t.Fatal("expected invalid operation policy rejection")
			}
		})
	}
}

// TestValidateOperationPolicyRequiresIdempotencyHeader 固定一次性 admission 与 invite accept 的安全重试边界。
func TestValidateOperationPolicyRequiresIdempotencyHeader(t *testing.T) {
	operation := map[string]any{
		"requestBody":                  map[string]any{},
		"x-ihomeland-body-limit-bytes": 4096,
		"x-ihomeland-timeout-ms":       5000,
		"x-ihomeland-idempotency":      "IDEMPOTENCY_KEY_REQUIRED",
	}
	if err := validateOperationPolicy("post", "issueWorldAdmission", operation); err == nil {
		t.Fatal("expected missing Idempotency-Key rejection")
	}
	operation["parameters"] = []any{map[string]any{"$ref": "#/components/parameters/IdempotencyKey"}}
	if err := validateOperationPolicy("post", "issueWorldAdmission", operation); err != nil {
		t.Fatalf("valid idempotency-key operation rejected: %v", err)
	}
}

// validWorldHTTPSchemaDocument 构造 validator 单元测试使用的最小完整 world/visit HTTP 合同。
func validWorldHTTPSchemaDocument() (map[string]any, map[string]any) {
	closedSchema := func(properties map[string]any, required ...string) map[string]any {
		values := make([]any, len(required))
		for index, name := range required {
			values[index] = name
		}
		return map[string]any{"additionalProperties": false, "required": values, "properties": properties}
	}
	parameterRef := []any{map[string]any{"$ref": "#/components/parameters/IdempotencyKey"}}
	document := map[string]any{"components": map[string]any{
		"parameters": map[string]any{"IdempotencyKey": map[string]any{
			"name": "Idempotency-Key", "in": "header", "required": true,
			"schema": map[string]any{"type": "string", "minLength": 16, "maxLength": 128, "pattern": safeASCIIIdentityPattern},
		}},
		"schemas": map[string]any{
			"AcceptVisitInviteRequest":  closedSchema(map[string]any{"expectedRevision": map[string]any{"type": "integer"}}, "expectedRevision"),
			"OwnWorldAdmissionTarget":   closedSchema(map[string]any{"kind": map[string]any{"type": "string", "enum": []any{"OWN_WORLD"}}}, "kind"),
			"VisitWorldAdmissionTarget": closedSchema(map[string]any{"kind": map[string]any{"type": "string", "enum": []any{"VISIT_WORLD"}}, "visitSessionId": map[string]any{"type": "string"}}, "kind", "visitSessionId"),
			"WorldAdmissionRequest": map[string]any{"oneOf": []any{
				map[string]any{"$ref": "#/components/schemas/OwnWorldAdmissionTarget"},
				map[string]any{"$ref": "#/components/schemas/VisitWorldAdmissionTarget"},
			}},
			"WorldAssignment": closedSchema(map[string]any{
				"personalWorldId": map[string]any{"type": "string"}, "worldInstanceId": map[string]any{"type": "string"},
				"endpoint": map[string]any{"$ref": "#/components/schemas/GameplayEndpoint"}, "generation": map[string]any{"type": "integer"}, "leaseExpiresAtMs": map[string]any{"type": "integer"},
			}, "personalWorldId", "worldInstanceId", "endpoint", "generation", "leaseExpiresAtMs"),
			"WorldAdmissionResponse": closedSchema(map[string]any{
				"credential": map[string]any{"type": "string", "minLength": 32, "maxLength": 4096, "pattern": safeOpaqueCredentialPattern, "description": "opaque credential"},
				"endpoint":   map[string]any{"$ref": "#/components/schemas/GameplayEndpoint"}, "role": map[string]any{"type": "string", "enum": []any{"OWNER", "VISITOR"}},
				"purpose": map[string]any{"type": "string", "enum": []any{"OWN_WORLD", "JOIN", "RECONNECT"}}, "visitRevision": map[string]any{"type": "integer", "format": "int64", "minimum": 0}, "expiresAtMs": map[string]any{"type": "integer"},
			}, "credential", "endpoint", "role", "purpose", "visitRevision", "expiresAtMs"),
			"GameplayEndpoint": closedSchema(map[string]any{
				"channel": map[string]any{"type": "string", "enum": []any{"TLS_TCP"}}, "host": map[string]any{"type": "string"}, "port": map[string]any{"type": "integer"},
			}, "channel", "host", "port"),
		},
	}}
	paths := map[string]any{
		"/v1/visits/{visitSessionId}/invites/{inviteId}/accept": map[string]any{"post": map[string]any{"parameters": parameterRef}},
		"/v1/world/admissions": map[string]any{"post": map[string]any{"parameters": parameterRef}},
	}
	return document, paths
}

// TestValidateWorldHTTPSchemasRejectsIdentityExpansion 保护 admission target 只能表达受限目标选择。
func TestValidateWorldHTTPSchemasRejectsIdentityExpansion(t *testing.T) {
	document, paths := validWorldHTTPSchemaDocument()
	if err := validateWorldHTTPSchemas(document, paths); err != nil {
		t.Fatalf("valid world HTTP schemas rejected: %v", err)
	}
	target := document["components"].(map[string]any)["schemas"].(map[string]any)["VisitWorldAdmissionTarget"].(map[string]any)
	target["properties"].(map[string]any)["playerId"] = map[string]any{"type": "string"}
	if err := validateWorldHTTPSchemas(document, paths); err == nil {
		t.Fatal("actor identity expansion unexpectedly accepted")
	}
}

// TestValidateWorldHTTPSchemasRejectsAdmissionDrift 固定 credential、purpose 与 TLS/TCP endpoint 边界。
func TestValidateWorldHTTPSchemasRejectsAdmissionDrift(t *testing.T) {
	mutations := []struct {
		// name 标识被放松的 admission 合同维度。
		name string
		// mutate 只改变一个公开 schema 约束。
		mutate func(map[string]any)
	}{
		{name: "generic endpoint", mutate: func(schemas map[string]any) {
			schemas["WorldAdmissionResponse"].(map[string]any)["properties"].(map[string]any)["endpoint"] = map[string]any{"$ref": "#/components/schemas/Endpoint"}
		}},
		{name: "missing opaque description", mutate: func(schemas map[string]any) {
			delete(schemas["WorldAdmissionResponse"].(map[string]any)["properties"].(map[string]any)["credential"].(map[string]any), "description")
		}},
		{name: "unsafe credential alphabet", mutate: func(schemas map[string]any) {
			schemas["WorldAdmissionResponse"].(map[string]any)["properties"].(map[string]any)["credential"].(map[string]any)["pattern"] = `^.*$`
		}},
		{name: "expanded purpose", mutate: func(schemas map[string]any) {
			schemas["WorldAdmissionResponse"].(map[string]any)["properties"].(map[string]any)["purpose"].(map[string]any)["enum"] = []any{"OWN_WORLD", "JOIN", "RECONNECT", "ADMIN"}
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			document, paths := validWorldHTTPSchemaDocument()
			schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
			mutation.mutate(schemas)
			if err := validateWorldHTTPSchemas(document, paths); err == nil {
				t.Fatal("drifted admission schema unexpectedly accepted")
			}
		})
	}
}

// TestValidateWorldVisitErrorsRejectsRecoveryDrift 固定 stable error 的 owner、状态码与原样重试语义。
func TestValidateWorldVisitErrorsRejectsRecoveryDrift(t *testing.T) {
	entries := make([]ErrorEntry, 0, len(worldVisitErrorProfiles))
	for _, entry := range worldVisitErrorProfiles {
		entries = append(entries, entry)
	}
	if err := validateWorldVisitErrors(entries); err != nil {
		t.Fatalf("valid world/visit error profiles rejected: %v", err)
	}
	for index := range entries {
		if entries[index].Code == 2105 {
			entries[index].Retryable = true
			break
		}
	}
	if err := validateWorldVisitErrors(entries); err == nil {
		t.Fatal("revision conflict retryability drift unexpectedly accepted")
	}
}

// TestValidateOperationErrorResponseRejectsTransportLocalSchema 确保所有 HTTP route 复用 stable error envelope。
func TestValidateOperationErrorResponseRejectsTransportLocalSchema(t *testing.T) {
	operation := map[string]any{"responses": map[string]any{"default": map[string]any{"$ref": "#/components/responses/ErrorResponse"}}}
	if err := validateOperationErrorResponse("testOperation", operation); err != nil {
		t.Fatalf("shared error response rejected: %v", err)
	}
	operation["responses"].(map[string]any)["default"] = map[string]any{"description": "free text"}
	if err := validateOperationErrorResponse("testOperation", operation); err == nil {
		t.Fatal("transport-local error schema unexpectedly accepted")
	}
}

// TestForbiddenActorFieldNames 防止 command schema 重新引入由 payload 决定的操作者身份。
// 测试覆盖 snake_case 与 camelCase；target_seat 作为合法反例，避免规则误伤业务目标字段。
func TestForbiddenActorFieldNames(t *testing.T) {
	for _, name := range []string{"actor_id", "accountId", "player_id", "userId", "session_id", "sessionEpoch", "world_id", "personalWorldId", "world_instance_id", "role", "endpoint", "fencingToken", "assignment_stamp", "runtimeNodeId"} {
		if !forbiddenCommandIdentityField.MatchString(name) {
			t.Fatalf("expected %s to be forbidden", name)
		}
	}
	for _, name := range []string{"target_seat", "target_visitor_id", "player_profile", "world_view_revision"} {
		if forbiddenCommandIdentityField.MatchString(name) {
			t.Fatalf("business target field %s must remain legal", name)
		}
	}
}

// TestCommandDescriptorIdentityScan 覆盖合法target反例和嵌套assignment身份字段发现。
func TestCommandDescriptorIdentityScan(t *testing.T) {
	files := &protoregistryFiles{files: protoregistry.GlobalFiles}
	command, err := files.FindMessage("ihomeland.visit.v1.VisitCreateInviteCommand")
	if err != nil {
		t.Fatal(err)
	}
	if path, found := findForbiddenActorField(command, nil); found {
		t.Fatalf("valid target command rejected at %s", path)
	}
	assignment, err := files.FindMessage("ihomeland.world.v1.WorldAssignment")
	if err != nil {
		t.Fatal(err)
	}
	if path, found := findForbiddenActorField(assignment, nil); !found || path != "personal_world_id" {
		t.Fatalf("expected nested assignment identity detection, path=%q found=%v", path, found)
	}
}

// TestPublishedControlRegistryBaseline 固定已经发布的 control message、route 与 stable error 身份。
func TestPublishedControlRegistryBaseline(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	wantMessages := map[uint32]string{
		500: "CONTROL_MAINTENANCE_PUSH", 501: "CONTROL_FORCED_LOGOUT_PUSH", 502: "CONTROL_QUEUE_STATUS_PUSH",
		503: "CONTROL_ENDPOINT_UPDATE_PUSH", 504: "CONTROL_SESSION_INVALIDATED_PUSH",
	}
	for id, name := range wantMessages {
		message, route, err := catalog.LookupRoute(id, "WSS")
		if err != nil {
			t.Fatal(err)
		}
		if message.Name != name || message.Owner != "control" || message.Kind != "PUSH" || message.Direction != "SERVER_TO_CLIENT" || route.Idempotency != "NONE" {
			t.Fatalf("published control message %d changed incompatibly", id)
		}
	}
	wantErrors := map[uint32]string{1: "PROTOCOL_INVALID_ENVELOPE", 2: "PROTOCOL_UNSUPPORTED_VERSION", 100: "AUTH_UNAUTHENTICATED", 101: "AUTH_FORBIDDEN", 102: "AUTH_INVALID_CREDENTIALS", 103: "AUTH_TICKET_EXPIRED", 104: "ACCOUNT_USERNAME_TAKEN", 200: "VALIDATION_FAILED", 400: "RATE_LIMITED", 500: "DEPENDENCY_UNAVAILABLE", 501: "INTERNAL_ERROR"}
	seen := make(map[uint32]string, len(catalog.Errors.Errors))
	for _, entry := range catalog.Errors.Errors {
		seen[entry.Code] = entry.Name
	}
	for code, name := range wantErrors {
		if seen[code] != name {
			t.Fatalf("published error %d changed from %s to %s", code, name, seen[code])
		}
	}
}

// FuzzWorldAdmissionOneOfIdentityBoundary 验证 YAML/JSON map 解码后的额外字段不能扩张 admission one-of。
func FuzzWorldAdmissionOneOfIdentityBoundary(f *testing.F) {
	for _, seed := range []string{"playerId", "actor_id", "endpoint", "role", "kind", "visitSessionId", "futureField"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, property string) {
		document, paths := validWorldHTTPSchemaDocument()
		target := document["components"].(map[string]any)["schemas"].(map[string]any)["VisitWorldAdmissionTarget"].(map[string]any)
		allowed := property == "kind" || property == "visitSessionId"
		if !allowed {
			target["properties"].(map[string]any)[property] = map[string]any{"type": "string"}
		}
		err := validateWorldHTTPSchemas(document, paths)
		if allowed && err != nil {
			t.Fatalf("allowed property %q rejected: %v", property, err)
		}
		if !allowed && err == nil {
			t.Fatalf("extra property %q expanded admission identity", property)
		}
	})
}

// TestValidateWorldVisitRouteProfiles 固定 world/visit 的唯一通道、size、rate 与 timeout 合同。
func TestValidateWorldVisitRouteProfiles(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	message, route, err := catalog.LookupRoute(2103, "TLS_TCP")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorldVisitRoute(message, route); err != nil {
		t.Fatalf("valid visit command rejected: %v", err)
	}
	mutations := []struct {
		// name 标识被破坏的路由维度。
		name string
		// mutate 只改变一个已评审字段。
		mutate func(*RouteEntry)
	}{
		{name: "channel", mutate: func(value *RouteEntry) { value.Channel = "WSS" }},
		{name: "auth scope", mutate: func(value *RouteEntry) { value.AuthScope = "CONTROL" }},
		{name: "max size", mutate: func(value *RouteEntry) { value.MaxSize++ }},
		{name: "rate policy", mutate: func(value *RouteEntry) { value.RatePolicy = "server_world" }},
		{name: "idempotency", mutate: func(value *RouteEntry) { value.Idempotency = "REQUEST_ID" }},
		{name: "timeout", mutate: func(value *RouteEntry) { value.TimeoutMS++ }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			invalid := route
			mutation.mutate(&invalid)
			if err := validateWorldVisitRoute(message, invalid); err == nil {
				t.Fatal("drifted route unexpectedly accepted")
			}
		})
	}
	unknown := message
	unknown.ID = 2123
	if err := validateWorldVisitRoute(unknown, route); err == nil {
		t.Fatal("unreviewed world/visit message unexpectedly accepted")
	}
}

// TestValidateAccountHTTPSchemasRejectsLooseUsername 保护 OpenAPI 不得放宽或只描述账号字段边界。
func TestValidateAccountHTTPSchemasRejectsLooseUsername(t *testing.T) {
	validUsername := func() map[string]any {
		return map[string]any{"type": "string", "minLength": 3, "maxLength": 64, "pattern": accountUsernamePattern, "description": "canonical ASCII username"}
	}
	validPassword := func() map[string]any {
		return map[string]any{"type": "string", "maxLength": 128, "x-ihomeland-max-bytes": 128, "description": "raw UTF-8 byte budget"}
	}
	document := map[string]any{"components": map[string]any{"schemas": map[string]any{
		"RegisterRequest": map[string]any{"properties": map[string]any{"username": validUsername(), "password": validPassword(), "displayName": map[string]any{"description": "normalized Unicode display name"}}},
		"LoginRequest":    map[string]any{"properties": map[string]any{"username": validUsername(), "password": validPassword()}},
	}}}
	if err := validateAccountHTTPSchemas(document); err != nil {
		t.Fatalf("valid account schema rejected: %v", err)
	}
	register := document["components"].(map[string]any)["schemas"].(map[string]any)["RegisterRequest"].(map[string]any)
	register["properties"].(map[string]any)["username"].(map[string]any)["pattern"] = `^.*$`
	if err := validateAccountHTTPSchemas(document); err == nil {
		t.Fatal("loose username pattern unexpectedly accepted")
	}
	register["properties"].(map[string]any)["username"] = validUsername()
	register["properties"].(map[string]any)["password"].(map[string]any)["x-ihomeland-max-bytes"] = 256
	if err := validateAccountHTTPSchemas(document); err == nil {
		t.Fatal("loose password byte budget unexpectedly accepted")
	}
}

// TestAccountUsernamePatternMatchesDomain 验证公开 regex 对代表性输入执行与 account domain 相同的接受规则。
func TestAccountUsernamePatternMatchesDomain(t *testing.T) {
	pattern := regexp.MustCompile(accountUsernamePattern)
	tests := []struct {
		// value 是待执行公开 schema pattern 的 username。
		value string
		// valid 表示 account domain 是否允许该输入。
		valid bool
	}{
		{value: "a01", valid: true},
		{value: "Player.One", valid: true},
		{value: "a1", valid: false},
		{value: ".player", valid: false},
		{value: "player-", valid: false},
		{value: "player one", valid: false},
		{value: "playеr", valid: false},
	}
	for _, test := range tests {
		if pattern.MatchString(test.value) != test.valid {
			t.Fatalf("pattern acceptance for %q does not match domain", test.value)
		}
	}
}
