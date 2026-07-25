package battleticket

import (
	"strings"
	"testing"

	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
)

// TestDefinitionRegistersExpiringDigestOnlyHash 验证 key owner、TTL、version 与预算完整。
func TestDefinitionRegistersExpiringDigestOnlyHash(t *testing.T) {
	definitions := Definitions()
	if len(definitions) != 1 {
		t.Fatalf("definition count=%d", len(definitions))
	}
	definition := definitions[0]
	if definition.Owner != "battleticket" || definition.TTLPolicy != storageredis.TTLRequired ||
		definition.SchemaVersion != redisSchemaVersion || definition.MaxEncodedBytes != maximumIssueBytes {
		t.Fatalf("invalid definition: %#v", definition)
	}
	registry, err := storageredis.NewRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := storageredis.NewKeyspace("test", registry)
	if err != nil {
		t.Fatal(err)
	}
	key, err := keyspace.Build(issueDefinitionName, "fixture")
	if err != nil || !strings.HasPrefix(key.Value(), "ih:test:battleticket:issue:") {
		t.Fatalf("key=%s err=%v", key, err)
	}
}

// TestLuaSchemaNeverStoresRawSecrets 验证 owner Lua 字段表不包含 secret/proof 明文槽位。
func TestLuaSchemaNeverStoresRawSecrets(t *testing.T) {
	for _, forbidden := range []string{"'ticket_secret'", "'secret'", "'proof_key'", "'derivation_key'"} {
		if strings.Contains(issueScriptSource, forbidden) || strings.Contains(resolveScriptSource, forbidden) {
			t.Fatalf("BattleTicket Lua contains forbidden raw secret field %s", forbidden)
		}
	}
	for _, required := range []string{"'secret_digest'", "'proof_digest'", "'ticket_id'", "'fingerprint'"} {
		if !strings.Contains(issueScriptSource, required) {
			t.Fatalf("BattleTicket Lua lacks required digest/handle field %s", required)
		}
	}
}
