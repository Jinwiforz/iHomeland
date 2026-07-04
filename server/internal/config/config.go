// Package config 负责服务端配置的默认值、环境变量覆盖和校验。
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath         = "config/local.yaml"
	defaultHTTPAddr           = ":8080"
	defaultLogLevel           = "info"
	defaultProtocolVersion    = 0
	defaultReleasePath        = "../release.json"
	defaultServerVersionPath  = "version.json"
	defaultClientVersionPath  = "../client/version.json"
	defaultMySQLAddr          = "127.0.0.1:33306"
	defaultMySQLDatabase      = "ihomeland"
	defaultMySQLUser          = "ihomeland"
	defaultRedisAddr          = "127.0.0.1:36379"
	defaultGatewayIdleTimeout = 30 * time.Second
)

const (
	envConfigPath         = "IHOMELAND_CONFIG"
	envHTTPAddr           = "IHOMELAND_HTTP_ADDR"
	envLogLevel           = "IHOMELAND_LOG_LEVEL"
	envProtocolVersion    = "IHOMELAND_PROTOCOL_VERSION"
	envReleasePath        = "IHOMELAND_RELEASE_PATH"
	envServerVersionPath  = "IHOMELAND_SERVER_VERSION_PATH"
	envClientVersionPath  = "IHOMELAND_CLIENT_VERSION_PATH"
	envMySQLAddr          = "IHOMELAND_MYSQL_ADDR"
	envMySQLDatabase      = "IHOMELAND_MYSQL_DATABASE"
	envMySQLUser          = "IHOMELAND_MYSQL_USER"
	envRedisAddr          = "IHOMELAND_REDIS_ADDR"
	envGatewayIdleTimeout = "IHOMELAND_GATEWAY_IDLE_TIMEOUT"
)

// Config 描述服务端启动所需配置。
type Config struct {
	ConfigPath        string
	HTTPAddr          string
	LogLevel          string
	ProtocolVersion   int
	ReleasePath       string
	ServerVersionPath string
	ClientVersionPath string
	MySQL             MySQLConfig
	Redis             RedisConfig
	Gateway           GatewayConfig
}

// MySQLConfig 描述本地 MySQL 基础设施连接配置。
type MySQLConfig struct {
	Addr     string
	Database string
	User     string
}

// RedisConfig 描述本地 Redis 基础设施连接配置。
type RedisConfig struct {
	Addr string
}

// GatewayConfig 描述实时网关连接生命周期配置。
type GatewayConfig struct {
	IdleTimeout time.Duration
}

// Default 返回本地开发可直接使用的默认配置。
func Default() Config {
	return Config{
		ConfigPath:        defaultConfigPath,
		HTTPAddr:          defaultHTTPAddr,
		LogLevel:          defaultLogLevel,
		ProtocolVersion:   defaultProtocolVersion,
		ReleasePath:       defaultReleasePath,
		ServerVersionPath: defaultServerVersionPath,
		ClientVersionPath: defaultClientVersionPath,
		MySQL: MySQLConfig{
			Addr:     defaultMySQLAddr,
			Database: defaultMySQLDatabase,
			User:     defaultMySQLUser,
		},
		Redis: RedisConfig{
			Addr: defaultRedisAddr,
		},
		Gateway: GatewayConfig{
			IdleTimeout: defaultGatewayIdleTimeout,
		},
	}
}

// LoadFromEnv 从环境变量加载配置，并在返回前完成校验。
func LoadFromEnv() (Config, error) {
	cfg := Default()

	if value := strings.TrimSpace(os.Getenv(envConfigPath)); value != "" {
		cfg.ConfigPath = value
	}
	if err := applyFile(&cfg); err != nil {
		return Config{}, err
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

type fileConfig struct {
	HTTPAddr          string `yaml:"httpAddr"`
	LogLevel          string `yaml:"logLevel"`
	ProtocolVersion   *int   `yaml:"protocolVersion"`
	ReleasePath       string `yaml:"releasePath"`
	ServerVersionPath string `yaml:"serverVersionPath"`
	ClientVersionPath string `yaml:"clientVersionPath"`
	MySQL             struct {
		Addr     string `yaml:"addr"`
		Database string `yaml:"database"`
		User     string `yaml:"user"`
	} `yaml:"mysql"`
	Redis struct {
		Addr string `yaml:"addr"`
	} `yaml:"redis"`
	Gateway struct {
		IdleTimeout string `yaml:"idleTimeout"`
	} `yaml:"gateway"`
}

func applyFile(cfg *Config) error {
	if strings.TrimSpace(cfg.ConfigPath) == "" {
		return nil
	}

	data, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && cfg.ConfigPath == defaultConfigPath {
			return nil
		}
		return fmt.Errorf("read config file %s: %w", cfg.ConfigPath, err)
	}

	var fileCfg fileConfig
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return fmt.Errorf("decode config file %s: %w", cfg.ConfigPath, err)
	}

	if strings.TrimSpace(fileCfg.HTTPAddr) != "" {
		cfg.HTTPAddr = fileCfg.HTTPAddr
	}
	if strings.TrimSpace(fileCfg.LogLevel) != "" {
		cfg.LogLevel = strings.ToLower(fileCfg.LogLevel)
	}
	if fileCfg.ProtocolVersion != nil {
		cfg.ProtocolVersion = *fileCfg.ProtocolVersion
	}
	if strings.TrimSpace(fileCfg.ReleasePath) != "" {
		cfg.ReleasePath = fileCfg.ReleasePath
	}
	if strings.TrimSpace(fileCfg.ServerVersionPath) != "" {
		cfg.ServerVersionPath = fileCfg.ServerVersionPath
	}
	if strings.TrimSpace(fileCfg.ClientVersionPath) != "" {
		cfg.ClientVersionPath = fileCfg.ClientVersionPath
	}
	if strings.TrimSpace(fileCfg.MySQL.Addr) != "" {
		cfg.MySQL.Addr = fileCfg.MySQL.Addr
	}
	if strings.TrimSpace(fileCfg.MySQL.Database) != "" {
		cfg.MySQL.Database = fileCfg.MySQL.Database
	}
	if strings.TrimSpace(fileCfg.MySQL.User) != "" {
		cfg.MySQL.User = fileCfg.MySQL.User
	}
	if strings.TrimSpace(fileCfg.Redis.Addr) != "" {
		cfg.Redis.Addr = fileCfg.Redis.Addr
	}
	if strings.TrimSpace(fileCfg.Gateway.IdleTimeout) != "" {
		timeout, err := time.ParseDuration(fileCfg.Gateway.IdleTimeout)
		if err != nil {
			return fmt.Errorf("parse gateway idle timeout: %w", err)
		}
		cfg.Gateway.IdleTimeout = timeout
	}

	return nil
}

func applyEnv(cfg *Config) error {
	if value := strings.TrimSpace(os.Getenv(envHTTPAddr)); value != "" {
		cfg.HTTPAddr = value
	}
	if value := strings.TrimSpace(os.Getenv(envLogLevel)); value != "" {
		cfg.LogLevel = strings.ToLower(value)
	}
	if value := strings.TrimSpace(os.Getenv(envProtocolVersion)); value != "" {
		version, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("parse %s: %w", envProtocolVersion, err)
		}
		cfg.ProtocolVersion = version
	}
	if value := strings.TrimSpace(os.Getenv(envReleasePath)); value != "" {
		cfg.ReleasePath = value
	}
	if value := strings.TrimSpace(os.Getenv(envServerVersionPath)); value != "" {
		cfg.ServerVersionPath = value
	}
	if value := strings.TrimSpace(os.Getenv(envClientVersionPath)); value != "" {
		cfg.ClientVersionPath = value
	}
	if value := strings.TrimSpace(os.Getenv(envMySQLAddr)); value != "" {
		cfg.MySQL.Addr = value
	}
	if value := strings.TrimSpace(os.Getenv(envMySQLDatabase)); value != "" {
		cfg.MySQL.Database = value
	}
	if value := strings.TrimSpace(os.Getenv(envMySQLUser)); value != "" {
		cfg.MySQL.User = value
	}
	if value := strings.TrimSpace(os.Getenv(envRedisAddr)); value != "" {
		cfg.Redis.Addr = value
	}
	if value := strings.TrimSpace(os.Getenv(envGatewayIdleTimeout)); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("parse %s: %w", envGatewayIdleTimeout, err)
		}
		cfg.Gateway.IdleTimeout = timeout
	}
	return nil
}

// Validate 校验配置是否满足服务端启动的最低要求。
func (c Config) Validate() error {
	if strings.TrimSpace(c.HTTPAddr) == "" {
		return errors.New("http addr is required")
	}
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		return fmt.Errorf("invalid http addr %q: %w", c.HTTPAddr, err)
	}
	if _, err := ParseLogLevel(c.LogLevel); err != nil {
		return err
	}
	if c.ProtocolVersion < 0 {
		return errors.New("protocol version must be non-negative")
	}
	if strings.TrimSpace(c.ReleasePath) == "" {
		return errors.New("release path is required")
	}
	if strings.TrimSpace(c.ServerVersionPath) == "" {
		return errors.New("server version path is required")
	}
	if strings.TrimSpace(c.ClientVersionPath) == "" {
		return errors.New("client version path is required")
	}
	if err := validateHostPort("mysql addr", c.MySQL.Addr); err != nil {
		return err
	}
	if strings.TrimSpace(c.MySQL.Database) == "" {
		return errors.New("mysql database is required")
	}
	if strings.TrimSpace(c.MySQL.User) == "" {
		return errors.New("mysql user is required")
	}
	if err := validateHostPort("redis addr", c.Redis.Addr); err != nil {
		return err
	}
	if c.Gateway.IdleTimeout <= 0 {
		return errors.New("gateway idle timeout must be positive")
	}
	return nil
}

func validateHostPort(name string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if _, _, err := net.SplitHostPort(value); err != nil {
		return fmt.Errorf("invalid %s %q: %w", name, value, err)
	}
	return nil
}

// ParseLogLevel 将配置中的日志等级转换为 slog.Level。
func ParseLogLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unsupported log level %q", level)
	}
}
