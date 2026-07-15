package wscontrol

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
	controlv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/control/v1"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// newTestRegistry 构造带确定依赖的registry。
func newTestRegistry(t *testing.T, config Config) *Registry {
	t.Helper()
	clock := testClock{now: time.UnixMilli(100)}
	codec, err := NewCodec(contract.WSSPushCatalog(), config.FrameBytes, clock)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(config, codec, clock, &testIDs{}, testObserver{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = registry.Stop(ctx)
	})
	return registry
}

// TestRegistryIndexesAndPublishes 验证connection/session/player索引与target publisher。
func TestRegistryIndexesAndPublishes(t *testing.T) {
	registry := newTestRegistry(t, testConfig())
	for index := range 2 {
		reserved, err := registry.reserve("127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := registry.register(reserved, "ses_shared", "ply_shared", 1, newTestSocket()); err != nil {
			t.Fatal(err)
		}
		_ = index
	}
	result, err := registry.PublishSession(mustSessionID(t, "ses_shared"), 500, controlv1.MaintenancePush_builder{}.Build())
	if err != nil || result.Matched != 2 || result.Enqueued != 2 {
		t.Fatalf("session delivery mismatch: %+v %v", result, err)
	}
	result, err = registry.PublishPlayer("ply_shared", 502, controlv1.QueueStatusPush_builder{}.Build())
	if err != nil || result.Matched != 2 || result.Enqueued != 2 {
		t.Fatalf("player delivery mismatch: %+v %v", result, err)
	}
	if _, err := registry.PublishPlayer("missing", 500, controlv1.MaintenancePush_builder{}.Build()); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("missing target did not fail closed: %v", err)
	}
	if _, err := registry.PublishPlayer("ply_shared", 500, controlv1.QueueStatusPush_builder{}.Build()); err == nil {
		t.Fatal("wrong payload type did not fail closed")
	}
}

// TestRegistryReservationLimitsAndRollback 验证并发storm不会越过全局预算且release无泄漏。
func TestRegistryReservationLimitsAndRollback(t *testing.T) {
	config := testConfig()
	config.Policy.MaxConnections = 4
	config.Policy.MaxPerRemote = 4
	config.Policy.MaxPerSession = 4
	config.Policy.MaxPerPlayer = 4
	registry := newTestRegistry(t, config)
	var wg sync.WaitGroup
	results := make(chan *reservation, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reserved, err := registry.reserve("127.0.0.1")
			if err == nil {
				results <- reserved
			}
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for reserved := range results {
		count++
		reserved.Release()
		reserved.Release()
	}
	if count != 4 {
		t.Fatalf("reserved %d connections, want 4", count)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.reserved != 0 || registry.remotes["127.0.0.1"].reserved != 0 {
		t.Fatal("reservation rollback leaked budget")
	}
}

// TestRegistryBindsIdentityLimitsBeforeUpgrade 验证SessionID/PlayerID预算在socket注册前已被reservation占用。
func TestRegistryBindsIdentityLimitsBeforeUpgrade(t *testing.T) {
	config := testConfig()
	config.Policy.MaxPerSession = 1
	registry := newTestRegistry(t, config)
	first, err := registry.reserve("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.bind(first, "ses_bound", "ply_bound"); err != nil {
		t.Fatal(err)
	}
	second, err := registry.reserve("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.bind(second, "ses_bound", "ply_other"); !errors.Is(err, ErrConnectionLimit) {
		t.Fatalf("identity reservation exceeded pre-upgrade limit: %v", err)
	}
	second.Release()
	first.Release()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.reservedBySession) != 0 || len(registry.reservedByPlayer) != 0 {
		t.Fatal("identity reservation rollback leaked budget")
	}
}

// TestRegistryRejectsUnsafeConnectionIDMaterial 验证异常generator文本不会进入索引或日志。
func TestRegistryRejectsUnsafeConnectionIDMaterial(t *testing.T) {
	config := testConfig()
	clock := testClock{now: time.UnixMilli(100)}
	codec, err := NewCodec(contract.WSSPushCatalog(), config.FrameBytes, clock)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(config, codec, clock, testStaticIDs{value: "sensitive\nlog"}, testObserver{})
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := registry.reserve("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer reserved.Release()
	if _, err := registry.register(reserved, "ses_id", "ply_id", 1, newTestSocket()); err == nil {
		t.Fatal("unsafe connection ID material was accepted")
	}
}

// TestRegistryClosesSlowConsumerAndCleansIndexes 验证queue overflow关闭连接并解除反向索引。
func TestRegistryClosesSlowConsumerAndCleansIndexes(t *testing.T) {
	config := testConfig()
	config.Policy.QueueItems = 1
	registry := newTestRegistry(t, config)
	block := make(chan struct{})
	socket := newTestSocket()
	socket.writeBlock = block
	socket.writeSignal = make(chan struct{}, 1)
	reserved, _ := registry.reserve("127.0.0.1")
	connectionID, err := registry.register(reserved, "ses_slow", "ply_slow", 1, socket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.PublishConnection(connectionID, 500, controlv1.MaintenancePush_builder{}.Build()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-socket.writeSignal:
	case <-time.After(time.Second):
		t.Fatal("writer did not block")
	}
	_, _ = registry.PublishConnection(connectionID, 500, controlv1.MaintenancePush_builder{}.Build())
	result, _ := registry.PublishConnection(connectionID, 500, controlv1.MaintenancePush_builder{}.Build())
	if result.Closed != 1 {
		t.Fatalf("slow consumer was not closed: %+v", result)
	}
	close(block)
	deadline := time.After(time.Second)
	for {
		registry.mu.Lock()
		remaining := len(registry.connections) + len(registry.bySession) + len(registry.byPlayer)
		registry.mu.Unlock()
		if remaining == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("slow consumer left zombie indexes")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// TestRegistryInvalidationProtectsNewEpoch 验证旧epoch关闭而新epoch连接不受重试影响。
func TestRegistryInvalidationProtectsNewEpoch(t *testing.T) {
	registry := newTestRegistry(t, testConfig())
	oldSocket, newSocket := newTestSocket(), newTestSocket()
	for _, item := range []struct {
		epoch  uint64
		socket *testSocket
	}{{1, oldSocket}, {2, newSocket}} {
		reserved, _ := registry.reserve("127.0.0.1")
		if _, err := registry.register(reserved, "ses_epoch", "ply_epoch", item.epoch, item.socket); err != nil {
			t.Fatal(err)
		}
	}
	invalidation := session.Invalidation{SessionID: mustSessionID(t, "ses_epoch"), Epoch: session.Epoch(2), Reason: session.InvalidationReasonLogout}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := registry.Invalidate(ctx, invalidation); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldSocket.closed:
	case <-time.After(time.Second):
		t.Fatal("old epoch connection stayed open")
	}
	select {
	case <-newSocket.closed:
		t.Fatal("new epoch connection was closed")
	default:
	}
	if err := registry.Invalidate(ctx, invalidation); err != nil {
		t.Fatal(err)
	}
}

// TestRegistryInvalidationUsesOneNotificationWindow 验证多连接通知等待共享一个deadline而不是逐连接乘算。
func TestRegistryInvalidationUsesOneNotificationWindow(t *testing.T) {
	config := testConfig()
	config.Policy.WriteTimeout = time.Second
	config.Policy.CloseTimeout = 30 * time.Millisecond
	registry := newTestRegistry(t, config)
	writeBlock := make(chan struct{})
	defer close(writeBlock)
	for index := range 3 {
		reserved, err := registry.reserve("127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		socket := newTestSocket()
		socket.writeBlock = writeBlock
		if _, err := registry.register(reserved, "ses_notify", "ply_notify", 1, socket); err != nil {
			t.Fatal(err)
		}
		_ = index
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	err := registry.Invalidate(ctx, session.Invalidation{SessionID: mustSessionID(t, "ses_notify"), Epoch: 2, Reason: session.InvalidationReasonLogout})
	if err == nil {
		t.Fatal("blocked final notifications were reported as delivered")
	}
	if elapsed := time.Since(started); elapsed >= 150*time.Millisecond {
		t.Fatalf("invalidation multiplied the notification deadline: %s", elapsed)
	}
}

// TestRegistryStopClosesConnectionsInParallel 验证总关闭耗时不随连接数乘算closeTimeout。
func TestRegistryStopClosesConnectionsInParallel(t *testing.T) {
	config := testConfig()
	config.Policy.CloseTimeout = 30 * time.Millisecond
	registry := newTestRegistry(t, config)
	closeBlock := make(chan struct{})
	defer close(closeBlock)
	for index := range 8 {
		reserved, err := registry.reserve("127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		socket := newTestSocket()
		socket.closeBlock = closeBlock
		if _, err := registry.register(reserved, "ses_stop_"+fmt.Sprint(index), "ply_stop_"+fmt.Sprint(index), 1, socket); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	if err := registry.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("parallel shutdown exceeded one bounded close window: %s", elapsed)
	}
}

// TestRegistryStopWaitsForReservedHandshake 验证关闭快照不会漏掉已经取得预算但尚未完成upgrade的连接。
func TestRegistryStopWaitsForReservedHandshake(t *testing.T) {
	registry := newTestRegistry(t, testConfig())
	reserved, err := registry.reserve("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	stopResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		stopResult <- registry.Stop(ctx)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		registry.mu.Lock()
		draining := registry.draining
		registry.mu.Unlock()
		if draining {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("registry did not enter draining state")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-stopResult:
		t.Fatalf("shutdown returned before reserved handshake completed: %v", err)
	default:
	}
	if _, err := registry.PublishConnection("reserved", 500, controlv1.MaintenancePush_builder{}.Build()); !errors.Is(err, ErrRegistryStopped) {
		t.Fatalf("PublishConnection during draining error=%v", err)
	}

	socket := newTestSocket()
	if _, err := registry.register(reserved, "ses_handshake", "ply_handshake", 1, socket); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not complete after reserved handshake registered")
	}
	select {
	case <-socket.closed:
	default:
		t.Fatal("connection committed during draining was omitted from graceful close")
	}
}

// mustSessionID 构造测试session索引键。
func mustSessionID(t *testing.T, value string) session.SessionID {
	t.Helper()
	id, err := session.NewSessionID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
