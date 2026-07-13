package session

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// deterministicSecretGenerator 提供可重复但非生产安全的测试 entropy。
type deterministicSecretGenerator struct {
	// mutex 保证并发 refresh 测试不会让测试 entropy 自身产生 data race。
	mutex sync.Mutex
	// next 使连续生成结果不同，同时保持测试完全确定。
	next byte
}

// zeroSecretGenerator 模拟返回成功但没有提供有效 entropy 的错误依赖。
type zeroSecretGenerator struct{}

// Fill 故意保留全零 buffer，用于验证 nonce 零值防线。
func (zeroSecretGenerator) Fill(_ []byte) error { return nil }

// Fill 使用递增字节覆盖完整 buffer，证明编码不依赖随机环境。
func (generator *deterministicSecretGenerator) Fill(buffer []byte) error {
	generator.mutex.Lock()
	defer generator.mutex.Unlock()
	for index := range buffer {
		buffer[index] = generator.next
		generator.next++
	}
	return nil
}

// TestSecretRoundTripAndRedaction 验证类型前缀、digest、显式 Reveal 与所有默认输出的脱敏行为。
func TestSecretRoundTripAndRedaction(t *testing.T) {
	generator := new(deterministicSecretGenerator)
	secret, err := NewSecret(SecretKindAccess, generator)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret.Reveal(), "ih_at_") || len(secret.Reveal()) < 32 {
		t.Fatal("access credential lacks stable type prefix or entropy")
	}
	parsed, err := ParseSecret(SecretKindAccess, secret.Reveal())
	if err != nil || parsed.Digest() != secret.Digest() {
		t.Fatalf("secret round trip failed: %v", err)
	}
	if fmt.Sprintf("%v %#v", secret, secret) != secretPlaceholder+" "+secretPlaceholder {
		t.Fatal("default formatting exposed secret internals")
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logger.Info("credential", "secret", secret, "digest", secret.Digest())
	if strings.Contains(output.String(), secret.Reveal()) || strings.Contains(output.String(), fmt.Sprintf("%x", secret.Digest().Bytes())) {
		t.Fatalf("structured log exposed credential material: %s", output.String())
	}
	if secret.Digest().Summary() == "" || len(secret.Digest().Summary()) != 16 {
		t.Fatal("digest summary must remain a fixed 64-bit correlation value")
	}
}

// TestSecretKindsCannotBeExchanged 防止 access、refresh 和 ticket 共享解析入口。
func TestSecretKindsCannotBeExchanged(t *testing.T) {
	generator := new(deterministicSecretGenerator)
	access, err := NewSecret(SecretKindAccess, generator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSecret(SecretKindRefresh, access.Reveal()); err == nil {
		t.Fatal("access token parsed as refresh token")
	}
	if _, err := NewSecret(SecretKindUnspecified, generator); err == nil {
		t.Fatal("unspecified secret kind generated a credential")
	}
	if (Digest{}).Valid() {
		t.Fatal("zero digest reported valid")
	}
}

// TestCryptoSecretGeneratorProducesDistinctMaterial 验证生产 CSPRNG 会覆盖完整 buffer。
func TestCryptoSecretGeneratorProducesDistinctMaterial(t *testing.T) {
	first := make([]byte, tokenSecretBytes)
	second := make([]byte, tokenSecretBytes)
	generator := CryptoSecretGenerator{}
	if err := generator.Fill(first); err != nil {
		t.Fatal(err)
	}
	if err := generator.Fill(second); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, make([]byte, tokenSecretBytes)) || bytes.Equal(first, second) {
		t.Fatal("crypto secret generator returned zero or repeated material")
	}
}

// FuzzParseSecret 固定 malformed 与超长输入不能 panic 或被错误信息回显。
func FuzzParseSecret(f *testing.F) {
	f.Add(uint8(SecretKindAccess), "")
	f.Add(uint8(SecretKindRefresh), "ih_rt_invalid")
	f.Add(uint8(SecretKindUnspecified), strings.Repeat("a", 4096))
	f.Fuzz(func(t *testing.T, kind uint8, raw string) {
		_, err := ParseSecret(SecretKind(kind), raw)
		if err == nil {
			return
		}
		switch err.Error() {
		case "secret kind is unsupported", "credential type is invalid", "credential encoding is invalid":
			// 固定消息证明错误不由 untrusted credential 拼接而成。
		default:
			t.Fatalf("parse returned an unstable error message: %q", err.Error())
		}
	})
}
