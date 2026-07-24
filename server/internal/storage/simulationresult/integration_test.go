//go:build storage_integration

package simulationresult

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
)

// integrationObserver 丢弃低基数 adapter outcome。
type integrationObserver struct{}

// RecordStorageOperation 满足 production observer。
func (integrationObserver) RecordStorageOperation(string, string, string) {}

// failingCommitter 模拟 receipt insert 前 owner mutation 失败。
type failingCommitter struct{}

// Commit 返回固定失败，事务必须回滚。
func (failingCommitter) Commit(context.Context, *sql.Tx, simulationcontrol.ResultProposal) error {
	return errors.New("owner mutation failed")
}

// TestStoreIntegrationDecisionReplayConflictAndRollback 验证真实 MySQL immutable receipt。
func TestStoreIntegrationDecisionReplayConflictAndRollback(t *testing.T) {
	if os.Getenv("IHOMELAND_STORAGE_INTEGRATION") != "1" {
		t.Skip("storage integration harness is required")
	}
	db := openIntegrationDB(t)
	defer db.Close()
	if _, err := storagemysql.Migrate(context.Background(), db, "ihomeland", 15*time.Second); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store, err := New(db, integrationObserver{})
	if err != nil {
		t.Fatal(err)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	proposal := integrationProposal(t, "sresult_"+suffix)
	decision := simulationcontrol.ResultDecision{
		Disposition: simulationcontrol.ResultDispositionCommitted,
		Reason:      "accepted",
		DecidedAt:   time.Now().UTC().Truncate(time.Microsecond),
	}
	defer func() {
		_, _ = db.Exec("DELETE FROM simulation_result_receipts WHERE result_id IN (?, ?)", proposal.ResultID, proposal.ResultID+"_rollback")
	}()
	if _, outcome, err := store.Decide(context.Background(), proposal, decision); err != nil || outcome != simulationcontrol.ReceiptCommitApplied {
		t.Fatalf("first Decide outcome=%v err=%v", outcome, err)
	}
	restarted, _ := New(db, integrationObserver{})
	if _, outcome, err := restarted.Decide(context.Background(), proposal, decision); err != nil || outcome != simulationcontrol.ReceiptCommitReplayed {
		t.Fatalf("restart replay outcome=%v err=%v", outcome, err)
	}
	conflict := proposal
	conflict.PayloadDigest = integrationDigest(t, "9")
	conflict.ProposalFingerprint = conflict.CanonicalFingerprint()
	if _, outcome, err := restarted.Decide(context.Background(), conflict, decision); err != nil || outcome != simulationcontrol.ReceiptCommitConflict {
		t.Fatalf("conflict outcome=%v err=%v", outcome, err)
	}
	rollbackProposal := integrationProposal(t, proposal.ResultID+"_rollback")
	restarted.committers[simulationcontrol.LifecycleSummaryKind] = failingCommitter{}
	if _, _, err := restarted.Decide(context.Background(), rollbackProposal, decision); err == nil {
		t.Fatal("owner mutation failure was accepted")
	}
	if _, outcome, err := restarted.Lookup(context.Background(), rollbackProposal.ResultID); err != nil || outcome != simulationcontrol.ReceiptLookupNotFound {
		t.Fatalf("rollback lookup outcome=%v err=%v", outcome, err)
	}
}

// openIntegrationDB 连接资格 harness MySQL。
func openIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	password, err := os.ReadFile(os.Getenv("IHOMELAND_TEST_MYSQL_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	config := mysqldriver.NewConfig()
	config.User = "ihomeland"
	config.Passwd = string(password)
	config.Net = "tcp"
	config.Addr = os.Getenv("IHOMELAND_TEST_MYSQL_ADDRESS")
	config.DBName = "ihomeland"
	config.Timeout = 3 * time.Second
	config.ParseTime = true
	config.Loc = time.UTC
	config.Params = map[string]string{
		"time_zone": "'+00:00'",
		"sql_mode":  "'STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION'",
	}
	connector, err := mysqldriver.NewConnector(config)
	if err != nil {
		t.Fatal(err)
	}
	return sql.OpenDB(connector)
}

// integrationProposal 构造完整 receipt proposal。
func integrationProposal(t *testing.T, resultID string) simulationcontrol.ResultProposal {
	t.Helper()
	instanceID, _ := simulationcontrol.NewSimulationInstanceID("sinst_00112233445566778899aabbccddeeff")
	proposal := simulationcontrol.ResultProposal{
		ResultID:              resultID,
		Kind:                  simulationcontrol.LifecycleSummaryKind,
		AssignmentFingerprint: integrationDigest(t, "1"),
		InstanceID:            instanceID,
		TickStart:             1,
		TickEnd:               2,
		PayloadDigest:         integrationDigest(t, "2"),
		EvidenceDigest:        integrationDigest(t, "3"),
	}
	proposal.ProposalFingerprint = proposal.CanonicalFingerprint()
	return proposal
}

// integrationDigest 返回单字符重复的规范 SHA-256。
func integrationDigest(t *testing.T, value string) simulationcontrol.Digest {
	t.Helper()
	digest, err := simulationcontrol.NewDigest(strings.Repeat(value, 64))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
