package worldadmission

import (
	"strings"
	"testing"

	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
)

// TestDefinitionsRegisterBoundedExpiringHashes 验证两类 key 均有独立 owner 与严格治理字段。
func TestDefinitionsRegisterBoundedExpiringHashes(t *testing.T) {
	definitions := Definitions()
	if len(definitions) != 2 {
		t.Fatalf("definition count=%d", len(definitions))
	}
	registry, err := storageredis.NewRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := storageredis.NewKeyspace("test", registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.Owner != "worldadmission" || definition.TTLPolicy != storageredis.TTLRequired || definition.SchemaVersion != 1 || definition.MaxEncodedBytes <= 0 {
			t.Fatalf("invalid definition: %#v", definition)
		}
		key, buildErr := keyspace.Build(definition.Name, "fixture")
		if buildErr != nil || !strings.HasPrefix(key.Value(), "ih:test:worldadmission:") {
			t.Fatalf("key=%s err=%v", key, buildErr)
		}
	}
}

// TestDecodeBindingRejectsIncompleteResult 验证 adapter 不从部分 Lua 结果猜测 binding。
func TestDecodeBindingRejectsIncompleteResult(t *testing.T) {
	if _, err := decodeBindingReply([]string{"partial"}); err == nil {
		t.Fatal("partial binding was accepted")
	}
}
