package storage

import (
	"strings"
	"testing"
)

// TestRedisDefinitionsComposeAllOwners 固定共享registry包含已实现owner且没有重复项。
func TestRedisDefinitionsComposeAllOwners(t *testing.T) {
	definitions := RedisDefinitions()
	expected := map[string]struct{}{
		"session_record": {}, "session_access": {}, "session_refresh": {}, "session_ticket": {}, "session_principal": {},
		"placement_assignment": {}, "placement_transition": {},
		"visitsession_active": {}, "visitsession_session": {}, "visitsession_command": {},
		"worldadmission_issue": {}, "worldadmission_credential": {},
		"battleticket_issue": {},
	}
	if len(definitions) != len(expected) {
		t.Fatalf("definition count = %d", len(definitions))
	}
	keyspace, err := NewRedisKeyspace("test")
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if _, exists := expected[definition.Name]; !exists {
			t.Fatalf("unexpected definition %s", definition.Name)
		}
		delete(expected, definition.Name)
		key, buildErr := keyspace.Build(definition.Name, "validation")
		if buildErr != nil || !strings.HasPrefix(key.Value(), "ih:test:"+definition.Owner+":"+definition.Kind+":") {
			t.Fatalf("definition %s failed: key=%s err=%v", definition.Name, key, buildErr)
		}
	}
	if len(expected) != 0 {
		t.Fatalf("missing definitions: %v", expected)
	}
	production, err := NewRedisKeyspace("production")
	if err != nil {
		t.Fatal(err)
	}
	testKey, _ := keyspace.Build("visitsession_session", "vses_validation")
	productionKey, _ := production.Build("visitsession_session", "vses_validation")
	if testKey.Value() == productionKey.Value() {
		t.Fatal("environment namespaces overlap")
	}
	if _, err := NewRedisKeyspace("INVALID ENV"); err == nil {
		t.Fatal("invalid environment accepted")
	}
}
