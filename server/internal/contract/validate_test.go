package contract

import (
	"os"
	"path/filepath"
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

// TestForbiddenActorFieldNames 防止 command schema 重新引入由 payload 决定的操作者身份。
// 测试覆盖 snake_case 与 camelCase；target_seat 作为合法反例，避免规则误伤业务目标字段。
func TestForbiddenActorFieldNames(t *testing.T) {
	for _, name := range []string{"actor_id", "accountId", "player_id", "userId"} {
		if !forbiddenActorField.MatchString(name) {
			t.Fatalf("expected %s to be forbidden", name)
		}
	}
	if forbiddenActorField.MatchString("target_seat") {
		t.Fatal("business target fields must remain legal")
	}
}
