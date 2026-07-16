package testclient

import (
	"bytes"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// TestSecretLifecycleAndRedaction 验证 credential 消费、轮换、清除和格式化均 fail closed。
func TestSecretLifecycleAndRedaction(t *testing.T) {
	const raw = "credential-that-must-never-appear"
	secret, err := NewSecret(raw)
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	logger.Info("credential", "value", secret)
	formatted := fmt.Sprintf("%s %v %#v %q", secret, secret, secret, secret)
	if strings.Contains(formatted+log.String(), raw) || !strings.Contains(formatted, redactedValue) {
		t.Fatalf("credential formatting leaked or omitted redaction: %q %q", formatted, log.String())
	}
	value, err := secret.Take()
	if err != nil || value != raw {
		t.Fatalf("take secret value=%q err=%v", value, err)
	}
	if _, err := secret.Reveal(); err == nil {
		t.Fatal("consumed credential remained available")
	}
	if err := secret.Replace("rotated"); err != nil {
		t.Fatalf("replace secret: %v", err)
	}
	secret.Clear()
	secret.Clear()
	if _, err := secret.Reveal(); err == nil {
		t.Fatal("cleared credential remained available")
	}
}

// TestSecretConcurrentLifecycle 验证并发读取、替换和清除没有数据竞争或 panic。
func TestSecretConcurrentLifecycle(t *testing.T) {
	secret, err := NewSecret("initial")
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}
	var group sync.WaitGroup
	for index := 0; index < 32; index++ {
		group.Add(1)
		go func(value int) {
			defer group.Done()
			if value%3 == 0 {
				_ = secret.Replace(fmt.Sprintf("value-%d", value))
			} else if value%3 == 1 {
				_, _ = secret.Reveal()
			} else {
				secret.Clear()
			}
		}(index)
	}
	group.Wait()
}

// TestActorAndIdentityFormatting 验证 actor 日志只含低敏 label，随机 identity 符合公开 grammar。
func TestActorAndIdentityFormatting(t *testing.T) {
	actor, err := NewActor()
	if err != nil {
		t.Fatalf("new actor: %v", err)
	}
	actor.AccountID = "account-secretish-identity"
	actor.PlayerID = "player-secretish-identity"
	actor.SessionID = "session-secretish-identity"
	formatted := fmt.Sprintf("%v %#v", actor, actor)
	for _, forbidden := range []string{actor.AccountID, actor.PlayerID, actor.SessionID} {
		if strings.Contains(formatted, forbidden) {
			t.Fatalf("actor formatting leaked %q", forbidden)
		}
	}
	if !regexp.MustCompile(`^actor_[0-9a-f]{16}$`).MatchString(actor.Label) {
		t.Fatalf("actor label=%q", actor.Label)
	}
	identity, err := NewIdentity("command", 16)
	if err != nil || !regexp.MustCompile(`^command_[0-9a-f]{32}$`).MatchString(identity) {
		t.Fatalf("identity=%q err=%v", identity, err)
	}
	for _, prefix := range []string{"", "A", "with-hyphen", "abcdefghijklmnopq"} {
		if _, err := NewIdentity(prefix, 16); err == nil {
			t.Fatalf("invalid prefix %q was accepted", prefix)
		}
	}
}
