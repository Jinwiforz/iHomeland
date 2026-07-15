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
	// logger 只接收公开HTTP/WSS transport允许的低敏字段。
	logger *slog.Logger
	// component 只在Start完整构图成功后存在。
	component *httpapi.Component
	// websocketRegistry 显式拥有http.Server无法等待的hijacked连接。
	websocketRegistry *wscontrol.Registry
	// tcpServer 显式拥有独立listener、连接registry与关闭顺序。
	tcpServer *tcpgameplay.Server
}

// Name 返回lifecycle稳定component名。
func (*publicRuntimeComponent) Name() string { return "public_http" }

// Start 借用已启动storage，构造production adapters/services并最后绑定公开listener。
func (component *publicRuntimeComponent) Start(ctx context.Context) error {
	// 无论构图在哪个阶段结束，bootstrap key都只允许存活到本次Start返回。
	defer component.prepared.Destroy()
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
		_ = websocketRegistry.Stop(cleanupContext)
		return fmt.Errorf("supervise websocket control registry: %w", err)
	}
	cleanupWebSocket := true
	defer func() {
		if !cleanupWebSocket {
			return
		}
		cleanupContext, cancel := context.WithTimeout(context.Background(), component.settings.PublicAPI.WebSocketControl.CloseTimeout)
		defer cancel()
		_ = websocketRegistry.Stop(cleanupContext)
		_ = component.websocketTasks.Stop(cleanupContext, errors.New("websocket control startup rolled back"))
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
	visitService, err := visitsession.NewService(visitStore, visitWorldReader{worlds: worldAdapter}, visitAssignmentReader{placements: placementStore}, component.clock, component.ids, visitPolicy)
	if err != nil {
		return fmt.Errorf("construct visit session service: %w", err)
	}
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
	worldEntry, err := worldentry.NewService(worldAdapter, placementStore, visitService, admissionService, endpointProvider, component.clock, component.settings.PublicAPI.WorldAdmission.MaximumLifetime)
	if err != nil {
		return fmt.Errorf("construct world entry service: %w", err)
	}
	tcpApplication, err := newTCPGameplayApplication(worldRepository, placementStore, visitService, tcpEndpoint, component.clock)
	if err != nil {
		return fmt.Errorf("construct tcp gameplay application: %w", err)
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
			_ = tcpServer.Stop(cleanupContext)
		}
	}()
	if err := httpComponent.Start(ctx); err != nil {
		return err
	}
	component.component = httpComponent
	component.websocketRegistry = websocketRegistry
	component.tcpServer = tcpServer
	cleanupTCP = false
	cleanupWebSocket = false
	return nil
}

// Stop 在全局readiness已进入draining后并行关闭实时连接，再停止公开listener并排空HTTP请求。
func (component *publicRuntimeComponent) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("public runtime stop context is nil")
	}
	var realtime sync.WaitGroup
	var tcpErr, websocketErr error
	if component.tcpServer != nil {
		realtime.Add(1)
		go func() {
			defer realtime.Done()
			tcpContext, cancel := context.WithTimeout(ctx, component.settings.PublicAPI.GameplayTCP.ShutdownTimeout)
			defer cancel()
			tcpErr = component.tcpServer.Stop(tcpContext)
		}()
	}
	if component.websocketRegistry != nil || component.websocketTasks != nil {
		realtime.Add(1)
		go func() {
			defer realtime.Done()
			if component.websocketRegistry != nil {
				websocketErr = component.websocketRegistry.Stop(ctx)
			}
			if component.websocketTasks != nil {
				websocketErr = errors.Join(websocketErr, component.websocketTasks.Stop(ctx, errors.New("websocket control component stopped")))
			}
		}()
	}
	realtime.Wait()
	var httpErr error
	if component.component != nil {
		httpErr = component.component.Stop(ctx)
	}
	return errors.Join(tcpErr, websocketErr, httpErr)
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
