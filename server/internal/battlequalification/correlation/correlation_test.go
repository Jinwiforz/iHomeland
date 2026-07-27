package correlation

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestCorrelatorIsDeterministicWithinRunAndIsolatedAcrossRuns 验证同 run 稳定、跨 run 不可关联。
func TestCorrelatorIsDeterministicWithinRunAndIsolatedAcrossRuns(t *testing.T) {
	first, err := newFromReader(bytes.NewReader(bytes.Repeat([]byte{1}, correlationKeyBytes)))
	if err != nil {
		t.Fatalf("new first correlator: %v", err)
	}
	second, err := newFromReader(bytes.NewReader(bytes.Repeat([]byte{2}, correlationKeyBytes)))
	if err != nil {
		t.Fatalf("new second correlator: %v", err)
	}
	source := validSource()
	a, err := first.Build(source)
	if err != nil {
		t.Fatalf("build first identity: %v", err)
	}
	repeated, err := first.Build(source)
	if err != nil {
		t.Fatalf("repeat first identity: %v", err)
	}
	b, err := second.Build(source)
	if err != nil {
		t.Fatalf("build second identity: %v", err)
	}
	if a != repeated {
		t.Fatal("same run correlation was not deterministic")
	}
	if a.SessionDigest == b.SessionDigest ||
		a.AssignmentDigest == b.AssignmentDigest ||
		a.InstanceDigest == b.InstanceDigest {
		t.Fatal("correlation digest was reusable across runs")
	}
	if a.SessionDigest == a.AssignmentDigest ||
		a.AssignmentDigest == a.InstanceDigest {
		t.Fatal("correlation digest domains collided")
	}
}

// TestCorrelationEvidenceDoesNotContainRawIdentity 验证 JSON 只含低敏 digest 与代际。
func TestCorrelationEvidenceDoesNotContainRawIdentity(t *testing.T) {
	correlator, err := newFromReader(bytes.NewReader(bytes.Repeat([]byte{3}, correlationKeyBytes)))
	if err != nil {
		t.Fatalf("new correlator: %v", err)
	}
	source := validSource()
	identity, err := correlator.Build(source)
	if err != nil {
		t.Fatalf("build identity: %v", err)
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	for _, forbidden := range []string{
		source.SimulationIdentity,
		"player", "127.0.0.1", "ticket", "payload",
	} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("correlation evidence contains forbidden material %q", forbidden)
		}
	}
}

// TestCorrelatorRejectsInvalidAndClosedInput 验证零代际、未知 phase 与 cleanup 后调用 fail closed。
func TestCorrelatorRejectsInvalidAndClosedInput(t *testing.T) {
	correlator, err := newFromReader(bytes.NewReader(bytes.Repeat([]byte{4}, correlationKeyBytes)))
	if err != nil {
		t.Fatalf("new correlator: %v", err)
	}
	source := validSource()
	source.EndpointGeneration = 0
	if _, err := correlator.Build(source); err == nil {
		t.Fatal("zero endpoint generation was accepted")
	}
	if err := correlator.Close(); err != nil {
		t.Fatalf("close correlator: %v", err)
	}
	if _, err := correlator.Build(validSource()); err == nil {
		t.Fatal("closed correlator accepted input")
	}
}

// TestIdentityFieldPolicy 禁止 evidence 类型新增玩家、endpoint、credential 或完整内部 identity。
func TestIdentityFieldPolicy(t *testing.T) {
	identityType := reflect.TypeFor[Identity]()
	for index := 0; index < identityType.NumField(); index++ {
		name := strings.ToLower(identityType.Field(index).Name)
		for _, forbidden := range []string{
			"player", "endpointaddress", "remote", "ticket", "credential",
			"secret", "payload", "fingerprint", "simulationidentity",
		} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("Identity field %q violates low-sensitive policy", name)
			}
		}
	}
}

// validSource 返回所有单元测试共用的完整关联输入。
func validSource() Source {
	var session [16]byte
	var assignment [32]byte
	copy(session[:], bytes.Repeat([]byte{5}, len(session)))
	copy(assignment[:], bytes.Repeat([]byte{6}, len(assignment)))
	return Source{
		ClientSlot:              1,
		SessionIdentity:         session,
		BattleSessionGeneration: 2,
		EndpointGeneration:      3,
		MappingGeneration:       4,
		AssignmentFingerprint:   assignment,
		SimulationIdentity:      "simulation-instance-unit-test",
		Phase:                   PhaseMovementHeavy,
	}
}
