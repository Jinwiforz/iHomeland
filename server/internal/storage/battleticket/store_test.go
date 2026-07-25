package battleticket

import (
	"testing"

	domain "github.com/jinwiforz/ihomeland/server/internal/battleticket"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	redisclient "github.com/redis/go-redis/v9"
)

// noopObserver 丢弃 unit test 的低基数结果。
type noopObserver struct{}

// RecordStorageOperation 实现 Observer 且不保留任何输入。
func (noopObserver) RecordStorageOperation(string, string, string) {}

// TestNewRejectsMissingDependenciesAndDefinitions 验证 adapter 不延迟暴露 composition 缺陷。
func TestNewRejectsMissingDependenciesAndDefinitions(t *testing.T) {
	if _, err := New(nil, nil, nil); err == nil {
		t.Fatal("nil dependencies accepted")
	}
	registry, err := storageredis.NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	keyspace, err := storageredis.NewKeyspace("test", registry)
	if err != nil {
		t.Fatal(err)
	}
	client := redisclient.NewClient(&redisclient.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	if _, err := New(client, keyspace, noopObserver{}); err == nil {
		t.Fatal("missing definition accepted")
	}
}

// TestDecodeSnapshotRejectsPartialReply 验证 adapter 不从部分 Lua fields 猜测 binding。
func TestDecodeSnapshotRejectsPartialReply(t *testing.T) {
	if _, err := decodeSnapshot(domain.IssueID{}, []string{"partial"}); err == nil {
		t.Fatal("partial BattleTicket snapshot accepted")
	}
}
