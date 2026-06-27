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

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath        = "config/local.yaml"
	defaultHTTPAddr          = ":8080"
	defaultLogLevel          = "info"
	defaultProtocolVersion   = 0
	defaultReleasePath       = "../release.json"
	defaultServerVersionPath = "version.json"
	defaultClientVersionPath = "../client/version.json"
)

const (
	envConfigPath        = "IHOMELAND_CONFIG"
	envHTTPAddr          = "IHOMELAND_HTTP_ADDR"
	envLogLevel          = "IHOMELAND_LOG_LEVEL"
	envProtocolVersion   = "IHOMELAND_PROTOCOL_VERSION"
	envReleasePath       = "IHOMELAND_RELEASE_PATH"
	envServerVersionPath = "IHOMELAND_SERVER_VERSION_PATH"
	envClientVersionPath = "IHOMELAND_CLIENT_VERSION_PATH"
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
