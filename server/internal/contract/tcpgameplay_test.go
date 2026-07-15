package contract

import "testing"

// TestTLSGameplayCatalogMatchesRegistry 防止编译期TCP路由投影与唯一registry事实漂移。
func TestTLSGameplayCatalogMatchesRegistry(t *testing.T) {
	t.Parallel()
	source, err := Load(testRepositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	runtime := TLSGameplayCatalog()
	if len(runtime.Messages.Messages) != 23 || len(runtime.Routes.Routes) != 23 {
		t.Fatalf("runtime TLS/TCP catalog count drifted: messages=%d routes=%d", len(runtime.Messages.Messages), len(runtime.Routes.Routes))
	}
	if len(runtime.Errors.Errors) != 23 {
		t.Fatalf("runtime TLS/TCP error count drifted: errors=%d", len(runtime.Errors.Errors))
	}
	for _, message := range runtime.Messages.Messages {
		want, err := source.LookupTLSGameplay(message.ID, message.Direction)
		if err != nil {
			t.Fatal(err)
		}
		got, err := runtime.LookupTLSGameplay(message.ID, message.Direction)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("runtime TLS/TCP profile %d drifted: got=%+v want=%+v", message.ID, got, want)
		}
	}
	for _, publicError := range runtime.Errors.Errors {
		want, err := source.LookupError(publicError.Code)
		if err != nil {
			t.Fatal(err)
		}
		if publicError != want {
			t.Fatalf("runtime TLS/TCP public error %d drifted: got=%+v want=%+v", publicError.Code, publicError, want)
		}
	}
}

// TestLookupTLSGameplayRejectsWrongProfiles 固定未知、WSS、方向与kind漂移的拒绝行为。
func TestLookupTLSGameplayRejectsWrongProfiles(t *testing.T) {
	t.Parallel()
	catalog, err := Load(testRepositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id        uint32
		direction string
	}{{0, "CLIENT_TO_SERVER"}, {500, "SERVER_TO_CLIENT"}, {2000, "SERVER_TO_CLIENT"}, {2001, "CLIENT_TO_SERVER"}} {
		if _, err := catalog.LookupTLSGameplay(test.id, test.direction); err == nil {
			t.Fatalf("LookupTLSGameplay(%d, %s) unexpectedly succeeded", test.id, test.direction)
		}
	}
	mutated := catalog
	for index := range mutated.Messages.Messages {
		if mutated.Messages.Messages[index].ID == 2000 {
			mutated.Messages.Messages[index].Kind = "PUSH"
		}
	}
	if _, err := mutated.LookupTLSGameplay(2000, "CLIENT_TO_SERVER"); err == nil {
		t.Fatal("kind drift was accepted")
	}
}
