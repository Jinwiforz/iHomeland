package session

import (
	"strings"
	"testing"
	"time"
)

// TestIdentityAndAuthContextValidation 固定可信身份、scope 副本和 realtime capability 边界。
func TestIdentityAndAuthContextValidation(t *testing.T) {
	principal, err := NewPrincipal("account-1", "player-1")
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := NewSessionID("session-1")
	if err != nil {
		t.Fatal(err)
	}
	scopes, err := NewScopeSet(ScopeGameplay, ScopeGameplay)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := newAuthContext(principal, sessionID, InitialEpoch, ChannelTLSTCP, scopes)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.HasScope(ScopeGameplay) || auth.HasScope(ScopeControl) {
		t.Fatalf("unexpected auth scopes: %v", auth.Scopes())
	}
	copyOfScopes := auth.Scopes()
	copyOfScopes[0] = ScopeControl
	if auth.HasScope(ScopeControl) {
		t.Fatal("caller mutated immutable auth scopes")
	}

	emptyScopes, err := NewScopeSet()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newAuthContext(principal, sessionID, InitialEpoch, ChannelWSS, emptyScopes); err == nil {
		t.Fatal("realtime context accepted an empty scope set")
	}
	if _, err := newAuthContext(principal, sessionID, InitialEpoch, ChannelHTTPS, emptyScopes); err != nil {
		t.Fatalf("HTTPS context should allow empty transport scopes: %v", err)
	}
	if _, err := newAuthContext(principal, sessionID, InitialEpoch, ChannelHTTPS, scopes); err == nil {
		t.Fatal("HTTPS context accepted realtime scopes")
	}
	controlScopes, _ := NewScopeSet(ScopeControl)
	if _, err := newAuthContext(principal, sessionID, InitialEpoch, ChannelTLSTCP, controlScopes); err == nil {
		t.Fatal("TLS/TCP context accepted control scope")
	}
}

// TestValueConstructorsRejectUnsafeInput 覆盖 identifier、epoch、scope 和 endpoint 的零值与注入边界。
func TestValueConstructorsRejectUnsafeInput(t *testing.T) {
	for name, test := range map[string]func() error{
		"empty session":     func() error { _, err := NewSessionID(""); return err },
		"control session":   func() error { _, err := NewSessionID("session\n1"); return err },
		"unicode session":   func() error { _, err := NewSessionID("会话-1"); return err },
		"key metacharacter": func() error { _, err := NewSessionID("session{1}"); return err },
		"long principal":    func() error { _, err := NewPrincipal(strings.Repeat("a", 129), "player"); return err },
		"unsupported scope": func() error { _, err := NewScopeSet(Scope(255)); return err },
		"HTTP endpoint":     func() error { _, err := NewEndpoint(ChannelHTTPS, "api.example.test", 443); return err },
		"invalid host":      func() error { _, err := NewEndpoint(ChannelWSS, "bad_host", 443); return err },
		"zero port":         func() error { _, err := NewEndpoint(ChannelWSS, "control.example.test", 0); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := test(); err == nil {
				t.Fatal("unsafe input should fail")
			}
		})
	}
	if _, err := Epoch(0).Next(); err == nil {
		t.Fatal("zero epoch advanced")
	}
	if next, err := InitialEpoch.Next(); err != nil || next != 2 {
		t.Fatalf("valid epoch did not advance: next=%d err=%v", next, err)
	}
}

// TestEndpointNormalization 固定受信 endpoint 的 DNS 大小写与完整 identity 比较。
func TestEndpointNormalization(t *testing.T) {
	endpoint, err := NewEndpoint(ChannelWSS, "Control.Example.Test.", 8443)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Host() != "control.example.test" || endpoint.Port() != 8443 || endpoint.Channel() != ChannelWSS {
		t.Fatalf("endpoint was not normalized: %+v", endpoint)
	}
	same, _ := NewEndpoint(ChannelWSS, "control.example.test", 8443)
	if !endpoint.Equal(same) {
		t.Fatal("equivalent endpoint identities differ")
	}
}

// TestPolicyValidation 固定 TTL 独立范围与严格生命周期顺序。
func TestPolicyValidation(t *testing.T) {
	valid, err := NewPolicy(30*time.Second, 15*time.Minute, 24*time.Hour, 7*24*time.Hour)
	if err != nil || !valid.Valid() {
		t.Fatalf("valid policy rejected: %v", err)
	}
	if valid.TicketTTL() != 30*time.Second || valid.AccessTTL() != 15*time.Minute ||
		valid.RefreshTTL() != 24*time.Hour || valid.SessionTTL() != 7*24*time.Hour {
		t.Fatal("policy accessors changed configured TTL values")
	}
	for name, values := range map[string][4]time.Duration{
		"ticket too short":      {time.Millisecond, 15 * time.Minute, 24 * time.Hour, 7 * 24 * time.Hour},
		"access before ticket":  {time.Minute, time.Minute, 24 * time.Hour, 7 * 24 * time.Hour},
		"refresh after session": {time.Minute, 15 * time.Minute, 48 * time.Hour, 24 * time.Hour},
		"refresh too long":      {time.Minute, 15 * time.Minute, 91 * 24 * time.Hour, 91 * 24 * time.Hour},
		"session too long":      {time.Minute, 15 * time.Minute, 24 * time.Hour, 91 * 24 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewPolicy(values[0], values[1], values[2], values[3]); err == nil {
				t.Fatal("invalid policy should fail")
			}
		})
	}
}
