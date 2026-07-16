package testclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndpointManifestExample 验证交付示例同时包含公开 WSS、TLS_TCP、版本与资源边界。
func TestEndpointManifestExample(t *testing.T) {
	root := repositoryRoot(t)
	file, err := os.Open(filepath.Join(root, "shared", "contracts", "fixtures", "qualification", "endpoint-manifest.json"))
	if err != nil {
		t.Fatalf("open endpoint manifest example: %v", err)
	}
	defer file.Close()
	if _, err := LoadEndpointManifestExample(file); err != nil {
		t.Fatalf("load endpoint manifest example: %v", err)
	}

	openAPI, err := os.ReadFile(filepath.Join(root, "shared", "contracts", "http", "v1", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	for _, required := range []string{"VersionResponse:", "BootstrapConfigResponse:", "enum: [WSS, TLS_TCP]", "httpBodyBytes:", "realtimeFrameBytes:"} {
		if !strings.Contains(string(openAPI), required) {
			t.Fatalf("OpenAPI no longer owns endpoint example field %q", required)
		}
	}
}

// TestEndpointManifestRejectsClosedSchemaViolations 验证未知字段与缺失 channel 被拒绝。
func TestEndpointManifestRejectsClosedSchemaViolations(t *testing.T) {
	input := `{"schemaVersion":1,"version":{"protocolVersion":1,"minimumClientVersion":"0.1.0","serverVersion":"0.1.0"},"config":{"endpoints":[{"channel":"WSS","host":"127.0.0.1","port":1}],"limits":{"httpBodyBytes":1024,"realtimeFrameBytes":1024}}}`
	if _, err := LoadEndpointManifestExample(strings.NewReader(input)); err == nil {
		t.Fatal("endpoint manifest without TLS_TCP was accepted")
	}
	input = strings.Replace(input, `"schemaVersion":1`, `"schemaVersion":1,"internalEndpoint":"x"`, 1)
	if _, err := LoadEndpointManifestExample(strings.NewReader(input)); err == nil {
		t.Fatal("endpoint manifest with unknown field was accepted")
	}
}
