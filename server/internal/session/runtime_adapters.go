package session

import (
	"context"
	"errors"
)

// StaticEndpointProvider 公开启动时已验证且之后不可变的 realtime endpoint manifest。
//
// Provider 不读取请求 host、配置文件或环境变量；客户端只能选择 channel，不能覆盖
// endpoint 或 scope。它不表示对应 realtime listener 已经启动。
type StaticEndpointProvider struct {
	// wss 是 control channel 唯一受信 endpoint。
	wss Endpoint
	// tlsTCP 是 gameplay channel 唯一受信 endpoint。
	tlsTCP Endpoint
}

// NewStaticEndpointProvider 校验两个受支持 channel 均且仅登记一次。
func NewStaticEndpointProvider(wss Endpoint, tlsTCP Endpoint) (*StaticEndpointProvider, error) {
	if !wss.Valid() || wss.Channel() != ChannelWSS || !tlsTCP.Valid() || tlsTCP.Channel() != ChannelTLSTCP {
		return nil, errors.New("session endpoint manifest is incomplete")
	}
	return &StaticEndpointProvider{wss: wss, tlsTCP: tlsTCP}, nil
}

// EndpointFor 返回 channel 对应的启动期受信 endpoint。
func (provider *StaticEndpointProvider) EndpointFor(_ context.Context, channel Channel) (Endpoint, error) {
	if provider == nil {
		return Endpoint{}, errors.New("session endpoint manifest is unavailable")
	}
	switch channel {
	case ChannelWSS:
		return provider.wss, nil
	case ChannelTLSTCP:
		return provider.tlsTCP, nil
	default:
		return Endpoint{}, errors.New("session endpoint channel is unsupported")
	}
}

// NoActiveRealtimeConnections 表示当前 Composition Root 尚未构造任何 realtime connection。
//
// 该实现只适用于没有 WSS/TLS-TCP listener 与 connection registry 的进程图；它不保存
// 状态，也不能与未来的真实 invalidator 同时接线。
type NoActiveRealtimeConnections struct{}

// Invalidate 接受已提交失效事实；当前不存在可关闭连接，因此完成为空操作。
func (NoActiveRealtimeConnections) Invalidate(_ context.Context, invalidation Invalidation) error {
	if !invalidation.SessionID.Valid() || !invalidation.Epoch.Valid() {
		return errors.New("session invalidation is incomplete")
	}
	return nil
}
