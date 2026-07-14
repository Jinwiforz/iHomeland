package account

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	domain "github.com/jinwiforz/ihomeland/server/internal/account"
	"golang.org/x/crypto/argon2"
)

const (
	// argonVersion 固定 PHC v=19；unknown version 在分配昂贵内存前拒绝。
	argonVersion = argon2.Version
	// argonMemoryKiB 采用 RFC 9106 memory-constrained recommended profile 的 64 MiB。
	argonMemoryKiB uint32 = 64 * 1024
	// argonIterations 固定 current profile 的时间成本。
	argonIterations uint32 = 3
	// argonLanes 固定 current profile 的并行 lanes；不改变单次 64 MiB 总内存预算。
	argonLanes uint8 = 4
	// argonSaltBytes 为每条 credential 提供独立 128-bit CSPRNG salt。
	argonSaltBytes = 16
	// argonTagBytes 固定 256-bit verifier tag。
	argonTagBytes uint32 = 32
	// maximumHashConcurrency 防止错误配置放大同进程 Argon2 内存占用。
	maximumHashConcurrency = 16
	// hasherPlaceholder 是 hasher 与安全错误唯一允许的默认诊断文本前缀。
	hasherPlaceholder = "[PASSWORD_HASHER]"
)

// argonProfile 是 parser 验证后的有界计算参数。
type argonProfile struct {
	// memoryKiB 是单次计算的总内存预算(KiB)。
	memoryKiB uint32
	// iterations 是固定迭代次数。
	iterations uint32
	// lanes 是内部并行 lanes 数量。
	lanes uint8
}

// Hasher 使用固定 Argon2id profile 实现 production CredentialHasher。
//
// slots 限制并发计算的总内存放大；对象不保存 plaintext、salt、tag 或完整 PHC。Hasher
// 允许并发调用，且不会为 context cancellation 创建不可停止的后台 goroutine。
type Hasher struct {
	// random 只在 Hash 当前调用中填充新 salt；production 固定 crypto/rand.Reader。
	random io.Reader
	// slots 是 acquisition 可响应 context 的有界并发门。
	slots chan struct{}
}

var _ domain.CredentialHasher = (*Hasher)(nil)

// NewHasher 创建 production hasher，并生成供 unknown username 使用的同成本 dummy hash。
//
// maxConcurrent 是同时进行的 64 MiB Argon2 计算数，不是 lanes 数；调用方必须按进程内存
// 预算选择 1-16。Dummy plaintext 由 CSPRNG 临时生成且不会返回或保存。
func NewHasher(maxConcurrent int) (*Hasher, domain.CredentialHash, error) {
	hasher, err := newHasher(rand.Reader, maxConcurrent)
	if err != nil {
		return nil, domain.CredentialHash{}, err
	}
	dummyMaterial := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dummyMaterial); err != nil {
		return nil, domain.CredentialHash{}, newHasherError("dummy_entropy")
	}
	defer clear(dummyMaterial)
	dummyPassword, err := domain.NewRegisterPassword(base64.RawURLEncoding.EncodeToString(dummyMaterial))
	if err != nil {
		return nil, domain.CredentialHash{}, newHasherError("dummy_password")
	}
	dummyHash, err := hasher.Hash(context.Background(), dummyPassword)
	if err != nil {
		return nil, domain.CredentialHash{}, err
	}
	return hasher, dummyHash, nil
}

// newHasher 构造可注入确定性 entropy 的测试实例，不生成 dummy credential。
func newHasher(random io.Reader, maxConcurrent int) (*Hasher, error) {
	if random == nil || maxConcurrent < 1 || maxConcurrent > maximumHashConcurrency {
		return nil, errors.New("password hasher configuration is invalid")
	}
	return &Hasher{random: random, slots: make(chan struct{}, maxConcurrent)}, nil
}

// Hash 为注册密码生成 current-profile canonical PHC string。
func (hasher *Hasher) Hash(ctx context.Context, password domain.RegisterPassword) (domain.CredentialHash, error) {
	if hasher == nil || hasher.random == nil || hasher.slots == nil || !password.Valid() {
		return domain.CredentialHash{}, newHasherError("hash_input")
	}
	release, err := hasher.acquire(ctx)
	if err != nil {
		return domain.CredentialHash{}, err
	}
	defer release()

	salt := make([]byte, argonSaltBytes)
	if _, err := io.ReadFull(hasher.random, salt); err != nil {
		clear(salt)
		return domain.CredentialHash{}, newHasherError("salt_entropy")
	}
	plaintext := []byte(password.Value())
	tag := argon2.IDKey(plaintext, salt, argonIterations, argonMemoryKiB, argonLanes, argonTagBytes)
	clear(plaintext)
	if err := ctx.Err(); err != nil {
		clear(salt)
		clear(tag)
		return domain.CredentialHash{}, newHasherError("canceled")
	}
	encoded := encodePHC(currentArgonProfile(), salt, tag)
	clear(salt)
	clear(tag)
	hash, err := domain.NewCredentialHash(encoded)
	if err != nil {
		return domain.CredentialHash{}, newHasherError("encode")
	}
	return hash, nil
}

// Verify 严格解析有界 PHC profile，再以 constant-time compare 验证原始登录密码。
func (hasher *Hasher) Verify(ctx context.Context, hash domain.CredentialHash, password domain.LoginPassword) (bool, error) {
	if hasher == nil || hasher.slots == nil || !hash.Valid() || !password.Valid() {
		return false, newHasherError("verify_input")
	}
	profile, salt, expected, err := parsePHC(hash.Encoded())
	if err != nil {
		return false, newHasherError("phc_format")
	}
	defer clear(salt)
	defer clear(expected)
	release, err := hasher.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	plaintext := []byte(password.Value())
	actual := argon2.IDKey(plaintext, salt, profile.iterations, profile.memoryKiB, profile.lanes, uint32(len(expected)))
	clear(plaintext)
	defer clear(actual)
	if err := ctx.Err(); err != nil {
		return false, newHasherError("canceled")
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

// String 返回不包含配置或运行状态的稳定占位符。
func (*Hasher) String() string { return hasherPlaceholder }

// GoString 阻止默认 Go-syntax 展开并发门或 entropy reader。
func (*Hasher) GoString() string { return hasherPlaceholder }

// LogValue 让 slog 不展开内部依赖。
func (*Hasher) LogValue() slog.Value { return slog.StringValue(hasherPlaceholder) }

// acquire 等待并发预算；一旦返回，调用方必须 defer release 且同步完成计算。
func (hasher *Hasher) acquire(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, newHasherError("context")
	}
	select {
	case hasher.slots <- struct{}{}:
		return func() { <-hasher.slots }, nil
	case <-ctx.Done():
		return nil, newHasherError("canceled")
	}
}

// currentArgonProfile 返回唯一允许新写入的参数集合。
func currentArgonProfile() argonProfile {
	return argonProfile{memoryKiB: argonMemoryKiB, iterations: argonIterations, lanes: argonLanes}
}

// encodePHC 使用无 padding standard base64 生成 canonical Argon2id PHC string。
func encodePHC(profile argonProfile, salt []byte, tag []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argonVersion, profile.memoryKiB,
		profile.iterations, profile.lanes, base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(tag))
}

// parsePHC 在调用 Argon2 前拒绝未知、非 canonical 或超预算参数。
func parsePHC(encoded string) (argonProfile, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argonVersion) {
		return argonProfile{}, nil, nil, errors.New("password hash header is invalid")
	}
	profile := currentArgonProfile()
	expectedParameters := fmt.Sprintf("m=%d,t=%d,p=%d", profile.memoryKiB, profile.iterations, profile.lanes)
	if parts[3] != expectedParameters {
		return argonProfile{}, nil, nil, errors.New("password hash profile is unsupported")
	}
	salt, err := decodeCanonicalBase64(parts[4], argonSaltBytes)
	if err != nil {
		return argonProfile{}, nil, nil, errors.New("password hash salt is invalid")
	}
	tag, err := decodeCanonicalBase64(parts[5], int(argonTagBytes))
	if err != nil {
		clear(salt)
		return argonProfile{}, nil, nil, errors.New("password hash tag is invalid")
	}
	return profile, salt, tag, nil
}

// validatePHC 供 repository 在持久化和 hydration 边界验证 current accepted profiles。
func validatePHC(encoded string) error {
	_, salt, tag, err := parsePHC(encoded)
	clear(salt)
	clear(tag)
	return err
}

// decodeCanonicalBase64 拒绝 padding、alternate alphabet 与非规范等价表达。
func decodeCanonicalBase64(value string, expectedBytes int) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil || len(decoded) != expectedBytes || base64.RawStdEncoding.EncodeToString(decoded) != value {
		clear(decoded)
		return nil, errors.New("base64 value is invalid")
	}
	return decoded, nil
}

// hasherError 只保留固定低基数阶段，不包装 plaintext、PHC 或底层 entropy 文本。
type hasherError struct {
	// stage 是代码定义的固定失败分类。
	stage string
}

// Error 返回安全稳定文本。
func (failure *hasherError) Error() string { return "password hashing failed (" + failure.stage + ")" }

// newHasherError 构造不含外部输入的安全错误。
func newHasherError(stage string) error { return &hasherError{stage: stage} }
