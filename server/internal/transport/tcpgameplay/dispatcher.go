package tcpgameplay

import (
	"context"
	"errors"
	"time"

	commonv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/common/v1"
	visitv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/visit/v1"
	worldv1 "github.com/jinwiforz/ihomeland/server/internal/generated/proto/ihomeland/world/v1"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
	"google.golang.org/protobuf/proto"
)

// OperationContext 是application port接收的只读可信连接上下文。
type OperationContext struct {
	// ConnectionID 是服务端生成并用于binding的连接身份。
	ConnectionID string
	// Auth 来自已消费TLS_TCP ticket，不可由payload替换。
	Auth session.AuthContext
	// Qualification 是preface已消费并复核placement的只读资格。
	Qualification worldadmission.Qualification
}

// Application 是TCP dispatcher消费的逐operation窄端口。
type Application interface {
	// WorldSnapshot 读取当前connection binding唯一允许的PersonalWorld快照。
	WorldSnapshot(context.Context, OperationContext, *worldv1.WorldSnapshotRequest) (*worldv1.WorldSnapshotResponse, error)
	// VisitSnapshot 读取当前active VisitSession完整替换快照。
	VisitSnapshot(context.Context, OperationContext, *visitv1.VisitSnapshotRequest) (*visitv1.VisitSnapshotResponse, error)
	// VisitOpen 为Owner自己的world开启或解析active VisitSession。
	VisitOpen(context.Context, OperationContext, *visitv1.VisitOpenCommand, []byte) (*visitv1.VisitOpenResponse, error)
	// VisitCreateInvite 创建定向邀请并保留application幂等边界。
	VisitCreateInvite(context.Context, OperationContext, *visitv1.VisitCreateInviteCommand, []byte) (*visitv1.VisitCreateInviteResponse, error)
	// VisitRevokeInvite 撤销指定pending邀请。
	VisitRevokeInvite(context.Context, OperationContext, *visitv1.VisitRevokeInviteCommand, []byte) (*visitv1.VisitRevokeInviteResponse, error)
	// VisitJoin 用恢复且完整相等的JOIN Qualification提交membership。
	VisitJoin(context.Context, OperationContext, worldadmission.Qualification, *visitv1.VisitJoinCommand, []byte) (*visitv1.VisitJoinResponse, error)
	// VisitLeave 移除当前Visitor自己的membership。
	VisitLeave(context.Context, OperationContext, *visitv1.VisitLeaveCommand, []byte) (*visitv1.VisitLeaveResponse, error)
	// VisitKick 由Owner移除指定Visitor。
	VisitKick(context.Context, OperationContext, *visitv1.VisitKickCommand, []byte) (*visitv1.VisitKickResponse, error)
	// VisitReconnect 用恢复且完整相等的RECONNECT Qualification更新membership binding。
	VisitReconnect(context.Context, OperationContext, worldadmission.Qualification, *visitv1.VisitReconnectCommand, []byte) (*visitv1.VisitReconnectResponse, error)
	// VisitClose 由Owner终止当前VisitSession。
	VisitClose(context.Context, OperationContext, *visitv1.VisitCloseCommand, []byte) (*visitv1.VisitCloseResponse, error)
}

// PublicError 是application返回给实时客户端的稳定安全错误，不包含cause文本。
type PublicError struct {
	// Code 必须登记在errors.json。
	Code uint32
	// MessageKey 必须与登记错误语义一致。
	MessageKey string
	// Retryable 决定客户端是否可保持输入，并在必要的重连后稍后重试。
	Retryable bool
	// CloseConnection 要求唯一 writer 在安全错误写出后关闭已失效 target。
	CloseConnection bool
}

// Error 返回固定文本，避免调用路径误把内部cause编码进payload。
func (PublicError) Error() string { return "tcp gameplay application error" }

// Dispatcher 串行执行已解码route并把结果加入同一connection writer队列。
type Dispatcher struct {
	// application 是不依赖socket或storage adapter的业务端口。
	application Application
	// handshake 只用于Join/Reconnect精确恢复preface消费结果。
	handshake *Handshake
	// registry 负责pending到active的恰好一次迁移。
	registry *Registry
	// observer 只记录低基数处理结果。
	observer Observer
}

// NewDispatcher 构造不启动goroutine的集中handler catalog。
func NewDispatcher(application Application, handshake *Handshake, registry *Registry, observer Observer) (*Dispatcher, error) {
	if application == nil || handshake == nil || registry == nil || observer == nil {
		return nil, errors.New("tcp gameplay dispatcher dependencies are incomplete")
	}
	return &Dispatcher{application: application, handshake: handshake, registry: registry, observer: observer}, nil
}

// Dispatch 同步处理一条消息；reader顺序即command执行顺序，transport不缓存application结果。
func (dispatcher *Dispatcher) Dispatch(parent context.Context, entry *connection, message DecodedMessage) (err error) {
	if parent == nil || entry == nil || message.payload == nil {
		return errors.New("tcp gameplay dispatch input is invalid")
	}
	startedAt := time.Now()
	dispatchStarted := false
	dispatchFinished := false
	responseEnqueued := false
	observe := func(outcome string) {
		dispatcher.observer.ObserveTCPDispatch(message.route.MessageID, outcome, time.Since(startedAt))
	}
	defer func() {
		if recover() != nil {
			observe("panic")
			enqueueErr := dispatcher.enqueueError(entry, message, PublicError{Code: 501, MessageKey: "error.internal", Retryable: true})
			responseEnqueued = enqueueErr == nil
			err = enqueueErr
			if dispatchStarted && !dispatchFinished {
				err = errors.Join(err, entry.finishDispatch(responseEnqueued))
				dispatchFinished = true
			}
		}
		if dispatchStarted && !dispatchFinished {
			err = errors.Join(err, entry.finishDispatch(responseEnqueued))
		}
	}()
	if err := dispatcher.authorizeState(entry, message.route.MessageID); err != nil {
		observe("state_rejected")
		return dispatcher.enqueueError(entry, message, PublicError{Code: 101, MessageKey: "error.auth.forbidden"})
	}
	allowed, closeConnection := allowRoute(entry, message.route.RatePolicy, dispatcher.registry.clock.Now().UTC())
	if !allowed {
		observe("rate_limited")
		err := dispatcher.enqueueError(entry, message, PublicError{Code: 400, MessageKey: "error.rate_limited", Retryable: true})
		if closeConnection {
			return errors.Join(ErrRateLimited, err)
		}
		return err
	}
	dispatcher.observer.AddTCPInFlight(1)
	defer dispatcher.observer.AddTCPInFlight(-1)
	deadline := time.Duration(message.route.TimeoutMS) * time.Millisecond
	ctx, cancel := context.WithTimeout(parent, deadline)
	defer cancel()
	if err := entry.beginDispatch(); err != nil {
		observe("state_rejected")
		return err
	}
	dispatchStarted = true
	operation := OperationContext{ConnectionID: entry.id, Auth: entry.auth, Qualification: entry.qualification}
	responseID, response, callErr := dispatcher.call(ctx, entry, operation, message)
	if callErr != nil {
		public := PublicError{Code: 500, MessageKey: "error.dependency.unavailable", Retryable: true}
		if errors.As(callErr, &public) {
			// application已经提供登记错误；不读取或编码cause文本。
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			public = PublicError{Code: 500, MessageKey: "error.dependency.unavailable", Retryable: true}
		}
		observe("application_error")
		enqueueErr := dispatcher.enqueueError(entry, message, public)
		responseEnqueued = enqueueErr == nil
		finishErr := entry.finishDispatch(responseEnqueued)
		dispatchFinished = true
		return errors.Join(enqueueErr, finishErr)
	}
	if err := entry.enqueue(responseID, response, message.correlation, nil); err != nil {
		finishErr := entry.finishDispatch(false)
		dispatchFinished = true
		observe("queue_rejected")
		return errors.Join(err, finishErr)
	}
	responseEnqueued = true
	if err := entry.finishDispatch(true); err != nil {
		dispatchFinished = true
		observe("queue_rejected")
		return err
	}
	dispatchFinished = true
	observe("ok")
	return nil
}

// beginDispatch 标记当前同步command，使自身safe-return延迟到response入队之后。
func (entry *connection) beginDispatch() error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.dispatching || len(entry.pendingPushes) != 0 {
		return errors.New("tcp gameplay connection already dispatches a command")
	}
	entry.dispatching = true
	return nil
}

// finishDispatch 清除同步 command 标记；仅在 response 已入队时原子追加暂存 PUSH。
func (entry *connection) finishDispatch(flush bool) error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if !entry.dispatching {
		return nil
	}
	pending := entry.pendingPushes
	entry.pendingPushes = nil
	entry.pendingPushBytes = 0
	entry.dispatching = false
	if !flush {
		return nil
	}
	var resultErr error
	for _, push := range pending {
		resultErr = errors.Join(resultErr, entry.enqueueLocked(push.messageID, push.payload, Correlation{}, nil, push.closeAfter))
	}
	return resultErr
}

// pendingDispatchPush 保存尚未分配S2C sequence的有界PUSH。
type pendingDispatchPush struct {
	// messageID 必须已经通过 publisher target 校验。
	messageID uint32
	// payload 是尚未分配 S2C sequence 的不可变消息。
	payload proto.Message
	// closeAfter 只允许safe-return在写出后关闭旧target。
	closeAfter bool
}

// enqueuePush 在当前command执行期间延迟PUSH；其他时刻直接进入serialized writer队列。
func (entry *connection) enqueuePush(messageID uint32, payload proto.Message, closeAfter bool) error {
	entry.mu.Lock()
	if entry.dispatching {
		estimatedBytes := proto.Size(payload) + 256
		if len(entry.pendingPushes) >= cap(entry.queue.items) || entry.pendingPushBytes+estimatedBytes > entry.queue.byteLimit {
			entry.mu.Unlock()
			return ErrQueueFull
		}
		entry.pendingPushes = append(entry.pendingPushes, pendingDispatchPush{messageID: messageID, payload: proto.Clone(payload), closeAfter: closeAfter})
		entry.pendingPushBytes += estimatedBytes
		entry.mu.Unlock()
		return nil
	}
	entry.mu.Unlock()
	if closeAfter {
		return entry.enqueueAndClose(messageID, payload)
	}
	return entry.enqueue(messageID, payload, Correlation{}, nil)
}

// call 把每个message ID集中映射到唯一typed application method和response ID。
func (dispatcher *Dispatcher) call(ctx context.Context, entry *connection, operation OperationContext, message DecodedMessage) (uint32, proto.Message, error) {
	commandID := append([]byte(nil), message.correlation.CommandID...)
	switch payload := message.payload.(type) {
	case *worldv1.WorldSnapshotRequest:
		response, err := dispatcher.application.WorldSnapshot(ctx, operation, payload)
		return 2001, response, err
	case *visitv1.VisitOpenCommand:
		response, err := dispatcher.application.VisitOpen(ctx, operation, payload, commandID)
		return 2104, response, err
	case *visitv1.VisitCreateInviteCommand:
		response, err := dispatcher.application.VisitCreateInvite(ctx, operation, payload, commandID)
		return 2106, response, err
	case *visitv1.VisitRevokeInviteCommand:
		response, err := dispatcher.application.VisitRevokeInvite(ctx, operation, payload, commandID)
		return 2108, response, err
	case *visitv1.VisitJoinCommand:
		credential, err := worldadmission.ParseCredential(payload.GetAdmissionCredential())
		if err != nil {
			return 2110, nil, PublicError{Code: 2003, MessageKey: "error.world.admission_invalid"}
		}
		qualification, err := dispatcher.handshake.Reverify(ctx, entry, credential, worldadmission.PurposeJoin)
		if err != nil {
			return 2110, nil, PublicError{Code: 2003, MessageKey: "error.world.admission_invalid"}
		}
		response, err := dispatcher.application.VisitJoin(ctx, operation, qualification, payload, commandID)
		if err == nil {
			err = dispatcher.registry.ActivatePending(entry.id, qualification)
		}
		return 2110, response, err
	case *visitv1.VisitLeaveCommand:
		response, err := dispatcher.application.VisitLeave(ctx, operation, payload, commandID)
		return 2112, response, err
	case *visitv1.VisitKickCommand:
		response, err := dispatcher.application.VisitKick(ctx, operation, payload, commandID)
		return 2114, response, err
	case *visitv1.VisitReconnectCommand:
		credential, err := worldadmission.ParseCredential(payload.GetAdmissionCredential())
		if err != nil {
			return 2116, nil, PublicError{Code: 2003, MessageKey: "error.world.admission_invalid"}
		}
		qualification, err := dispatcher.handshake.Reverify(ctx, entry, credential, worldadmission.PurposeReconnect)
		if err != nil {
			return 2116, nil, PublicError{Code: 2003, MessageKey: "error.world.admission_invalid"}
		}
		response, err := dispatcher.application.VisitReconnect(ctx, operation, qualification, payload, commandID)
		if err == nil {
			err = dispatcher.registry.ActivatePending(entry.id, qualification)
		}
		return 2116, response, err
	case *visitv1.VisitCloseCommand:
		response, err := dispatcher.application.VisitClose(ctx, operation, payload, commandID)
		return 2118, response, err
	case *visitv1.VisitSnapshotRequest:
		response, err := dispatcher.application.VisitSnapshot(ctx, operation, payload)
		return 2120, response, err
	default:
		return 0, nil, PublicError{Code: 1, MessageKey: "error.protocol.invalid_envelope"}
	}
}

// authorizeState 限制pending只执行匹配首个Join/Reconnect，returning/closing拒绝全部新业务。
func (dispatcher *Dispatcher) authorizeState(entry *connection, messageID uint32) error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	switch entry.state {
	case ConnectionStateActive:
		return nil
	case ConnectionStatePending:
		purpose := entry.qualification.Binding().Purpose()
		if (purpose == worldadmission.PurposeJoin && messageID == 2109) || (purpose == worldadmission.PurposeReconnect && messageID == 2115) {
			if dispatcher.registry.clock.Now().UTC().Before(entry.pendingDeadline) {
				return nil
			}
		}
	}
	return errors.New("tcp gameplay connection state forbids operation")
}

// enqueueError 只编码登记code/key/retry语义，不包含application错误文本。
func (dispatcher *Dispatcher) enqueueError(entry *connection, message DecodedMessage, public PublicError) error {
	closeConnection := public.CloseConnection
	registered, err := entryCodec(entry).catalog.LookupError(public.Code)
	if err != nil || registered.MessageKey != public.MessageKey || registered.Retryable != public.Retryable {
		public = PublicError{Code: 501, MessageKey: "error.internal", Retryable: true, CloseConnection: closeConnection}
	}
	payload := commonv1.ErrorPayload_builder{Code: proto.Uint32(public.Code), MessageKey: proto.String(public.MessageKey), Retryable: proto.Bool(public.Retryable), RequestId: append([]byte(nil), message.correlation.RequestID...)}.Build()
	if closeConnection {
		return entry.enqueueErrorAndClose(message.route.MessageID+1, payload, message.correlation)
	}
	return entry.enqueueError(message.route.MessageID+1, payload, message.correlation, nil)
}

// maximumConsecutiveRateRejections 是关闭持续滥用连接前允许返回的限流错误次数。
const maximumConsecutiveRateRejections = 3

// allowRoute 按contract固定policy名称执行单连接token bucket，并标记持续滥用连接。
func allowRoute(entry *connection, policy string, now time.Time) (bool, bool) {
	requests, window, burst := 0, time.Minute, 0
	switch policy {
	case "world_read":
		requests, burst = 120, 20
	case "visit_command":
		requests, burst = 60, 10
	default:
		return false, false
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	state := entry.routeRates[policy]
	if state == nil {
		state = &routeRateState{tokens: float64(burst), lastRefill: now}
		entry.routeRates[policy] = state
	}
	elapsed := now.Sub(state.lastRefill)
	if elapsed > 0 {
		state.tokens += elapsed.Seconds() * float64(requests) / window.Seconds()
		if state.tokens > float64(burst) {
			state.tokens = float64(burst)
		}
		state.lastRefill = now
	}
	if state.tokens < 1 {
		entry.consecutiveRateRejections++
		return false, entry.consecutiveRateRejections >= maximumConsecutiveRateRejections
	}
	state.tokens--
	entry.consecutiveRateRejections = 0
	return true, false
}

// enqueue 原子分配S2C sequence并加入共享serialized writer队列。
func (entry *connection) enqueue(messageID uint32, payload proto.Message, correlation Correlation, completion chan<- error) error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.enqueueLocked(messageID, payload, correlation, completion, false)
}

// enqueueLocked 在 connection 临界区内分配 sequence；调用方必须已经持有 entry.mu。
func (entry *connection) enqueueLocked(messageID uint32, payload proto.Message, correlation Correlation, completion chan<- error, closeAfter bool) error {
	encoded, err := entryCodec(entry).Encode(messageID, payload, correlation, entry.nextServerSequence)
	if err != nil {
		return err
	}
	if err := entry.queue.tryPushWithClose(encoded, completion, closeAfter); err != nil {
		items, bytes := entry.queue.snapshot()
		entry.observer.ObserveTCPQueue("rejected", items, bytes)
		return err
	}
	entry.nextServerSequence++
	items, bytes := entry.queue.snapshot()
	entry.observer.ObserveTCPQueue("accepted", items, bytes)
	return nil
}

// enqueueAndClose 与普通push共享sequence，但让serialized writer成功写出后关闭旧target。
func (entry *connection) enqueueAndClose(messageID uint32, payload proto.Message) error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.enqueueLocked(messageID, payload, Correlation{}, nil, true)
}

// enqueueError 与普通response共享sequence与writer所有权。
func (entry *connection) enqueueError(messageID uint32, payload *commonv1.ErrorPayload, correlation Correlation, completion chan<- error) error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.enqueueErrorLocked(messageID, payload, correlation, completion, false)
}

// enqueueErrorAndClose 原子封闭失效 target，并让 writer 在安全错误写出后结束连接。
func (entry *connection) enqueueErrorAndClose(messageID uint32, payload *commonv1.ErrorPayload, correlation Correlation) error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.state = ConnectionStateClosing
	entry.closeClass = CloseClassInvalidated
	return entry.enqueueErrorLocked(messageID, payload, correlation, nil, true)
}

// enqueueErrorLocked 在 connection 临界区内编码错误；调用方必须已经持有 entry.mu。
func (entry *connection) enqueueErrorLocked(messageID uint32, payload *commonv1.ErrorPayload, correlation Correlation, completion chan<- error, closeAfter bool) error {
	encoded, err := entryCodec(entry).EncodeError(messageID, payload, correlation, entry.nextServerSequence)
	if err != nil {
		return err
	}
	if err := entry.queue.tryPushWithClose(encoded, completion, closeAfter); err != nil {
		items, bytes := entry.queue.snapshot()
		entry.observer.ObserveTCPQueue("rejected", items, bytes)
		return err
	}
	entry.nextServerSequence++
	items, bytes := entry.queue.snapshot()
	entry.observer.ObserveTCPQueue("accepted", items, bytes)
	return nil
}

// entryCodec 由registry在connection构造时注入；该helper在接线完成前保持单点访问。
func entryCodec(entry *connection) *Codec { return entry.codec }
