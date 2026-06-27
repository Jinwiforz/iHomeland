package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg.HTTPAddr != defaultHTTPAddr {
		t.Fatalf("HTTPAddr = %q, want %q", cfg.HTTPAddr, defaultHTTPAddr)
	}
	if cfg.LogLevel != defaultLogLevel {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, defaultLogLevel)
	}
	if cfg.ProtocolVersion != defaultProtocolVersion {
		t.Fatalf("ProtocolVersion = %d, want %d", cfg.ProtocolVersion, defaultProtocolVersion)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv(envHTTPAddr, "127.0.0.1:9090")
	t.Setenv(envLogLevel, "debug")
	t.Setenv(envProtocolVersion, "7")
	t.Setenv(envReleasePath, "testdata/release.json")
	t.Setenv(envServerVersionPath, "testdata/server.json")
	t.Setenv(envClientVersionPath, "testdata/client.json")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:9090" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.ProtocolVersion != 7 {
		t.Fatalf("ProtocolVersion = %d", cfg.ProtocolVersion)
	}
	if cfg.ReleasePath != "testdata/release.json" {
		t.Fatalf("ReleasePath = %q", cfg.ReleasePath)
	}
}

func TestLoadFromEnvReadsConfigFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "local.yaml")
	content := []byte(`
httpAddr: "127.0.0.1:9091"
logLevel: "warn"
protocolVersion: 9
releasePath: "../test-release.json"
serverVersionPath: "test-server.json"
clientVersionPath: "../test-client.json"
`)
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	t.Setenv(envConfigPath, configPath)

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:9091" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.ProtocolVersion != 9 {
		t.Fatalf("ProtocolVersion = %d", cfg.ProtocolVersion)
	}
}

func TestLoadFromEnvAllowsEnvironmentOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "local.yaml")
	content := []byte(`httpAddr: "127.0.0.1:9091"`)
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	t.Setenv(envConfigPath, configPath)
	t.Setenv(envHTTPAddr, "127.0.0.1:9092")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:9092" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
}

func TestLoadFromEnvRejectsInvalidProtocolVersionEnv(t *testing.T) {
	t.Setenv(envProtocolVersion, "not-a-number")

	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("LoadFromEnv() error = nil, want error")
	}
}

func TestValidateRejectsInvalidHTTPAddr(t *testing.T) {
	cfg := Default()
	cfg.HTTPAddr = "not-an-addr"

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  slog.Level
	}{
		{name: "debug", input: "debug", want: slog.LevelDebug},
		{name: "info", input: "info", want: slog.LevelInfo},
		{name: "warn", input: "warn", want: slog.LevelWarn},
		{name: "warning", input: "warning", want: slog.LevelWarn},
		{name: "error", input: "error", want: slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLogLevel(tt.input)
			if err != nil {
				t.Fatalf("ParseLogLevel(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseLogLevelRejectsUnsupportedLevel(t *testing.T) {
	if _, err := ParseLogLevel("verbose"); err == nil {
		t.Fatal("ParseLogLevel() error = nil, want error")
	}
}
