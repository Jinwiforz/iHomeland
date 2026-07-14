package config

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/secret"
)

const (
	// maximumPoolSize 防止配置错误为单进程创建无界连接。
	maximumPoolSize = 256
	// maximumStorageLifetime 限制陈旧连接在 pool 中存活的最长时间。
	maximumStorageLifetime = 24 * time.Hour
)

var (
	// storageNamePattern 约束 database、username 与稳定 metadata 名称，禁止空白和控制字符。
	storageNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	// dnsLabelPattern 逐段验证 storage DNS endpoint，允许 IP 由 net.ParseIP 独立处理。
	dnsLabelPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

// Storage 保存 MySQL 与 Redis runtime 的非敏感配置快照。
type Storage struct {
	// MySQL 定义持久事实数据库的连接和资源边界。
	MySQL MySQL `yaml:"mysql"`
	// Redis 定义可恢复运行态数据库的连接和资源边界。
	Redis Redis `yaml:"redis"`
}

// MySQL 定义唯一 MySQL pool、migration 和 probe 的配置。
type MySQL struct {
	// Address 是包含主机和非零端口的单一 endpoint。
	Address string `yaml:"address"`
	// Database 是 runtime 唯一允许访问的逻辑数据库名。
	Database string `yaml:"database"`
	// Username 是不包含凭据的数据库账号名。
	Username string `yaml:"username"`
	// PasswordSecret 是由 SecretProvider 解析的 env: 或绝对 file: reference。
	PasswordSecret string `yaml:"passwordSecret"`
	// ConnectTimeout 限制建立新连接的耗时。
	ConnectTimeout time.Duration `yaml:"connectTimeout"`
	// ReadTimeout 限制 driver socket read 的耗时。
	ReadTimeout time.Duration `yaml:"readTimeout"`
	// WriteTimeout 限制 driver socket write 的耗时。
	WriteTimeout time.Duration `yaml:"writeTimeout"`
	// MigrationLockTimeout 限制等待 migration advisory lock 的耗时。
	MigrationLockTimeout time.Duration `yaml:"migrationLockTimeout"`
	// Pool 定义 database/sql 的容量和连接寿命。
	Pool SQLPool `yaml:"pool"`
	// Probe 定义 required dependency 的周期健康检查策略。
	Probe Probe `yaml:"probe"`
	// TLS 定义连接身份验证策略；production 必须启用。
	TLS StorageTLS `yaml:"tls"`
}

// Redis 定义唯一 standalone Redis client 和 probe 的配置。
type Redis struct {
	// Address 是包含主机和非零端口的单一 endpoint。
	Address string `yaml:"address"`
	// Database 是 standalone Redis 的逻辑数据库编号。
	Database int `yaml:"database"`
	// Username 是可选 ACL username，不包含凭据。
	Username string `yaml:"username"`
	// PasswordSecret 是由 SecretProvider 解析的 env: 或绝对 file: reference。
	PasswordSecret string `yaml:"passwordSecret"`
	// DialTimeout 限制建立新连接的耗时。
	DialTimeout time.Duration `yaml:"dialTimeout"`
	// ReadTimeout 限制单次 command response read 的耗时。
	ReadTimeout time.Duration `yaml:"readTimeout"`
	// WriteTimeout 限制单次 command request write 的耗时。
	WriteTimeout time.Duration `yaml:"writeTimeout"`
	// Pool 定义 Redis connection pool 的容量和连接寿命。
	Pool RedisPool `yaml:"pool"`
	// Probe 定义 required dependency 的周期健康检查策略。
	Probe Probe `yaml:"probe"`
	// TLS 定义连接身份验证策略；production 必须启用。
	TLS StorageTLS `yaml:"tls"`
}

// SQLPool 定义 database/sql pool 的有界资源策略。
type SQLPool struct {
	// MaxOpenConns 是同时打开连接的硬上限。
	MaxOpenConns int `yaml:"maxOpenConns"`
	// MaxIdleConns 是保留空闲连接的上限，不能超过 MaxOpenConns。
	MaxIdleConns int `yaml:"maxIdleConns"`
	// ConnMaxLifetime 限制连接的总寿命。
	ConnMaxLifetime time.Duration `yaml:"connMaxLifetime"`
	// ConnMaxIdleTime 限制连接的空闲寿命。
	ConnMaxIdleTime time.Duration `yaml:"connMaxIdleTime"`
}

// RedisPool 定义 standalone Redis connection pool 的有界资源策略。
type RedisPool struct {
	// Size 是同时保留连接的硬上限。
	Size int `yaml:"size"`
	// MinIdleConns 是预留空闲连接的下限，不能超过 Size。
	MinIdleConns int `yaml:"minIdleConns"`
	// ConnMaxLifetime 限制连接的总寿命。
	ConnMaxLifetime time.Duration `yaml:"connMaxLifetime"`
	// ConnMaxIdleTime 限制连接的空闲寿命。
	ConnMaxIdleTime time.Duration `yaml:"connMaxIdleTime"`
}

// Probe 定义 required storage dependency 的周期检查和退出阈值。
type Probe struct {
	// Interval 是两次 probe 开始之间的固定间隔。
	Interval time.Duration `yaml:"interval"`
	// Timeout 是单次 probe 的最大耗时，不能超过 Interval。
	Timeout time.Duration `yaml:"timeout"`
	// FailureThreshold 是触发 fatal task error 的连续失败次数。
	FailureThreshold int `yaml:"failureThreshold"`
}

// StorageTLS 定义受验证的 storage TLS client policy。
type StorageTLS struct {
	// Enabled 决定是否建立 TLS 连接；production 必须为 true。
	Enabled bool `yaml:"enabled"`
	// ServerName 是证书身份验证使用的 DNS name，production 不得为空。
	ServerName string `yaml:"serverName"`
	// CAFile 是包含受信 CA PEM 的绝对路径，production 不得为空。
	CAFile string `yaml:"caFile"`
	// ClientCertificateFile 是可选 mTLS client certificate PEM 的绝对路径。
	ClientCertificateFile string `yaml:"clientCertificateFile"`
	// ClientKeySecret 是与 client certificate 配套的 private key secret reference。
	ClientKeySecret string `yaml:"clientKeySecret"`
}

// DefaultStorage 返回适合本机 Docker harness 的 loopback plaintext 配置。
func DefaultStorage() Storage {
	return Storage{
		MySQL: MySQL{
			Address: "127.0.0.1:3306", Database: "ihomeland", Username: "ihomeland",
			PasswordSecret: "env:IHOMELAND_MYSQL_PASSWORD",
			ConnectTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
			MigrationLockTimeout: 15 * time.Second,
			Pool:                 SQLPool{MaxOpenConns: 16, MaxIdleConns: 8, ConnMaxLifetime: 30 * time.Minute, ConnMaxIdleTime: 5 * time.Minute},
			Probe:                Probe{Interval: 5 * time.Second, Timeout: time.Second, FailureThreshold: 3},
		},
		Redis: Redis{
			Address: "127.0.0.1:6379", PasswordSecret: "env:IHOMELAND_REDIS_PASSWORD",
			DialTimeout: 3 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
			Pool:  RedisPool{Size: 16, MinIdleConns: 2, ConnMaxLifetime: 30 * time.Minute, ConnMaxIdleTime: 5 * time.Minute},
			Probe: Probe{Interval: 5 * time.Second, Timeout: time.Second, FailureThreshold: 3},
		},
	}
}

// validate 聚合 MySQL/Redis 配置校验，保证下游 component 不再解释零值或交叉约束。
func (storage Storage) validate(environment string) error {
	if err := storage.MySQL.validate(environment); err != nil {
		return fmt.Errorf("storage.mysql: %w", err)
	}
	if err := storage.Redis.validate(environment); err != nil {
		return fmt.Errorf("storage.redis: %w", err)
	}
	return nil
}

// validate 检查 MySQL endpoint、凭据 reference、pool、probe 与 TLS 的启动不变量。
func (config MySQL) validate(environment string) error {
	if err := validateStorageAddress(config.Address); err != nil {
		return err
	}
	if !storageNamePattern.MatchString(config.Database) {
		return errors.New("database has invalid format")
	}
	if !storageNamePattern.MatchString(config.Username) {
		return errors.New("username has invalid format")
	}
	if err := validateSecretReference(config.PasswordSecret); err != nil {
		return fmt.Errorf("passwordSecret: %w", err)
	}
	for name, value := range map[string]time.Duration{
		"connectTimeout": config.ConnectTimeout, "readTimeout": config.ReadTimeout,
		"writeTimeout": config.WriteTimeout, "migrationLockTimeout": config.MigrationLockTimeout,
	} {
		if err := validateDuration(name, value); err != nil {
			return err
		}
	}
	if err := config.Pool.validate(); err != nil {
		return fmt.Errorf("pool: %w", err)
	}
	if err := config.Probe.validate(); err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	return config.TLS.validate(environment)
}

// validate 检查 standalone Redis endpoint、凭据 reference、pool、probe 与 TLS 的启动不变量。
func (config Redis) validate(environment string) error {
	if err := validateStorageAddress(config.Address); err != nil {
		return err
	}
	if config.Database < 0 || config.Database > 15 {
		return errors.New("database must be between 0 and 15")
	}
	if config.Username != "" && !storageNamePattern.MatchString(config.Username) {
		return errors.New("username has invalid format")
	}
	if err := validateSecretReference(config.PasswordSecret); err != nil {
		return fmt.Errorf("passwordSecret: %w", err)
	}
	for name, value := range map[string]time.Duration{
		"dialTimeout": config.DialTimeout, "readTimeout": config.ReadTimeout, "writeTimeout": config.WriteTimeout,
	} {
		if err := validateDuration(name, value); err != nil {
			return err
		}
	}
	if err := config.Pool.validate(); err != nil {
		return fmt.Errorf("pool: %w", err)
	}
	if err := config.Probe.validate(); err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	return config.TLS.validate(environment)
}

// validate 限制 database/sql pool 容量和连接寿命，避免 idle 超过 open。
func (pool SQLPool) validate() error {
	if pool.MaxOpenConns < 1 || pool.MaxOpenConns > maximumPoolSize {
		return fmt.Errorf("maxOpenConns must be between 1 and %d", maximumPoolSize)
	}
	if pool.MaxIdleConns < 0 || pool.MaxIdleConns > pool.MaxOpenConns {
		return errors.New("maxIdleConns must be between 0 and maxOpenConns")
	}
	if err := validateStorageLifetime("connMaxLifetime", pool.ConnMaxLifetime); err != nil {
		return err
	}
	return validateStorageLifetime("connMaxIdleTime", pool.ConnMaxIdleTime)
}

// validate 限制 Redis pool 容量和连接寿命，避免预留 idle 超过总容量。
func (pool RedisPool) validate() error {
	if pool.Size < 1 || pool.Size > maximumPoolSize {
		return fmt.Errorf("size must be between 1 and %d", maximumPoolSize)
	}
	if pool.MinIdleConns < 0 || pool.MinIdleConns > pool.Size {
		return errors.New("minIdleConns must be between 0 and size")
	}
	if err := validateStorageLifetime("connMaxLifetime", pool.ConnMaxLifetime); err != nil {
		return err
	}
	return validateStorageLifetime("connMaxIdleTime", pool.ConnMaxIdleTime)
}

// validate 保证 probe timeout 不超过 interval，且 fatal 阈值保持有界。
func (probe Probe) validate() error {
	if err := validateDuration("interval", probe.Interval); err != nil {
		return err
	}
	if err := validateDuration("timeout", probe.Timeout); err != nil {
		return err
	}
	if probe.Timeout > probe.Interval {
		return errors.New("timeout must not exceed interval")
	}
	if probe.FailureThreshold < 1 || probe.FailureThreshold > 10 {
		return errors.New("failureThreshold must be between 1 and 10")
	}
	return nil
}

// validate 强制 production 身份验证，并确保 mTLS certificate/key 成对配置。
func (policy StorageTLS) validate(environment string) error {
	if !policy.Enabled {
		if environment == "production" {
			return errors.New("tls must be enabled in production")
		}
		if policy.ServerName != "" || policy.CAFile != "" || policy.ClientCertificateFile != "" || policy.ClientKeySecret != "" {
			return errors.New("disabled tls must not contain tls material")
		}
		return nil
	}
	if strings.TrimSpace(policy.ServerName) == "" {
		return errors.New("tls.serverName is required when tls is enabled")
	}
	if strings.ContainsAny(policy.ServerName, "\r\n\t ") {
		return errors.New("tls.serverName contains forbidden whitespace")
	}
	if !filepath.IsAbs(policy.CAFile) {
		return errors.New("tls.caFile must be an absolute path")
	}
	if (policy.ClientCertificateFile == "") != (policy.ClientKeySecret == "") {
		return errors.New("tls client certificate and key must be configured together")
	}
	if policy.ClientCertificateFile != "" {
		if !filepath.IsAbs(policy.ClientCertificateFile) {
			return errors.New("tls.clientCertificateFile must be an absolute path")
		}
		if err := validateSecretReference(policy.ClientKeySecret); err != nil {
			return fmt.Errorf("tls.clientKeySecret: %w", err)
		}
	}
	return nil
}

// validateStorageAddress 接受明确 DNS/IP 与非零端口，但拒绝 namespace/control 字符注入。
func validateStorageAddress(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || strings.TrimSpace(host) == "" {
		return errors.New("address must contain a host and port")
	}
	parsed, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsed == 0 {
		return errors.New("address port must be between 1 and 65535")
	}
	if strings.ContainsAny(host, "{}\r\n\t ") {
		return errors.New("address host contains forbidden characters")
	}
	if net.ParseIP(host) == nil {
		if len(host) > 253 {
			return errors.New("address DNS host exceeds size limit")
		}
		for _, label := range strings.Split(host, ".") {
			if !dnsLabelPattern.MatchString(label) {
				return errors.New("address host must be an IP or valid DNS name")
			}
		}
	}
	return nil
}

// validateSecretReference 复用 secret package 的唯一语法 owner，不读取或保留 secret material。
func validateSecretReference(value string) error {
	if _, err := secret.ParseReference(value); err != nil {
		return err
	}
	return nil
}

// validateStorageLifetime 统一限制 pool connection 的 idle/total 寿命。
func validateStorageLifetime(name string, value time.Duration) error {
	if value < time.Second || value > maximumStorageLifetime {
		return fmt.Errorf("%s must be between 1s and %s", name, maximumStorageLifetime)
	}
	return nil
}

// storageEnvironmentOverrides 返回唯一允许写入 storage 配置的显式环境键集合。
func storageEnvironmentOverrides(config *Config) []environmentOverride {
	return []environmentOverride{
		{key: "IHOMELAND_MYSQL_ADDRESS", apply: stringSetter(&config.Storage.MySQL.Address)},
		{key: "IHOMELAND_MYSQL_DATABASE", apply: stringSetter(&config.Storage.MySQL.Database)},
		{key: "IHOMELAND_MYSQL_USERNAME", apply: stringSetter(&config.Storage.MySQL.Username)},
		{key: "IHOMELAND_MYSQL_PASSWORD_SECRET", apply: stringSetter(&config.Storage.MySQL.PasswordSecret)},
		{key: "IHOMELAND_MYSQL_CONNECT_TIMEOUT", apply: durationSetter(&config.Storage.MySQL.ConnectTimeout)},
		{key: "IHOMELAND_MYSQL_READ_TIMEOUT", apply: durationSetter(&config.Storage.MySQL.ReadTimeout)},
		{key: "IHOMELAND_MYSQL_WRITE_TIMEOUT", apply: durationSetter(&config.Storage.MySQL.WriteTimeout)},
		{key: "IHOMELAND_MYSQL_MIGRATION_LOCK_TIMEOUT", apply: durationSetter(&config.Storage.MySQL.MigrationLockTimeout)},
		{key: "IHOMELAND_MYSQL_MAX_OPEN_CONNS", apply: intSetter(&config.Storage.MySQL.Pool.MaxOpenConns)},
		{key: "IHOMELAND_MYSQL_MAX_IDLE_CONNS", apply: intSetter(&config.Storage.MySQL.Pool.MaxIdleConns)},
		{key: "IHOMELAND_MYSQL_CONN_MAX_LIFETIME", apply: durationSetter(&config.Storage.MySQL.Pool.ConnMaxLifetime)},
		{key: "IHOMELAND_MYSQL_CONN_MAX_IDLE_TIME", apply: durationSetter(&config.Storage.MySQL.Pool.ConnMaxIdleTime)},
		{key: "IHOMELAND_MYSQL_PROBE_INTERVAL", apply: durationSetter(&config.Storage.MySQL.Probe.Interval)},
		{key: "IHOMELAND_MYSQL_PROBE_TIMEOUT", apply: durationSetter(&config.Storage.MySQL.Probe.Timeout)},
		{key: "IHOMELAND_MYSQL_PROBE_FAILURE_THRESHOLD", apply: intSetter(&config.Storage.MySQL.Probe.FailureThreshold)},
		{key: "IHOMELAND_MYSQL_TLS_ENABLED", apply: boolSetter(&config.Storage.MySQL.TLS.Enabled)},
		{key: "IHOMELAND_MYSQL_TLS_SERVER_NAME", apply: stringSetter(&config.Storage.MySQL.TLS.ServerName)},
		{key: "IHOMELAND_MYSQL_TLS_CA_FILE", apply: stringSetter(&config.Storage.MySQL.TLS.CAFile)},
		{key: "IHOMELAND_MYSQL_TLS_CLIENT_CERTIFICATE_FILE", apply: stringSetter(&config.Storage.MySQL.TLS.ClientCertificateFile)},
		{key: "IHOMELAND_MYSQL_TLS_CLIENT_KEY_SECRET", apply: stringSetter(&config.Storage.MySQL.TLS.ClientKeySecret)},
		{key: "IHOMELAND_REDIS_ADDRESS", apply: stringSetter(&config.Storage.Redis.Address)},
		{key: "IHOMELAND_REDIS_DATABASE", apply: intSetter(&config.Storage.Redis.Database)},
		{key: "IHOMELAND_REDIS_USERNAME", apply: stringSetter(&config.Storage.Redis.Username)},
		{key: "IHOMELAND_REDIS_PASSWORD_SECRET", apply: stringSetter(&config.Storage.Redis.PasswordSecret)},
		{key: "IHOMELAND_REDIS_DIAL_TIMEOUT", apply: durationSetter(&config.Storage.Redis.DialTimeout)},
		{key: "IHOMELAND_REDIS_READ_TIMEOUT", apply: durationSetter(&config.Storage.Redis.ReadTimeout)},
		{key: "IHOMELAND_REDIS_WRITE_TIMEOUT", apply: durationSetter(&config.Storage.Redis.WriteTimeout)},
		{key: "IHOMELAND_REDIS_POOL_SIZE", apply: intSetter(&config.Storage.Redis.Pool.Size)},
		{key: "IHOMELAND_REDIS_MIN_IDLE_CONNS", apply: intSetter(&config.Storage.Redis.Pool.MinIdleConns)},
		{key: "IHOMELAND_REDIS_CONN_MAX_LIFETIME", apply: durationSetter(&config.Storage.Redis.Pool.ConnMaxLifetime)},
		{key: "IHOMELAND_REDIS_CONN_MAX_IDLE_TIME", apply: durationSetter(&config.Storage.Redis.Pool.ConnMaxIdleTime)},
		{key: "IHOMELAND_REDIS_PROBE_INTERVAL", apply: durationSetter(&config.Storage.Redis.Probe.Interval)},
		{key: "IHOMELAND_REDIS_PROBE_TIMEOUT", apply: durationSetter(&config.Storage.Redis.Probe.Timeout)},
		{key: "IHOMELAND_REDIS_PROBE_FAILURE_THRESHOLD", apply: intSetter(&config.Storage.Redis.Probe.FailureThreshold)},
		{key: "IHOMELAND_REDIS_TLS_ENABLED", apply: boolSetter(&config.Storage.Redis.TLS.Enabled)},
		{key: "IHOMELAND_REDIS_TLS_SERVER_NAME", apply: stringSetter(&config.Storage.Redis.TLS.ServerName)},
		{key: "IHOMELAND_REDIS_TLS_CA_FILE", apply: stringSetter(&config.Storage.Redis.TLS.CAFile)},
		{key: "IHOMELAND_REDIS_TLS_CLIENT_CERTIFICATE_FILE", apply: stringSetter(&config.Storage.Redis.TLS.ClientCertificateFile)},
		{key: "IHOMELAND_REDIS_TLS_CLIENT_KEY_SECRET", apply: stringSetter(&config.Storage.Redis.TLS.ClientKeySecret)},
	}
}

// stringSetter 为已经白名单登记的字符串字段创建赋值闭包。
func stringSetter(target *string) func(string) error {
	return func(value string) error {
		*target = value
		return nil
	}
}

// intSetter 为白名单整数配置创建不回显原值的解析闭包。
func intSetter(target *int) func(string) error {
	return func(value string) error {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		*target = parsed
		return nil
	}
}

// boolSetter 为白名单 TLS 开关创建严格布尔解析闭包。
func boolSetter(target *bool) func(string) error {
	return func(value string) error {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		*target = parsed
		return nil
	}
}
