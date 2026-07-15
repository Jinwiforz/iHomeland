package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"gopkg.in/yaml.v3"
)

// TestOperationTableMatchesOpenAPI 验证method/path/operationId与资源、超时、幂等metadata精确一致。
func TestOperationTableMatchesOpenAPI(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "shared", "contracts", "http", "v1", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		// Paths 保存OpenAPI path/method节点。
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	want := make(map[string]Operation)
	for path, methods := range document.Paths {
		for method, node := range methods {
			if node.Kind != yaml.MappingNode {
				continue
			}
			var metadata struct {
				// OperationID 是冻结operation名称。
				OperationID string `yaml:"operationId"`
				// BodyLimit 是请求body字节上限。
				BodyLimit int64 `yaml:"x-ihomeland-body-limit-bytes"`
				// TimeoutMS 是operation deadline毫秒数。
				TimeoutMS int64 `yaml:"x-ihomeland-timeout-ms"`
				// Idempotency 是OpenAPI登记的幂等模式。
				Idempotency string `yaml:"x-ihomeland-idempotency"`
				// Security 保存是否显式声明anonymous security。
				Security yaml.Node `yaml:"security"`
			}
			if err := node.Decode(&metadata); err != nil {
				t.Fatal(err)
			}
			if metadata.OperationID == "" {
				continue
			}
			mode := map[string]IdempotencyMode{"SAFE": IdempotencySafe, "IDEMPOTENT": IdempotencyIdempotent, "NON_IDEMPOTENT": IdempotencyNonIdempotent, "IDEMPOTENCY_KEY_REQUIRED": IdempotencyKeyRequired}[metadata.Idempotency]
			normalized := strings.NewReplacer("{visitSessionId}", ":visitSessionId", "{inviteId}", ":inviteId").Replace(path)
			authenticated := metadata.Security.Kind == 0
			want[metadata.OperationID] = Operation{ID: metadata.OperationID, Method: strings.ToUpper(method), Path: normalized, Authenticated: authenticated, BodyLimit: metadata.BodyLimit, Timeout: time.Duration(metadata.TimeoutMS) * time.Millisecond, Idempotency: mode}
		}
	}
	if len(want) != len(operations) {
		t.Fatalf("OpenAPI operations=%d runtime=%d", len(want), len(operations))
	}
	configuredRates := config.DefaultPublicAPI().Rates
	if len(configuredRates) != len(operations) {
		t.Fatalf("default rate policies=%d runtime operations=%d", len(configuredRates), len(operations))
	}
	for _, operation := range operations {
		expected, exists := want[operation.ID]
		if !exists || expected.Method != operation.Method || expected.Path != operation.Path || expected.Authenticated != operation.Authenticated || expected.BodyLimit != operation.BodyLimit || expected.Timeout != operation.Timeout || expected.Idempotency != operation.Idempotency {
			t.Fatalf("operation %s drifted: runtime=%+v OpenAPI=%+v", operation.ID, operation, expected)
		}
		if _, exists := configuredRates[operation.ID]; !exists {
			t.Fatalf("operation %s lacks a default rate policy", operation.ID)
		}
	}
}
