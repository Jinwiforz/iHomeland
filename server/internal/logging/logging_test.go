package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/config"
)

// TestLoggerRedactsSensitiveAttributes 防止常见凭据字段因调用者失误进入日志。
func TestLoggerRedactsSensitiveAttributes(t *testing.T) {
	var output bytes.Buffer
	logger := New(config.Logging{Level: "info", Format: "json"}, &output)
	logger.Info("login", "access_token", "token-value", "password", "password-value", "component", "test", slog.Group("authorization", slog.String("value", "bearer-value")))
	text := output.String()
	if strings.Contains(text, "token-value") || strings.Contains(text, "password-value") || strings.Contains(text, "bearer-value") {
		t.Fatalf("sensitive value leaked: %s", text)
	}
	if strings.Count(text, redactedValue) != 3 {
		t.Fatalf("expected three redacted attributes: %s", text)
	}
	errorLogger := New(config.Logging{Level: "error", Format: "json"}, &output)
	errorLogger.Info("hidden")
	if strings.Contains(output.String(), "hidden") {
		t.Fatal("updated level should suppress info logs")
	}
}
