package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLoadAppliesStrictPrecedence 验证默认值、文件和白名单环境覆盖具有唯一顺序。
func TestLoadAppliesStrictPrecedence(t *testing.T) {
	path := writeConfig(t, `environment: test
logging:
  level: warn
diagnostic:
  address: 127.0.0.1:19090
`)
	environment := map[string]string{
		"IHOMELAND_LOG_LEVEL":        "error",
		"IHOMELAND_SHUTDOWN_TIMEOUT": "250ms",
	}
	settings, err := Load(path, func(key string) (string, bool) {
		value, exists := environment[key]
		return value, exists
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Environment != "test" || settings.Logging.Level != "error" || settings.Logging.Format != "text" {
		t.Fatalf("unexpected precedence result: %+v", settings)
	}
	if settings.Runtime.ShutdownTimeout != 250*time.Millisecond {
		t.Fatalf("unexpected shutdown timeout: %s", settings.Runtime.ShutdownTimeout)
	}
	if !settings.DiagnosticIsLoopback() {
		t.Fatal("local diagnostic address should be loopback")
	}

	// 修改返回快照不能污染下一次 Load 的默认值或文件解码结果。
	settings.Logging.Level = "debug"
	again, err := Load(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if again.Logging.Level != "warn" {
		t.Fatalf("configuration snapshots share mutable state: %s", again.Logging.Level)
	}
}

// TestLoadRejectsUnknownAndTrailingData 防止拼写错误或多文档配置被静默忽略。
func TestLoadRejectsUnknownAndTrailingData(t *testing.T) {
	for name, contents := range map[string]string{
		"unknown":  "unknownField: true\n",
		"trailing": "environment: local\n---\nenvironment: test\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, contents), func(string) (string, bool) { return "", false }); err == nil {
				t.Fatal("invalid configuration should fail")
			}
		})
	}
}

// TestLoadEnvironmentErrorDoesNotEchoValue 确保部署 secret 或错误值不会进入启动诊断。
func TestLoadEnvironmentErrorDoesNotEchoValue(t *testing.T) {
	secretValue := "private-invalid-duration"
	_, err := Load(writeConfig(t, "environment: local\n"), func(key string) (string, bool) {
		if key == "IHOMELAND_SHUTDOWN_TIMEOUT" {
			return secretValue, true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("invalid environment override should fail")
	}
	if strings.Contains(err.Error(), secretValue) || !strings.Contains(err.Error(), "IHOMELAND_SHUTDOWN_TIMEOUT") {
		t.Fatalf("unsafe environment error: %v", err)
	}
}

// TestValidateRejectsUnsafeRanges 固定所有地址、格式和资源预算边界。
func TestValidateRejectsUnsafeRanges(t *testing.T) {
	tests := []func(*Config){
		func(value *Config) { value.Diagnostic.Address = "localhost:9090" },
		func(value *Config) { value.Diagnostic.Address = "127.0.0.1:0" },
		func(value *Config) { value.Diagnostic.MaxHeaderBytes = 100 },
		func(value *Config) { value.Runtime.ShutdownTimeout = 0 },
		func(value *Config) { value.Logging.Format = "pretty" },
	}
	for index, mutate := range tests {
		settings := Default()
		mutate(&settings)
		if err := settings.Validate(); err == nil {
			t.Fatalf("invalid configuration %d should fail", index)
		}
	}
}

// writeConfig 以仅当前用户可读权限创建独立配置 fixture。
func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
