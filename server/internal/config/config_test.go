package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg.HTTPAddr != defaultHTTPAddr {
		t.Fatalf("HTTPAddr = %q, want %q", cfg.HTTPAddr, defaultHTTPAddr)
	}
	if cfg.Env != defaultEnv {
		t.Fatalf("Env = %q, want %q", cfg.Env, defaultEnv)
	}
	if cfg.LogLevel != defaultLogLevel {
		t.Fatalf("LogLevel = %q, want %q", cfg.LogLevel, defaultLogLevel)
	}
	if cfg.ProtocolVersion != defaultProtocolVersion {
		t.Fatalf("ProtocolVersion = %d, want %d", cfg.ProtocolVersion, defaultProtocolVersion)
	}
	if cfg.MySQL.Addr != defaultMySQLAddr {
		t.Fatalf("MySQL.Addr = %q, want %q", cfg.MySQL.Addr, defaultMySQLAddr)
	}
	if cfg.MySQL.Database != defaultMySQLDatabase {
		t.Fatalf("MySQL.Database = %q, want %q", cfg.MySQL.Database, defaultMySQLDatabase)
	}
	if cfg.MySQL.User != defaultMySQLUser {
		t.Fatalf("MySQL.User = %q, want %q", cfg.MySQL.User, defaultMySQLUser)
	}
	if cfg.MySQL.MaxOpenConns != defaultMySQLMaxOpenConns {
		t.Fatalf("MySQL.MaxOpenConns = %d, want %d", cfg.MySQL.MaxOpenConns, defaultMySQLMaxOpenConns)
	}
	if cfg.MySQL.MaxIdleConns != defaultMySQLMaxIdleConns {
		t.Fatalf("MySQL.MaxIdleConns = %d, want %d", cfg.MySQL.MaxIdleConns, defaultMySQLMaxIdleConns)
	}
	if cfg.MySQL.ConnMaxLifetime != defaultMySQLConnMaxLife {
		t.Fatalf("MySQL.ConnMaxLifetime = %s, want %s", cfg.MySQL.ConnMaxLifetime, defaultMySQLConnMaxLife)
	}
	if cfg.Redis.Addr != defaultRedisAddr {
		t.Fatalf("Redis.Addr = %q, want %q", cfg.Redis.Addr, defaultRedisAddr)
	}
	if cfg.Redis.DB != defaultRedisDB {
		t.Fatalf("Redis.DB = %d, want %d", cfg.Redis.DB, defaultRedisDB)
	}
	if cfg.Redis.DialTimeout != defaultRedisDialTimeout {
		t.Fatalf("Redis.DialTimeout = %s, want %s", cfg.Redis.DialTimeout, defaultRedisDialTimeout)
	}
	if cfg.Gateway.IdleTimeout != defaultGatewayIdleTimeout {
		t.Fatalf("Gateway.IdleTimeout = %s, want %s", cfg.Gateway.IdleTimeout, defaultGatewayIdleTimeout)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv(envHTTPAddr, "127.0.0.1:9090")
	t.Setenv(envName, "prod")
	t.Setenv(envLogLevel, "debug")
	t.Setenv(envProtocolVersion, "7")
	t.Setenv(envReleasePath, "testdata/release.json")
	t.Setenv(envServerVersionPath, "testdata/server.json")
	t.Setenv(envClientVersionPath, "testdata/client.json")
	t.Setenv(envMySQLAddr, "127.0.0.1:33306")
	t.Setenv(envMySQLDatabase, "ihomeland_test")
	t.Setenv(envMySQLUser, "ihomeland_test")
	t.Setenv(envMySQLPassword, "secret")
	t.Setenv(envMySQLMaxOpenConns, "20")
	t.Setenv(envMySQLMaxIdleConns, "10")
	t.Setenv(envMySQLConnMaxLife, "15m")
	t.Setenv(envRedisAddr, "127.0.0.1:36379")
	t.Setenv(envRedisPassword, "redis-secret")
	t.Setenv(envRedisDB, "2")
	t.Setenv(envRedisDialTimeout, "3s")
	t.Setenv(envGatewayIdleTimeout, "45s")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:9090" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.Env != "prod" {
		t.Fatalf("Env = %q", cfg.Env)
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
	if cfg.MySQL.Addr != "127.0.0.1:33306" {
		t.Fatalf("MySQL.Addr = %q", cfg.MySQL.Addr)
	}
	if cfg.MySQL.Database != "ihomeland_test" {
		t.Fatalf("MySQL.Database = %q", cfg.MySQL.Database)
	}
	if cfg.MySQL.User != "ihomeland_test" {
		t.Fatalf("MySQL.User = %q", cfg.MySQL.User)
	}
	if cfg.MySQL.Password != "secret" {
		t.Fatalf("MySQL.Password = %q", cfg.MySQL.Password)
	}
	if cfg.MySQL.MaxOpenConns != 20 || cfg.MySQL.MaxIdleConns != 10 || cfg.MySQL.ConnMaxLifetime != 15*time.Minute {
		t.Fatalf("MySQL pool config = %+v", cfg.MySQL)
	}
	if cfg.Redis.Addr != "127.0.0.1:36379" {
		t.Fatalf("Redis.Addr = %q", cfg.Redis.Addr)
	}
	if cfg.Redis.Password != "redis-secret" || cfg.Redis.DB != 2 || cfg.Redis.DialTimeout != 3*time.Second {
		t.Fatalf("Redis config = %+v", cfg.Redis)
	}
	if cfg.Gateway.IdleTimeout != 45*time.Second {
		t.Fatalf("Gateway.IdleTimeout = %s", cfg.Gateway.IdleTimeout)
	}
}

func TestLoadFromEnvReadsConfigFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "local.yaml")
	content := []byte(`
env: "stage"
httpAddr: "127.0.0.1:9091"
logLevel: "warn"
protocolVersion: 9
releasePath: "../test-release.json"
serverVersionPath: "test-server.json"
clientVersionPath: "../test-client.json"
mysql:
  addr: "127.0.0.1:33306"
  database: "ihomeland_file"
  user: "ihomeland_file"
  password: "file-secret"
  maxOpenConns: 12
  maxIdleConns: 4
  connMaxLifetime: "12m"
redis:
  addr: "127.0.0.1:36379"
  password: "file-redis-secret"
  db: 3
  dialTimeout: "4s"
gateway:
  idleTimeout: "20s"
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
	if cfg.Env != "stage" {
		t.Fatalf("Env = %q", cfg.Env)
	}
	if cfg.LogLevel != "warn" {
		t.Fatalf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.ProtocolVersion != 9 {
		t.Fatalf("ProtocolVersion = %d", cfg.ProtocolVersion)
	}
	if cfg.MySQL.Addr != "127.0.0.1:33306" {
		t.Fatalf("MySQL.Addr = %q", cfg.MySQL.Addr)
	}
	if cfg.MySQL.Database != "ihomeland_file" {
		t.Fatalf("MySQL.Database = %q", cfg.MySQL.Database)
	}
	if cfg.MySQL.User != "ihomeland_file" {
		t.Fatalf("MySQL.User = %q", cfg.MySQL.User)
	}
	if cfg.MySQL.Password != "file-secret" || cfg.MySQL.MaxOpenConns != 12 || cfg.MySQL.MaxIdleConns != 4 || cfg.MySQL.ConnMaxLifetime != 12*time.Minute {
		t.Fatalf("MySQL file config = %+v", cfg.MySQL)
	}
	if cfg.Redis.Addr != "127.0.0.1:36379" {
		t.Fatalf("Redis.Addr = %q", cfg.Redis.Addr)
	}
	if cfg.Redis.Password != "file-redis-secret" || cfg.Redis.DB != 3 || cfg.Redis.DialTimeout != 4*time.Second {
		t.Fatalf("Redis file config = %+v", cfg.Redis)
	}
	if cfg.Gateway.IdleTimeout != 20*time.Second {
		t.Fatalf("Gateway.IdleTimeout = %s", cfg.Gateway.IdleTimeout)
	}
}

func TestLoadFromEnvAllowsEnvironmentOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "local.yaml")
	content := []byte(`
httpAddr: "127.0.0.1:9091"
mysql:
  addr: "127.0.0.1:33306"
redis:
  addr: "127.0.0.1:36379"
`)
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	t.Setenv(envConfigPath, configPath)
	t.Setenv(envHTTPAddr, "127.0.0.1:9092")
	t.Setenv(envMySQLAddr, "127.0.0.1:33307")
	t.Setenv(envRedisAddr, "127.0.0.1:36380")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if cfg.HTTPAddr != "127.0.0.1:9092" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.MySQL.Addr != "127.0.0.1:33307" {
		t.Fatalf("MySQL.Addr = %q", cfg.MySQL.Addr)
	}
	if cfg.Redis.Addr != "127.0.0.1:36380" {
		t.Fatalf("Redis.Addr = %q", cfg.Redis.Addr)
	}
}

func TestLoadFromEnvRejectsInvalidProtocolVersionEnv(t *testing.T) {
	t.Setenv(envProtocolVersion, "not-a-number")

	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("LoadFromEnv() error = nil, want error")
	}
}

func TestLoadFromEnvRejectsInvalidGatewayIdleTimeoutEnv(t *testing.T) {
	t.Setenv(envGatewayIdleTimeout, "not-a-duration")

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

func TestValidateRejectsInvalidGatewayIdleTimeout(t *testing.T) {
	cfg := Default()
	cfg.Gateway.IdleTimeout = 0

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want error")
	}
}

func TestValidateRejectsInvalidInfraAddr(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "mysql",
			mutate: func(cfg *Config) {
				cfg.MySQL.Addr = "not-an-addr"
			},
		},
		{
			name: "redis",
			mutate: func(cfg *Config) {
				cfg.Redis.Addr = "not-an-addr"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestValidateRejectsInvalidStorageOptions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "mysql max open",
			mutate: func(cfg *Config) {
				cfg.MySQL.MaxOpenConns = 0
			},
		},
		{
			name: "mysql max idle",
			mutate: func(cfg *Config) {
				cfg.MySQL.MaxIdleConns = cfg.MySQL.MaxOpenConns + 1
			},
		},
		{
			name: "mysql conn lifetime",
			mutate: func(cfg *Config) {
				cfg.MySQL.ConnMaxLifetime = 0
			},
		},
		{
			name: "redis db",
			mutate: func(cfg *Config) {
				cfg.Redis.DB = -1
			},
		},
		{
			name: "redis dial timeout",
			mutate: func(cfg *Config) {
				cfg.Redis.DialTimeout = 0
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
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
