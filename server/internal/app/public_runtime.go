package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/buildinfo"
	"github.com/jinwiforz/ihomeland/server/internal/config"
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
	// logger 只接收HTTP transport允许的低敏字段。
	logger *slog.Logger
	// component 只在Start完整构图成功后存在。
	component *httpapi.Component
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
	sessionStore, err := storagesession.New(client, keyspace, component.metrics)
	if err != nil {
		return fmt.Errorf("construct session store: %w", err)
	}
	sessionPolicy, err := session.NewPolicy(component.settings.PublicAPI.Session.TicketTTL, component.settings.PublicAPI.Session.AccessTTL, component.settings.PublicAPI.Session.RefreshTTL, component.settings.PublicAPI.Session.SessionTTL)
	if err != nil {
		return fmt.Errorf("construct session policy: %w", err)
	}
	sessionService, err := session.NewService(sessionStore, endpointProvider, session.NoActiveRealtimeConnections{}, component.clock, component.ids, session.CryptoSecretGenerator{}, sessionPolicy)
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
	httpComponent, err := httpapi.NewComponent(component.settings.PublicAPI, router.Handler(), component.prepared.tlsConfig, component.tasks)
	if err != nil {
		return fmt.Errorf("construct public HTTP component: %w", err)
	}
	if err := httpComponent.Start(ctx); err != nil {
		return err
	}
	component.component = httpComponent
	return nil
}

// Stop 先关闭公开请求，再由外层lifecycle继续关闭Redis与MySQL。
func (component *publicRuntimeComponent) Stop(ctx context.Context) error {
	if component.component == nil {
		return nil
	}
	return component.component.Stop(ctx)
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
