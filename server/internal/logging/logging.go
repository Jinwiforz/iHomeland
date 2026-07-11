// Package logging 创建符合项目字段与脱敏规则的 slog logger。
package logging

import (
	"io"
	"log/slog"
	"strings"

	"github.com/jinwiforz/ihomeland/server/internal/config"
)

// redactedValue 是日志中替换敏感属性值的稳定占位符。
const redactedValue = "[REDACTED]"

// New 创建使用启动配置固定最低级别的 logger，调用方可安全派生 component 属性。
//
// Runtime 不支持热重载，因此不暴露 LevelVar，避免出现只有测试使用的可变配置入口。
func New(settings config.Logging, output io.Writer) *slog.Logger {
	options := &slog.HandlerOptions{Level: parseLevel(settings.Level), ReplaceAttr: redactAttribute}
	var handler slog.Handler
	if settings.Format == "json" {
		handler = slog.NewJSONHandler(output, options)
	} else {
		handler = slog.NewTextHandler(output, options)
	}
	return slog.New(handler)
}

// parseLevel 将已验证配置映射到 slog，不为未知值创建隐式级别。
func parseLevel(value string) slog.Level {
	switch value {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// redactAttribute 检查完整 group 路径和字段名，防止凭据藏在 authorization 等分组下绕过脱敏。
func redactAttribute(groups []string, attribute slog.Attr) slog.Attr {
	for _, group := range groups {
		if containsSensitiveFragment(group) {
			return slog.String(attribute.Key, redactedValue)
		}
	}
	if containsSensitiveFragment(attribute.Key) {
		return slog.String(attribute.Key, redactedValue)
	}
	return attribute
}

// containsSensitiveFragment 使用保守片段匹配覆盖常见凭据键；误脱敏优先于凭据泄漏。
func containsSensitiveFragment(value string) bool {
	key := strings.ToLower(value)
	for _, fragment := range []string{"password", "token", "ticket", "secret", "authorization", "cookie"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}
