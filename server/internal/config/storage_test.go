package config

import (
	"strings"
	"testing"
	"time"
)

// TestDefaultStorageValidatesForLocal 保护本地 Docker 默认值能在无环境覆盖时通过纯校验。
func TestDefaultStorageValidatesForLocal(t *testing.T) {
	t.Parallel()

	if err := Default().Validate(); err != nil {
		t.Fatalf("默认 storage 配置应有效：%v", err)
	}
}

// TestStorageValidationRejectsUnsafeValues 覆盖 endpoint、pool、probe、secret 与 TLS 的交叉约束。
func TestStorageValidationRejectsUnsafeValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// name 描述被破坏的 storage 不变量。
		name string
		// mutate 只修改默认有效配置中的目标字段。
		mutate func(*Config)
		// want 是不包含原始配置值的稳定错误片段。
		want string
	}{
		{name: "缺少端口", mutate: func(config *Config) { config.Storage.MySQL.Address = "mysql" }, want: "address"},
		{name: "非法 DNS", mutate: func(config *Config) { config.Storage.MySQL.Address = "bad_host:3306" }, want: "valid DNS"},
		{name: "非法数据库名", mutate: func(config *Config) { config.Storage.MySQL.Database = "bad name" }, want: "database"},
		{name: "相对 secret 文件", mutate: func(config *Config) { config.Storage.Redis.PasswordSecret = "file:secret" }, want: "absolute"},
		{name: "MySQL idle 超过 open", mutate: func(config *Config) { config.Storage.MySQL.Pool.MaxIdleConns = 17 }, want: "maxIdleConns"},
		{name: "Redis database 越界", mutate: func(config *Config) { config.Storage.Redis.Database = 16 }, want: "between 0 and 15"},
		{name: "probe timeout 超过 interval", mutate: func(config *Config) { config.Storage.Redis.Probe.Timeout = 6 * time.Second }, want: "must not exceed"},
		{name: "禁用 TLS 却保留材料", mutate: func(config *Config) { config.Storage.MySQL.TLS.ServerName = "mysql.internal" }, want: "disabled tls"},
		{name: "mTLS certificate 缺少 key", mutate: func(config *Config) {
			config.Storage.Redis.TLS = StorageTLS{Enabled: true, ServerName: "redis.internal", CAFile: `C:\certs\ca.pem`, ClientCertificateFile: `C:\certs\client.pem`}
		}, want: "configured together"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := Default()
			test.mutate(&config)
			err := config.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v，应包含 %q", err, test.want)
			}
		})
	}
}

// TestProductionStorageRequiresVerifiedTLS 防止 production 明文或缺少 CA/server name 的连接进入 runtime。
func TestProductionStorageRequiresVerifiedTLS(t *testing.T) {
	t.Parallel()

	config := Default()
	config.Environment = "production"
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "tls must be enabled") {
		t.Fatalf("production plaintext error = %v", err)
	}

	config.Storage.MySQL.TLS = StorageTLS{Enabled: true, ServerName: "mysql.internal", CAFile: `C:\certs\mysql-ca.pem`}
	config.Storage.Redis.TLS = StorageTLS{Enabled: true, ServerName: "redis.internal", CAFile: `C:\certs\redis-ca.pem`}
	if err := config.Validate(); err != nil {
		t.Fatalf("production verified TLS 配置应有效：%v", err)
	}
}

// TestStorageEnvironmentOverridesAreExplicitAndRedacted 验证白名单覆盖并确保非法 secret 原值不进入错误。
func TestStorageEnvironmentOverridesAreExplicitAndRedacted(t *testing.T) {
	t.Parallel()

	const invalidSecret = "not-a-reference-with-sensitive-material"
	path := writeConfig(t, "environment: local\n")
	overrides := map[string]string{
		"IHOMELAND_MYSQL_ADDRESS":            "mysql.local:3307",
		"IHOMELAND_MYSQL_MAX_OPEN_CONNS":     "24",
		"IHOMELAND_REDIS_DATABASE":           "2",
		"IHOMELAND_REDIS_PASSWORD_SECRET":    "env:REDIS_TEST_PASSWORD",
		"IHOMELAND_REDIS_PROBE_INTERVAL":     "8s",
		"IHOMELAND_REDIS_TLS_ENABLED":        "false",
		"IHOMELAND_UNREGISTERED_STORAGE_KEY": "ignored",
	}
	config, err := Load(path, func(key string) (string, bool) {
		value, exists := overrides[key]
		return value, exists
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.Storage.MySQL.Address != "mysql.local:3307" || config.Storage.MySQL.Pool.MaxOpenConns != 24 {
		t.Fatalf("MySQL overrides 未生效：%+v", config.Storage.MySQL)
	}
	if config.Storage.Redis.Database != 2 || config.Storage.Redis.Probe.Interval != 8*time.Second {
		t.Fatalf("Redis overrides 未生效：%+v", config.Storage.Redis)
	}

	_, err = Load(path, func(key string) (string, bool) {
		if key == "IHOMELAND_MYSQL_PASSWORD_SECRET" {
			return invalidSecret, true
		}
		return "", false
	})
	if err == nil || strings.Contains(err.Error(), invalidSecret) {
		t.Fatalf("secret reference 校验错误必须脱敏：%v", err)
	}
}

// TestStorageUnknownYAMLFieldRejected 防止拼写错误被默认值静默掩盖。
func TestStorageUnknownYAMLFieldRejected(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, "storage:\n  mysql:\n    unknownPoolPolicy: 1\n")
	_, err := Load(path, func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "field unknownPoolPolicy not found") {
		t.Fatalf("unknown storage field error = %v", err)
	}
}
