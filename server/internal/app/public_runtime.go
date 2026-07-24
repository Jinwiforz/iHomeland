package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/buildinfo"
	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/contract"
	"github.com/jinwiforz/ihomeland/server/internal/observability"
	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	storageall "github.com/jinwiforz/ihomeland/server/internal/storage"
	storageaccount "github.com/jinwiforz/ihomeland/server/internal/storage/account"
	storagemysql "github.com/jinwiforz/ihomeland/server/internal/storage/mysql"
	storagepersonalworld "github.com/jinwiforz/ihomeland/server/internal/storage/personalworld"
	storageplacement "github.com/jinwiforz/ihomeland/server/internal/storage/placement"
	storageredis "github.com/jinwiforz/ihomeland/server/internal/storage/redis"
	storagesession "github.com/jinwiforz/ihomeland/server/internal/storage/session"
	storagevisitsession "github.com/jinwiforz/ihomeland/server/internal/storage/visitsession"
	storageworldadmission "github.com/jinwiforz/ihomeland/server/internal/storage/worldadmission"
	"github.com/jinwiforz/ihomeland/server/internal/transport/httpapi"
	"github.com/jinwiforz/ihomeland/server/internal/transport/tcpgameplay"
	"github.com/jinwiforz/ihomeland/server/internal/transport/wscontrol"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
	"github.com/jinwiforz/ihomeland/server/internal/worldentry"
)

const (
	// publicProtocolVersion 是当前冻结OpenAPI契约主版本。
	publicProtocolVersion uint32 = 1
	// minimumPublicClientVersion 是当前HTTP契约允许的最低客户端版本。
	minimumPublicClientVersion = "0.1.0"
)

// publicRuntimeComponent 在MySQL/Redis启动后构造真实业务graph，再启动公开listener。
//
// 延迟构造是因为storage component只有Start成功后才公开借用DB/client；该component仍是
// 唯一Composition Root的一部分，并在逆序Stop中先于storage关闭全部公开请求。
type publicRuntimeComponent struct {
	// settings 是完整验证后的启动配置。
	settings config.Config
	// prepared 保存启动副作用前解析的TLS identity与待转移admission key；key在issuer复制后立即清零。
	prepared preparedPublicAPI
	// mysql 指向由外层lifecycle持有的共享pool component，本component只在其Start后借用DB。
	mysql *storagemysql.Component
	// redis 指向由外层lifecycle持有的共享client component，本component只在其Start后借用client。
	redis *storageredis.Component
	// clock 被全部service与store共享。
	clock Clock
	// ids 是唯一CSPRNG identity source。
	ids IDGenerator
	// info 提供公开server version。
	info buildinfo.Info
	// readiness 在完整lifecycle Start后由root切换。
	readiness *Readiness
	// metrics 实现全部storage observer窄接口。
	metrics *observability.Metrics
	// tasks 监督公开Serve loop。
	tasks httpapi.TaskOwner
	// websocketTasks 监督control连接owner，不与HTTP Serve任务混用。
	websocketTasks wscontrol.TaskOwner
	// tcpTasks 监督独立gameplay accept loop，不与HTTP/WSS任务混用。
	tcpTasks tcpgameplay.TaskOwner
	// sliceTasks 监督PersonalWorld竖切的deadline与reconciliation worker。
	sliceTasks *TaskOwner
	// logger 只接收公开HTTP/WSS transport允许的低敏字段。
	logger *slog.Logger
	// simulation 是已先启动的唯一真实 C++ child owner。
	simulation *simulationNodeComponent
	// component 只在Start完整构图成功后存在。
	component *httpapi.Component
	// websocketRegistry 显式拥有http.Server无法等待的hijacked连接。
	websocketRegistry *wscontrol.Registry
	// tcpServer 显式拥有独立listener、连接registry与关闭顺序。
	tcpServer *tcpgameplay.Server
	// deadlineOwner 拥有本竖切全部semantic deadline entry。
	deadlineOwner *semanticDeadlineOwner
	// assignmentCoordinator 在关闭时先撤销 assignment，再由 simulation component 关闭 child。
	assignmentCoordinator *worldAssignmentCoordinator
}

// Name 返回lifecycle稳定component名。
func (*publicRuntimeComponent) Name() string { return "public_http" }

// Start 借用已启动storage，构造production adapters/services并最后绑定公开listener。
func (component *publicRuntimeComponent) Start(ctx context.Context) error {
	// 无论构图在哪个阶段结束，bootstrap key都只允许存活到本次Start返回。
	defer component.prepared.Destroy()
	if component.simulation == nil {
		return errors.New("public runtime requires simulation node component")
	}
	db, client := component.mysql.DB(), component.redis.Client()
	if db == nil || client == nil {
		return errors.New("public HTTP requires started MySQL and Redis components")
	}
	keyspace, err := storageall.NewRedisKeyspace(component.settings.Environment)
	if err != nil {
		return fmt.Errorf("construct public Redis keyspace: %w", err)
	}
	wssEndpoint, err := session.NewEndpoint(session.ChannelWSS, component.settings.PublicAPI.Endpoints.WSS.Host, uint16(component.settings.PublicAPI.Endpoints.WSS.Port))
	if err != nil {
		return fmt.Errorf("construct WSS endpoint: %w", err)
	}
	tcpEndpoint, err := session.NewEndpoint(session.ChannelTLSTCP, component.settings.PublicAPI.Endpoints.TLSTCP.Host, uint16(component.settings.PublicAPI.Endpoints.TLSTCP.Port))
	if err != nil {
		return fmt.Errorf("construct TLS/TCP endpoint: %w", err)
	}
	endpointProvider, err := session.NewStaticEndpointProvider(wssEndpoint, tcpEndpoint)
	if err != nil {
		return err
	}
	websocketConfig := wscontrol.Config{
		Policy:         component.settings.PublicAPI.WebSocketControl,
		FrameBytes:     component.settings.PublicAPI.Limits.RealtimeFrameBytes,
		AllowPlaintext: !component.settings.PublicAPI.TLS.Enabled,
		Logger:         component.logger.With("transport", "websocket_control"),
	}
	websocketCodec, err := wscontrol.NewCodec(contract.WSSPushCatalog(), websocketConfig.FrameBytes, component.clock)
	if err != nil {
		return fmt.Errorf("construct websocket control codec: %w", err)
	}
	websocketRegistry, err := wscontrol.NewRegistry(websocketConfig, websocketCodec, component.clock, component.ids, component.metrics)
	if err != nil {
		return fmt.Errorf("construct websocket control registry: %w", err)
	}
	if err := websocketRegistry.Supervise(component.websocketTasks); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), component.settings.PublicAPI.WebSocketControl.CloseTimeout)
		defer cancel()
		stopErr := websocketRegistry.Stop(cleanupContext)
		return errors.Join(fmt.Errorf("supervise websocket control registry: %w", err), stopErr)
	}
	cleanupWebSocket := true
	defer func() {
		if !cleanupWebSocket {
			return
		}
		cleanupContext, cancel := context.WithTimeout(context.Background(), component.settings.PublicAPI.WebSocketControl.CloseTimeout)
		defer cancel()
		if stopErr := websocketRegistry.Stop(cleanupContext); stopErr != nil {
			component.logger.Error("websocket registry startup rollback failed", "operation", "websocket_registry_stop", "outcome", "failed")
		}
		if stopErr := component.websocketTasks.Stop(cleanupContext, errors.New("websocket control startup rolled back")); stopErr != nil {
			component.logger.Error("websocket task startup rollback failed", "operation", "websocket_task_stop", "outcome", "failed")
		}
	}()
	tcpConfig := tcpgameplay.Config{
		Policy: component.settings.PublicAPI.GameplayTCP, FrameBytes: component.settings.PublicAPI.Limits.RealtimeFrameBytes,
		AllowPlaintext: !component.settings.PublicAPI.TLS.Enabled, Logger: component.logger.With("transport", "tcp_gameplay"),
	}
	tcpCodec, err := tcpgameplay.NewCodec(contract.TLSGameplayCatalog(), tcpConfig.FrameBytes, component.clock)
	if err != nil {
		return fmt.Errorf("construct tcp gameplay codec: %w", err)
	}
	tcpRegistry, err := tcpgameplay.NewRegistry(tcpConfig, tcpCodec, component.clock, component.ids, component.metrics)
	if err != nil {
		return fmt.Errorf("construct tcp gameplay registry: %w", err)
	}
	connectionInvalidator, err := session.NewCompositeConnectionInvalidator(websocketRegistry, tcpRegistry)
	if err != nil {
		return fmt.Errorf("construct realtime invalidator: %w", err)
	}
	sessionStore, err := storagesession.New(client, keyspace, component.metrics)
	if err != nil {
		return fmt.Errorf("construct session store: %w", err)
	}
	sessionPolicy, err := session.NewPolicy(component.settings.PublicAPI.Session.TicketTTL, component.settings.PublicAPI.Session.AccessTTL, component.settings.PublicAPI.Session.RefreshTTL, component.settings.PublicAPI.Session.SessionTTL)
	if err != nil {
		return fmt.Errorf("construct session policy: %w", err)
	}
	sessionService, err := session.NewService(sessionStore, endpointProvider, connectionInvalidator, component.clock, component.ids, session.CryptoSecretGenerator{}, sessionPolicy)
	if err != nil {
		return fmt.Errorf("construct session service: %w", err)
	}
	accountRepository, err := storageaccount.New(db, component.metrics)
	if err != nil {
		return fmt.Errorf("construct account repository: %w", err)
	}
	hasher, dummyHash, err := storageaccount.NewHasher(component.settings.PublicAPI.Account.MaxConcurrentHashes)
	if err != nil {
		return fmt.Errorf("construct account hasher: %w", err)
	}
	accountService, err := account.NewService(accountRepository, hasher, sessionService, component.clock, component.ids, dummyHash)
	if err != nil {
		return fmt.Errorf("construct account service: %w", err)
	}
	worldRepository, err := storagepersonalworld.New(db, component.metrics)
	if err != nil {
		return fmt.Errorf("construct personal world repository: %w", err)
	}
	worldService, err := personalworld.NewService(worldRepository, component.clock, component.ids)
	if err != nil {
		return fmt.Errorf("construct personal world service: %w", err)
	}
	worldAdapter, err := worldentry.NewPersonalWorldServiceAdapter(worldService)
	if err != nil {
		return err
	}
	placementStore, err := storageplacement.New(db, client, keyspace, component.settings.PublicAPI.PlacementReplayTTL, component.metrics)
	if err != nil {
		return fmt.Errorf("construct placement store: %w", err)
	}
	if err := component.simulation.BindCurrent(placementStore); err != nil {
		return fmt.Errorf("bind simulation result current store: %w", err)
	}
	remoteRuntime := component.simulation.Runtime()
	runtimeController := placement.RuntimeController(remoteRuntime)
	runtimeRegistry := runtimeInventory(remoteRuntime)
	runtimeNodeID := component.simulation.RuntimeNodeID()
	placementService, err := placement.NewService(placementStore, runtimeController, component.clock, component.ids, component.settings.PublicAPI.WorldRuntime.PlacementLeaseTTL)
	if err != nil {
		return fmt.Errorf("construct placement service: %w", err)
	}
	deadlineOwner, err := newSemanticDeadlineOwner(component.settings.PublicAPI.WorldRuntime.DeadlineEntries, applicationSemanticClock{Clock: component.clock})
	if err != nil {
		return fmt.Errorf("construct semantic deadline owner: %w", err)
	}
	if err := deadlineOwner.BindObserver(component.metrics); err != nil {
		deadlineOwner.Close()
		return fmt.Errorf("bind semantic deadline observer: %w", err)
	}
	if component.sliceTasks == nil {
		return errors.New("personal world slice task owner is unavailable")
	}
	if err := component.sliceTasks.Go("semantic_deadlines", deadlineOwner.Run); err != nil {
		deadlineOwner.Close()
		return fmt.Errorf("supervise semantic deadline owner: %w", err)
	}
	cleanupSlice := true
	defer func() {
		if !cleanupSlice {
			return
		}
		deadlineOwner.Close()
		cleanupContext, cancel := context.WithTimeout(context.Background(), component.settings.PublicAPI.GameplayTCP.ShutdownTimeout)
		defer cancel()
		if stopErr := component.sliceTasks.Stop(cleanupContext, errors.New("personal world slice startup rolled back")); stopErr != nil {
			component.logger.Error("personal world slice startup rollback failed", "operation", "slice_task_stop", "outcome", "failed")
		}
	}()
	assignmentCoordinator, err := newWorldAssignmentCoordinator(placementService, placementStore, runtimeRegistry, deadlineOwner, component.clock, component.ids, runtimeNodeID, nil)
	if err != nil {
		return fmt.Errorf("construct world assignment coordinator: %w", err)
	}
	if err := assignmentCoordinator.bindSimulationTargets(component.simulation.TargetResolver()); err != nil {
		return fmt.Errorf("bind simulation target resolver: %w", err)
	}
	if err := assignmentCoordinator.bindObserver(component.metrics); err != nil {
		return fmt.Errorf("bind world assignment observer: %w", err)
	}
	visitStore, err := storagevisitsession.New(client, keyspace, component.clock, component.settings.PublicAPI.VisitSession.ReplayRetention, component.metrics)
	if err != nil {
		return fmt.Errorf("construct visit session store: %w", err)
	}
	capacity, err := visitsession.NewCapacity(uint8(component.settings.PublicAPI.VisitSession.Capacity))
	if err != nil {
		return fmt.Errorf("construct visit capacity: %w", err)
	}
	visitPolicy, err := visitsession.NewPolicy(capacity, component.settings.PublicAPI.VisitSession.SessionLifetime, component.settings.PublicAPI.VisitSession.InviteLifetime, component.settings.PublicAPI.VisitSession.ReservationLifetime, component.settings.PublicAPI.VisitSession.OwnerGrace, component.settings.PublicAPI.VisitSession.VisitorReconnectGrace)
	if err != nil {
		return fmt.Errorf("construct visit session policy: %w", err)
	}
	visitService, err := visitsession.NewService(visitStore, visitWorldReader{worlds: worldAdapter}, accountRepository, visitAssignmentReader{placements: placementStore}, component.clock, component.ids, visitPolicy)
	if err != nil {
		return fmt.Errorf("construct visit session service: %w", err)
	}
	tcpPublisher, err := tcpgameplay.NewPublisher(tcpRegistry, component.metrics)
	if err != nil {
		return fmt.Errorf("construct tcp gameplay publisher: %w", err)
	}
	visitCoordinator, err := newPersonalWorldVisitCoordinator(
		visitService, deadlineOwner, component.clock, visitPolicy, websocketRegistry, tcpPublisher, tcpRegistry,
		component.logger.With("owner", "visit_session"), component.metrics, component.settings.PublicAPI.WorldRuntime.DeadlineEntries,
		component.failClosedPersonalWorldSlice,
	)
	if err != nil {
		return fmt.Errorf("construct visit session coordinator: %w", err)
	}
	assignmentCoordinator.onLoss = visitCoordinator.InvalidateAssignment
	admissionStore, err := storageworldadmission.New(client, keyspace, component.metrics)
	if err != nil {
		return fmt.Errorf("construct world admission store: %w", err)
	}
	admissionPolicy, err := worldadmission.NewPolicy(component.settings.PublicAPI.WorldAdmission.MaximumLifetime, component.settings.PublicAPI.WorldAdmission.ReplayRetention)
	if err != nil {
		return fmt.Errorf("construct world admission policy: %w", err)
	}
	admissionService, err := worldadmission.NewService(admissionStore, placementStore, component.clock, component.prepared.derivationKey, admissionPolicy)
	if err != nil {
		return fmt.Errorf("construct world admission service: %w", err)
	}
	worldEntryVisits := coordinatedWorldEntryVisits{visits: visitService, coordinator: visitCoordinator}
	worldEntryAssignments := coordinatedWorldEntryAssignments{assignments: assignmentCoordinator, visits: visitService, coordinator: visitCoordinator}
	worldEntry, err := worldentry.NewService(worldAdapter, worldEntryAssignments, worldEntryVisits, admissionService, endpointProvider, component.clock, component.settings.PublicAPI.WorldAdmission.MaximumLifetime)
	if err != nil {
		return fmt.Errorf("construct world entry service: %w", err)
	}
	tcpApplication, err := newTCPGameplayApplication(worldRepository, placementStore, visitService, visitCoordinator, tcpEndpoint, component.clock)
	if err != nil {
		return fmt.Errorf("construct tcp gameplay application: %w", err)
	}
	if err := visitCoordinator.BindProjector(tcpApplication.projectVisit); err != nil {
		return fmt.Errorf("bind visit snapshot projector: %w", err)
	}
	if err := tcpRegistry.BindLifecycleSink(visitCoordinator); err != nil {
		return fmt.Errorf("bind tcp gameplay lifecycle: %w", err)
	}
	tcpHandshake, err := tcpgameplay.NewHandshake(sessionService, admissionService, tcpEndpoint, component.metrics)
	if err != nil {
		return fmt.Errorf("construct tcp gameplay handshake: %w", err)
	}
	tcpDispatcher, err := tcpgameplay.NewDispatcher(tcpApplication, tcpHandshake, tcpRegistry, component.metrics)
	if err != nil {
		return fmt.Errorf("construct tcp gameplay dispatcher: %w", err)
	}
	tcpServer, err := tcpgameplay.NewServer(tcpConfig, component.prepared.tlsConfig, tcpRegistry, tcpHandshake, tcpDispatcher, component.tcpTasks, component.metrics)
	if err != nil {
		return fmt.Errorf("construct tcp gameplay server: %w", err)
	}
	router, err := httpapi.NewRouter(accountService, sessionService, worldEntry, httpapi.RouterConfig{
		ServerVersion:        component.info.Version,
		ProtocolVersion:      publicProtocolVersion,
		MinimumClientVersion: minimumPublicClientVersion,
		Endpoints:            []session.Endpoint{wssEndpoint, tcpEndpoint},
		HTTPBodyBytes:        component.settings.PublicAPI.Limits.HTTPBodyBytes,
		RealtimeFrameBytes:   component.settings.PublicAPI.Limits.RealtimeFrameBytes,
		Ready:                component.ready,
		Rates:                component.settings.PublicAPI.Rates,
		RateMaxEntries:       component.settings.PublicAPI.RateMaxEntries,
		RateIdleTTL:          component.settings.PublicAPI.RateIdleTTL,
		Observer:             component.metrics,
		Logger:               component.logger,
	})
	if err != nil {
		return fmt.Errorf("construct public HTTP router: %w", err)
	}
	websocketHandler, err := wscontrol.NewHandler(websocketConfig, component.ready, sessionService, wssEndpoint, websocketRegistry, component.metrics)
	if err != nil {
		return fmt.Errorf("construct websocket control handler: %w", err)
	}
	publicHandler, err := wscontrol.Mux(router.Handler(), websocketHandler)
	if err != nil {
		return fmt.Errorf("construct public listener mux: %w", err)
	}
	httpComponent, err := httpapi.NewComponent(component.settings.PublicAPI, publicHandler, component.prepared.tlsConfig, component.tasks)
	if err != nil {
		return fmt.Errorf("construct public HTTP component: %w", err)
	}
	if err := tcpServer.Start(ctx); err != nil {
		return err
	}
	cleanupTCP := true
	defer func() {
		if cleanupTCP {
			cleanupContext, cancel := context.WithTimeout(context.Background(), component.settings.PublicAPI.GameplayTCP.ShutdownTimeout)
			defer cancel()
			if stopErr := tcpServer.Stop(cleanupContext); stopErr != nil {
				component.logger.Error("tcp gameplay startup rollback failed", "operation", "tcp_server_stop", "outcome", "failed")
			}
		}
	}()
	if err := httpComponent.Start(ctx); err != nil {
		return err
	}
	component.component = httpComponent
	component.websocketRegistry = websocketRegistry
	component.tcpServer = tcpServer
	component.deadlineOwner = deadlineOwner
	component.assignmentCoordinator = assignmentCoordinator
	cleanupTCP = false
	cleanupWebSocket = false
	cleanupSlice = false
	return nil
}

// Stop 在全局 readiness 已进入 draining 后先停止公开输入，再停止语义 worker。
func (component *publicRuntimeComponent) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("public runtime stop context is nil")
	}
	var publicInput sync.WaitGroup
	var httpErr, tcpErr, websocketErr error
	if component.component != nil {
		publicInput.Add(1)
		go func() {
			defer publicInput.Done()
			httpErr = component.component.Stop(ctx)
		}()
	}
	if component.tcpServer != nil {
		publicInput.Add(1)
		go func() {
			defer publicInput.Done()
			tcpContext, cancel := context.WithTimeout(ctx, component.settings.PublicAPI.GameplayTCP.ShutdownTimeout)
			defer cancel()
			tcpErr = component.tcpServer.Stop(tcpContext)
		}()
	}
	if component.websocketRegistry != nil || component.websocketTasks != nil {
		publicInput.Add(1)
		go func() {
			defer publicInput.Done()
			if component.websocketRegistry != nil {
				websocketErr = component.websocketRegistry.Stop(ctx)
			}
			if component.websocketTasks != nil {
				websocketErr = errors.Join(websocketErr, component.websocketTasks.Stop(ctx, errors.New("websocket control component stopped")))
			}
		}()
	}
	publicInput.Wait()
	if component.deadlineOwner != nil {
		component.deadlineOwner.Close()
	}
	var sliceErr error
	if component.sliceTasks != nil {
		sliceErr = component.sliceTasks.Stop(ctx, errors.New("personal world slice component stopped"))
	}
	var assignmentErr error
	if component.assignmentCoordinator != nil {
		assignmentErr = component.assignmentCoordinator.Shutdown(ctx)
	}
	return errors.Join(tcpErr, websocketErr, httpErr, sliceErr, assignmentErr)
}

// failClosedPersonalWorldSlice 撤销 readiness；重复 draining/stopped 只记录稳定结果，不吞掉异常状态。
func (component *publicRuntimeComponent) failClosedPersonalWorldSlice() {
	if err := component.readiness.BeginDraining(); err != nil {
		component.logger.Error("personal world slice fail-closed transition failed", "operation", "readiness_draining", "outcome", "failed")
	}
}

// Address 返回公开listener实际地址；未启动时为空。
func (component *publicRuntimeComponent) Address() string {
	if component.component == nil {
		return ""
	}
	return component.component.Address()
}

// ready 复用全局readiness且不允许handler修改状态。
func (component *publicRuntimeComponent) ready() bool {
	_, ready := component.readiness.DiagnosticState()
	return ready
}

// visitWorldReader 把world-entry primary owner适配为VisitSession仅在Open使用的owned-world端口。
type visitWorldReader struct {
	// worlds 保留PersonalWorld owner窄适配器。
	worlds *worldentry.PersonalWorldServiceAdapter
}

// ResolveOwnedWorld 返回active primary world；本change不公开VisitSession Open，因此不会由HTTP创建新world。
func (reader visitWorldReader) ResolveOwnedWorld(ctx context.Context, ownerID account.PlayerID) (personalworld.Snapshot, visitsession.OwnedWorldOutcome, error) {
	snapshot, err := reader.worlds.EnsurePrimary(ctx, ownerID)
	if err != nil {
		return personalworld.Snapshot{}, visitsession.OwnedWorldOutcomeUnspecified, err
	}
	return snapshot, visitsession.OwnedWorldOutcomeFound, nil
}

// visitAssignmentReader 把placement统一outcome映射为VisitSession owner的封闭读取决议。
type visitAssignmentReader struct {
	// placements 是共享production store，不创建第二client。
	placements *storageplacement.Store
}

// ResolveCurrent 委托placement store并严格转换Found/NotFound。
func (reader visitAssignmentReader) ResolveCurrent(ctx context.Context, worldID personalworld.PersonalWorldID, observedAt time.Time) (placement.AssignmentSnapshot, visitsession.AssignmentOutcome, error) {
	snapshot, outcome, err := reader.placements.Resolve(ctx, worldID, observedAt)
	if err != nil {
		return placement.AssignmentSnapshot{}, visitsession.AssignmentOutcomeUnspecified, err
	}
	if outcome == placement.ResolveOutcomeFound {
		return snapshot, visitsession.AssignmentOutcomeFound, nil
	}
	if outcome == placement.ResolveOutcomeNotFound {
		return placement.AssignmentSnapshot{}, visitsession.AssignmentOutcomeNotFound, nil
	}
	return placement.AssignmentSnapshot{}, visitsession.AssignmentOutcomeUnspecified, errors.New("placement returned invalid resolve outcome")
}
