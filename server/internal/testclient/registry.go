package testclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var scenarioRegistry = map[string]ScenarioFunction{
	"contract-freeze":                runContractFreeze,
	"endpoint-manifest":              runEndpointManifest,
	"auth-session-lifecycle":         runAuthSessionLifecycle,
	"wss-session-invalidation":       runWSSSessionInvalidation,
	"own-world-bootstrap":            runOwnWorldBootstrap,
	"own-world-concurrent-bootstrap": runOwnWorldConcurrentBootstrap,
	"own-world-admission-snapshot":   runOwnWorldAdmissionSnapshot,
	"visit-open-invite-join":         runVisitOpenInviteJoin,
	"visit-leave":                    runVisitLeave,
	"visit-kick":                     runVisitKick,
	"visit-close-multiple":           runVisitCloseMultiple,
	"visit-disconnect-reconnect":     runVisitDisconnectReconnect,
	"visit-grace-expiry":             runVisitGraceExpiry,
	"authorization-negative-matrix":  runAuthorizationNegativeMatrix,
	"credential-rejection-matrix":    runCredentialRejectionMatrix,
	"server-process-restart":         runServerProcessRestart,
	"redis-loss-recovery":            runRedisLossRecovery,
	"mysql-restart-recovery":         runMySQLRestartRecovery,
	"commit-unknown-evidence":        requireLayeredEvidence("commit-unknown-evidence"),
	"slow-consumer-isolation":        requireLayeredEvidence("slow-consumer-isolation"),
	"partial-frame-backpressure":     requireLayeredEvidence("partial-frame-backpressure"),
	"bounded-connection-storm":       requireLayeredEvidence("bounded-connection-storm"),
	"graceful-shutdown":              requireLayeredEvidence("graceful-shutdown"),
	"shutdown-deadline":              requireLayeredEvidence("shutdown-deadline"),
	"process-failure-restart":        requireLayeredEvidence("process-failure-restart"),
	"diagnostic-convergence":         requireLayeredEvidence("diagnostic-convergence"),
}

// Registry 返回 scenario ID 到唯一 runner 的副本，禁止调用方隐藏增删全局场景。
func Registry() map[string]ScenarioFunction {
	copyRegistry := make(map[string]ScenarioFunction, len(scenarioRegistry))
	for id, function := range scenarioRegistry {
		copyRegistry[id] = function
	}
	return copyRegistry
}

// RunScenario 执行一个已登记场景；未知 ID 和 nil runner 一律拒绝。
func RunScenario(ctx context.Context, id string, runtime *ScenarioRuntime) error {
	if ctx == nil || runtime == nil {
		return errors.New("qualification scenario invocation is invalid")
	}
	function, exists := scenarioRegistry[id]
	if !exists || function == nil {
		return fmt.Errorf("qualification scenario %q is not registered", id)
	}
	return function(ctx, runtime)
}

// runContractFreeze 重算并比较提交的公开契约聚合摘要。
func runContractFreeze(_ context.Context, runtime *ScenarioRuntime) error {
	if runtime.RepositoryRoot == "" {
		return errors.New("qualification repository root is missing")
	}
	file, err := os.Open(filepath.Join(runtime.RepositoryRoot, "shared", "contracts", "fixtures", "qualification", "freeze.json"))
	if err != nil {
		return fmt.Errorf("open contract freeze record: %w", err)
	}
	defer file.Close()
	record, err := LoadFreezeRecord(file)
	if err != nil {
		return err
	}
	digest, _, err := ContractFreezeDigest(runtime.RepositoryRoot)
	if err != nil {
		return err
	}
	if digest != record.Digest {
		return errors.New("contract freeze digest drifted")
	}
	return nil
}

// runEndpointManifest 严格加载交付示例并确认 runtime bootstrap 仍由同一公开 schema 表达。
func runEndpointManifest(_ context.Context, runtime *ScenarioRuntime) error {
	file, err := os.Open(filepath.Join(runtime.RepositoryRoot, "shared", "contracts", "fixtures", "qualification", "endpoint-manifest.json"))
	if err != nil {
		return fmt.Errorf("open endpoint manifest example: %w", err)
	}
	defer file.Close()
	if _, err := LoadEndpointManifestExample(file); err != nil {
		return err
	}
	return runtime.Bootstrap.validate()
}

// requireLayeredEvidence 创建只检查明确 gate 证据的 runner，不伪造无法由 wire 稳定制造的故障。
func requireLayeredEvidence(id string) ScenarioFunction {
	return func(_ context.Context, runtime *ScenarioRuntime) error {
		if runtime.Evidence == nil || !runtime.Evidence[id] {
			return fmt.Errorf("mandatory layered evidence %q is missing", id)
		}
		return nil
	}
}
