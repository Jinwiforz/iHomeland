package wscontrol

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	controlv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"google.golang.org/protobuf/proto"
)

// DeliveryResult 汇总一次窄target投递，不暴露身份或连接集合。
type DeliveryResult struct {
	// Matched 是目标快照中的连接数。
	Matched int
	// Enqueued 是成功进入有界队列的连接数。
	Enqueued int
	// Closed 是因慢消费者被关闭的连接数。
	Closed int
}

// reservation 在ticket消费前占用全局与remote连接预算。
type reservation struct {
	// registry 是唯一可以commit/release本reservation的owner。
	registry *Registry
	// remoteKey 是规范remote identity，只在进程内存活。
	remoteKey string
	// sessionID和playerID在ticket消费后、upgrade前绑定，用于预留身份预算。
	sessionID string
	playerID  string
	// done 防止失败路径重复归还预算。
	done bool
}

// remoteState 合并pre-auth token bucket、连接计数与idle回收时间。
type remoteState struct {
	// tokens 是当前可消费握手预算。
	tokens float64
	// lastRefill 是token计算基点。
	lastRefill time.Time
	// lastSeen 是有界idle回收基点。
	lastSeen time.Time
	// reserved和active共同受MaxPerRemote约束。
	reserved int
	active   int
}

// Registry 线性化连接预算、索引、typed投递和Session失效。
type Registry struct {
	// config 是不可变运行策略。
	config Config
	// codec 为每连接sequence构造PUSH。
	codec *Codec
	// clock、ids和observer是无网络副作用依赖。
	clock    Clock
	ids      IDGenerator
	observer Observer

	// ctx/cancel 拥有全部连接生命周期。
	ctx    context.Context
	cancel context.CancelCauseFunc
	// mu 保护全部索引、remote state与停止状态。
	mu sync.Mutex
	// stopped 使draining后的所有入口fail closed。
	stopped bool
	// reserved 是尚未消费ticket/upgrade的全局预算。
	reserved int
	// connections 是ConnectionID主索引。
	connections map[string]*connection
	// bySession和byPlayer是只含ConnectionID的反向索引。
	bySession map[string]map[string]struct{}
	byPlayer  map[string]map[string]struct{}
	// reservedBySession和reservedByPlayer覆盖已认证但尚未完成upgrade的身份预算。
	reservedBySession map[string]int
	reservedByPlayer  map[string]int
	// remotes 是有界pre-auth与连接预算状态。
	remotes map[string]*remoteState
	// wg 等待所有已注册connection run退出。
	wg sync.WaitGroup
}

// NewRegistry 构造不启动listener或goroutine的连接owner。
func NewRegistry(config Config, codec *Codec, clock Clock, ids IDGenerator, observer Observer) (*Registry, error) {
	if err := config.validate(); err != nil || codec == nil || clock == nil || ids == nil || observer == nil {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("websocket control registry dependencies are incomplete")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	return &Registry{
		config: config, codec: codec, clock: clock, ids: ids, observer: observer, ctx: ctx, cancel: cancel,
		connections: make(map[string]*connection), bySession: make(map[string]map[string]struct{}), byPlayer: make(map[string]map[string]struct{}),
		reservedBySession: make(map[string]int), reservedByPlayer: make(map[string]int), remotes: make(map[string]*remoteState),
	}, nil
}

// Supervise 登记单一稳定owner任务，使外层取消也能触发registry有界关闭。
func (registry *Registry) Supervise(tasks TaskOwner) error {
	if tasks == nil {
		return errors.New("websocket control task owner is required")
	}
	return tasks.Go("connections", func(ctx context.Context) error {
		select {
		case <-registry.ctx.Done():
			return nil
		case <-ctx.Done():
			closeContext, cancel := context.WithTimeout(context.Background(), registry.config.Policy.CloseTimeout)
			defer cancel()
			return registry.Stop(closeContext)
		}
	})
}

// reserve 在ticket消费与upgrade前执行速率、全局和remote预算检查。
func (registry *Registry) reserve(remoteKey string) (*reservation, error) {
	if remoteKey == "" {
		return nil, ErrConnectionLimit
	}
	now := registry.clock.Now().UTC()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.stopped {
		return nil, ErrRegistryStopped
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
	registry.reserved++
	state.reserved++
	return &reservation{registry: registry, remoteKey: remoteKey}, nil
}

// Release 归还未commit reservation，允许所有握手失败路径统一defer调用。
func (reservation *reservation) Release() {
	if reservation == nil || reservation.registry == nil {
		return
	}
	registry := reservation.registry
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if reservation.done {
		return
	}
	reservation.done = true
	registry.releaseReserved(reservation)
}

// bindAuth 在ticket已经原子消费后、socket upgrade前预留SessionID与PlayerID连接预算。
func (registry *Registry) bindAuth(reservation *reservation, auth session.AuthContext) error {
	if reservation == nil || reservation.registry != registry || !auth.Valid() || auth.Channel() != session.ChannelWSS || !auth.HasScope(session.ScopeControl) {
		return errors.New("websocket control reservation binding is invalid")
	}
	return registry.bind(reservation, auth.SessionID().String(), auth.Principal().PlayerID())
}

// bind 接受已验证身份字符串，便于并发预算测试不伪造只能由Session owner构造的AuthContext。
func (registry *Registry) bind(reservation *reservation, sessionID string, playerID string) error {
	if reservation == nil || reservation.registry != registry || sessionID == "" || playerID == "" {
		return errors.New("websocket control reservation identity is invalid")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if reservation.done || registry.stopped {
		return ErrRegistryStopped
	}
	if reservation.sessionID != "" || reservation.playerID != "" {
		if reservation.sessionID == sessionID && reservation.playerID == playerID {
			return nil
		}
		return errors.New("websocket control reservation identity changed")
	}
	if len(registry.bySession[sessionID])+registry.reservedBySession[sessionID] >= registry.config.Policy.MaxPerSession ||
		len(registry.byPlayer[playerID])+registry.reservedByPlayer[playerID] >= registry.config.Policy.MaxPerPlayer {
		return ErrConnectionLimit
	}
	reservation.sessionID = sessionID
	reservation.playerID = playerID
	registry.reservedBySession[sessionID]++
	registry.reservedByPlayer[playerID]++
	return nil
}

// registerAuth 把已消费ticket与已upgrade socket原子加入全部索引并启动I/O owner。
func (registry *Registry) registerAuth(reservation *reservation, auth session.AuthContext, socket webSocket) (string, error) {
	if reservation == nil || reservation.registry != registry || socket == nil || !auth.Valid() || auth.Channel() != session.ChannelWSS || !auth.HasScope(session.ScopeControl) {
		return "", errors.New("websocket control registration is invalid")
	}
	return registry.register(reservation, auth.SessionID().String(), auth.Principal().PlayerID(), uint64(auth.Epoch()), socket)
}

// register 接受已经由Register验证并提取的身份摘要，便于并发索引测试不伪造AuthContext。
func (registry *Registry) register(reservation *reservation, sessionID string, playerID string, epoch uint64, socket webSocket) (string, error) {
	if reservation == nil || reservation.registry != registry || sessionID == "" || playerID == "" || epoch == 0 || socket == nil {
		return "", errors.New("websocket control registration identity is invalid")
	}
	if err := registry.bind(reservation, sessionID, playerID); err != nil {
		return "", err
	}
	material, err := registry.ids.NewID()
	if err != nil {
		return "", fmt.Errorf("generate websocket control connection id: %w", err)
	}
	if !validConnectionIDMaterial(material) {
		return "", errors.New("websocket control connection id material is invalid")
	}
	connectionID := "con_" + material
	registry.mu.Lock()
	if reservation.done || registry.stopped || reservation.sessionID != sessionID || reservation.playerID != playerID {
		registry.mu.Unlock()
		return "", ErrConnectionLimit
	}
	if _, exists := registry.connections[connectionID]; exists {
		registry.mu.Unlock()
		return "", errors.New("websocket control connection id collision")
	}
	reservation.done = true
	registry.releaseReserved(reservation)
	state := registry.remotes[reservation.remoteKey]
	state.active++
	entry := newConnection(connectionID, sessionID, playerID, epoch, reservation.remoteKey, socket, registry.codec, registry.config, registry.observer)
	registry.connections[connectionID] = entry
	addIndex(registry.bySession, sessionID, connectionID)
	addIndex(registry.byPlayer, playerID, connectionID)
	active := len(registry.connections)
	registry.wg.Add(1)
	registry.mu.Unlock()
	registry.observer.SetWSSConnections(active)
	registry.config.Logger.Info("websocket control connection registered", "operation", "register", "connection_id", connectionID, "outcome", "accepted")
	go registry.runConnection(entry)
	return connectionID, nil
}

// runConnection 确保任意I/O退出都解除所有反向索引。
func (registry *Registry) runConnection(entry *connection) {
	defer registry.wg.Done()
	entry.run(registry.ctx)
	registry.remove(entry.id)
}

// PublishConnection 向单个受信ConnectionID投递typed PUSH。
func (registry *Registry) PublishConnection(connectionID string, messageID uint32, payload proto.Message) (DeliveryResult, error) {
	if _, err := registry.codec.Encode(messageID, payload, 1); err != nil {
		return DeliveryResult{}, err
	}
	registry.mu.Lock()
	entry := registry.connections[connectionID]
	stopped := registry.stopped
	registry.mu.Unlock()
	if stopped {
		return DeliveryResult{}, ErrRegistryStopped
	}
	if entry == nil {
		return DeliveryResult{}, ErrConnectionNotFound
	}
	return registry.publish([]*connection{entry}, messageID, payload), nil
}

// PublishSession 向单个SessionID当前全部连接投递typed PUSH。
func (registry *Registry) PublishSession(sessionID session.SessionID, messageID uint32, payload proto.Message) (DeliveryResult, error) {
	if !sessionID.Valid() {
		return DeliveryResult{}, ErrConnectionNotFound
	}
	if _, err := registry.codec.Encode(messageID, payload, 1); err != nil {
		return DeliveryResult{}, err
	}
	entries, stopped := registry.snapshot(registry.bySession, sessionID.String())
	if stopped {
		return DeliveryResult{}, ErrRegistryStopped
	}
	if len(entries) == 0 {
		return DeliveryResult{}, ErrConnectionNotFound
	}
	return registry.publish(entries, messageID, payload), nil
}

// PublishPlayer 向认证PlayerID的全部连接投递typed PUSH。
func (registry *Registry) PublishPlayer(playerID string, messageID uint32, payload proto.Message) (DeliveryResult, error) {
	if playerID == "" {
		return DeliveryResult{}, ErrConnectionNotFound
	}
	if _, err := registry.codec.Encode(messageID, payload, 1); err != nil {
		return DeliveryResult{}, err
	}
	entries, stopped := registry.snapshot(registry.byPlayer, playerID)
	if stopped {
		return DeliveryResult{}, ErrRegistryStopped
	}
	if len(entries) == 0 {
		return DeliveryResult{}, ErrConnectionNotFound
	}
	return registry.publish(entries, messageID, payload), nil
}

// Invalidate 实现Session提交后ConnectionInvalidator；通知失败也不会保留旧epoch连接。
func (registry *Registry) Invalidate(ctx context.Context, invalidation session.Invalidation) error {
	if !invalidation.SessionID.Valid() || !invalidation.Epoch.Valid() || invalidation.Reason == session.InvalidationReasonUnspecified {
		return errors.New("websocket control invalidation is invalid")
	}
	entries, stopped := registry.snapshot(registry.bySession, invalidation.SessionID.String())
	if stopped || len(entries) == 0 {
		registry.observer.ObserveWSSInvalidation("no_active_connection")
		return nil
	}
	messageID, payload, reason := invalidationPush(invalidation)
	targets := make([]*connection, 0, len(entries))
	completions := make([]<-chan error, 0, len(entries))
	var notificationErr error
	for _, entry := range entries {
		if entry.epoch >= uint64(invalidation.Epoch) {
			continue
		}
		targets = append(targets, entry)
		completion := make(chan error, 1)
		if err := entry.enqueue(messageID, payload, completion); err == nil {
			completions = append(completions, completion)
		} else if !errors.Is(err, ErrQueueClosed) {
			notificationErr = errors.Join(notificationErr, err)
		}
	}
	if len(targets) == 0 {
		registry.observer.ObserveWSSInvalidation("no_active_connection")
		return nil
	}
	notificationTimeout := registry.config.Policy.CloseTimeout
	if registry.config.Policy.WriteTimeout < notificationTimeout {
		notificationTimeout = registry.config.Policy.WriteTimeout
	}
	notificationContext, cancel := context.WithTimeout(ctx, notificationTimeout)

waitForNotifications:
	for _, completion := range completions {
		select {
		case writeErr := <-completion:
			if writeErr != nil {
				notificationErr = errors.Join(notificationErr, writeErr)
			}
		case <-notificationContext.Done():
			notificationErr = errors.Join(notificationErr, context.Cause(notificationContext))
			break waitForNotifications
		}
	}
	cancel()
	for _, entry := range targets {
		entry.stop(websocket.StatusPolicyViolation, reason)
	}
	if notificationErr != nil {
		registry.observer.ObserveWSSInvalidation("notify_failed_closed")
		return fmt.Errorf("websocket control invalidation notification was not delivered: %w", notificationErr)
	}
	registry.observer.ObserveWSSInvalidation("closed")
	return nil
}

// Stop 先拒绝新注册和投递，再graceful close并等待全部连接退出。
func (registry *Registry) Stop(ctx context.Context) error {
	registry.mu.Lock()
	if !registry.stopped {
		registry.stopped = true
	}
	entries := make([]*connection, 0, len(registry.connections))
	for _, entry := range registry.connections {
		entries = append(entries, entry)
	}
	registry.mu.Unlock()
	for _, entry := range entries {
		entry.stop(websocket.StatusGoingAway, "server draining")
	}
	done := make(chan struct{})
	go func() { registry.wg.Wait(); close(done) }()
	select {
	case <-done:
		registry.cancel(errors.New("websocket control registry stopped"))
		return nil
	case <-ctx.Done():
		registry.cancel(errors.New("websocket control registry stopped"))
		for _, entry := range entries {
			// CloseNow可能与正在进行的graceful Close共享内部owner，必须避免逐连接同步放大总deadline。
			go func(entry *connection) { _ = entry.socket.CloseNow() }(entry)
		}
		return fmt.Errorf("wait for websocket control connections: %w", context.Cause(ctx))
	}
}

// publish 对快照逐连接入队，并关闭queue overflow连接。
func (registry *Registry) publish(entries []*connection, messageID uint32, payload proto.Message) DeliveryResult {
	result := DeliveryResult{Matched: len(entries)}
	for _, entry := range entries {
		if err := entry.enqueue(messageID, payload, nil); err != nil {
			registry.observer.ObserveWSSPush(messageID, "rejected", 0)
			if errors.Is(err, ErrQueueFull) {
				result.Closed++
				entry.stop(websocket.StatusPolicyViolation, "slow consumer")
			}
			continue
		}
		result.Enqueued++
	}
	return result
}

// snapshot 复制目标连接引用，publisher离开锁后不直接访问索引。
func (registry *Registry) snapshot(index map[string]map[string]struct{}, key string) ([]*connection, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entries := make([]*connection, 0, len(index[key]))
	for id := range index[key] {
		if entry := registry.connections[id]; entry != nil {
			entries = append(entries, entry)
		}
	}
	return entries, registry.stopped
}

// remove 线性化删除主索引、反向索引和remote active预算。
func (registry *Registry) remove(connectionID string) {
	registry.mu.Lock()
	entry := registry.connections[connectionID]
	if entry == nil {
		registry.mu.Unlock()
		return
	}
	delete(registry.connections, connectionID)
	removeIndex(registry.bySession, entry.sessionID, connectionID)
	removeIndex(registry.byPlayer, entry.playerID, connectionID)
	if state := registry.remotes[entry.remoteKey]; state != nil && state.active > 0 {
		state.active--
		state.lastSeen = registry.clock.Now().UTC()
	}
	active := len(registry.connections)
	registry.mu.Unlock()
	registry.observer.SetWSSConnections(active)
	registry.config.Logger.Info("websocket control connection removed", "operation", "remove", "connection_id", connectionID, "outcome", "closed")
}

// evictIdleRemotes 只回收没有active/reserved连接且超过TTL的状态。
func (registry *Registry) evictIdleRemotes(now time.Time) {
	for key, state := range registry.remotes {
		if state.active == 0 && state.reserved == 0 && now.Sub(state.lastSeen) >= registry.config.Policy.RemoteIdleTTL {
			delete(registry.remotes, key)
		}
	}
}

// refill 按持续速率补充token且不超过Burst。
func (registry *Registry) refill(state *remoteState, now time.Time) {
	elapsed := now.Sub(state.lastRefill)
	if elapsed <= 0 {
		return
	}
	rate := float64(registry.config.Policy.PreAuthRate.Requests) / registry.config.Policy.PreAuthRate.Window.Seconds()
	state.tokens += elapsed.Seconds() * rate
	if maximum := float64(registry.config.Policy.PreAuthRate.Burst); state.tokens > maximum {
		state.tokens = maximum
	}
	state.lastRefill = now
}

// releaseReserved 归还全局、remote与已绑定身份的全部reservation预算；调用方必须持有mu。
func (registry *Registry) releaseReserved(reservation *reservation) {
	if registry.reserved > 0 {
		registry.reserved--
	}
	if state := registry.remotes[reservation.remoteKey]; state != nil && state.reserved > 0 {
		state.reserved--
		state.lastSeen = registry.clock.Now().UTC()
	}
	if reservation.sessionID != "" {
		decrementReservation(registry.reservedBySession, reservation.sessionID)
	}
	if reservation.playerID != "" {
		decrementReservation(registry.reservedByPlayer, reservation.playerID)
	}
}

// invalidationPush 把安全Session reason映射到已登记generated payload。
func invalidationPush(invalidation session.Invalidation) (uint32, proto.Message, string) {
	epoch := uint64(invalidation.Epoch)
	if invalidation.Reason == session.InvalidationReasonForcedLogout || invalidation.Reason == session.InvalidationReasonPrincipalBan {
		reason := "session.forced_logout"
		return 501, controlv1.ForcedLogoutPush_builder{ReasonKey: proto.String(reason), SessionEpoch: proto.Uint64(epoch)}.Build(), "forced logout"
	}
	reason := "session.invalidated"
	if invalidation.Reason == session.InvalidationReasonRefreshReplay {
		reason = "session.refresh_replay"
	}
	return 504, controlv1.SessionInvalidatedPush_builder{SessionEpoch: proto.Uint64(epoch), ReasonKey: proto.String(reason)}.Build(), "session invalidated"
}

// addIndex 把ConnectionID加入反向索引。
func addIndex(index map[string]map[string]struct{}, key string, connectionID string) {
	values := index[key]
	if values == nil {
		values = make(map[string]struct{})
		index[key] = values
	}
	values[connectionID] = struct{}{}
}

// removeIndex 删除ConnectionID并回收空集合。
func removeIndex(index map[string]map[string]struct{}, key string, connectionID string) {
	values := index[key]
	delete(values, connectionID)
	if len(values) == 0 {
		delete(index, key)
	}
}

// decrementReservation 归还一个身份预算并删除零值键，避免已完成握手扩大map。
func decrementReservation(index map[string]int, key string) {
	if index[key] <= 1 {
		delete(index, key)
		return
	}
	index[key]--
}

// validConnectionIDMaterial 防止异常ID generator把非规范或潜在敏感文本带入索引与日志。
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
