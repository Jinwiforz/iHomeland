package manifest

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// repositoryRoot 从当前 package 向上解析共享 fixture 根。
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate manifest test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", "..", ".."))
}

// TestLoadAllProfileScenarios 验证 12 个 overlay/source pair 均可生成显式 gateway config。
func TestLoadAllProfileScenarios(t *testing.T) {
	root := repositoryRoot(t)
	ids := []string{
		"real-baseline-gap",
		"real-clean-default",
		"real-clean-solo-shape",
		"real-compatibility-combined-shape",
		"real-disconnect-drain",
		"real-downlink-latency-jitter",
		"real-kcp-retransmit",
		"real-mtu-boundary",
		"real-queue-saturation",
		"real-raw-loss-burst",
		"real-reorder-duplicate",
		"real-uplink-latency-jitter",
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			definition, err := Load(root, id)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			config, err := definition.SocketConfig(
				netip.MustParseAddrPort("127.0.0.1:58445"),
				netip.MustParseAddrPort("127.0.0.1:0"),
			)
			if err != nil {
				t.Fatalf("SocketConfig: %v", err)
			}
			if definition.WorkloadID == "" ||
				config.ClientCount != 5 ||
				config.Scheduler.MaximumDatagramBytes != 1_200 ||
				config.Scheduler.PatternDuration <= 0 ||
				config.MappingLateDelivery != 3*time.Second ||
				config.Scheduler.Uplink.Bandwidth.Enabled ||
				config.Scheduler.Downlink.Bandwidth.Enabled {
				t.Fatalf("gateway config drifted: %+v", config)
			}
		})
	}
}

// TestFaultPatternSeedMatchesB02 验证 Go 派生与 PowerShell source simulator 完全一致。
func TestFaultPatternSeedMatchesB02(t *testing.T) {
	definition, err := Load(repositoryRoot(t), "real-baseline-gap")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	seed, err := definition.FaultPatternSeed(definition.Phases[0])
	if err != nil {
		t.Fatalf("FaultPatternSeed: %v", err)
	}
	const expectedSeed uint32 = 3_068_004_870
	if seed != expectedSeed {
		t.Fatalf("seed=%d want=%d", seed, expectedSeed)
	}
	config, err := definition.SocketConfig(
		netip.MustParseAddrPort("127.0.0.1:58445"),
		netip.MustParseAddrPort("127.0.0.1:0"),
	)
	if err != nil {
		t.Fatalf("SocketConfig: %v", err)
	}
	if config.Scheduler.PatternDuration != 2*time.Second {
		t.Fatalf("pattern duration=%s", config.Scheduler.PatternDuration)
	}
}

// TestDirectionalScenarioOnlyImpairsRegisteredDirection 验证方向型 source 不会被双向扩大。
func TestDirectionalScenarioOnlyImpairsRegisteredDirection(t *testing.T) {
	root := repositoryRoot(t)
	uplink, err := Load(root, "real-uplink-latency-jitter")
	if err != nil {
		t.Fatalf("Load uplink: %v", err)
	}
	uplinkConfig, err := uplink.SocketConfig(
		netip.MustParseAddrPort("127.0.0.1:58445"),
		netip.MustParseAddrPort("127.0.0.1:0"),
	)
	if err != nil {
		t.Fatalf("SocketConfig uplink: %v", err)
	}
	if uplinkConfig.Scheduler.Uplink.BaseLatency == 0 ||
		uplinkConfig.Scheduler.Downlink.BaseLatency != 0 {
		t.Fatal("uplink scenario direction drifted")
	}
	downlink, err := Load(root, "real-downlink-latency-jitter")
	if err != nil {
		t.Fatalf("Load downlink: %v", err)
	}
	downlinkConfig, err := downlink.SocketConfig(
		netip.MustParseAddrPort("127.0.0.1:58445"),
		netip.MustParseAddrPort("127.0.0.1:0"),
	)
	if err != nil {
		t.Fatalf("SocketConfig downlink: %v", err)
	}
	if downlinkConfig.Scheduler.Downlink.BaseLatency == 0 ||
		downlinkConfig.Scheduler.Uplink.BaseLatency != 0 {
		t.Fatal("downlink scenario direction drifted")
	}
}

// TestLoadRejectsUnknownFieldAndSourceDrift 验证 Go runtime loader 自身 fail closed。
func TestLoadRejectsUnknownFieldAndSourceDrift(t *testing.T) {
	sourceRoot := repositoryRoot(t)
	for name, mutate := range map[string]func(string) string{
		"unknown-field": func(value string) string {
			return strings.Replace(
				value,
				`"formatVersion": 1`,
				`"formatVersion": 1, "hiddenDefault": 1`,
				1,
			)
		},
		"duration-drift": func(value string) string {
			return strings.Replace(
				value,
				`"sourceDurationTicks": 40`,
				`"sourceDurationTicks": 39`,
				1,
			)
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			for _, relative := range []string{
				faultExecutionRelativePath,
				faultMatrixRelativePath,
			} {
				source := filepath.Join(sourceRoot, filepath.FromSlash(relative))
				payload, err := os.ReadFile(source)
				if err != nil {
					t.Fatalf("ReadFile: %v", err)
				}
				if relative == faultExecutionRelativePath {
					payload = []byte(mutate(string(payload)))
				}
				target := filepath.Join(root, filepath.FromSlash(relative))
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
				if err := os.WriteFile(target, payload, 0o600); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
			}
			if _, err := Load(root, "real-baseline-gap"); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("mutation error=%v", err)
			}
		})
	}
}

// TestLoadRejectsProfileV1 验证资格消费者不会把旧 profile 代际解释为 current binding。
func TestLoadRejectsProfileV1(t *testing.T) {
	sourceRoot := repositoryRoot(t)
	root := t.TempDir()
	for _, relative := range []string{
		faultExecutionRelativePath,
		faultMatrixRelativePath,
	} {
		source := filepath.Join(sourceRoot, filepath.FromSlash(relative))
		payload, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if relative == faultMatrixRelativePath {
			payload = []byte(strings.Replace(
				string(payload),
				`"profile_version": "battle-network-profile-v2"`,
				`"profile_version": "battle-network-profile-v1"`,
				1,
			))
		}
		target := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(target, payload, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	if _, err := Load(root, "real-baseline-gap"); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("profile v1 error=%v", err)
	}
}

// TestLoadRejectsUnknownScenario 验证 CLI 不能退回隐式 clean mode。
func TestLoadRejectsUnknownScenario(t *testing.T) {
	if _, err := Load(repositoryRoot(t), "unknown"); !errors.Is(err, ErrScenarioNotFound) {
		t.Fatalf("unknown scenario error=%v", err)
	}
}

// TestLoadSoakPolicy 验证 30 分钟、两次 rekey 与 cleanup 预算只来自 tracked corpus。
func TestLoadSoakPolicy(t *testing.T) {
	policy, err := LoadSoakPolicy(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if policy.DurationMilliseconds != 1_800_000 ||
		policy.RekeyIntervalMilliseconds != 600_000 ||
		policy.MinimumObservedRekeys != 2 ||
		policy.CleanupMilliseconds != 30_000 {
		t.Fatalf("soak policy drifted: %+v", policy)
	}
}

// TestLoadLifecyclePolicyBindsOwnersAndModes 验证每个 case 只有一个执行 owner 与迁移语义。
func TestLoadLifecyclePolicyBindsOwnersAndModes(t *testing.T) {
	policy, err := LoadLifecyclePolicy(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Cases) != 10 ||
		policy.Owners["visitor-reconnect"] != LifecycleOwnerPublicProtocol ||
		policy.Modes["visitor-reconnect"] != "replaced" ||
		policy.Owners["valid-endpoint-rebind"] != LifecycleOwnerFaultGateway ||
		policy.Modes["network-pause-resume"] != "preserved" ||
		policy.Owners["go-restart"] != LifecycleOwnerProcessSupervisor ||
		policy.Modes["shutdown-drain-deadline"] != "terminated" {
		t.Fatalf("lifecycle policy drifted: %+v", policy)
	}
	policy.Owners["go-restart"] = LifecycleOwnerPublicProtocol
	reloaded, err := LoadLifecyclePolicy(repositoryRoot(t))
	if err != nil ||
		reloaded.Owners["go-restart"] != LifecycleOwnerProcessSupervisor {
		t.Fatal("lifecycle policy returned shared mutable owner state")
	}
}

// TestLoadMetricCatalog 验证 runtime 从 tracked catalog 读取 process growth 预算。
func TestLoadMetricCatalog(t *testing.T) {
	catalog, err := LoadMetricCatalog(repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	metric, err := catalog.Metric(ProcessWorkingSetGrowthMetricID)
	if err != nil {
		t.Fatal(err)
	}
	if metric.Maximum != 8_388_608 || metric.Unit != "bytes" {
		t.Fatalf("process growth metric drifted: %+v", metric)
	}
	if _, err := catalog.Metric("unknown"); err == nil {
		t.Fatal("unknown metric was accepted")
	}
}

// TestLoadCapacityDefinitions 验证 1/5/8 actor curve 共享 exact clean gateway policy。
func TestLoadCapacityDefinitions(t *testing.T) {
	tests := map[string]uint8{
		"solo-owner":         1,
		"default-capacity":   5,
		"qualified-capacity": 8,
	}
	for workloadID, actorCount := range tests {
		definition, err := LoadCapacityDefinition(repositoryRoot(t), workloadID)
		if err != nil {
			t.Fatalf("%s: %v", workloadID, err)
		}
		if definition.ActorCount != actorCount ||
			definition.ScenarioID != "capacity-"+workloadID ||
			definition.WorkloadID != workloadID ||
			len(definition.Phases) != 5 {
			t.Fatalf("%s definition=%+v", workloadID, definition)
		}
	}
	if _, err := LoadCapacityDefinition(repositoryRoot(t), "overflow-probe"); err == nil {
		t.Fatal("admission-only workload was accepted as network capacity")
	}
}
