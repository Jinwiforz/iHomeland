package session

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// recordingInvalidator 记录调用并可返回固定失败或等待context。
type recordingInvalidator struct {
	// calls 是并发安全调用次数。
	calls atomic.Int32
	// failure 模拟单个transport失败。
	failure error
	// block 使调用等待共享deadline。
	block bool
}

// Invalidate 记录调用且不读取身份内容。
func (invalidator *recordingInvalidator) Invalidate(ctx context.Context, _ Invalidation) error {
	invalidator.calls.Add(1)
	if invalidator.block {
		<-ctx.Done()
		return context.Cause(ctx)
	}
	return invalidator.failure
}

// TestStaticEndpointProviderRejectsChannelConfusion 验证 manifest 不能交换 channel 或接受 HTTPS。
func TestStaticEndpointProviderRejectsChannelConfusion(t *testing.T) {
	wss, _ := NewEndpoint(ChannelWSS, "localhost", 8443)
	tcp, _ := NewEndpoint(ChannelTLSTCP, "localhost", 8444)
	provider, err := NewStaticEndpointProvider(wss, tcp)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint, err := provider.EndpointFor(context.Background(), ChannelTLSTCP); err != nil || endpoint != tcp {
		t.Fatalf("TLS/TCP endpoint mismatch: %v", err)
	}
	if _, err := provider.EndpointFor(context.Background(), ChannelHTTPS); err == nil {
		t.Fatal("HTTPS unexpectedly resolved as a realtime endpoint")
	}
	if _, err := NewStaticEndpointProvider(tcp, wss); err == nil {
		t.Fatal("swapped endpoint channels were accepted")
	}
}

// TestNoActiveRealtimeConnectionsValidatesCommittedFact 验证空实现仍拒绝不完整失效通知。
func TestNoActiveRealtimeConnectionsValidatesCommittedFact(t *testing.T) {
	invalidator := NoActiveRealtimeConnections{}
	if err := invalidator.Invalidate(context.Background(), Invalidation{}); err == nil {
		t.Fatal("empty invalidation was accepted")
	}
	id, _ := NewSessionID("ses_runtimeAdapter")
	if err := invalidator.Invalidate(context.Background(), Invalidation{SessionID: id, Epoch: InitialEpoch, Reason: InvalidationReasonLogout}); err != nil {
		t.Fatal(err)
	}
}

// TestCompositeConnectionInvalidatorAttemptsEveryOwner 验证一个通道失败不会跳过另一个通道。
func TestCompositeConnectionInvalidatorAttemptsEveryOwner(t *testing.T) {
	t.Parallel()
	first := &recordingInvalidator{failure: errors.New("first failed")}
	second := new(recordingInvalidator)
	composite, err := NewCompositeConnectionInvalidator(first, second)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := NewSessionID("ses_compositeAdapter")
	err = composite.Invalidate(context.Background(), Invalidation{SessionID: id, Epoch: InitialEpoch, Reason: InvalidationReasonLogout})
	if err == nil || first.calls.Load() != 1 || second.calls.Load() != 1 {
		t.Fatalf("composite result=%v calls=%d/%d", err, first.calls.Load(), second.calls.Load())
	}
}

// TestCompositeConnectionInvalidatorSharesDeadline 验证全部owner观察同一context而不会串行倍增等待。
func TestCompositeConnectionInvalidatorSharesDeadline(t *testing.T) {
	t.Parallel()
	first, second := &recordingInvalidator{block: true}, &recordingInvalidator{block: true}
	composite, _ := NewCompositeConnectionInvalidator(first, second)
	id, _ := NewSessionID("ses_compositeDeadline")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := composite.Invalidate(ctx, Invalidation{SessionID: id, Epoch: InitialEpoch, Reason: InvalidationReasonLogout})
	if err == nil || time.Since(started) > 100*time.Millisecond || first.calls.Load() != 1 || second.calls.Load() != 1 {
		t.Fatalf("deadline result=%v elapsed=%s calls=%d/%d", err, time.Since(started), first.calls.Load(), second.calls.Load())
	}
}
