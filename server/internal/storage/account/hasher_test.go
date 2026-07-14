package account

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	domain "github.com/jinwiforz/ihomeland/server/internal/account"
)

// TestHasherDeterministicVector 验证固定 salt 的 canonical PHC 与成功/失败校验。
func TestHasherDeterministicVector(t *testing.T) {
	hasher, err := newHasher(bytes.NewReader(bytes.Repeat([]byte{0x2a}, argonSaltBytes)), 1)
	if err != nil {
		t.Fatalf("newHasher() error = %v", err)
	}
	password, err := domain.NewRegisterPassword("  StrongPass12  ")
	if err != nil {
		t.Fatalf("NewRegisterPassword() error = %v", err)
	}
	hash, err := hasher.Hash(context.Background(), password)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	const expected = "$argon2id$v=19$m=65536,t=3,p=4$KioqKioqKioqKioqKioqKg$+Tl+ay8OPb3dhToKRkb65sFBJhil/hiw9gpZAqVamu8"
	if expected != hash.Encoded() {
		t.Fatalf("Hash() = %q", hash.Encoded())
	}
	login, _ := domain.NewLoginPassword("  StrongPass12  ")
	matched, err := hasher.Verify(context.Background(), hash, login)
	if err != nil || !matched {
		t.Fatalf("Verify(correct) = %v, %v", matched, err)
	}
	wrong, _ := domain.NewLoginPassword("WrongPassword1")
	matched, err = hasher.Verify(context.Background(), hash, wrong)
	if err != nil || matched {
		t.Fatalf("Verify(wrong) = %v, %v", matched, err)
	}
}

// TestHasherRejectsMalformedPHC 验证恶意参数在昂贵计算前被固定格式门拒绝。
func TestHasherRejectsMalformedPHC(t *testing.T) {
	hasher, _ := newHasher(strings.NewReader(strings.Repeat("s", 64)), 1)
	password, _ := domain.NewLoginPassword("StrongPassword1")
	cases := []string{
		"$argon2i$v=19$m=65536,t=3,p=4$KioqKioqKioqKioqKioqKg$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=16$m=65536,t=3,p=4$KioqKioqKioqKioqKioqKg$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=1048576,t=3,p=4$KioqKioqKioqKioqKioqKg$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=65536,t=3,p=4$bad=$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	for _, encoded := range cases {
		hash, err := domain.NewCredentialHash(encoded)
		if err != nil {
			continue
		}
		if matched, verifyErr := hasher.Verify(context.Background(), hash, password); verifyErr == nil || matched {
			t.Fatalf("Verify(%q) = %v, %v", encoded, matched, verifyErr)
		}
	}
}

// TestHasherConcurrencyGateCancellation 验证等待内存预算的调用可由 context 取消。
func TestHasherConcurrencyGateCancellation(t *testing.T) {
	hasher, _ := newHasher(strings.NewReader(strings.Repeat("s", 64)), 1)
	hasher.slots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	password, _ := domain.NewRegisterPassword("StrongPassword1")
	if _, err := hasher.Hash(ctx, password); err == nil {
		t.Fatal("Hash() accepted canceled waiter")
	}
	<-hasher.slots
	if text := fmt.Sprintf("%v %#v", hasher, hasher); strings.Contains(text, "Reader") || !strings.Contains(text, hasherPlaceholder) {
		t.Fatalf("Hasher formatting leaked internals: %s", text)
	}
}

// TestProductionHasherProvidesSameProfileDummy 验证 production constructor 返回可由同实例校验的 current-profile dummy。
func TestProductionHasherProvidesSameProfileDummy(t *testing.T) {
	hasher, dummy, err := NewHasher(1)
	if err != nil {
		t.Fatalf("NewHasher() error = %v", err)
	}
	if _, _, _, err := parsePHC(dummy.Encoded()); err != nil {
		t.Fatalf("dummy hash profile error = %v", err)
	}
	password, _ := domain.NewLoginPassword("unrelated")
	matched, err := hasher.Verify(context.Background(), dummy, password)
	if err != nil || matched {
		t.Fatalf("Verify(dummy) = %v, %v", matched, err)
	}
}

// FuzzParsePHC 验证任意文本不会绕过 canonical parser 或触发 panic。
func FuzzParsePHC(f *testing.F) {
	f.Add("$argon2id$v=19$m=65536,t=3,p=4$KioqKioqKioqKioqKioqKg$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	f.Add("")
	f.Fuzz(func(t *testing.T, encoded string) {
		profile, salt, tag, err := parsePHC(encoded)
		if err != nil {
			return
		}
		defer clear(salt)
		defer clear(tag)
		if profile != currentArgonProfile() || len(salt) != argonSaltBytes || len(tag) != int(argonTagBytes) {
			t.Fatal("parsePHC accepted non-current shape")
		}
		if encodePHC(profile, salt, tag) != encoded {
			t.Fatal("parsePHC accepted non-canonical encoding")
		}
	})
}

// BenchmarkHasherHash 记录 current profile 在目标机器上的单次成本，不设置易波动的墙钟断言。
func BenchmarkHasherHash(b *testing.B) {
	password, _ := domain.NewRegisterPassword("StrongPassword1")
	for index := 0; index < b.N; index++ {
		hasher, _ := newHasher(bytes.NewReader(bytes.Repeat([]byte{byte(index + 1)}, argonSaltBytes)), 1)
		if _, err := hasher.Hash(context.Background(), password); err != nil {
			b.Fatal(err)
		}
	}
}
