// Command battlequalificationtool 是 B0.6 黑盒网络资格的唯一 Go harness 入口。
//
// 该命令只允许依赖公开 testclient contract 与 internal/battlequalification 工具边界，
// 不得导入 production battle transport、simulation gameplay 或 application service。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/gateevidence"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/manifest"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/protocolclient"
	qualificationreport "github.com/jinwiforz/ihomeland/server/internal/battlequalification/report"
	"github.com/jinwiforz/ihomeland/server/internal/battlequalification/runner"
	"github.com/jinwiforz/ihomeland/server/internal/testclient"
)

const (
	// diagnosticRequestTimeout 限制一次 existing metrics listener scrape。
	diagnosticRequestTimeout = 2 * time.Second
)

// main 是稳定进程退出边界，不回显 executable path、endpoint、credential 或 child stderr。
func main() {
	if err := run(os.Args[1:]); err != nil {
		var publicError testclient.PublicError
		var qualificationFailure interface {
			QualificationFailureCode() string
		}
		if errors.As(err, &qualificationFailure) {
			fmt.Fprintf(
				os.Stderr,
				"battle qualification tool failed: stage-%s\n",
				qualificationFailure.QualificationFailureCode(),
			)
		} else if errors.As(err, &publicError) {
			fmt.Fprintf(
				os.Stderr,
				"battle qualification tool failed: public-error-%d\n",
				publicError.Code,
			)
		} else {
			fmt.Fprintln(os.Stderr, "battle qualification tool failed")
		}
		os.Exit(1)
	}
}

// run 分派 closed action；完整 verify/soak/finalize 由 PowerShell owner 编排。
func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("battle qualification action is missing")
	}
	switch arguments[0] {
	case "client-probe":
		return runClientProbe(arguments[1:])
	case "run-scenario":
		return runScenario(arguments[1:], "scenario")
	case "run-soak":
		return runScenario(arguments[1:], "soak")
	case "run-capacity":
		return runScenario(arguments[1:], "capacity")
	case "run-security":
		return runScenario(arguments[1:], "security")
	case "run-business-lifecycle":
		return runOwnedLifecycle(
			arguments[1:],
			manifest.LifecycleOwnerPublicProtocol,
		)
	case "run-process-lifecycle":
		return runOwnedLifecycle(
			arguments[1:],
			manifest.LifecycleOwnerProcessSupervisor,
		)
	case "run-gateway-lifecycle":
		return runScenario(arguments[1:], "gateway-lifecycle")
	case "run-admission-capacity":
		return runAdmissionCapacity(arguments[1:])
	case "finalize":
		return runFinalize(arguments[1:])
	default:
		return errors.New("battle qualification action is unknown")
	}
}

// runOwnedLifecycle 运行一个由 manifest owner 精确分配的生命周期场景。
func runOwnedLifecycle(arguments []string, expectedOwner string) error {
	flags := flag.NewFlagSet("run-lifecycle", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	baseURL := flags.String("base-url", "", "public HTTPS base URL")
	caFile := flags.String("ca-file", "", "temporary CA PEM")
	repositoryRoot := flags.String("repository-root", "", "absolute repository root")
	caseID := flags.String("case", "", "registered lifecycle case ID")
	controlDirectory := flags.String(
		"control-directory",
		"",
		"absolute process supervisor checkpoint directory",
	)
	evidencePath := flags.String("evidence", "", "absolute lifecycle evidence path")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 ||
		!filepath.IsAbs(*repositoryRoot) || !filepath.IsAbs(*caFile) ||
		!filepath.IsAbs(*evidencePath) {
		return errors.New("battle lifecycle arguments are invalid")
	}
	policy, err := manifest.LoadLifecyclePolicy(*repositoryRoot)
	if err != nil {
		return err
	}
	if !slices.Contains(policy.Cases, *caseID) ||
		policy.Owners[*caseID] != expectedOwner {
		return errors.New("battle lifecycle case is not registered")
	}
	var faults *testclient.FileFaultController
	if expectedOwner == manifest.LifecycleOwnerProcessSupervisor {
		if !filepath.IsAbs(*controlDirectory) {
			return errors.New("battle process lifecycle control directory is invalid")
		}
		faults, err = testclient.NewFileFaultController(*controlDirectory)
		if err != nil {
			return err
		}
	} else if *controlDirectory != "" {
		return errors.New("battle public lifecycle cannot use process control")
	}
	definition, err := manifest.Load(*repositoryRoot, "real-clean-default")
	if err != nil {
		return err
	}
	deadline := time.Duration(
		definition.ExecutionPolicy.ScenarioDeadlineMilliseconds,
	) * time.Millisecond
	publicClient, tlsConfig, err := newPublicClient(*baseURL, *caFile, deadline)
	if err != nil {
		return err
	}
	bootstrapContext, cancelBootstrap := context.WithTimeout(
		context.Background(),
		diagnosticRequestTimeout,
	)
	bootstrap, err := publicClient.BootstrapConfig(bootstrapContext)
	cancelBootstrap()
	if err != nil {
		return err
	}
	runContext, cancelRun := context.WithTimeout(context.Background(), deadline)
	defer cancelRun()
	evidence, err := runner.RunLifecycle(
		runContext,
		&testclient.ScenarioRuntime{
			RepositoryRoot: *repositoryRoot,
			HTTP:           publicClient,
			TLSConfig:      tlsConfig,
			Bootstrap:      bootstrap,
			Faults:         faults,
		},
		*caseID,
	)
	if err != nil {
		return err
	}
	if evidence.Transition == nil ||
		evidence.Transition.Mode != policy.Modes[*caseID] {
		return errors.New("battle lifecycle transition mode drifted")
	}
	if err := writeEvidence(*evidencePath, evidence); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "battle-lifecycle-passed")
	return nil
}

// runFinalize 只消费显式三 run 目录并原子写出低敏 final report。
func runFinalize(arguments []string) error {
	flags := flag.NewFlagSet("finalize", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	repositoryRoot := flags.String("repository-root", "", "absolute repository root")
	runA := flags.String("run-a", "", "absolute first verify directory")
	runB := flags.String("run-b", "", "absolute second verify directory")
	soakRun := flags.String("soak-run", "", "absolute soak directory")
	reportPath := flags.String("report", "", "absolute final report path")
	overlayPath := flags.String("overlay", "", "absolute profile implementation overlay path")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 ||
		!filepath.IsAbs(*repositoryRoot) || !filepath.IsAbs(*runA) ||
		!filepath.IsAbs(*runB) || !filepath.IsAbs(*soakRun) ||
		!filepath.IsAbs(*reportPath) || !filepath.IsAbs(*overlayPath) {
		return errors.New("battle qualification finalize arguments are invalid")
	}
	finalReport, err := qualificationreport.Finalize(qualificationreport.Input{
		RepositoryRoot: *repositoryRoot,
		RunADirectory:  *runA,
		RunBDirectory:  *runB,
		SoakDirectory:  *soakRun,
	})
	if err != nil {
		return err
	}
	if err := writeEvidence(*reportPath, finalReport); err != nil {
		return err
	}
	if finalReport.Qualification == "battle-network-qualified-windows-x64-controlled" {
		reportDigest, err := fileSHA256(*reportPath)
		if err != nil {
			return err
		}
		overlay, err := qualificationreport.NewProfileImplementationOverlay(
			*repositoryRoot,
			finalReport,
			reportDigest,
		)
		if err != nil {
			return err
		}
		if err := writeEvidence(*overlayPath, overlay); err != nil {
			return err
		}
	}
	fmt.Fprintln(os.Stdout, finalReport.Qualification)
	return nil
}

// runClientProbe 验证 exact C++ client identity 与有界正常关闭。
func runClientProbe(arguments []string) error {
	flags := flag.NewFlagSet("client-probe", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	executablePath := flags.String("executable", "", "absolute protocol client executable")
	buildIdentityHex := flags.String("build-identity", "", "expected 32-byte lowercase hex identity")
	timeout := flags.Duration("timeout", 5*time.Second, "probe and cleanup deadline")
	if err := flags.Parse(arguments); err != nil ||
		flags.NArg() != 0 || *timeout <= 0 {
		return errors.New("client probe arguments are invalid")
	}
	identity, err := parseBuildIdentity(*buildIdentityHex)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	supervisor, err := protocolclient.Start(ctx, protocolclient.Config{
		ExecutablePath:        *executablePath,
		ExpectedBuildIdentity: identity,
	})
	if err != nil {
		return err
	}
	if err := supervisor.Close(ctx); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "battle-protocol-client-ready")
	return nil
}

// runScenario 运行一个 manifest-registered fault scenario 并原子写出四源 evidence。
func runScenario(arguments []string, mode string) error {
	flags := flag.NewFlagSet("run-scenario", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	baseURL := flags.String("base-url", "", "public HTTPS base URL")
	caFile := flags.String("ca-file", "", "temporary CA PEM")
	repositoryRoot := flags.String("repository-root", "", "absolute repository root")
	scenarioID := flags.String("scenario", "", "registered B0.6 scenario ID")
	workloadID := flags.String("workload", "", "registered B0.6 capacity workload ID")
	executablePath := flags.String("protocol-client", "", "absolute protocol client executable")
	buildIdentityHex := flags.String("build-identity", "", "expected 32-byte lowercase hex identity")
	backendText := flags.String("backend", "", "numeric C++ UDP backend")
	frontendText := flags.String("frontend-bind", "", "numeric advertised gateway bind")
	diagnosticURL := flags.String("diagnostic-url", "", "loopback diagnostic metrics URL")
	evidencePath := flags.String("evidence", "", "absolute scenario evidence path")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 ||
		!filepath.IsAbs(*repositoryRoot) || !filepath.IsAbs(*caFile) ||
		!filepath.IsAbs(*executablePath) || !filepath.IsAbs(*evidencePath) {
		return errors.New("battle qualification scenario arguments are invalid")
	}
	var definition manifest.Definition
	var securityPolicy *manifest.SecurityPolicy
	var lifecyclePolicy *manifest.LifecyclePolicy
	var err error
	switch mode {
	case "capacity":
		definition, err = manifest.LoadCapacityDefinition(*repositoryRoot, *workloadID)
	case "security":
		definition, err = manifest.Load(*repositoryRoot, "real-clean-default")
		if err == nil {
			loadedPolicy, loadErr := manifest.LoadSecurityPolicy(*repositoryRoot)
			if loadErr != nil {
				err = loadErr
			} else {
				securityPolicy = &loadedPolicy
			}
		}
	case "gateway-lifecycle":
		loadedPolicy, loadErr := manifest.LoadLifecyclePolicy(*repositoryRoot)
		if loadErr != nil {
			err = loadErr
			break
		}
		if !slices.Contains(loadedPolicy.Cases, *scenarioID) ||
			loadedPolicy.Owners[*scenarioID] !=
				manifest.LifecycleOwnerFaultGateway {
			err = errors.New("battle gateway lifecycle case is not registered")
			break
		}
		lifecyclePolicy = &loadedPolicy
		definitionID := "real-clean-default"
		if *scenarioID == "valid-endpoint-rebind" {
			definitionID = "real-uplink-latency-jitter"
		}
		definition, err = manifest.Load(*repositoryRoot, definitionID)
	default:
		definition, err = manifest.Load(*repositoryRoot, *scenarioID)
	}
	if err != nil {
		return err
	}
	metricCatalog, err := manifest.LoadMetricCatalog(*repositoryRoot)
	if err != nil {
		return err
	}
	var soakPolicy *manifest.SoakPolicy
	deadlineMilliseconds := definition.ExecutionPolicy.ScenarioDeadlineMilliseconds
	if mode == "soak" {
		loaded, loadErr := manifest.LoadSoakPolicy(*repositoryRoot)
		if loadErr != nil {
			return loadErr
		}
		soakPolicy = &loaded
		deadlineMilliseconds = loaded.WarmupMilliseconds +
			loaded.DurationMilliseconds + loaded.CleanupMilliseconds
	}
	backend, err := netip.ParseAddrPort(*backendText)
	if err != nil {
		return errors.New("battle qualification backend is invalid")
	}
	frontend, err := netip.ParseAddrPort(*frontendText)
	if err != nil {
		return errors.New("battle qualification frontend is invalid")
	}
	socketConfig, err := definition.SocketConfig(backend, frontend)
	if err != nil {
		return err
	}
	identity, err := parseBuildIdentity(*buildIdentityHex)
	if err != nil {
		return err
	}
	publicClient, tlsConfig, err := newPublicClient(
		*baseURL,
		*caFile,
		time.Duration(deadlineMilliseconds)*time.Millisecond,
	)
	if err != nil {
		return err
	}
	bootstrapContext, cancelBootstrap := context.WithTimeout(
		context.Background(),
		diagnosticRequestTimeout,
	)
	bootstrap, err := publicClient.BootstrapConfig(bootstrapContext)
	cancelBootstrap()
	if err != nil {
		return err
	}
	deadline := time.Duration(deadlineMilliseconds) * time.Millisecond
	scenarioContext, cancelScenario := context.WithTimeout(context.Background(), deadline)
	defer cancelScenario()
	runnerConfig := runner.Config{
		Definition:    definition,
		GatewayConfig: socketConfig,
		Runtime: &testclient.ScenarioRuntime{
			RepositoryRoot: *repositoryRoot,
			HTTP:           publicClient,
			TLSConfig:      tlsConfig,
			Bootstrap:      bootstrap,
		},
		ProtocolClientConfig: protocolclient.Config{
			ExecutablePath:        *executablePath,
			ExpectedBuildIdentity: identity,
		},
		DiagnosticURL: *diagnosticURL,
		DiagnosticClient: &http.Client{
			Transport: &http.Transport{
				Proxy:                 nil,
				DialContext:           (&net.Dialer{}).DialContext,
				ForceAttemptHTTP2:     false,
				MaxIdleConns:          1,
				MaxIdleConnsPerHost:   1,
				IdleConnTimeout:       diagnosticRequestTimeout,
				ResponseHeaderTimeout: diagnosticRequestTimeout,
			},
			Timeout: diagnosticRequestTimeout,
		},
		Soak:    soakPolicy,
		Metrics: metricCatalog,
	}
	var evidence any
	if securityPolicy != nil {
		evidence, err = runner.RunSecurity(
			scenarioContext,
			runnerConfig,
			*securityPolicy,
			*scenarioID,
		)
	} else if lifecyclePolicy != nil {
		evidence, err = runner.RunGatewayLifecycle(
			scenarioContext,
			runnerConfig,
			*scenarioID,
		)
	} else {
		evidence, err = runner.Run(scenarioContext, runnerConfig)
	}
	if err != nil {
		if failureEvidence, available :=
			runner.NewScenarioFailureEvidence(definition, err); available {
			if writeErr := writeEvidence(*evidencePath, failureEvidence); writeErr != nil {
				return errors.Join(err, writeErr)
			}
		}
		return err
	}
	if lifecyclePolicy != nil {
		lifecycleEvidence, ok := evidence.(gateevidence.Evidence)
		if !ok || lifecycleEvidence.Transition == nil ||
			lifecycleEvidence.Transition.Mode !=
				lifecyclePolicy.Modes[*scenarioID] {
			return errors.New("battle gateway lifecycle transition mode drifted")
		}
	}
	if err := writeEvidence(*evidencePath, evidence); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "battle-scenario-passed")
	return nil
}

// runAdmissionCapacity 验证第九 BattleTicket 拒绝与 33 人 VisitSession revision 不变。
func runAdmissionCapacity(arguments []string) error {
	flags := flag.NewFlagSet("run-admission-capacity", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	baseURL := flags.String("base-url", "", "public HTTPS base URL")
	caFile := flags.String("ca-file", "", "temporary CA PEM")
	repositoryRoot := flags.String("repository-root", "", "absolute repository root")
	evidencePath := flags.String("evidence", "", "absolute admission evidence path")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 ||
		!filepath.IsAbs(*repositoryRoot) || !filepath.IsAbs(*caFile) ||
		!filepath.IsAbs(*evidencePath) {
		return errors.New("battle admission capacity arguments are invalid")
	}
	definition, err := manifest.Load(*repositoryRoot, "real-clean-default")
	if err != nil {
		return err
	}
	deadline := time.Duration(
		definition.ExecutionPolicy.ScenarioDeadlineMilliseconds,
	) * time.Millisecond
	publicClient, tlsConfig, err := newPublicClient(*baseURL, *caFile, deadline)
	if err != nil {
		return err
	}
	bootstrapContext, cancelBootstrap := context.WithTimeout(
		context.Background(),
		diagnosticRequestTimeout,
	)
	bootstrap, err := publicClient.BootstrapConfig(bootstrapContext)
	cancelBootstrap()
	if err != nil {
		return err
	}
	runContext, cancelRun := context.WithTimeout(context.Background(), deadline)
	defer cancelRun()
	evidence, err := runner.RunAdmissionCapacity(
		runContext,
		&testclient.ScenarioRuntime{
			RepositoryRoot: *repositoryRoot,
			HTTP:           publicClient,
			TLSConfig:      tlsConfig,
			Bootstrap:      bootstrap,
		},
	)
	if err != nil {
		return err
	}
	if err := writeEvidence(*evidencePath, evidence); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "battle-admission-capacity-passed")
	return nil
}

// parseBuildIdentity 解码 canonical lowercase SHA-256 binary identity。
func parseBuildIdentity(value string) ([32]byte, error) {
	var identity [32]byte
	identityBytes, err := hex.DecodeString(value)
	if err != nil || len(identityBytes) != len(identity) ||
		hex.EncodeToString(identityBytes) != value {
		clear(identityBytes)
		return identity, errors.New("client build identity is invalid")
	}
	copy(identity[:], identityBytes)
	clear(identityBytes)
	return identity, nil
}

// writeEvidence 在目标目录内写临时文件并 rename，避免 partial JSON 被聚合器接受。
func writeEvidence(path string, evidence any) error {
	directory := filepath.Dir(path)
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return errors.New("battle qualification evidence directory is unavailable")
	}
	file, err := os.CreateTemp(directory, ".scenario-evidence-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(evidence); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// fileSHA256 对刚刚提交的低敏 evidence 计算 lowercase SHA-256。
func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	clear(raw)
	return hex.EncodeToString(digest[:]), nil
}
