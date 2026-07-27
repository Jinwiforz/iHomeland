package battleticket

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
)

// TestProofKeyV2MatchesPublicVector 冻结 ticket ID public salt 与 versioned domain。
func TestProofKeyV2MatchesPublicVector(t *testing.T) {
	ticketID, err := ParseTicketID("btk1_AAECAwQFBgcICQoLDA0ODw")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := ParseTicketSecret("bts1_ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8")
	if err != nil {
		t.Fatal(err)
	}
	proof, err := DeriveProofKey(ticketID, secret)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "40d65068dd108e2d727be477529ec572db2b6df9aa1edc1495b1cefd72d9f517"
	actual := proof.Bytes()
	if hex.EncodeToString(actual[:]) != expected {
		t.Fatalf("proof-key/v2 vector mismatch: got %x", actual)
	}

	changedTicketID, err := ParseTicketID("btk1_AQECAwQFBgcICQoLDA0ODw")
	if err != nil {
		t.Fatal(err)
	}
	changedProof, err := DeriveProofKey(changedTicketID, secret)
	if err != nil {
		t.Fatal(err)
	}
	if changedProof.Bytes() == actual {
		t.Fatal("ticket ID drift did not change proof-key/v2 output")
	}
	changedSecretBytes := secret.Bytes()
	changedSecretBytes[0] ^= 0xff
	changedSecret := TicketSecret{material: changedSecretBytes}
	clear(changedSecretBytes[:])
	changedSecretProof, err := DeriveProofKey(ticketID, changedSecret)
	if err != nil {
		t.Fatal(err)
	}
	if changedSecretProof.Bytes() == actual {
		t.Fatal("ticket secret drift did not change proof-key/v2 output")
	}
	legacyProof, err := deriveProofKey(
		ticketID,
		secret,
		"ihomeland/battle-ticket/proof-key/v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if legacyProof.Bytes() == actual {
		t.Fatal("legacy proof domain matched proof-key/v2 output")
	}
	if _, err := DeriveProofKey(TicketID{}, secret); ErrorCodeOf(err) != ErrorCodeInvalidArgument {
		t.Fatalf("invalid ticket ID was not rejected: %v", err)
	}
}

// TestDeriveIsDeterministicAndTargetBound 验证 response-loss 重放稳定且任一 target revision 漂移改写 material。
func TestDeriveIsDeterministicAndTargetBound(t *testing.T) {
	deriver, err := NewDeriver(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	facts := validFacts(t)
	first, err := deriver.Derive(facts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := deriver.Derive(facts)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Valid() || !first.Binding().Equal(second.Binding()) ||
		first.Secret().Value() != second.Secret().Value() ||
		first.ProofKey().Bytes() != second.ProofKey().Bytes() ||
		!first.Fingerprint().Equal(second.Fingerprint()) {
		t.Fatal("same authority facts did not produce byte-identical BattleTicket material")
	}
	changed := facts
	changed.TargetRevision++
	third, err := deriver.Derive(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first.Binding().TicketID().Value() == third.Binding().TicketID().Value() ||
		first.Secret().Value() == third.Secret().Value() ||
		first.ProofKey().Bytes() == third.ProofKey().Bytes() {
		t.Fatal("target revision drift did not change ticket identity, secret, and proof key")
	}
}

// TestOwnerVisitorBindingIsClosed 验证 Owner 不携带 VisitSession，而 Visitor 必须携带。
func TestOwnerVisitorBindingIsClosed(t *testing.T) {
	deriver, err := NewDeriver(bytes.Repeat([]byte{0x24}, 32))
	if err != nil {
		t.Fatal(err)
	}
	owner := validFacts(t)
	visitID := mustConstruct(t, func() (visitsession.VisitSessionID, error) {
		return visitsession.NewVisitSessionID("vses_visitor1")
	})
	invalidOwner := owner
	invalidOwner.VisitSessionID = visitID
	if _, err := deriver.Derive(invalidOwner); ErrorCodeOf(err) != ErrorCodeInvalidArgument {
		t.Fatalf("Owner with VisitSession was not rejected: %v", err)
	}
	visitor := owner
	visitor.Role = RoleVisitor
	visitor.VisitSessionID = visitID
	if _, err := deriver.Derive(visitor); err != nil {
		t.Fatalf("valid Visitor facts rejected: %v", err)
	}
	visitor.VisitSessionID = visitsession.VisitSessionID{}
	if _, err := deriver.Derive(visitor); ErrorCodeOf(err) != ErrorCodeInvalidArgument {
		t.Fatalf("Visitor without membership was not rejected: %v", err)
	}
}

// TestPolicyFreezesFirstResponse 验证晚到的同语义重试只返回首次结果，收紧或漂移语义冲突。
func TestPolicyFreezesFirstResponse(t *testing.T) {
	policy, err := NewPolicy(30*time.Second, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	deriver, err := NewDeriver(bytes.Repeat([]byte{0x18}, 32))
	if err != nil {
		t.Fatal(err)
	}
	facts := validFacts(t)
	material, err := deriver.Derive(facts)
	if err != nil {
		t.Fatal(err)
	}
	retry := facts
	retry.IssuedAt = retry.IssuedAt.Add(time.Second)
	retry.ExpiresAt = retry.ExpiresAt.Add(time.Second)
	outcome, err := policy.ResolveReplay(material.Binding(), retry, retry.IssuedAt)
	if err != nil || outcome != ReplayOutcomeReturnExisting {
		t.Fatalf("same authority response-loss retry rejected: outcome=%v err=%v", outcome, err)
	}
	tightened := retry
	tightened.ExpiresAt = facts.ExpiresAt.Add(-time.Second)
	if _, err := policy.ResolveReplay(material.Binding(), tightened, retry.IssuedAt); ErrorCodeOf(err) != ErrorCodeIdempotencyConflict {
		t.Fatalf("tightened deadline did not conflict: %v", err)
	}
	drifted := retry
	drifted.MappingGeneration++
	if _, err := policy.ResolveReplay(material.Binding(), drifted, retry.IssuedAt); ErrorCodeOf(err) != ErrorCodeIdempotencyConflict {
		t.Fatalf("target drift did not conflict: %v", err)
	}
	if _, err := policy.ResolveReplay(material.Binding(), retry, facts.ExpiresAt); ErrorCodeOf(err) != ErrorCodeExpired {
		t.Fatalf("expiry boundary did not reject replay: %v", err)
	}
}

// TestPolicyRejectsLongLivedTicket 验证 ticket lifetime 不会扩张为长寿命 bearer。
func TestPolicyRejectsLongLivedTicket(t *testing.T) {
	if _, err := NewPolicy(3*time.Minute, 5*time.Minute); ErrorCodeOf(err) != ErrorCodeInvalidArgument {
		t.Fatalf("long ticket policy accepted: %v", err)
	}
	policy, err := NewPolicy(30*time.Second, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	facts := validFacts(t)
	facts.ExpiresAt = facts.IssuedAt.Add(31 * time.Second)
	if err := policy.ValidateFacts(facts, facts.IssuedAt); ErrorCodeOf(err) != ErrorCodeInvalidArgument {
		t.Fatalf("over-policy expiry accepted: %v", err)
	}
}

// TestSensitiveValuesAreRedacted 验证默认格式化、GoString 与 slog 不输出 secret 或 proof bytes。
func TestSensitiveValuesAreRedacted(t *testing.T) {
	deriver, err := NewDeriver(bytes.Repeat([]byte{0x36}, 32))
	if err != nil {
		t.Fatal(err)
	}
	material, err := deriver.Derive(validFacts(t))
	if err != nil {
		t.Fatal(err)
	}
	buffer := new(bytes.Buffer)
	logger := slog.New(slog.NewJSONHandler(buffer, nil))
	logger.Info("battle", "secret", material.Secret(), "proof", material.ProofKey(), "binding", material.Binding())
	formatted := fmt.Sprintf("%v %#v %v", material.Secret(), material.ProofKey(), material)
	combined := formatted + buffer.String()
	if strings.Contains(combined, material.Secret().Value()) {
		t.Fatal("BattleTicket secret leaked through default formatting")
	}
	proof := material.ProofKey().Bytes()
	if strings.Contains(combined, fmt.Sprintf("%x", proof[:])) {
		t.Fatal("BattleTicket proof key leaked through default formatting")
	}
	if strings.Count(combined, redactedValue) < 6 {
		t.Fatalf("redaction placeholders missing: %s", combined)
	}
}

// TestPackageHasNoTransportStorageOrGeneratedImports 保护 value policy 不依赖 Gin、Redis、
// generated DTO 或 C++ process adapter。
func TestPackageHasNoTransportStorageOrGeneratedImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Clean(entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range file.Imports {
			path := strings.Trim(imported.Path.Value, `"`)
			for _, forbidden := range []string{
				"github.com/gin-gonic/gin",
				"github.com/redis/go-redis",
				"/internal/generated/",
				"/internal/storage/",
				"/internal/simulationcontrol/process",
			} {
				if strings.Contains(path, forbidden) {
					t.Fatalf("%s imports forbidden dependency %s", entry.Name(), path)
				}
			}
		}
	}
}

// validFacts 构造完整 Owner target；各测试只修改一个独立边界。
func validFacts(t *testing.T) Facts {
	t.Helper()
	playerID := mustConstruct(t, func() (account.PlayerID, error) {
		return account.NewPlayerID("ply_battleowner")
	})
	sessionID := mustConstruct(t, func() (session.SessionID, error) {
		return session.NewSessionID("ses_battleowner")
	})
	worldID := mustConstruct(t, func() (personalworld.PersonalWorldID, error) {
		return personalworld.NewPersonalWorldID("pworld_battleowner")
	})
	worldInstanceID := mustConstruct(t, func() (placement.WorldInstanceID, error) {
		return placement.NewWorldInstanceID("winst_battleowner")
	})
	runtimeNodeID := mustConstruct(t, func() (placement.RuntimeNodeID, error) {
		return placement.NewRuntimeNodeID("rnode_battleowner")
	})
	generation := mustConstruct(t, func() (placement.AssignmentGeneration, error) {
		return placement.NewAssignmentGeneration(7)
	})
	fencingToken := mustConstruct(t, func() (placement.FencingToken, error) {
		return placement.NewFencingToken(9)
	})
	assignment := mustConstruct(t, func() (placement.AssignmentStamp, error) {
		return placement.NewAssignmentStamp(worldID, worldInstanceID, runtimeNodeID, generation, fencingToken)
	})
	simulationNodeID := mustConstruct(t, func() (simulationcontrol.SimulationNodeID, error) {
		return simulationcontrol.NewSimulationNodeID("snode_battleowner")
	})
	simulationInstanceID := mustConstruct(t, func() (simulationcontrol.SimulationInstanceID, error) {
		return simulationcontrol.NewSimulationInstanceID("sinst_0123456789abcdef0123456789abcdef")
	})
	assignmentFingerprint := mustConstruct(t, func() (simulationcontrol.Digest, error) {
		return simulationcontrol.NewDigest(strings.Repeat("1", 64))
	})
	modelIdentity := mustConstruct(t, func() (simulationcontrol.Digest, error) {
		return simulationcontrol.NewDigest(strings.Repeat("2", 64))
	})
	profileIdentity := mustConstruct(t, func() (simulationcontrol.Digest, error) {
		return simulationcontrol.NewDigest(strings.Repeat("3", 64))
	})
	configIdentity := mustConstruct(t, func() (simulationcontrol.Digest, error) {
		return simulationcontrol.NewDigest(strings.Repeat("4", 64))
	})
	wireIdentity := mustConstruct(t, func() (Digest, error) {
		return ParseDigestHex(strings.Repeat("5", 64))
	})
	actorSlot := mustConstruct(t, func() (ActorSlot, error) {
		return NewActorSlot(0)
	})
	endpoint := mustConstruct(t, func() (Endpoint, error) {
		return NewEndpoint("battle.example.invalid", 58445)
	})
	issueID := mustConstruct(t, func() (IssueID, error) {
		return NewIssueID("issue_battle_owner_00000001")
	})
	issuedAt := time.Date(2026, 7, 24, 12, 0, 0, 123456000, time.UTC)
	return Facts{
		PlayerID:              playerID,
		SessionID:             sessionID,
		SessionEpoch:          session.InitialEpoch,
		Role:                  RoleOwner,
		WorldID:               worldID,
		Assignment:            assignment,
		AssignmentFingerprint: assignmentFingerprint,
		RuntimeNodeID:         runtimeNodeID,
		SimulationNodeID:      simulationNodeID,
		SimulationInstanceID:  simulationInstanceID,
		MappingGeneration:     7,
		TargetRevision:        11,
		ModelIdentity:         modelIdentity,
		ProfileIdentity:       profileIdentity,
		ConfigIdentity:        configIdentity,
		WireIdentity:          wireIdentity,
		ActorSlot:             actorSlot,
		Endpoint:              endpoint,
		IssueID:               issueID,
		IssuedAt:              issuedAt,
		ExpiresAt:             issuedAt.Add(30 * time.Second),
	}
}

// mustConstruct 执行测试值 constructor，失败立即终止当前测试。
func mustConstruct[T any](t *testing.T, constructor func() (T, error)) T {
	t.Helper()
	value, err := constructor()
	if err != nil {
		t.Fatal(err)
	}
	return value
}
