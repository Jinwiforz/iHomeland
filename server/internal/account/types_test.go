package account

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestUsernameValidation 固定跨语言可移植的 username 接受集合和 canonical form。
func TestUsernameValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		// name 标识当前 username 边界场景。
		name string
		// input 是未经规范化的调用方输入。
		input string
		// canonical 是成功时预期的唯一 repository key。
		canonical string
		// valid 决定该输入是否属于允许集合。
		valid bool
	}{
		{name: "lowercase", input: "player.one", canonical: "player.one", valid: true},
		{name: "case folded", input: "Player-One_2", canonical: "player-one_2", valid: true},
		{name: "minimum", input: "a01", canonical: "a01", valid: true},
		{name: "too short", input: "a1", valid: false},
		{name: "leading punctuation", input: ".player", valid: false},
		{name: "trailing punctuation", input: "player-", valid: false},
		{name: "space", input: "player one", valid: false},
		{name: "unicode look alike", input: "playеr", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			username, err := NewUsername(test.input)
			if test.valid && err != nil {
				t.Fatalf("NewUsername() error = %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("NewUsername() unexpectedly succeeded")
			}
			if test.valid && username.String() != test.canonical {
				t.Fatalf("canonical = %q, want %q", username.String(), test.canonical)
			}
		})
	}
}

// FuzzUsernameValidation 确认任意 bytes 不会绕过 canonical ASCII 不变量或触发 panic。
func FuzzUsernameValidation(f *testing.F) {
	f.Add("Player.One")
	f.Add("a01")
	f.Add("用户")
	f.Fuzz(func(t *testing.T, input string) {
		username, err := NewUsername(input)
		if err != nil {
			return
		}
		if username.String() != strings.ToLower(username.String()) {
			t.Fatalf("canonical username contains uppercase: %q", username.String())
		}
		if len(username.String()) < minimumUsernameBytes || len(username.String()) > maximumUsernameBytes {
			t.Fatalf("accepted username has invalid size: %d", len(username.String()))
		}
	})
}

// TestDisplayNameNormalization 验证 NFC、Unicode whitespace 折叠和危险字符拒绝。
func TestDisplayNameNormalization(t *testing.T) {
	t.Parallel()
	name, err := NewDisplayName("  Cafe\u0301\u2003\u00a0  Player  ")
	if err != nil {
		t.Fatalf("NewDisplayName() error = %v", err)
	}
	if got := name.String(); got != "Café Player" {
		t.Fatalf("display name = %q, want %q", got, "Café Player")
	}
	for _, input := range []string{"", "   ", "safe\u202ename", "safe\x00name", strings.Repeat("界", 33), string([]byte{0xff})} {
		if _, err := NewDisplayName(input); err == nil {
			t.Fatalf("NewDisplayName(%q) unexpectedly succeeded", input)
		}
	}
}

// FuzzDisplayNameNormalization 确认成功结果始终是有效 UTF-8、NFC 后有界公开文本。
func FuzzDisplayNameNormalization(f *testing.F) {
	f.Add("  玩家  One ")
	f.Add("Cafe\u0301")
	f.Add("\u202eevil")
	f.Fuzz(func(t *testing.T, input string) {
		name, err := NewDisplayName(input)
		if err != nil {
			return
		}
		if !utf8.ValidString(name.String()) || utf8.RuneCountInString(name.String()) > maximumDisplayNameRunes {
			t.Fatalf("accepted display name violates output bounds: %q", name.String())
		}
		if strings.TrimSpace(name.String()) != name.String() || strings.Contains(name.String(), "  ") {
			t.Fatalf("accepted display name has unstable whitespace: %q", name.String())
		}
	})
}

// TestCredentialValuesPreserveAndRedact 验证密码保持原始 bytes 且所有默认输出仅含占位符。
func TestCredentialValuesPreserveAndRedact(t *testing.T) {
	t.Parallel()
	plaintext := "  CaseSensitive密码  "
	register, err := NewRegisterPassword(plaintext)
	if err != nil {
		t.Fatalf("NewRegisterPassword() error = %v", err)
	}
	login, err := NewLoginPassword(plaintext)
	if err != nil {
		t.Fatalf("NewLoginPassword() error = %v", err)
	}
	hash, err := NewCredentialHash("$fake$v=1$abcdef")
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}
	if register.Value() != plaintext || login.Value() != plaintext {
		t.Fatal("password value was normalized")
	}
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	registerCommand := NewRegisterCommand("sensitive.user", plaintext, "Sensitive User")
	loginCommand := NewLoginCommand("sensitive.user", plaintext)
	logger.Info("credentials", "register", register, "login", login, "hash", hash, "register_command", registerCommand, "login_command", loginCommand)
	formatted := fmt.Sprintf("%v %#v %v %#v %v %#v %v %#v %v %#v %v %#v", register, register, login, login, hash, hash, RegisterPassword{}, RegisterPassword{}, LoginPassword{}, LoginPassword{}, CredentialHash{}, CredentialHash{})
	formatted += fmt.Errorf("credential boundary: %v %v %v", register, login, hash).Error()
	formatted += fmt.Sprintf(" %v %#v %v %#v %v %#v", registerCommand, registerCommand, loginCommand, loginCommand, RegisterCommand{}, LoginCommand{})
	combined := formatted + log.String()
	if strings.Contains(combined, plaintext) || strings.Contains(combined, hash.Encoded()) || strings.Contains(combined, "sensitive.user") {
		t.Fatalf("credential leaked through formatting: %s", combined)
	}
	if strings.Count(combined, credentialPlaceholder) < 6 {
		t.Fatalf("credential placeholder missing: %s", combined)
	}
	for _, invalid := range []string{"", strings.Repeat("a", maximumCredentialHashBytes+1), "hash with space"} {
		if _, err := NewCredentialHash(invalid); err == nil {
			t.Fatalf("NewCredentialHash(%q) unexpectedly succeeded", invalid)
		}
	}
}

// TestAccountIdentityAndSummary 验证服务端 identity 前缀和公开投影不会携带内部字段。
func TestAccountIdentityAndSummary(t *testing.T) {
	t.Parallel()
	account := mustAccount(t, "alpha", StatusActive)
	summary := account.Summary()
	if !summary.Valid() || summary.AccountID() != account.ID() || summary.DisplayName() != account.DisplayName() || !summary.CreatedAt().Equal(account.CreatedAt()) {
		t.Fatal("account summary does not preserve allowed public facts")
	}
	formatted := fmt.Sprintf("%v %#v", account, account)
	if strings.Contains(formatted, account.Username().String()) || strings.Contains(formatted, account.PlayerID().String()) {
		t.Fatalf("account formatting leaked private identity: %s", formatted)
	}
	if _, err := NewAccountID("ply_wrong"); err == nil {
		t.Fatal("account ID accepted player prefix")
	}
	if _, err := NewPlayerID("acc_wrong"); err == nil {
		t.Fatal("player ID accepted account prefix")
	}
}

// mustAccount 创建满足 repository contract 的固定测试账号。
func mustAccount(t testing.TB, suffix string, status Status) Account {
	t.Helper()
	accountID, err := NewAccountID("acc_" + suffix)
	if err != nil {
		t.Fatalf("NewAccountID() error = %v", err)
	}
	playerID, err := NewPlayerID("ply_" + suffix)
	if err != nil {
		t.Fatalf("NewPlayerID() error = %v", err)
	}
	username, err := NewUsername(suffix + "01")
	if err != nil {
		t.Fatalf("NewUsername() error = %v", err)
	}
	displayName, err := NewDisplayName("Player " + suffix)
	if err != nil {
		t.Fatalf("NewDisplayName() error = %v", err)
	}
	account, err := NewAccount(accountID, playerID, username, displayName, status, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	return account
}
