// Package config 加载并验证服务端启动所需的不可变配置。
//
// 该包只进行文件读取、显式环境覆盖和纯内存校验，不创建 listener、日志文件或后台任务，
// 使配置错误始终发生在进程产生外部副作用之前。
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// minimumTimeout 防止零值或近零预算使正常清理必然失败。
	minimumTimeout = 10 * time.Millisecond
	// maximumTimeout 防止错误配置让进程长时间保持不确定状态。
	maximumTimeout = time.Minute
)

// LookupEnv 返回环境键是否存在及其原始值。
//
// 注入该函数而不是读取 package global 环境，使覆盖优先级和敏感值错误行为可以确定性测试。
type LookupEnv func(string) (string, bool)

// Config 是启动验证完成后由 Composition Root 只读共享的配置快照。
//
// 字段不包含 secret；后续引入凭据时必须使用独立 secret provider，不能把明文写入该结构的日志。
type Config struct {
	// Environment 区分 local、test 与 production 运行约束，不参与业务授权。
	Environment string `yaml:"environment"`
	// Runtime 定义进程初始化和关闭的总时间预算。
	Runtime Runtime `yaml:"runtime"`
	// Logging 定义结构化日志编码与最低级别。
	Logging Logging `yaml:"logging"`
	// Diagnostic 定义与公开业务面隔离的诊断 listener。
	Diagnostic Diagnostic `yaml:"diagnostic"`
	// PublicAPI 定义公开启动、账号与世界准入 HTTPS 面。
	PublicAPI PublicAPI `yaml:"publicApi"`
	// Storage 定义 MySQL 与 Redis 的非敏感连接、资源和探针策略。
	Storage Storage `yaml:"storage"`
}

// Runtime 保存所有组件共享的启动与关闭总预算。
type Runtime struct {
	// StartupTimeout 限制全部必需组件进入 ready 的总耗时。
	StartupTimeout time.Duration `yaml:"startupTimeout"`
	// ShutdownTimeout 限制 draining 到进程退出的总耗时。
	ShutdownTimeout time.Duration `yaml:"shutdownTimeout"`
}

// Logging 定义 slog handler 的稳定运行配置。
type Logging struct {
	// Level 只接受 debug、info、warn、error，避免不同 package 自定义级别。
	Level string `yaml:"level"`
	// Format 选择机器可读 json 或本地可读 text。
	Format string `yaml:"format"`
}

// Diagnostic 定义内部探针 listener 的资源边界。
type Diagnostic struct {
	// Address 必须是显式 IP 与非零端口，默认只绑定 loopback。
	Address string `yaml:"address"`
	// ReadHeaderTimeout 限制慢速 header 攻击占用连接的时间。
	ReadHeaderTimeout time.Duration `yaml:"readHeaderTimeout"`
	// ReadTimeout 限制完整诊断请求读取时间。
	ReadTimeout time.Duration `yaml:"readTimeout"`
	// WriteTimeout 限制有界诊断响应写出时间。
	WriteTimeout time.Duration `yaml:"writeTimeout"`
	// IdleTimeout 限制 keep-alive 空闲连接寿命。
	IdleTimeout time.Duration `yaml:"idleTimeout"`
	// MaxHeaderBytes 限制请求 header 内存占用。
	MaxHeaderBytes int `yaml:"maxHeaderBytes"`
}

// Default 返回只适合本地启动且默认不暴露到外部网卡的安全配置。
func Default() Config {
	return Config{
		Environment: "local",
		Runtime:     Runtime{StartupTimeout: 10 * time.Second, ShutdownTimeout: 10 * time.Second},
		Logging:     Logging{Level: "info", Format: "text"},
		Diagnostic: Diagnostic{
			Address:           "127.0.0.1:8081",
			ReadHeaderTimeout: 3 * time.Second,
			ReadTimeout:       5 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       30 * time.Second,
			MaxHeaderBytes:    8192,
		},
		PublicAPI: DefaultPublicAPI(),
		Storage:   DefaultStorage(),
	}
}

// Load 按安全默认值、严格 YAML、白名单环境覆盖的固定顺序生成配置。
//
// path 必须显式提供。lookupEnv 为空时使用 os.LookupEnv；错误只包含键名和约束，不回显原始值。
func Load(path string, lookupEnv LookupEnv) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, errors.New("config path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	// 文件只读且内容会在返回前完整解码；关闭失败不会改变已经获得的配置快照。
	defer func() { _ = file.Close() }()

	result := Default()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&result); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, errors.New("config must contain exactly one YAML document")
		}
		return Config{}, fmt.Errorf("decode trailing config: %w", err)
	}
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if err := applyEnvironment(&result, lookupEnv); err != nil {
		return Config{}, err
	}
	if err := result.Validate(); err != nil {
		return Config{}, err
	}
	return result, nil
}

// Validate 检查所有启动期不变量，避免下游 package 各自解释范围。
func (config Config) Validate() error {
	if config.Environment != "local" && config.Environment != "test" && config.Environment != "production" {
		return errors.New("environment must be local, test, or production")
	}
	if err := validateDuration("runtime.startupTimeout", config.Runtime.StartupTimeout); err != nil {
		return err
	}
	if err := validateDuration("runtime.shutdownTimeout", config.Runtime.ShutdownTimeout); err != nil {
		return err
	}
	if config.Logging.Level != "debug" && config.Logging.Level != "info" && config.Logging.Level != "warn" && config.Logging.Level != "error" {
		return errors.New("logging.level must be debug, info, warn, or error")
	}
	if config.Logging.Format != "json" && config.Logging.Format != "text" {
		return errors.New("logging.format must be json or text")
	}
	if err := validateAddress(config.Diagnostic.Address); err != nil {
		return err
	}
	for key, value := range map[string]time.Duration{
		"diagnostic.readHeaderTimeout": config.Diagnostic.ReadHeaderTimeout,
		"diagnostic.readTimeout":       config.Diagnostic.ReadTimeout,
		"diagnostic.writeTimeout":      config.Diagnostic.WriteTimeout,
		"diagnostic.idleTimeout":       config.Diagnostic.IdleTimeout,
	} {
		if err := validateDuration(key, value); err != nil {
			return err
		}
	}
	if config.Diagnostic.MaxHeaderBytes < 1024 || config.Diagnostic.MaxHeaderBytes > 65536 {
		return errors.New("diagnostic.maxHeaderBytes must be between 1024 and 65536")
	}
	if err := config.PublicAPI.validate(config.Environment, config.Diagnostic.Address); err != nil {
		return fmt.Errorf("publicApi: %w", err)
	}
	if err := config.Storage.validate(config.Environment); err != nil {
		return err
	}
	return nil
}

// DiagnosticIsLoopback 报告诊断地址是否限制在本机接口。
func (config Config) DiagnosticIsLoopback() bool {
	host, _, err := net.SplitHostPort(config.Diagnostic.Address)
	if err != nil {
		return false
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

// environmentOverride 将一个完整白名单环境键绑定到不回显原值的解析动作。
type environmentOverride struct {
	// key 是唯一允许读取的完整环境变量名，避免任意前缀键通过反射写入配置。
	key string
	// apply 只负责解析并写入目标字段；调用方统一隐藏可能敏感的原始值。
	apply func(string) error
}

// applyEnvironment 只执行显式登记的覆盖，未知 IHOMELAND_ 键不会通过反射写入配置。
func applyEnvironment(config *Config, lookup LookupEnv) error {
	overrides := []environmentOverride{
		{key: "IHOMELAND_ENVIRONMENT", apply: func(value string) error { config.Environment = value; return nil }},
		{key: "IHOMELAND_LOG_LEVEL", apply: func(value string) error { config.Logging.Level = strings.ToLower(value); return nil }},
		{key: "IHOMELAND_LOG_FORMAT", apply: func(value string) error { config.Logging.Format = strings.ToLower(value); return nil }},
		{key: "IHOMELAND_DIAGNOSTIC_ADDRESS", apply: func(value string) error { config.Diagnostic.Address = value; return nil }},
		{key: "IHOMELAND_STARTUP_TIMEOUT", apply: durationSetter(&config.Runtime.StartupTimeout)},
		{key: "IHOMELAND_SHUTDOWN_TIMEOUT", apply: durationSetter(&config.Runtime.ShutdownTimeout)},
		{key: "IHOMELAND_DIAGNOSTIC_READ_HEADER_TIMEOUT", apply: durationSetter(&config.Diagnostic.ReadHeaderTimeout)},
		{key: "IHOMELAND_DIAGNOSTIC_READ_TIMEOUT", apply: durationSetter(&config.Diagnostic.ReadTimeout)},
		{key: "IHOMELAND_DIAGNOSTIC_WRITE_TIMEOUT", apply: durationSetter(&config.Diagnostic.WriteTimeout)},
		{key: "IHOMELAND_DIAGNOSTIC_IDLE_TIMEOUT", apply: durationSetter(&config.Diagnostic.IdleTimeout)},
		{key: "IHOMELAND_DIAGNOSTIC_MAX_HEADER_BYTES", apply: func(value string) error {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			config.Diagnostic.MaxHeaderBytes = parsed
			return nil
		}},
	}
	overrides = append(overrides, storageEnvironmentOverrides(config)...)
	overrides = append(overrides, publicAPIEnvironmentOverrides(config)...)
	for _, override := range overrides {
		value, exists := lookup(override.key)
		if !exists {
			continue
		}
		if err := override.apply(value); err != nil {
			return fmt.Errorf("environment override %s is invalid", override.key)
		}
	}
	return nil
}

// durationSetter 为白名单 duration 字段创建不回显原值的解析闭包。
func durationSetter(target *time.Duration) func(string) error {
	return func(value string) error {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*target = parsed
		return nil
	}
}

// validateDuration 统一限制进程资源 timeout，避免零值禁用保护或异常长等待。
func validateDuration(key string, value time.Duration) error {
	if value < minimumTimeout || value > maximumTimeout {
		return fmt.Errorf("%s must be between %s and %s", key, minimumTimeout, maximumTimeout)
	}
	return nil
}

// validateAddress 要求显式 IP 和非零端口，避免 DNS 变化扩大诊断暴露面。
func validateAddress(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return errors.New("diagnostic.address must contain an IP and port")
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return errors.New("diagnostic.address host must be an explicit IP")
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 {
		return errors.New("diagnostic.address port must be between 1 and 65535")
	}
	return nil
}
