package tcpgameplay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
)

// ConnectionState 是transport拥有的封闭连接生命周期，不代表领域事实。
type ConnectionState uint8

const (
	// ConnectionStatePending 等待首个匹配Join或Reconnect command完成。
	ConnectionStatePending ConnectionState = iota + 1
	// ConnectionStateActive 允许读取绑定target并执行授权mutation。
	ConnectionStateActive
	// ConnectionStateReturning 已投递safe-return且禁止旧target新mutation。
	ConnectionStateReturning
	// ConnectionStateClosing 已停止接收新业务或发送任务。
	ConnectionStateClosing
)

// String 返回metrics使用的稳定低基数状态。
func (state ConnectionState) String() string {
	switch state {
	case ConnectionStatePending:
		return "pending"
	case ConnectionStateActive:
		return "active"
	case ConnectionStateReturning:
		return "returning"
	case ConnectionStateClosing:
		return "closing"
	default:
		return "invalid"
	}
}

// reservation 在credential消费前占用ConnectionID、global与remote预算。
type reservation struct {
	// registry 是唯一能够commit或release本reservation的owner。
	registry *Registry
	// connectionID 在ticket消费前由CSPRNG生成，也派生稳定admission consume identity。
	connectionID string
	// remoteKey 只在进程内存活，不进入日志或metrics。
	remoteKey string
	// done 防止失败路径重复归还预算。
	done bool
}

// ConnectionID 返回服务端生成的非credential连接标识。
func (value *reservation) ConnectionID() string {
	if value == nil {
		return ""
	}
	return value.connectionID
}

// ConsumeID 返回与ConnectionID一一对应的admission消费身份。
func (value *reservation) ConsumeID() (worldadmission.ConsumeID, error) {
	if value == nil {
		return worldadmission.ConsumeID{}, errors.New("tcp gameplay reservation is nil")
	}
	return worldadmission.NewConsumeID(value.connectionID)
}

// Release 归还未commit reservation；所有握手失败路径都应defer调用。
func (value *reservation) Release() {
	if value == nil || value.registry == nil {
		return
	}
	registry := value.registry
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if value.done {
		return
	}
	value.done = true
	registry.reserved--
	if state := registry.remotes[value.remoteKey]; state != nil {
		state.reserved--
		state.lastSeen = registry.clock.Now().UTC()
	}
}

// remoteState 合并pre-auth token bucket、连接计数和idle回收时间。
type remoteState struct {
	// tokens 是当前可消费preface预算。
	tokens float64
	// lastRefill 是token补充计算基点。
	lastRefill time.Time
	// lastSeen 是有界idle回收基点。
	lastSeen time.Time
	// reserved 是尚未完成认证的连接预算。
	reserved int
	// active 是已提交认证的连接预算。
	active int
}

// connection 保存只读认证摘要、唯一socket、队列和transport状态。
type connection struct {
	// id 是服务端CSPRNG连接身份。
	id string
	// remoteKey 只用于进程内预算回收。
	remoteKey string
	// auth 来自 production Session service 且永不由 payload 覆盖。
	auth session.AuthContext
	// qualification 来自 admission service 并冻结完整 binding。
	qualification worldadmission.Qualification
	// sessionID 只用于 Session 失效反向索引。
	sessionID string
	// playerID 只用于玩家连接反向索引。
	playerID string
	// worldID 只用于 PersonalWorld 连接反向索引。
	worldID string
	// visitID 只用于 VisitSession 连接反向索引。
	visitID string
	// socket 由恰好一个reader和serialized writer共同拥有。
	socket net.Conn
	// queue 隔离publisher/dispatcher与阻塞写操作。
	queue *sendQueue
	// codec 在sequence临界区内构造不可变frame。
	codec *Codec
	// observer 只接收队列和连接的低基数结果。
	observer Observer

	// mu 线性化state、sequence与route rate。
	mu sync.Mutex
	// state 是transport生命周期，不可替代VisitSession事实。
	state ConnectionState
	// nextServerSequence 是下一条成功入队S2C消息序号。
	nextServerSequence uint64
	// nextClientSequence 是下一条允许解码的C2S消息序号。
	nextClientSequence uint64
	// routeRates 按registry rate policy名称保存单连接有界token bucket。
	routeRates map[string]*routeRateState
	// consecutiveRateRejections 记录连续限流次数；成功取得预算后归零。
	consecutiveRateRejections int
	// pendingDeadline 限制JOIN/RECONNECT首个匹配command完成时间。
	pendingDeadline time.Time
	// closeOnce 使并发失效、peer close与shutdown只关闭一次。
	closeOnce sync.Once
	// doneOnce 统一I/O owner与启动前拒绝路径的完成信号，避免失效竞态重复关闭channel。
	doneOnce sync.Once
	// done 在I/O owner完成全部资源清理后关闭。
	done chan struct{}
	// closeClass 由invalidation、safe-return或draining覆盖默认unexpected语义。
	closeClass CloseClass
	// dispatching 表示当前 connection 正在执行一条 command。
	dispatching bool
	// pendingPushes 暂存必须排在当前 response 之后的 PUSH。
	pendingPushes []pendingDispatchPush
	// pendingPushBytes 是暂存 PUSH 的估算字节总量。
	pendingPushBytes int
	// started 由 registry.mu 保护，表示 I/O owner 已启动。
	started bool
	// startRejected 封闭 commit 到 I/O owner 启动之间的失效窗口。
	startRejected bool
}

// signalDone 幂等发布连接owner已经退出或确定不会启动。
func (entry *connection) signalDone() { entry.doneOnce.Do(func() { close(entry.done) }) }

// State 返回当前transport状态快照。
func (entry *connection) State() ConnectionState {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.state
}

// Binding 返回连接握手冻结的Qualification binding值副本。
func (entry *connection) Binding() worldadmission.Binding { return entry.qualification.Binding() }

// Registry 线性化连接预算、反向索引、状态迁移和关闭清理。
type Registry struct {
	// config 是不可变运行策略。
	config Config
	// codec 为每连接sequence编码response与push。
	codec *Codec
	// clock 提供 token bucket 与 idle 回收时间。
	clock Clock
	// ids 生成 CSPRNG ConnectionID。
	ids IDGenerator
	// observer 只接收低基数 transport 结果。
	observer Observer
	// lifecycle 在startup绑定，只观察受信connection view。
	lifecycle LifecycleSink

	// mu 保护全部索引、remote state与停止状态。
	mu sync.Mutex
	// stopped 使draining后的所有入口fail closed。
	stopped bool
	// reserved 是尚未提交双credential认证的全局预算。
	reserved int
	// connections 是ConnectionID主索引。
	connections map[string]*connection
	// bySession 只保存 ConnectionID，不复制 socket 或 credential。
	bySession map[string]map[string]struct{}
	// byPlayer 按 PlayerID 保存 ConnectionID 集合。
	byPlayer map[string]map[string]struct{}
	// byWorld 按 PersonalWorldID 保存 ConnectionID 集合。
	byWorld map[string]map[string]struct{}
	// byVisit 按 VisitSessionID 保存 ConnectionID 集合。
	byVisit map[string]map[string]struct{}
	// remotes 是有界pre-auth与连接预算状态。
	remotes map[string]*remoteState
	// wg 等待已启动连接owner退出。
	wg sync.WaitGroup
}

// BindLifecycleSink 在首条连接前一次性绑定application lifecycle consumer。
func (registry *Registry) BindLifecycleSink(sink LifecycleSink) error {
	if registry == nil || sink == nil {
		return errors.New("tcp gameplay lifecycle sink is invalid")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.lifecycle != nil || registry.reserved != 0 || len(registry.connections) != 0 || registry.stopped {
		return errors.New("tcp gameplay lifecycle sink cannot be rebound")
	}
	registry.lifecycle = sink
	return nil
}

// routeRateState 是单连接固定policy token bucket，不包含identity或动态label。
type routeRateState struct {
	// tokens 是当前可消费操作预算。
	tokens float64
	// lastRefill 是token补充基点。
	lastRefill time.Time
}

// NewRegistry 构造不启动listener或goroutine的连接owner。
func NewRegistry(config Config, codec *Codec, clock Clock, ids IDGenerator, observer Observer) (*Registry, error) {
	if codec == nil || clock == nil || ids == nil || observer == nil || config.Logger == nil {
		return nil, errors.New("tcp gameplay registry dependencies are incomplete")
	}
	if err := config.Policy.Validate(config.FrameBytes, "", "", !config.AllowPlaintext, true); err != nil {
		return nil, err
	}
	return &Registry{
		config: config, codec: codec, clock: clock, ids: ids, observer: observer,
		connections: make(map[string]*connection), bySession: make(map[string]map[string]struct{}), byPlayer: make(map[string]map[string]struct{}),
		byWorld: make(map[string]map[string]struct{}), byVisit: make(map[string]map[string]struct{}), remotes: make(map[string]*remoteState),
	}, nil
}

// Reserve 在读取preface前执行速率、全局、remote与CSPRNG ConnectionID检查。
func (registry *Registry) Reserve(remoteKey string) (*reservation, error) {
	if remoteKey == "" {
		return nil, ErrConnectionLimit
	}
	material, err := registry.ids.NewID()
	if err != nil || !validConnectionIDMaterial(material) {
		return nil, errors.New("generate tcp gameplay connection id")
	}
	connectionID := "tcp_" + material
	now := registry.clock.Now().UTC()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.stopped {
		return nil, ErrQueueClosed
	}
	registry.evictIdleRemotes(now)
	state := registry.remotes[remoteKey]
	if state == nil {
		if len(registry.remotes) >= registry.config.Policy.MaxRemoteEntries {
			return nil, ErrConnectionLimit
		}
		state = &remoteState{tokens: float64(registry.config.Policy.PreAuthRate.Burst), lastRefill: now, lastSeen: now}
		registry.remotes[remoteKey] = state
	}
	registry.refill(state, now)
	state.lastSeen = now
	if state.tokens < 1 {
		return nil, ErrRateLimited
	}
	state.tokens--
	if registry.reserved+len(registry.connections) >= registry.config.Policy.MaxConnections || state.reserved+state.active >= registry.config.Policy.MaxPerRemote {
		return nil, ErrConnectionLimit
	}
	if _, exists := registry.connections[connectionID]; exists {
		return nil, errors.New("tcp gameplay connection id collision")
	}
	registry.reserved++
	state.reserved++
	return &reservation{registry: registry, connectionID: connectionID, remoteKey: remoteKey}, nil
}

// Commit 把成功双credential握手与socket原子加入全部受信索引。
func (registry *Registry) Commit(reserved *reservation, auth session.AuthContext, qualification worldadmission.Qualification, socket net.Conn) (*connection, error) {
	if reserved == nil || reserved.registry != registry || socket == nil || !auth.Valid() || auth.Channel() != session.ChannelTLSTCP || !auth.HasScope(session.ScopeGameplay) || !qualification.Valid() {
		return nil, errors.New("tcp gameplay registration is invalid")
	}
	binding := qualification.Binding()
	if binding.SessionID() != auth.SessionID() || uint64(binding.Epoch()) != uint64(auth.Epoch()) || binding.PlayerID().String() != auth.Principal().PlayerID() || binding.Endpoint().Channel() != session.ChannelTLSTCP {
		return nil, errors.New("tcp gameplay authentication binding mismatch")
	}
	state := ConnectionStateActive
	if binding.Purpose() == worldadmission.PurposeJoin || binding.Purpose() == worldadmission.PurposeReconnect {
		state = ConnectionStatePending
	} else if binding.Purpose() != worldadmission.PurposeOwnWorld {
		return nil, errors.New("tcp gameplay admission purpose is invalid")
	}
	sessionID, playerID, worldID := auth.SessionID().String(), auth.Principal().PlayerID(), binding.WorldID().String()
	visitID := ""
	if binding.VisitSessionID().Valid() {
		visitID = binding.VisitSessionID().Value()
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if reserved.done || registry.stopped {
		return nil, ErrConnectionLimit
	}
	if len(registry.bySession[sessionID]) >= registry.config.Policy.MaxPerSession || len(registry.byPlayer[playerID]) >= registry.config.Policy.MaxPerPlayer ||
		len(registry.byWorld[worldID]) >= registry.config.Policy.MaxPerTarget || (visitID != "" && len(registry.byVisit[visitID]) >= registry.config.Policy.MaxPerTarget) {
		return nil, ErrConnectionLimit
	}
	if _, exists := registry.connections[reserved.connectionID]; exists {
		return nil, errors.New("tcp gameplay connection id collision")
	}
	reserved.done = true
	registry.reserved--
	remote := registry.remotes[reserved.remoteKey]
	remote.reserved--
	remote.active++
	entry := &connection{
		id: reserved.connectionID, remoteKey: reserved.remoteKey, auth: auth, qualification: qualification,
		sessionID: sessionID, playerID: playerID, worldID: worldID, visitID: visitID, socket: socket,
		queue: newSendQueue(registry.config.Policy.QueueItems, registry.config.Policy.QueueBytes), state: state,
		codec:    registry.codec,
		observer: registry.observer, closeClass: CloseClassUnexpected,
		nextServerSequence: 1, nextClientSequence: 1, routeRates: make(map[string]*routeRateState), pendingDeadline: binding.ExpiresAt(), done: make(chan struct{}),
	}
	registry.connections[entry.id] = entry
	addConnectionIndex(registry.bySession, sessionID, entry.id)
	addConnectionIndex(registry.byPlayer, playerID, entry.id)
	addConnectionIndex(registry.byWorld, worldID, entry.id)
	if visitID != "" {
		addConnectionIndex(registry.byVisit, visitID, entry.id)
	}
	registry.observer.SetTCPConnections(state.String(), registry.countStateLocked(state))
	registry.config.Logger.Info("tcp gameplay connection registered", "operation", "register", "connection_id", entry.id, "outcome", state.String())
	return entry, nil
}

// HasConnection 报告ConnectionID是否仍在当前registry主索引且未进入closing。
func (registry *Registry) HasConnection(connectionID string) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry := registry.connections[connectionID]
	return entry != nil && entry.State() != ConnectionStateClosing
}

// ActivatePending 只允许与preface完整相等的Join/Reconnect资格在application成功后转active。
func (registry *Registry) ActivatePending(connectionID string, qualification worldadmission.Qualification) error {
	if !qualification.Valid() {
		return errors.New("tcp gameplay activation qualification is invalid")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry := registry.connections[connectionID]
	if entry == nil {
		return ErrConnectionNotFound
	}
	entry.mu.Lock()
	if entry.state == ConnectionStateActive && entry.qualification.Binding().Equal(qualification.Binding()) {
		entry.mu.Unlock()
		return nil
	}
	if entry.state != ConnectionStatePending || !entry.qualification.Binding().Equal(qualification.Binding()) || !registry.clock.Now().UTC().Before(entry.pendingDeadline) {
		entry.mu.Unlock()
		return errors.New("tcp gameplay pending activation rejected")
	}
	entry.state = ConnectionStateActive
	entry.mu.Unlock()
	registry.observer.SetTCPConnections("pending", registry.countStateLocked(ConnectionStatePending))
	registry.observer.SetTCPConnections("active", registry.countStateLocked(ConnectionStateActive))
	return nil
}

// Entry 返回connection主索引快照指针，仅供同包dispatcher与publisher使用。
func (registry *Registry) Entry(connectionID string) (*connection, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.stopped {
		return nil, ErrQueueClosed
	}
	entry := registry.connections[connectionID]
	if entry == nil {
		return nil, ErrConnectionNotFound
	}
	return entry, nil
}

// Remove 解除全部索引、remote计数并释放队列与socket引用。
func (registry *Registry) Remove(connectionID string) {
	registry.mu.Lock()
	entry := registry.connections[connectionID]
	if entry == nil {
		registry.mu.Unlock()
		return
	}
	delete(registry.connections, connectionID)
	removeConnectionIndex(registry.bySession, entry.sessionID, connectionID)
	removeConnectionIndex(registry.byPlayer, entry.playerID, connectionID)
	removeConnectionIndex(registry.byWorld, entry.worldID, connectionID)
	if entry.visitID != "" {
		removeConnectionIndex(registry.byVisit, entry.visitID, connectionID)
	}
	if remote := registry.remotes[entry.remoteKey]; remote != nil {
		remote.active--
		remote.lastSeen = registry.clock.Now().UTC()
	}
	state := entry.State()
	started := entry.started
	active := registry.countStateLocked(state)
	registry.mu.Unlock()
	entry.closeOnce.Do(func() { _ = entry.socket.Close() })
	entry.queue.close()
	entry.queue.release()
	if !started {
		entry.signalDone()
	}
	registry.observer.SetTCPConnections(state.String(), active)
}

// Stop 拒绝新握手并在共享deadline内并行关闭全部socket和队列。
func (registry *Registry) Stop(ctx context.Context) error {
	registry.mu.Lock()
	if registry.stopped {
		registry.mu.Unlock()
		select {
		case <-waitGroupChannel(&registry.wg):
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	registry.stopped = true
	entries := make([]*connection, 0, len(registry.connections))
	for _, entry := range registry.connections {
		entries = append(entries, entry)
	}
	registry.mu.Unlock()
	for _, entry := range entries {
		entry.mu.Lock()
		entry.closeClass = CloseClassDraining
		entry.mu.Unlock()
		entry.queue.close()
		entry.closeOnce.Do(func() { _ = entry.socket.Close() })
	}
	select {
	case <-waitGroupChannel(&registry.wg):
		for _, entry := range entries {
			registry.Remove(entry.id)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("tcp gameplay shutdown: %w", context.Cause(ctx))
	}
}

// refill 按经过时间补充token且不超过burst。
func (registry *Registry) refill(state *remoteState, now time.Time) {
	elapsed := now.Sub(state.lastRefill)
	if elapsed <= 0 {
		return
	}
	rate := float64(registry.config.Policy.PreAuthRate.Requests) / registry.config.Policy.PreAuthRate.Window.Seconds()
	state.tokens += elapsed.Seconds() * rate
	if burst := float64(registry.config.Policy.PreAuthRate.Burst); state.tokens > burst {
		state.tokens = burst
	}
	state.lastRefill = now
}

// evictIdleRemotes 只回收没有reservation或active连接的过期entry。
func (registry *Registry) evictIdleRemotes(now time.Time) {
	for key, state := range registry.remotes {
		if state.reserved == 0 && state.active == 0 && !now.Before(state.lastSeen.Add(registry.config.Policy.RemoteIdleTTL)) {
			delete(registry.remotes, key)
		}
	}
}

// countStateLocked 统计指定transport状态；调用方必须持有registry.mu。
func (registry *Registry) countStateLocked(state ConnectionState) int {
	count := 0
	for _, entry := range registry.connections {
		if entry.State() == state {
			count++
		}
	}
	return count
}

// addConnectionIndex 把ConnectionID加入单个反向索引。
func addConnectionIndex(index map[string]map[string]struct{}, key string, connectionID string) {
	values := index[key]
	if values == nil {
		values = make(map[string]struct{})
		index[key] = values
	}
	values[connectionID] = struct{}{}
}

// removeConnectionIndex 删除ConnectionID并回收空集合。
func removeConnectionIndex(index map[string]map[string]struct{}, key string, connectionID string) {
	values := index[key]
	delete(values, connectionID)
	if len(values) == 0 {
		delete(index, key)
	}
}

// validConnectionIDMaterial 防止异常generator把敏感或非规范文本带入日志与consume identity。
func validConnectionIDMaterial(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range []byte(value) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// waitGroupChannel 把WaitGroup完成投影为可与context一起等待的只读通道。
func waitGroupChannel(group *sync.WaitGroup) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	return done
}
