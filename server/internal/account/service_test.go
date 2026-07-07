package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"ihomeland/server/internal/storage"
)

func TestServiceRegisterLoginLogoutAndResume(t *testing.T) {
	now := time.Unix(100, 0)
	service := newTestService(t, &now)
	ctx := context.Background()

	registered, err := service.Register(ctx, RegisterRequest{
		Account:      "Tester",
		Password:     "secret",
		DisplayName:  "测试玩家",
		ConnectionID: "conn-1",
	})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if registered.Player.PlayerID == "" || registered.Session.Token == "" {
		t.Fatalf("register result missing player or session: %+v", registered)
	}
	if registered.Player.Account != "tester" {
		t.Fatalf("Account = %q, want tester", registered.Player.Account)
	}

	if _, err := service.Register(ctx, RegisterRequest{Account: "tester", Password: "secret"}); !errors.Is(err, ErrAccountAlreadyExists) {
		t.Fatalf("duplicate Register error = %v, want ErrAccountAlreadyExists", err)
	}

	loggedIn, err := service.Login(ctx, LoginRequest{
		Account:      "tester",
		Password:     "secret",
		ConnectionID: "conn-2",
	})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if loggedIn.Player.PlayerID != registered.Player.PlayerID {
		t.Fatalf("login player id = %q, want %q", loggedIn.Player.PlayerID, registered.Player.PlayerID)
	}

	resumed, err := service.ResumeSession(ctx, ResumeSessionRequest{
		SessionToken: loggedIn.Session.Token,
		ConnectionID: "conn-3",
	})
	if err != nil {
		t.Fatalf("ResumeSession returned error: %v", err)
	}
	if resumed.Player.PlayerID != registered.Player.PlayerID {
		t.Fatalf("resume player id = %q, want %q", resumed.Player.PlayerID, registered.Player.PlayerID)
	}

	if err := service.Logout(ctx, LogoutRequest{SessionToken: loggedIn.Session.Token}); err != nil {
		t.Fatalf("Logout returned error: %v", err)
	}
	if _, err := service.ResumeSession(ctx, ResumeSessionRequest{SessionToken: loggedIn.Session.Token}); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("ResumeSession after logout error = %v, want ErrSessionInvalid", err)
	}
}

func TestServiceRejectsInvalidCredentials(t *testing.T) {
	now := time.Unix(100, 0)
	service := newTestService(t, &now)
	ctx := context.Background()

	if _, err := service.Register(ctx, RegisterRequest{Account: "", Password: "secret"}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("Register empty account error = %v, want ErrCredentialInvalid", err)
	}
	if _, err := service.Register(ctx, RegisterRequest{Account: "tester", Password: ""}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("Register empty password error = %v, want ErrCredentialInvalid", err)
	}

	if _, err := service.Register(ctx, RegisterRequest{Account: "tester", Password: "secret"}); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if _, err := service.Login(ctx, LoginRequest{Account: "tester", Password: "wrong"}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("Login wrong password error = %v, want ErrCredentialInvalid", err)
	}
	if _, err := service.Login(ctx, LoginRequest{Account: "missing", Password: "secret"}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("Login missing account error = %v, want ErrCredentialInvalid", err)
	}
}

func TestServiceRejectsExpiredSession(t *testing.T) {
	now := time.Unix(100, 0)
	service := newTestService(t, &now)
	ctx := context.Background()

	result, err := service.Register(ctx, RegisterRequest{Account: "tester", Password: "secret"})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	now = now.Add(defaultSessionTTL + time.Second)
	if _, err := service.ResumeSession(ctx, ResumeSessionRequest{SessionToken: result.Session.Token}); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("ResumeSession expired error = %v, want ErrSessionInvalid", err)
	}
}

func newTestService(t *testing.T, now *time.Time) *Service {
	t.Helper()
	store := storage.NewFakeStore()
	nextToken := 0
	service, err := NewService(Config{
		PlayerProfiles: store,
		Sessions:       store,
		Now: func() time.Time {
			return *now
		},
		TokenGenerator: func() (string, error) {
			nextToken++
			return "token-" + string(rune('0'+nextToken)), nil
		},
	})
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	return service
}
