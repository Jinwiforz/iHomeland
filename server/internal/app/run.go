package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/jinwiforz/ihomeland/server/internal/buildinfo"
	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/gameplaypackage"
	"github.com/jinwiforz/ihomeland/server/internal/logging"
	"github.com/jinwiforz/ihomeland/server/internal/observability"
	"github.com/jinwiforz/ihomeland/server/internal/secret"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	"github.com/jinwiforz/ihomeland/server/internal/transport/diagnostic"
)

// Options 是 cmd/server 可以注入的 bootstrap 输入，不允许传入业务 service 或 adapter。
type Options struct {
	// ConfigPath 是显式 YAML 配置路径。
	ConfigPath string
	// LookupEnv 允许测试替换环境快照；生产为空时使用 os.LookupEnv。
	LookupEnv config.LookupEnv
	// Output 接收结构化日志；为空时使用 stdout，保持直接调用与 cmd/server 的流语义一致。
	Output io.Writer
	// BuildInfo 是进程启动后不可变的公开构建身份。
	BuildInfo buildinfo.Info
	// Clock 允许 lifecycle 测试使用 deterministic time；为空时使用 SystemClock。
	Clock Clock
	// IDGenerator 创建进程实例 ID；为空时使用 CSPRNG。
	IDGenerator IDGenerator
	// SecretProvider 解析显式 env:/file: reference；为空时使用 production environment/file provider。
	SecretProvider secret.Provider
}

// Run 加载配置、构建唯一 Composition Root，并阻塞到关闭完成。
func Run(ctx context.Context, options Options) Result {
	settings, err := config.Load(options.ConfigPath, options.LookupEnv)
	if err != nil {
		return Result{Kind: ResultConfigError, Err: err}
	}
	if err := options.BuildInfo.Validate(); err != nil {
		return Result{Kind: ResultStartupError, Err: err}
	}
	if !settings.SimulationControl.Enabled {
		return Result{Kind: ResultConfigError, Err: errors.New("simulationControl.enabled is required by the server runtime")}
	}
	packageSelection, err := gameplaypackage.Select(gameplaypackage.Request{
		RootPath:           settings.GameplayPackage.RootPath,
		ArenaRootPath:      settings.GameplayPackage.ArenaRootPath,
		PackageID:          settings.GameplayPackage.PackageID,
		ConfigIdentity:     settings.GameplayPackage.ConfigIdentity,
		NavigationIdentity: settings.GameplayPackage.NavigationIdentity,
		PhysicsIdentity:    settings.GameplayPackage.PhysicsIdentity,
		WireIdentity:       settings.GameplayPackage.WireIdentity,
		ModelManifest:      settings.SimulationControl.ModelManifest,
		ProfileManifest:    settings.SimulationControl.ProfileManifest,
		BattleWireIdentity: settings.PublicAPI.BattleUDP.WireIdentity,
	})
	if err != nil {
		return Result{Kind: ResultConfigError, Err: err}
	}
	secretProvider := options.SecretProvider
	if secretProvider == nil {
		secretProvider = secret.NewEnvironmentFileProvider()
	}
	if err := validateSecretProvider(secretProvider); err != nil {
		return Result{Kind: ResultConfigError, Err: err}
	}
	startupContext, cancelStartup := context.WithTimeout(ctx, settings.Runtime.StartupTimeout)
	defer cancelStartup()
	preparedPublic, err := preparePublicAPI(startupContext, settings.PublicAPI, secretProvider)
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultConfigError, Err: err}
	}
	defer preparedPublic.Destroy()
	prepared, err := prepareStorage(startupContext, settings.Storage, secretProvider)
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultConfigError, Err: err}
	}
	defer prepared.mysqlPassword.Destroy()
	defer prepared.redisPassword.Destroy()
	output := options.Output
	if output == nil {
		output = os.Stdout
	}
	logger := logging.New(settings.Logging, output)
	clock := options.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	ids := options.IDGenerator
	if ids == nil {
		ids = RandomIDGenerator{}
	}
	instanceID, err := ids.NewID()
	if err != nil {
		return Result{Kind: ResultStartupError, Err: err}
	}
	logger = logger.With("instance_id", instanceID)
	runtimeLogger := logger.With("component", "runtime")
	runtimeLogger.Info(
		"gameplay package selected",
		"package_id", packageSelection.PackageID,
		"config_identity", packageSelection.ConfigIdentity,
		"navigation_identity", packageSelection.NavigationIdentity,
		"physics_identity", packageSelection.PhysicsIdentity,
		"wire_identity", packageSelection.WireIdentity,
		"mapping_identity", packageSelection.MappingIdentity,
	)
	metrics := observability.NewMetrics()
	readiness := NewReadiness()
	tasks := NewTaskGroup(metrics)

	// Component context 有意脱离进程取消；各组件只能在逆序 Stop 阶段取消自身任务。
	componentContext, cancelComponents := context.WithCancelCause(context.WithoutCancel(ctx))
	defer cancelComponents(errors.New("composition root released"))
	diagnosticTasks, err := tasks.NewOwner(componentContext, "diagnostic")
	if err != nil {
		return Result{Kind: ResultStartupError, Err: err}
	}
	mysqlTasks, err := tasks.NewOwner(componentContext, "mysql")
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultStartupError, Err: err}
	}
	redisTasks, err := tasks.NewOwner(componentContext, "redis")
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultStartupError, Err: err}
	}
	simulationTasks, err := tasks.NewOwner(componentContext, "simulation_node")
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultStartupError, Err: err}
	}
	publicTasks, err := tasks.NewOwner(componentContext, "public_http")
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultStartupError, Err: err}
	}
	websocketTasks, err := tasks.NewOwner(componentContext, "websocket_control")
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultStartupError, Err: err}
	}
	tcpTasks, err := tasks.NewOwner(componentContext, "tcp_gameplay")
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultStartupError, Err: err}
	}
	sliceTasks, err := tasks.NewOwner(componentContext, "personal_world_slice")
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultStartupError, Err: err}
	}
	diagnosticServer := diagnostic.New(settings.Diagnostic, readiness, options.BuildInfo, metrics, diagnosticTasks)
	mysqlComponent, err := storagemysql.New(settings.Storage.MySQL, prepared.mysqlPassword, prepared.mysqlTLS, mysqlTasks, metrics, logger.With("component", "mysql"))
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultStartupError, Err: err}
	}
	redisComponent, err := storageredis.New(settings.Storage.Redis, prepared.redisPassword, prepared.redisTLS, redisTasks, metrics, logger.With("component", "redis"))
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultStartupError, Err: err}
	}
	simulationComponent, err := newSimulationNodeComponent(
		settings.SimulationControl,
		packageSelection,
		settings.PublicAPI.BattleUDP,
		mysqlComponent,
		simulationTasks,
		clock,
		ids,
		metrics,
		logger.With("component", "simulation_node"),
	)
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultStartupError, Err: err}
	}
	publicComponent := &publicRuntimeComponent{settings: settings, prepared: preparedPublic, mysql: mysqlComponent, redis: redisComponent, clock: clock, ids: ids, info: options.BuildInfo, readiness: readiness, metrics: metrics, tasks: publicTasks, websocketTasks: websocketTasks, tcpTasks: tcpTasks, sliceTasks: sliceTasks, logger: logger.With("component", "public_http"), simulation: simulationComponent}
	components := []Component{diagnosticServer, mysqlComponent, redisComponent, simulationComponent, publicComponent}
	lifecycle, err := NewLifecycle(components, clock, logger, metrics)
	if err != nil {
		cancelStartup()
		return Result{Kind: ResultStartupError, Err: err}
	}
	if !settings.DiagnosticIsLoopback() {
		logger.Warn("diagnostic listener is not loopback-bound; enforce deployment network policy", "component", "diagnostic", "operation", "configure")
	}

	startErr := lifecycle.Start(startupContext)
	cancelStartup()
	if startErr != nil {
		metrics.RecordStartup("failed")
		return finishStartupFailure(settings, lifecycle, tasks, readiness, cancelComponents, runtimeLogger, metrics, startErr)
	}
	if ctx.Err() != nil {
		metrics.RecordStartup("cancelled")
		return finish(settings, lifecycle, tasks, readiness, cancelComponents, runtimeLogger, metrics, "signal", nil)
	}
	if err := readiness.MarkReady(); err != nil {
		metrics.RecordStartup("failed")
		return finishStartupFailure(settings, lifecycle, tasks, readiness, cancelComponents, runtimeLogger, metrics, err)
	}
	metrics.RecordStartup("ready")
	runtimeLogger.Info("server runtime ready", "operation", "startup", "diagnostic_address", diagnosticServer.Address(), "public_http_address", publicComponent.Address(), "version", options.BuildInfo.Version)

	var runtimeErr error
	reason := "signal"
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), ErrSignalShutdown) {
			reason = "context"
			runtimeErr = context.Cause(ctx)
		}
	case failure := <-tasks.Failures():
		reason = "task_failure"
		runtimeErr = failure
		runtimeLogger.Error("supervised task failed", "operation", "run", "task", failure.Task, "kind", failure.Kind, "error", failure.Err, "stack", string(failure.Stack))
	}
	return finish(settings, lifecycle, tasks, readiness, cancelComponents, runtimeLogger, metrics, reason, runtimeErr)
}

// finishStartupFailure 复用统一释放流程，并在干净回滚后保留 startup 退出分类。
func finishStartupFailure(settings config.Config, lifecycle *Lifecycle, tasks *TaskGroup, readiness *Readiness, cancelComponents context.CancelCauseFunc, logger *slog.Logger, metrics *observability.Metrics, startErr error) Result {
	result := finish(settings, lifecycle, tasks, readiness, cancelComponents, logger, metrics, "startup_failure", startErr)
	if result.Kind == ResultShutdownError {
		return result
	}
	result.Kind = ResultStartupError
	return result
}

// finish 执行 draining、逆序 Stop、任务确认与最终结果分类。
//
// shutdown context 有意从 Background 派生，因为触发关闭的进程 context 通常已经取消；
// 若复用原 context，所有 component 会在尚未获得清理预算时立即超时。
func finish(settings config.Config, lifecycle *Lifecycle, tasks *TaskGroup, readiness *Readiness, cancelComponents context.CancelCauseFunc, logger *slog.Logger, metrics *observability.Metrics, reason string, runtimeErr error) Result {
	drainErr := readiness.BeginDraining()
	if drainErr == nil {
		logger.Info("server runtime draining", "operation", "shutdown", "reason", reason)
	} else {
		logger.Error("server runtime failed to enter draining", "operation", "shutdown", "reason", reason, "error", drainErr)
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), settings.Runtime.ShutdownTimeout)
	defer cancelShutdown()
	stopErr := lifecycle.Stop(shutdownContext)
	cancelComponents(errors.New("all lifecycle components stopped"))
	waitErr := tasks.Wait(shutdownContext)
	select {
	case failure := <-tasks.Failures():
		if runtimeErr == nil {
			reason = "task_failure"
			runtimeErr = failure
		} else {
			runtimeErr = errors.Join(runtimeErr, failure)
		}
	default:
	}
	stateErr := readiness.MarkStopped()
	shutdownErr := errors.Join(drainErr, stopErr, waitErr, stateErr)
	if shutdownErr != nil {
		metrics.RecordShutdown(reason, "failed")
		logger.Error("server runtime shutdown failed", "operation", "shutdown", "reason", reason, "error", shutdownErr)
		return Result{Kind: ResultShutdownError, Err: errors.Join(runtimeErr, shutdownErr)}
	}
	if runtimeErr != nil {
		metrics.RecordShutdown(reason, "fatal")
		logger.Error("server runtime stopped after fatal error", "operation", "shutdown", "reason", reason, "error", runtimeErr)
		return Result{Kind: ResultRuntimeError, Err: runtimeErr}
	}
	metrics.RecordShutdown(reason, "clean")
	logger.Info("server runtime stopped", "operation", "shutdown", "reason", reason)
	return Result{Kind: ResultClean}
}

// ResultMessage 创建 cmd/server 可写入 stderr 的单行诊断，不包含配置原始值。
func ResultMessage(result Result) string {
	if result.Err == nil {
		return ""
	}
	return fmt.Sprintf("server exited with %s: %v", result.Kind, result.Err)
}
