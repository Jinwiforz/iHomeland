// Package secret 解析白名单凭据来源，并限制 secret material 的意外暴露。
//
// 该包只负责 reference、受限 I/O、内存清零与安全格式化，不构造 storage driver
// 配置，也不记录明文。调用方负责把 Value 的使用所有权转移给明确 consumer，并在
// 最短生命周期结束时调用 Destroy。
package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// redactedValue 是所有默认文本、Go 和 slog 格式的唯一公开表达。
	redactedValue = "[REDACTED]"
	// maximumBytes 限制单个 secret 文件或环境值的内存占用。
	maximumBytes = 1024 * 1024
)

// environmentNamePattern 只允许显式大写键，避免 provider 读取任意环境 namespace。
var environmentNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)

// Reference 是已验证的 env:NAME 或 file:absolute-path secret 定位符。
// 它不包含 secret material；默认格式化仅暴露稳定 source 和安全标识。
type Reference struct {
	// source 只允许 env 或 file，构造后不可变。
	source string
	// identifier 是已验证环境键或绝对路径；默认格式化会隐藏 file path。
	identifier string
}

// ParseReference 验证并构造白名单 secret reference，不访问外部环境。
func ParseReference(raw string) (Reference, error) {
	source, identifier, found := strings.Cut(raw, ":")
	if !found || identifier == "" {
		return Reference{}, errors.New("secret reference must use env:NAME or file:absolute-path")
	}
	switch source {
	case "env":
		if !environmentNamePattern.MatchString(identifier) {
			return Reference{}, errors.New("secret environment reference has invalid format")
		}
	case "file":
		if !filepath.IsAbs(identifier) {
			return Reference{}, errors.New("secret file reference must use an absolute path")
		}
	default:
		return Reference{}, errors.New("secret reference source must be env or file")
	}
	return Reference{source: source, identifier: identifier}, nil
}

// String 返回不包含 secret material 的稳定 reference 标识。
func (reference Reference) String() string {
	if reference.source == "file" {
		return "file:[REDACTED_PATH]"
	}
	return reference.source + ":" + reference.identifier
}

// GoString 阻止 %#v 绕过 String 展开 file identifier。
func (reference Reference) GoString() string { return reference.String() }

// LogValue 为 slog 提供不包含 file path 的稳定 reference。
func (reference Reference) LogValue() slog.Value { return slog.StringValue(reference.String()) }

// MarshalText 阻止通用文本编码器序列化 file identifier。
func (reference Reference) MarshalText() ([]byte, error) { return []byte(reference.String()), nil }

// Value 保存不可由默认格式化展开的 secret bytes。
//
// Value 的普通赋值只复制 slice header，所有副本共享 backing bytes；调用方必须把它
// 当作 move-only 值，在转移使用所有权后不再 Expose 源值。Expose 与 Destroy 不支持
// 并发调用，任一别名 Destroy 后全部别名都失效。
type Value struct {
	// content 由当前 consumer 使用；Value 副本共享 backing bytes，Destroy 负责统一清零。
	content []byte
}

// Destroy 清零共享 backing bytes，并释放当前 value 的 slice header。
//
// 该操作会使所有 Value 别名失效；调用方完成 driver 配置后必须尽快调用，且不得与
// Expose 并发执行。重复调用同一 value 是安全的。
func (value *Value) Destroy() {
	if value == nil {
		return
	}
	clear(value.content)
	value.content = nil
}

// String 始终返回脱敏占位符。
func (Value) String() string { return redactedValue }

// GoString 始终返回脱敏占位符，阻止 %#v 展开内部内容。
func (Value) GoString() string { return redactedValue }

// LogValue 为 slog 提供不含 secret material 的稳定值。
func (Value) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// MarshalText 阻止通用文本编码器序列化 secret material。
func (Value) MarshalText() ([]byte, error) { return []byte(redactedValue), nil }

// Expose 将 secret 副本交给 callback 一次，并在返回前清零该副本。
//
// callback 不得保存切片；若需要长期凭据，应只保存目标 driver 的受控配置。调用方
// 不得在 Value 已转移、Destroy 后或与 Destroy 并发时调用 Expose。
func (value Value) Expose(callback func([]byte) error) error {
	if callback == nil {
		return errors.New("secret callback is required")
	}
	copyOfContent := append([]byte(nil), value.content...)
	defer clear(copyOfContent)
	return callback(copyOfContent)
}

// Provider 按已验证 reference 解析 secret，错误不得包含 material。
type Provider interface {
	// Resolve 读取非空且有界的 secret value，并响应 context cancellation。
	Resolve(context.Context, Reference) (Value, error)
}

// EnvironmentFileProvider 只读取显式环境变量或绝对文件，不自动加载 .env。
type EnvironmentFileProvider struct {
	// lookupEnv 由 production constructor 绑定 os.LookupEnv，测试可注入确定性快照。
	lookupEnv func(string) (string, bool)
	// readFile 由 production constructor 绑定有界读取器，测试可注入读取失败。
	readFile func(string) ([]byte, error)
}

// NewEnvironmentFileProvider 返回使用进程环境和只读文件系统的 production provider。
func NewEnvironmentFileProvider() *EnvironmentFileProvider {
	return &EnvironmentFileProvider{lookupEnv: os.LookupEnv, readFile: readBoundedFile}
}

// Resolve 读取 reference，并将缺失、空值、超限或读取失败转换为脱敏分类。
func (provider *EnvironmentFileProvider) Resolve(ctx context.Context, reference Reference) (Value, error) {
	if provider == nil || provider.lookupEnv == nil || provider.readFile == nil {
		return Value{}, errors.New("resolve secret: provider is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, fmt.Errorf("resolve secret %s: canceled", reference)
	}
	var content []byte
	switch reference.source {
	case "env":
		value, exists := provider.lookupEnv(reference.identifier)
		if !exists {
			return Value{}, fmt.Errorf("resolve secret %s: missing", reference)
		}
		content = []byte(value)
	case "file":
		value, err := provider.readFile(reference.identifier)
		if err != nil {
			return Value{}, fmt.Errorf("resolve secret %s: read failed", reference)
		}
		// Docker/Kubernetes secret 文件通常以单个换行结尾；只移除该传输分隔符，不修改其他空白。
		content = trimOneTrailingNewline(value)
	default:
		return Value{}, errors.New("resolve secret: invalid reference")
	}
	defer clear(content)
	if len(content) == 0 {
		return Value{}, fmt.Errorf("resolve secret %s: empty", reference)
	}
	if len(content) > maximumBytes {
		return Value{}, fmt.Errorf("resolve secret %s: exceeds size limit", reference)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return Value{}, fmt.Errorf("resolve secret %s: contains forbidden NUL", reference)
	}
	return Value{content: append([]byte(nil), content...)}, nil
}

// readBoundedFile 在分配前限制读取量，超限由 Resolve 转换为稳定分类。
func readBoundedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 只读 secret 的关闭错误不改变已读取内容，也没有可执行的恢复动作。
		_ = file.Close()
	}()
	return io.ReadAll(io.LimitReader(file, maximumBytes+1))
}

// trimOneTrailingNewline 只移除 secret volume 常见的单个传输换行，不修改有效空白。
func trimOneTrailingNewline(value []byte) []byte {
	if bytes.HasSuffix(value, []byte("\r\n")) {
		return value[:len(value)-2]
	}
	if bytes.HasSuffix(value, []byte("\n")) {
		return value[:len(value)-1]
	}
	return value
}
