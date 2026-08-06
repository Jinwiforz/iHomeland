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
	"path/filepath"
	"regexp"
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
	// minimumQualificationSampleInterval 避免私有 control 被采样任务饱和。
	minimumQualificationSampleInterval = 100 * time.Millisecond
	// maximumQualificationSampleInterval 保证一秒默认 cadence 可被可靠覆盖。
	maximumQualificationSampleInterval = 10 * time.Second
)

var qualificationRunIDPattern = regexp.MustCompile(`^bqrun_[0-9a-f]{32}$`)

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
	// SimulationControl 定义必需本机 C++ child 的精确 artifact 与有界 control policy。
	SimulationControl SimulationControl `yaml:"simulationControl"`
	// GameplayPackage 定义 Go 在启动期冻结的 production package 与预期摘要。
	GameplayPackage GameplayPackage `yaml:"gameplayPackage"`
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

// SimulationControl 定义无 endpoint、无 credential 的私有 child control 配置。
type SimulationControl struct {
	// Enabled 必须为 true；false 只允许纯配置/单元测试构造安全零值。
	Enabled bool `yaml:"enabled"`
	// BinaryPath 是 ihomeland-sim-server 的绝对路径。
	BinaryPath string `yaml:"binaryPath"`
	// BinarySHA256 是 binary 内容 SHA-256。
	BinarySHA256 string `yaml:"binarySha256"`
	// QualificationReceiptPath 是 B0.3 gate receipt 绝对路径。
	QualificationReceiptPath string `yaml:"qualificationReceiptPath"`
	// QualificationReceiptSHA256 是 receipt 内容 SHA-256。
	QualificationReceiptSHA256 string `yaml:"qualificationReceiptSha256"`
	// BuildIdentity 是 hello 绑定的 B0.3 Release identity。
	BuildIdentity string `yaml:"buildIdentity"`
	// ModelManifest 是冻结 battle model manifest digest。
	ModelManifest string `yaml:"modelManifest"`
	// ProfileManifest 是冻结 network profile manifest digest。
	ProfileManifest string `yaml:"profileManifest"`
	// ConfigIdentity 是 control-baseline-v1 config digest。
	ConfigIdentity string `yaml:"configIdentity"`
	// NavigationIdentity 是 nav asset/config digest。
	NavigationIdentity string `yaml:"navigationIdentity"`
	// PhysicsIdentity 是 physics adapter/config digest。
	PhysicsIdentity string `yaml:"physicsIdentity"`
	// InstanceCapacity 是 child hard slots。
	InstanceCapacity int `yaml:"instanceCapacity"`
	// ActorCapacity 是每 instance B0.3 qualified actor cap。
	ActorCapacity int `yaml:"actorCapacity"`
	// FrameBytes 必须精确等于冻结 64 KiB limit。
	FrameBytes int `yaml:"frameBytes"`
	// PendingRequests 限制 session correlation queue。
	PendingRequests int `yaml:"pendingRequests"`
	// RequestTimeout 是已发送 request turn 的 hard deadline。
	RequestTimeout time.Duration `yaml:"requestTimeout"`
	// HealthInterval 是 node health 采样间隔。
	HealthInterval time.Duration `yaml:"healthInterval"`
	// HealthTimeout 是单次 health receipt deadline。
	HealthTimeout time.Duration `yaml:"healthTimeout"`
	// DrainTimeout 是 instance drain budget。
	DrainTimeout time.Duration `yaml:"drainTimeout"`
	// ShutdownTimeout 是 instance/node/process 关闭 budget。
	ShutdownTimeout time.Duration `yaml:"shutdownTimeout"`
	// StderrLineBytes 是低敏 child diagnostic 单行上限。
	StderrLineBytes int `yaml:"stderrLineBytes"`
	// QualificationMode 只允许受控 B0.6 Composition Root 启用只读采样。
	QualificationMode bool `yaml:"qualificationMode"`
	// QualificationRunID 是 B0.6 run-local 128-bit identity。
	QualificationRunID string `yaml:"qualificationRunId"`
	// QualificationSampleInterval 是 control/process 四源采样节奏。
	QualificationSampleInterval time.Duration `yaml:"qualificationSampleInterval"`
}

// GameplayPackage 定义 production package 的本机选择输入与跨边界预期身份。
//
// RootPath 只可传给本机 loader 与受监督 child，禁止写入日志、control frame 或 evidence。
type GameplayPackage struct {
	// RootPath 是包含五份 closed production document 的绝对目录。
	RootPath string `yaml:"rootPath"`
	// ArenaRootPath 是包含 arena/navigation/physics 三份 server authority source 的绝对目录。
	ArenaRootPath string `yaml:"arenaRootPath"`
	// PackageID 是部署明确选择的 production package semantic identity。
	PackageID string `yaml:"packageId"`
	// ConfigIdentity 是五份 source 与 governance binding 的聚合摘要。
	ConfigIdentity string `yaml:"configIdentity"`
	// NavigationIdentity 是 bindings 声明的 Detour source 摘要。
	NavigationIdentity string `yaml:"navigationIdentity"`
	// PhysicsIdentity 是 bindings 声明的 Jolt source 摘要。
	PhysicsIdentity string `yaml:"physicsIdentity"`
	// WireIdentity 是 package 绑定的 battle wire manifest 摘要。
	WireIdentity string `yaml:"wireIdentity"`
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
		SimulationControl: SimulationControl{
			Enabled: false,
		},
		GameplayPackage: GameplayPackage{},
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
	if err := config.SimulationControl.validate(config.Environment); err != nil {
		return err
	}
	if err := config.GameplayPackage.validate(config.SimulationControl.Enabled); err != nil {
		return err
	}
	if config.SimulationControl.Enabled &&
		(config.GameplayPackage.ConfigIdentity != config.SimulationControl.ConfigIdentity ||
			config.GameplayPackage.NavigationIdentity != config.SimulationControl.NavigationIdentity ||
			config.GameplayPackage.PhysicsIdentity != config.SimulationControl.PhysicsIdentity ||
			config.GameplayPackage.WireIdentity != config.PublicAPI.BattleUDP.WireIdentity) {
		return errors.New("gameplayPackage identity binding differs from simulation or battle configuration")
	}
	return nil
}

// validate 只校验部署选择形态；source bytes 与跨配置摘要由 selector 在副作用前验证。
func (config GameplayPackage) validate(required bool) error {
	if !required && config == (GameplayPackage{}) {
		return nil
	}
	if !filepath.IsAbs(config.RootPath) || filepath.Clean(config.RootPath) != config.RootPath {
		return errors.New("gameplayPackage.rootPath must be a clean absolute path")
	}
	if !filepath.IsAbs(config.ArenaRootPath) || filepath.Clean(config.ArenaRootPath) != config.ArenaRootPath {
		return errors.New("gameplayPackage.arenaRootPath must be a clean absolute path")
	}
	if config.PackageID != "personal-world-combat-v1" {
		return errors.New("gameplayPackage.packageId is not an allowed production package")
	}
	digestPattern := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for key, value := range map[string]string{
		"configIdentity":     config.ConfigIdentity,
		"navigationIdentity": config.NavigationIdentity,
		"physicsIdentity":    config.PhysicsIdentity,
		"wireIdentity":       config.WireIdentity,
	} {
		if !digestPattern.MatchString(value) {
			return fmt.Errorf("gameplayPackage.%s must be lowercase SHA-256", key)
		}
	}
	return nil
}

// validate 在 enabled 时拒绝相对路径、digest 漂移、超资格容量与 silent budget。
func (config SimulationControl) validate(environment string) error {
	if !config.Enabled {
		if environment == "production" {
			return errors.New("simulationControl.enabled is required in production")
		}
		return nil
	}
	if !filepath.IsAbs(config.BinaryPath) || !filepath.IsAbs(config.QualificationReceiptPath) {
		return errors.New("simulationControl artifact paths must be absolute")
	}
	digestPattern := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for key, value := range map[string]string{
		"binarySha256":               config.BinarySHA256,
		"qualificationReceiptSha256": config.QualificationReceiptSHA256,
		"buildIdentity":              config.BuildIdentity,
		"modelManifest":              config.ModelManifest,
		"profileManifest":            config.ProfileManifest,
		"configIdentity":             config.ConfigIdentity,
		"navigationIdentity":         config.NavigationIdentity,
		"physicsIdentity":            config.PhysicsIdentity,
	} {
		if !digestPattern.MatchString(value) {
			return fmt.Errorf("simulationControl.%s must be lowercase SHA-256", key)
		}
	}
	if config.InstanceCapacity < 1 || config.InstanceCapacity > 256 ||
		config.ActorCapacity < 1 || config.ActorCapacity > 8 {
		return errors.New("simulationControl capacity exceeds qualified limits")
	}
	if config.FrameBytes != 65_536 || config.PendingRequests < 1 ||
		config.PendingRequests > 256 {
		return errors.New("simulationControl frame or pending request limit is invalid")
	}
	for key, value := range map[string]time.Duration{
		"requestTimeout":  config.RequestTimeout,
		"healthInterval":  config.HealthInterval,
		"healthTimeout":   config.HealthTimeout,
		"drainTimeout":    config.DrainTimeout,
		"shutdownTimeout": config.ShutdownTimeout,
	} {
		if err := validateDuration("simulationControl."+key, value); err != nil {
			return err
		}
	}
	if config.HealthTimeout >= config.HealthInterval ||
		config.StderrLineBytes < 64 || config.StderrLineBytes > 4096 {
		return errors.New("simulationControl health or stderr policy is invalid")
	}
	qualificationEnabled := config.QualificationMode
	if qualificationEnabled != (config.QualificationRunID != "") ||
		(qualificationEnabled &&
			!qualificationRunIDPattern.MatchString(config.QualificationRunID)) ||
		(!qualificationEnabled && config.QualificationSampleInterval != 0) ||
		(qualificationEnabled &&
			(config.QualificationSampleInterval < minimumQualificationSampleInterval ||
				config.QualificationSampleInterval > maximumQualificationSampleInterval)) ||
		(environment == "production" && qualificationEnabled) {
		return errors.New("simulationControl qualification mode is invalid")
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
