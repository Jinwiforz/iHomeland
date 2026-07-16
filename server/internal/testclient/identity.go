package testclient

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

const redactedValue = "<redacted>"

// Secret 保存资格运行中的短期 credential，并在格式化时始终脱敏。
// Secret 拥有的 byte buffer 会在消费、替换或场景结束时尽力覆零；向 Go 标准库传值时形成的短期
// immutable string 无法可靠覆零，因此调用方必须保持 call-local，且不得记录或长期保存。
type Secret struct {
	// mutex 串行化 credential 的读取、替换与清除。
	mutex sync.Mutex
	// value 是仅供紧邻网络调用转换为 string 的自有 credential buffer。
	value []byte
}

// NewSecret 从非空 credential 创建默认脱敏容器。
func NewSecret(value string) (*Secret, error) {
	if value == "" {
		return nil, errors.New("credential is empty")
	}
	return &Secret{value: []byte(value)}, nil
}

// Reveal 向紧邻的网络调用返回当前 credential；调用方不得记录返回值。
func (secret *Secret) Reveal() (string, error) {
	if secret == nil {
		return "", errors.New("credential is unavailable")
	}
	secret.mutex.Lock()
	defer secret.mutex.Unlock()
	if len(secret.value) == 0 {
		return "", errors.New("credential was cleared")
	}
	return string(secret.value), nil
}

// Replace 清除旧 credential 并保存轮换后的非空值。
func (secret *Secret) Replace(value string) error {
	if secret == nil || value == "" {
		return errors.New("replacement credential is invalid")
	}
	secret.mutex.Lock()
	clear(secret.value)
	secret.value = []byte(value)
	secret.mutex.Unlock()
	return nil
}

// Take 原子取得并清除一次性 credential。
func (secret *Secret) Take() (string, error) {
	if secret == nil {
		return "", errors.New("credential is unavailable")
	}
	secret.mutex.Lock()
	defer secret.mutex.Unlock()
	if len(secret.value) == 0 {
		return "", errors.New("credential was already consumed")
	}
	value := string(secret.value)
	clear(secret.value)
	secret.value = nil
	return value, nil
}

// Clear 丢弃当前 credential；重复调用安全。
func (secret *Secret) Clear() {
	if secret == nil {
		return
	}
	secret.mutex.Lock()
	clear(secret.value)
	secret.value = nil
	secret.mutex.Unlock()
}

// Close 让 Secret 可由场景资源栈逆序清除；重复调用安全。
func (secret *Secret) Close() error {
	secret.Clear()
	return nil
}

// String 防止 `%s`、`%v` 与默认日志打印 credential。
func (*Secret) String() string { return redactedValue }

// GoString 防止 `%#v` 展开 credential 容器内部状态。
func (*Secret) GoString() string { return redactedValue }

// LogValue 防止 slog 反射 credential 容器字段。
func (*Secret) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// Actor 保存单个资格测试身份的 session 与公开 identity。
// 每个 actor 必须独占自己的 credential 和 realtime connection。
type Actor struct {
	// Label 是报告允许记录的随机低敏本地标签。
	Label string
	// AccountID 是服务端返回的公开账号 identity。
	AccountID string
	// PlayerID 是服务端公开流程返回的 Player identity。
	PlayerID string
	// SessionID 是服务端返回的当前 session identity。
	SessionID string
	// SessionEpoch 是服务端返回的当前单调 session epoch。
	SessionEpoch uint64
	// AccessToken 是当前 bearer credential。
	AccessToken *Secret
	// RefreshToken 是当前轮换 credential。
	RefreshToken *Secret
}

// NewActor 创建不含服务器身份和 credential 的随机低敏 actor context。
func NewActor() (*Actor, error) {
	label, err := NewIdentity("actor", 8)
	if err != nil {
		return nil, err
	}
	return &Actor{Label: label}, nil
}

// ClearCredentials 清除 actor 持有的全部 session credential。
func (actor *Actor) ClearCredentials() {
	if actor == nil {
		return
	}
	actor.AccessToken.Clear()
	actor.RefreshToken.Clear()
	actor.AccessToken = nil
	actor.RefreshToken = nil
}

// String 只输出低敏 actor 标签，不输出服务端 identity 或 credential。
func (actor *Actor) String() string {
	if actor == nil || actor.Label == "" {
		return "actor(<invalid>)"
	}
	return "actor(" + actor.Label + ")"
}

// GoString 与 String 保持相同低敏投影。
func (actor *Actor) GoString() string { return actor.String() }

// LogValue 只向结构化日志暴露低敏 actor 标签。
func (actor *Actor) LogValue() slog.Value { return slog.StringValue(actor.String()) }

// NewIdentity 使用 CSPRNG 创建符合公开 ASCII grammar 的 correlation、idempotency 或本地标签。
func NewIdentity(prefix string, randomBytes int) (string, error) {
	if !validIdentityPrefix(prefix) || randomBytes < 8 || randomBytes > 32 {
		return "", errors.New("identity parameters are invalid")
	}
	random := make([]byte, randomBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate identity randomness: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(random), nil
}

// validIdentityPrefix 限制本地 identity 前缀为小写 ASCII 字母。
func validIdentityPrefix(prefix string) bool {
	if len(prefix) < 2 || len(prefix) > 16 {
		return false
	}
	for _, character := range prefix {
		if character < 'a' || character > 'z' {
			return false
		}
	}
	return true
}
