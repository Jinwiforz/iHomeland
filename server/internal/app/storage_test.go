package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/buildinfo"
)

// TestRunRejectsMissingStorageSecretBeforeLoggingOrNetwork 保护 secret 缺失在所有可观测副作用前失败。
func TestRunRejectsMissingStorageSecretBeforeLoggingOrNetwork(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "server.yaml")
	configBody := "environment: local\nstorage:\n  mysql:\n    passwordSecret: env:DEFINITELY_MISSING_STORAGE_SECRET\n"
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	result := Run(context.Background(), Options{
		ConfigPath: configPath,
		LookupEnv:  func(string) (string, bool) { return "", false },
		Output:     &output,
		BuildInfo:  buildinfo.Current(),
	})
	if result.Kind != ResultConfigError {
		t.Fatalf("Run() result = %+v", result)
	}
	if output.Len() != 0 {
		t.Fatalf("secret failure 前不应创建 logger 或写日志：%q", output.String())
	}
	if strings.Contains(ResultMessage(result), "DEFINITELY_MISSING_STORAGE_SECRET_VALUE") {
		t.Fatalf("result message 泄露 secret material：%s", ResultMessage(result))
	}
}
