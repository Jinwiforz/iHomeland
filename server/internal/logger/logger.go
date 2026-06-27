// Package logger 封装项目级结构化日志初始化。
package logger

import (
	"log/slog"
	"os"

	"ihomeland/server/internal/config"
)

// New 根据日志等级创建 slog logger。
func New(level string) (*slog.Logger, error) {
	parsedLevel, err := config.ParseLogLevel(level)
	if err != nil {
		return nil, err
	}

	options := &slog.HandlerOptions{Level: parsedLevel}
	handler := slog.NewJSONHandler(os.Stdout, options)
	return slog.New(handler), nil
}
