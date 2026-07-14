package secret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// TestReferenceValidation 固定 env/file 白名单与绝对路径约束。
func TestReferenceValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// value 是待验证且不包含真实凭据的 reference fixture。
		value string
		// valid 表示 parser 是否必须接受该来源与格式。
		valid bool
	}{
		{value: "env:IHOMELAND_MYSQL_PASSWORD", valid: true},
		{value: `file:C:\run\secrets\mysql-password`, valid: true},
		{value: "env:lowercase", valid: false},
		{value: "file:relative", valid: false},
		{value: "vault:path", valid: false},
		{value: "", valid: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			_, err := ParseReference(test.value)
			if (err == nil) != test.valid {
				t.Fatalf("ParseReference(%q) error = %v", test.value, err)
			}
		})
	}
}

// TestReferenceFormattingRedactsFilePath 防止默认、Go、日志与文本编码展开 secret 路径。
func TestReferenceFormattingRedactsFilePath(t *testing.T) {
	t.Parallel()

	const privatePath = `C:\private\mysql-password`
	reference, err := ParseReference("file:" + privatePath)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := reference.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{fmt.Sprint(reference), fmt.Sprintf("%#v", reference), reference.LogValue().String(), string(encoded)} {
		if strings.Contains(output, privatePath) {
			t.Fatalf("reference 格式化泄露路径：%q", output)
		}
	}
}

// TestEnvironmentFileProviderResolvesAndRedacts 验证两种来源读取一致且所有默认编码保持脱敏。
func TestEnvironmentFileProviderResolvesAndRedacts(t *testing.T) {
	t.Parallel()

	const material = "highly-sensitive-value"
	provider := &EnvironmentFileProvider{
		lookupEnv: func(name string) (string, bool) { return material, name == "TEST_SECRET" },
		readFile:  func(string) ([]byte, error) { return []byte(material + "\r\n"), nil },
	}

	for _, raw := range []string{"env:TEST_SECRET", `file:C:\run\secret`} {
		reference, err := ParseReference(raw)
		if err != nil {
			t.Fatal(err)
		}
		value, err := provider.Resolve(context.Background(), reference)
		if err != nil {
			t.Fatalf("Resolve(%s) error = %v", reference, err)
		}
		if err := value.Expose(func(content []byte) error {
			if string(content) != material {
				t.Fatalf("secret content = %q", content)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}

		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		outputs := []string{fmt.Sprint(value), fmt.Sprintf("%#v", value), string(encoded), value.LogValue().String()}
		for _, output := range outputs {
			if strings.Contains(output, material) {
				t.Fatalf("格式化泄露 secret：%q", output)
			}
		}
	}
}

// TestEnvironmentFileProviderFailuresDoNotLeak 防止读取错误或敏感路径进入 error chain。
func TestEnvironmentFileProviderFailuresDoNotLeak(t *testing.T) {
	t.Parallel()

	const material = "sensitive-error-material"
	provider := &EnvironmentFileProvider{
		lookupEnv: func(string) (string, bool) { return "", false },
		readFile:  func(string) ([]byte, error) { return nil, errors.New(material) },
	}
	tests := []struct {
		// name 描述 provider 失败阶段。
		name string
		// reference 使用测试路径/键，不包含真实 secret。
		reference string
		// context 覆盖正常与 I/O 前取消状态。
		context context.Context
	}{
		{name: "环境变量缺失", reference: "env:MISSING", context: context.Background()},
		{name: "文件读取失败", reference: `file:C:\private\` + material, context: context.Background()},
		{name: "已取消", reference: "env:MISSING", context: canceledContext()},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			reference, err := ParseReference(test.reference)
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Resolve(test.context, reference)
			if err == nil || strings.Contains(err.Error(), material) {
				t.Fatalf("Resolve() 必须返回脱敏错误：%v", err)
			}
		})
	}
}

// TestEnvironmentFileProviderRejectsEmptyOversizeAndNUL 保护 secret 的非空、内存和字符串安全边界。
func TestEnvironmentFileProviderRejectsEmptyOversizeAndNUL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// name 描述被破坏的 secret 内容边界。
		name string
		// value 是 provider 返回的测试材料。
		value string
		// want 是稳定脱敏失败分类。
		want string
	}{
		{name: "空值", value: "", want: "empty"},
		{name: "超限", value: strings.Repeat("x", maximumBytes+1), want: "size limit"},
		{name: "NUL", value: "x\x00y", want: "NUL"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			provider := &EnvironmentFileProvider{
				lookupEnv: func(string) (string, bool) { return test.value, true },
				readFile:  func(string) ([]byte, error) { return nil, errors.New("unused") },
			}
			reference, _ := ParseReference("env:TEST_SECRET")
			_, err := provider.Resolve(context.Background(), reference)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Resolve() error = %v，应包含 %q", err, test.want)
			}
		})
	}
}

// TestValueExposeRequiresCallback 防止无消费 owner 的 secret 暴露并固定 slog 类型。
func TestValueExposeRequiresCallback(t *testing.T) {
	t.Parallel()

	if err := (Value{content: []byte("secret")}).Expose(nil); err == nil {
		t.Fatal("nil callback 应被拒绝")
	}
	if got := (Value{}).LogValue(); got.Kind() != slog.KindString {
		t.Fatalf("LogValue kind = %v", got.Kind())
	}
}

// TestValueDestroyClearsOwnedBytes 验证 driver 配置完成后 secret backing memory 会被主动清零。
func TestValueDestroyClearsOwnedBytes(t *testing.T) {
	t.Parallel()

	value := Value{content: []byte("secret")}
	owned := value.content
	value.Destroy()
	if value.content != nil {
		t.Fatal("Destroy() 必须释放当前 value 的 slice owner")
	}
	for _, current := range owned {
		if current != 0 {
			t.Fatal("Destroy() 必须清零原始 backing bytes")
		}
	}
}

// canceledContext 构造在 provider I/O 前已经取消的 deterministic context。
func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
