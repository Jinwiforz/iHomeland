package session

import (
	"context"
	"errors"
	"sync"
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

// CompositeConnectionInvalidator 在同一context内并行通知全部transport registry。
//
// 一个通道失败不会跳过另一个通道；返回值聚合owner错误，调用方不得据此回滚已提交epoch。
type CompositeConnectionInvalidator struct {
	// invalidators 是构图时冻结且非空的transport owner集合。
	invalidators []ConnectionInvalidator
}

// NewCompositeConnectionInvalidator 拒绝空集合与nil owner，避免静默遗漏某个实时通道。
func NewCompositeConnectionInvalidator(invalidators ...ConnectionInvalidator) (*CompositeConnectionInvalidator, error) {
	if len(invalidators) < 2 {
		return nil, errors.New("composite connection invalidator requires at least two owners")
	}
	copyValues := append([]ConnectionInvalidator(nil), invalidators...)
	for _, invalidator := range copyValues {
		if invalidator == nil {
			return nil, errors.New("composite connection invalidator contains nil owner")
		}
	}
	return &CompositeConnectionInvalidator{invalidators: copyValues}, nil
}

// Invalidate 并行尝试全部owner并等待共享deadline，顺序不影响是否被调用。
func (composite *CompositeConnectionInvalidator) Invalidate(ctx context.Context, invalidation Invalidation) error {
	if composite == nil || ctx == nil || !invalidation.SessionID.Valid() || !invalidation.Epoch.Valid() || invalidation.Reason == InvalidationReasonUnspecified {
		return errors.New("composite connection invalidation is invalid")
	}
	results := make(chan error, len(composite.invalidators))
	var started sync.WaitGroup
	started.Add(len(composite.invalidators))
	for _, invalidator := range composite.invalidators {
		owner := invalidator
		go func() {
			started.Done()
			results <- owner.Invalidate(ctx, invalidation)
		}()
	}
	started.Wait()
	var result error
	for range composite.invalidators {
		select {
		case err := <-results:
			result = errors.Join(result, err)
		case <-ctx.Done():
			return errors.Join(result, context.Cause(ctx))
		}
	}
	return result
}
