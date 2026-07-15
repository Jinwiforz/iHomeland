// Package tcpgameplay 实现独立TLS/TCP可靠业务通道的认证、framing、路由和连接生命周期。
//
// 本包只依赖application窄端口，不直接访问Gin、MySQL、Redis或其他transport adapter。
package tcpgameplay

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/config"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
)

const (
	// ProtocolVersion 是ReliableEnvelope当前唯一接受的主版本。
	ProtocolVersion uint32 = 1
	// PrefaceVersion 是authentication preface当前唯一接受的版本。
	PrefaceVersion uint16 = 1
	// PrefaceMagic 在解析credential长度前拒绝非gameplay流量。
	PrefaceMagic = "IHTP"
	// ticketTextBytes 是16-byte ticket nonce的小写十六进制长度。
	ticketTextBytes = 32
	// admissionTextBytes 是wad1_与32-byte base64url材料的规范长度。
	admissionTextBytes = 48
	// prefaceHeaderBytes 不含4-byte frame prefix与两个credential正文。
	prefaceHeaderBytes = 11
)

var (
	// ErrProtocol 表示peer发送了不可继续解析的协议数据。
	ErrProtocol = errors.New("tcp gameplay protocol violation")
	// ErrQueueFull 表示连接超过item或encoded-byte发送预算。
	ErrQueueFull = errors.New("tcp gameplay send queue is full")
	// ErrQueueClosed 表示连接已经停止接受待发送消息。
	ErrQueueClosed = errors.New("tcp gameplay send queue is closed")
	// ErrConnectionLimit 表示全局或受信索引达到硬上限。
	ErrConnectionLimit = errors.New("tcp gameplay connection limit reached")
	// ErrRateLimited 表示remote或route token bucket暂时耗尽。
	ErrRateLimited = errors.New("tcp gameplay rate limited")
	// ErrConnectionNotFound 表示publisher目标当前没有活动gameplay连接。
	ErrConnectionNotFound = errors.New("tcp gameplay target not found")
)

// CloseClass 是application可依赖的低基数连接结束语义，不包含I/O或backend错误文本。
type CloseClass uint8

const (
	// CloseClassUnexpected 表示peer、I/O、idle、protocol或slow-consumer导致的连接丢失。
	CloseClassUnexpected CloseClass = iota + 1
	// CloseClassInvalidated 表示Session owner提交新epoch后关闭旧连接。
	CloseClassInvalidated
	// CloseClassApplicationReturn 表示committed leave/close/safe-return主动结束旧target。
	CloseClassApplicationReturn
	// CloseClassDraining 表示进程关闭，不应建立新的reconnect grace。
	CloseClassDraining
)

// String 返回日志与metrics使用的稳定名称。
func (class CloseClass) String() string {
	switch class {
	case CloseClassUnexpected:
		return "unexpected"
	case CloseClassInvalidated:
		return "invalidated"
	case CloseClassApplicationReturn:
		return "application_return"
	case CloseClassDraining:
		return "draining"
	default:
		return "unspecified"
	}
}

// LifecycleView 是双credential认证后不可由payload覆盖的连接事实值副本。
type LifecycleView struct {
	// connectionID 是transport生成的当前connection binding材料。
	connectionID string
	// auth 是Session owner验证的只读身份与epoch。
	auth session.AuthContext
	// binding 是WorldAdmission owner验证的完整target与AssignmentStamp。
	binding worldadmission.Binding
}

// ConnectionID 返回服务端生成的连接identity。
func (view LifecycleView) ConnectionID() string { return view.connectionID }

// Auth 返回不可变AuthContext值副本。
func (view LifecycleView) Auth() session.AuthContext { return view.auth }

// Binding 返回不可变WorldAdmission binding值副本。
func (view LifecycleView) Binding() worldadmission.Binding { return view.binding }

// Valid 报告view是否保持同一session/player binding。
func (view LifecycleView) Valid() bool {
	return view.connectionID != "" && view.auth.Valid() && view.binding.Valid() &&
		view.binding.SessionID() == view.auth.SessionID() && uint64(view.binding.Epoch()) == uint64(view.auth.Epoch()) &&
		view.binding.PlayerID().String() == view.auth.Principal().PlayerID()
}

// LifecycleSink 接收受信连接建立/移除事件，但不拥有socket或transport registry。
type LifecycleSink interface {
	// Connected 在I/O owner启动前执行application reconciliation；失败必须fail closed。
	Connected(context.Context, LifecycleView) error
	// Disconnected 只根据不可变view与低基数class处理当前binding丢失。
	Disconnected(context.Context, LifecycleView, CloseClass) error
}

// Clock 为envelope timestamp、deadline与限流回收提供共享时间。
type Clock interface {
	// Now 返回当前时间；生产实现必须使用系统时钟。
	Now() time.Time
}

// IDGenerator 创建不可预测的ConnectionID材料。
type IDGenerator interface {
	// NewID 返回固定16-byte CSPRNG值的小写十六进制表达。
	NewID() (string, error)
}

// TicketConsumer 是preface握手所需的Session service最窄端口。
type TicketConsumer interface {
	// ConsumeTicket 原子消费绑定TLS_TCP、GAMEPLAY与受信advertised endpoint的一次性ticket。
	ConsumeTicket(ctx context.Context, nonce session.TicketNonce, channel session.Channel, endpoint session.Endpoint) (session.AuthContext, error)
}

// AdmissionVerifier 是preface与Join/Reconnect重试共享的WorldAdmission最窄端口。
type AdmissionVerifier interface {
	// Verify 原子消费或用同一consume identity恢复Qualification。
	Verify(ctx context.Context, credential worldadmission.Credential, consumeID worldadmission.ConsumeID, auth session.AuthContext, endpoint session.Endpoint, purpose worldadmission.Purpose) (worldadmission.Qualification, error)
}

// TaskOwner 把accept与connection任务纳入Composition Root统一监督。
type TaskOwner interface {
	// Go 登记稳定命名的长生命周期任务。
	Go(name string, task func(context.Context) error) error
	// Stop 取消并等待owner全部任务。
	Stop(ctx context.Context, cause error) error
}

// Observer 只接收稳定低基数结果与数值，不接收身份、remote、credential或payload。
type Observer interface {
	// ObserveTCPHandshake 记录TLS、ticket与admission阶段的稳定结果。
	ObserveTCPHandshake(stage string, outcome string)
	// SetTCPConnections 更新指定状态的连接数量。
	SetTCPConnections(state string, value int)
	// ObserveTCPFrame 记录方向、结果和完整frame字节数。
	ObserveTCPFrame(direction string, outcome string, bytes int)
	// ObserveTCPDispatch 记录message ID、稳定处理结果与端到端耗时。
	ObserveTCPDispatch(messageID uint32, outcome string, duration time.Duration)
	// AddTCPInFlight 调整当前进程正在执行的gameplay operation数量。
	AddTCPInFlight(delta int)
	// ObserveTCPQueue 记录有界队列结果与占用。
	ObserveTCPQueue(outcome string, items int, bytes int)
	// ObserveTCPPush 记录message ID与投递结果。
	ObserveTCPPush(messageID uint32, outcome string)
	// ObserveTCPClose 记录稳定关闭原因。
	ObserveTCPClose(reason string)
	// ObserveTCPInvalidation 记录跨连接失效结果。
	ObserveTCPInvalidation(outcome string)
}

// Config 保存启动时已经验证并复制的TCP运行策略。
type Config struct {
	// Policy 是config owner冻结的资源与deadline策略。
	Policy config.GameplayTCPPolicy
	// FrameBytes 是所有业务route共享的完整envelope上限(bytes)。
	FrameBytes int
	// AllowPlaintext 只允许已验证为loopback的local/test listener使用。
	AllowPlaintext bool
	// Logger 只能记录ConnectionID、稳定operation与稳定outcome。
	Logger *slog.Logger
}

// Validate 防止测试或未来调用方绕过config.Load构造无界运行策略。
func (value Config) Validate(publicAddress string, diagnosticAddress string) error {
	if value.Logger == nil {
		return errors.New("tcp gameplay config is incomplete")
	}
	return value.Policy.Validate(value.FrameBytes, publicAddress, diagnosticAddress, !value.AllowPlaintext, true)
}

// SystemClock 提供生产绝对时间。
type SystemClock struct{}

// Now 返回当前UTC时间。
func (SystemClock) Now() time.Time { return time.Now().UTC() }
