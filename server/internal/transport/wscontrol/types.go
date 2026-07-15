// Package wscontrol 实现共享公开listener上的只出不进WebSocket控制通道。
//
// 本包只拥有握手、连接引用、背压、心跳和已登记PUSH编码，不保存领域事实，
// 也不接收客户端业务消息或直接依赖storage adapter。
package wscontrol

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

const (
	// Path 是顶层public mux唯一转交WSS handler的精确路径。
	Path = "/v1/control"
	// Subprotocol 冻结control connection的协议代际。
	Subprotocol = "ihomeland.control.v1"
	// ProtocolVersion 是ReliableEnvelope当前主版本。
	ProtocolVersion uint32 = 1
)

var (
	// ErrQueueFull 表示连接已越过item或byte预算，调用方必须关闭慢消费者。
	ErrQueueFull = errors.New("websocket control send queue is full")
	// ErrQueueClosed 表示连接已停止接受新的PUSH。
	ErrQueueClosed = errors.New("websocket control send queue is closed")
	// ErrRegistryStopped 表示draining后的registry拒绝注册和投递。
	ErrRegistryStopped = errors.New("websocket control registry is stopped")
	// ErrConnectionLimit 表示全局或某个受信索引达到硬上限。
	ErrConnectionLimit = errors.New("websocket control connection limit reached")
	// ErrRateLimited 表示remote identity的握手预算耗尽。
	ErrRateLimited = errors.New("websocket control handshake rate limited")
	// ErrConnectionNotFound 表示publisher target当前没有活动连接。
	ErrConnectionNotFound = errors.New("websocket control target not found")
)

// Clock 为PUSH timestamp与限流回收提供共享绝对时间。
type Clock interface {
	// Now 返回当前时间；生产实现必须使用系统时钟。
	Now() time.Time
}

// IDGenerator 创建不可预测的ConnectionID。
type IDGenerator interface {
	// NewID 返回固定长度CSPRNG标识材料。
	NewID() (string, error)
}

// TicketConsumer 是WSS握手所需的Session service最窄端口。
type TicketConsumer interface {
	// ConsumeTicket 原子消费绑定channel与受信endpoint的一次性ticket。
	ConsumeTicket(ctx context.Context, nonce session.TicketNonce, channel session.Channel, endpoint session.Endpoint) (session.AuthContext, error)
}

// TaskOwner 把registry生命周期纳入Composition Root统一监督。
type TaskOwner interface {
	// Go 登记稳定命名的长生命周期任务。
	Go(name string, task func(context.Context) error) error
	// Stop 取消并等待owner全部任务。
	Stop(ctx context.Context, cause error) error
}

// Observer 只接收稳定低基数结果与数值，不接收身份、remote、header或payload。
type Observer interface {
	// ObserveWSSHandshake 记录握手的稳定结果。
	ObserveWSSHandshake(outcome string)
	// SetWSSConnections 更新活动连接数。
	SetWSSConnections(value int)
	// ObserveWSSPush 记录message ID、结果和编码字节数。
	ObserveWSSPush(messageID uint32, outcome string, encodedBytes int)
	// ObserveWSSQueue 观察有界队列状态。
	ObserveWSSQueue(outcome string, items int, bytes int)
	// ObserveWSSHeartbeat 记录ping/pong结果。
	ObserveWSSHeartbeat(outcome string)
	// ObserveWSSClose 记录稳定关闭原因。
	ObserveWSSClose(reason string)
	// ObserveWSSInvalidation 记录连接失效处理结果。
	ObserveWSSInvalidation(outcome string)
}

// Config 保存启动时已验证并复制的WSS运行策略。
type Config struct {
	// Policy 是配置owner验证后的冻结策略。
	Policy config.WebSocketControlPolicy
	// FrameBytes 是全局realtime完整frame上限。
	FrameBytes int
	// AllowPlaintext 只可用于已验证为loopback的local环境。
	AllowPlaintext bool
	// Logger 只记录ConnectionID、稳定operation与稳定outcome。
	Logger *slog.Logger
}

// validate 防止测试或未来调用方绕过config.Load构造不完整运行策略。
func (config Config) validate() error {
	if config.Logger == nil {
		return errors.New("websocket control config is incomplete")
	}
	return config.Policy.Validate(config.FrameBytes)
}
