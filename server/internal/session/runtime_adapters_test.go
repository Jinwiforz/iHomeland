package session

import (
	"context"
	"testing"
)

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
