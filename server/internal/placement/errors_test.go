package placement

import (
	"errors"
	"strings"
	"testing"
)

// TestPlacementErrorContract 验证稳定错误映射保留 cause，但不会把内部诊断写入安全文本。
func TestPlacementErrorContract(t *testing.T) {
	t.Parallel()
	cause := errors.New("redis payload contains sensitive placement material")
	err := newError(ErrorKindConflict, OperationReplace, CommitPhaseCommitted, cause)
	if ErrorKindOf(err) != ErrorKindConflict || CommitPhaseOf(err) != CommitPhaseCommitted || !errors.Is(err, cause) {
		t.Fatalf("placement error mapping mismatch: %v", err)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("placement error leaked cause: %q", err.Error())
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.Kind() != ErrorKindConflict || failure.Operation() != OperationReplace || failure.Phase() != CommitPhaseCommitted {
		t.Fatalf("placement error accessors mismatch: %#v", failure)
	}
	unknown := errors.New("foreign error")
	if ErrorKindOf(unknown) != ErrorKindUnspecified || CommitPhaseOf(unknown) != CommitPhaseNone {
		t.Fatal("foreign error was assigned placement semantics")
	}
}

// TestPlacementEnumStrings 冻结错误、操作、提交阶段、phase 与 store outcome 的低基数名称。
func TestPlacementEnumStrings(t *testing.T) {
	t.Parallel()
	errorKinds := map[ErrorKind]string{
		ErrorKindUnspecified:           "unspecified",
		ErrorKindValidation:            "validation",
		ErrorKindNotFound:              "not_found",
		ErrorKindConflict:              "conflict",
		ErrorKindInProgress:            "in_progress",
		ErrorKindExpired:               "expired",
		ErrorKindDependencyUnavailable: "dependency_unavailable",
		ErrorKindRuntimeUnavailable:    "runtime_unavailable",
		ErrorKindCleanupFailed:         "cleanup_failed",
	}
	for value, expected := range errorKinds {
		if actual := value.String(); actual != expected {
			t.Fatalf("ErrorKind(%d).String()=%q want=%q", value, actual, expected)
		}
	}
	operations := map[Operation]string{
		OperationUnspecified:  "unspecified",
		OperationConstruct:    "construct",
		OperationEnsureActive: "ensure_active",
		OperationRenew:        "renew",
		OperationQualifyWrite: "qualify_write",
		OperationSleep:        "sleep",
		OperationReplace:      "replace",
	}
	for value, expected := range operations {
		if actual := value.String(); actual != expected {
			t.Fatalf("Operation(%d).String()=%q want=%q", value, actual, expected)
		}
	}
	commitPhases := map[CommitPhase]string{
		CommitPhaseNone:      "none",
		CommitPhaseUnknown:   "placement_commit_unknown",
		CommitPhaseCommitted: "placement_committed",
	}
	for value, expected := range commitPhases {
		if actual := value.String(); actual != expected {
			t.Fatalf("CommitPhase(%d).String()=%q want=%q", value, actual, expected)
		}
	}
	phases := map[Phase]string{
		PhaseUnspecified: "unspecified",
		PhaseStarting:    "starting",
		PhaseActive:      "active",
	}
	for value, expected := range phases {
		if actual := value.String(); actual != expected {
			t.Fatalf("Phase(%d).String()=%q want=%q", value, actual, expected)
		}
	}
	storeOutcomes := map[StoreOutcome]string{
		StoreOutcomeUnspecified:   "unspecified",
		StoreOutcomeApplied:       "applied",
		StoreOutcomeExisting:      "existing",
		StoreOutcomeReplay:        "replay",
		StoreOutcomeInProgress:    "in_progress",
		StoreOutcomeNotFound:      "not_found",
		StoreOutcomeConflict:      "conflict",
		StoreOutcomeExpired:       "expired",
		StoreOutcomeNotCommitted:  "not_committed",
		StoreOutcomeCommitUnknown: "commit_unknown",
	}
	for value, expected := range storeOutcomes {
		if actual := value.String(); actual != expected {
			t.Fatalf("StoreOutcome(%d).String()=%q want=%q", value, actual, expected)
		}
	}
}
