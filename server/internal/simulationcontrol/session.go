package simulationcontrol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// SessionErrorKind 是 process control failure 的稳定低基数分类。
type SessionErrorKind string

const (
	// SessionErrorProtocol 表示 child 输出违反冻结契约。
	SessionErrorProtocol SessionErrorKind = "protocol"
	// SessionErrorTransport 表示 pipe EOF 或读写失败。
	SessionErrorTransport SessionErrorKind = "transport"
	// SessionErrorDeadline 表示内部有界等待耗尽。
	SessionErrorDeadline SessionErrorKind = "deadline"
	// SessionErrorClosed 表示 terminal session 不再接受请求。
	SessionErrorClosed SessionErrorKind = "closed"
	// SessionErrorBackpressure 表示对应优先级等待队列已达到 hard limit。
	SessionErrorBackpressure SessionErrorKind = "backpressure"
)

// SessionError 保存稳定分类且不携带 nonce、payload 或完整 assignment。
type SessionError struct {
	// Kind 是 caller 可判定的稳定分类。
	Kind SessionErrorKind
	// cause 只保存低敏内部错误。
	cause error
}

// Error 返回不展开 control 数据的稳定诊断。
func (failure *SessionError) Error() string {
	if failure == nil {
		return "simulation control failure"
	}
	return "simulation control " + string(failure.Kind)
}

// Unwrap 返回低敏底层 cause。
func (failure *SessionError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

// ProposalHandler 在 session reader 上接收已通过 frame contract 的 proposal。
//
// 实现不得阻塞；持久裁决由独立 coordinator 消费有界副本。
type ProposalHandler interface {
	// OfferResult 接收 immutable proposal；返回错误会终止 session。
	OfferResult(ResultProposal) error
}

// Session 在一对继承 pipes 上执行 serialized request/receipt。
type Session struct {
	// writer 是 child stdin 的唯一写 owner。
	writer io.Writer
	// nonce 绑定单次 child process。
	nonce Digest
	// requestTimeout 是发送后不受 caller cancel 扩大的内部 hard deadline。
	requestTimeout time.Duration
	// proposals 接收 result proposal。
	proposals ProposalHandler
	// callMutex 保证同一 session 同时只有一个 request turn。
	callMutex sync.Mutex
	// laneMutex 保护高低优先级等待者与 active turn。
	laneMutex sync.Mutex
	// laneChanged 以 close-and-recreate 广播优先级状态变化。
	laneChanged chan struct{}
	// laneActive 表示已有 request/send 占用不可抢占的单一 pipe turn。
	laneActive bool
	// highWaiting 是 lifecycle/health/revoke/result ack 等待者数量。
	highWaiting int
	// lowWaiting 是 ticket/snapshot 低优先级队列长度，不含 active turn。
	lowWaiting int
	// sequenceMutex 线性化 reader 与 writer 共享 sequence。
	sequenceMutex sync.Mutex
	// sequence 是已接受或写入的最后 frame sequence。
	sequence uint64
	// incoming 是 reader 解码后的有界 frame queue。
	incoming chan Frame
	// terminal 在 reader 结束后关闭。
	terminal chan struct{}
	// terminalMutex 保护一次性 terminal cause。
	terminalMutex sync.Mutex
	// terminalErr 是首个不可恢复 failure。
	terminalErr error
	// closeOnce 保证 pipe close 与 terminal transition 只执行一次。
	closeOnce sync.Once
	// closer 关闭 parent 持有的 child stdin/stdout。
	closer io.Closer
}

// NewSession 启动单 reader；构造后首个调用必须是 hello。
func NewSession(reader io.Reader, writer io.Writer, closer io.Closer, nonce Digest, requestTimeout time.Duration, proposals ProposalHandler) (*Session, error) {
	if reader == nil || writer == nil || closer == nil || !nonce.Valid() ||
		requestTimeout <= 0 || proposals == nil {
		return nil, errors.New("simulation control session dependencies are invalid")
	}
	session := &Session{
		writer:         writer,
		nonce:          nonce,
		requestTimeout: requestTimeout,
		proposals:      proposals,
		laneChanged:    make(chan struct{}),
		incoming:       make(chan Frame, PendingRequestLimit),
		terminal:       make(chan struct{}),
		closer:         closer,
	}
	go session.readLoop(bufio.NewReaderSize(reader, MaximumFrameBytes+4))
	return session, nil
}

// Call 发送一个 request 并等待 matching receipt。
//
// caller ctx 在 frame 发送后只影响返回值；Session 仍在内部 hard deadline 内消费 receipt，
// 防止取消后的响应留在 pipe 中破坏共享 sequence。
func (session *Session) Call(ctx context.Context, requestID RequestID, kind string, payload any, expectedKind string) (json.RawMessage, error) {
	if session == nil || ctx == nil || !requestID.Valid() || expectedKind == "" {
		return nil, errors.New("simulation control call input is invalid")
	}
	release, err := session.enterLane(ctx, lowPriorityRequest(kind))
	if err != nil {
		return nil, err
	}
	defer release()
	session.callMutex.Lock()
	defer session.callMutex.Unlock()
	if err := session.failure(); err != nil {
		return nil, err
	}
	session.sequenceMutex.Lock()
	next := session.sequence + 1
	frame, err := NewFrame(kind, payload, requestID, next, session.nonce)
	if err == nil {
		err = WriteFrame(session.writer, frame)
	}
	if err == nil {
		session.sequence = next
	}
	session.sequenceMutex.Unlock()
	if err != nil {
		session.fail(&SessionError{Kind: SessionErrorTransport, cause: err})
		return nil, session.failure()
	}

	timer := time.NewTimer(session.requestTimeout)
	defer timer.Stop()
	var callerErr error
	for {
		select {
		case <-ctx.Done():
			callerErr = ctx.Err()
			ctx = context.WithoutCancel(ctx)
		case <-timer.C:
			failure := &SessionError{Kind: SessionErrorDeadline, cause: errors.New("request hard deadline exceeded")}
			session.fail(failure)
			return nil, failure
		case <-session.terminal:
			return nil, session.failure()
		case incoming := <-session.incoming:
			// select 在 receipt 与取消同时 ready 时不保证分支顺序；读取 ctx 状态使已发生的取消稳定胜出，
			// 但仍继续消费并校验当前 turn 的 receipt，避免污染下一请求的 sequence。
			if callerErr == nil {
				callerErr = ctx.Err()
				if callerErr != nil {
					ctx = context.WithoutCancel(ctx)
				}
			}
			if incoming.Kind == "result.proposal" {
				if err := session.handleProposal(incoming); err != nil {
					session.fail(&SessionError{Kind: SessionErrorProtocol, cause: err})
					return nil, session.failure()
				}
				continue
			}
			if incoming.RequestID != requestID || incoming.Kind != expectedKind {
				failure := &SessionError{Kind: SessionErrorProtocol, cause: errors.New("receipt correlation mismatch")}
				session.fail(failure)
				return nil, failure
			}
			if callerErr != nil {
				return nil, callerErr
			}
			return append(json.RawMessage(nil), incoming.Payload...), nil
		}
	}
}

// Send 写入无需 receipt 的单向 frame；当前只用于 terminal result ack。
func (session *Session) Send(ctx context.Context, requestID RequestID, kind string, payload any) error {
	if session == nil || ctx == nil || !requestID.Valid() {
		return errors.New("simulation control send input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := session.enterLane(ctx, false)
	if err != nil {
		return err
	}
	defer release()
	session.callMutex.Lock()
	defer session.callMutex.Unlock()
	if err := session.failure(); err != nil {
		return err
	}
	session.sequenceMutex.Lock()
	next := session.sequence + 1
	frame, err := NewFrame(kind, payload, requestID, next, session.nonce)
	if err == nil {
		err = WriteFrame(session.writer, frame)
	}
	if err == nil {
		session.sequence = next
	}
	session.sequenceMutex.Unlock()
	if err != nil {
		session.fail(&SessionError{Kind: SessionErrorTransport, cause: err})
		return session.failure()
	}
	return nil
}

// enterLane 在单一 pipe turn 前执行 high-first 有界仲裁。
//
// Active turn 不可抢占，但结束后所有已等待 high 请求都先于 ticket/snapshot。
func (session *Session) enterLane(ctx context.Context, low bool) (func(), error) {
	if session == nil || ctx == nil {
		return nil, errors.New("simulation control lane input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session.laneMutex.Lock()
	if low {
		if session.lowWaiting >= LowPriorityRequestQueueLimit {
			session.laneMutex.Unlock()
			return nil, &SessionError{Kind: SessionErrorBackpressure}
		}
		session.lowWaiting++
	} else {
		if session.highWaiting >= PendingRequestLimit {
			session.laneMutex.Unlock()
			return nil, &SessionError{Kind: SessionErrorBackpressure}
		}
		session.highWaiting++
	}
	registered := true
	for {
		if !session.laneActive && (!low || session.highWaiting == 0) {
			session.laneActive = true
			if low {
				session.lowWaiting--
			} else {
				session.highWaiting--
			}
			registered = false
			session.laneMutex.Unlock()
			return func() { session.leaveLane() }, nil
		}
		changed := session.laneChanged
		session.laneMutex.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			session.laneMutex.Lock()
			if registered {
				if low {
					session.lowWaiting--
				} else {
					session.highWaiting--
				}
				session.broadcastLaneChange()
			}
			session.laneMutex.Unlock()
			return nil, ctx.Err()
		case <-session.terminal:
			session.laneMutex.Lock()
			if registered {
				if low {
					session.lowWaiting--
				} else {
					session.highWaiting--
				}
				session.broadcastLaneChange()
			}
			session.laneMutex.Unlock()
			return nil, session.failure()
		}
		session.laneMutex.Lock()
	}
}

// leaveLane 释放 active turn 并同时唤醒全部高低优先级等待者。
func (session *Session) leaveLane() {
	session.laneMutex.Lock()
	session.laneActive = false
	session.broadcastLaneChange()
	session.laneMutex.Unlock()
}

// broadcastLaneChange 要求调用方持有 laneMutex。
func (session *Session) broadcastLaneChange() {
	close(session.laneChanged)
	session.laneChanged = make(chan struct{})
}

// lowPriorityRequest 把可丢弃的 ticket/snapshot 查询放入有界低优先级 lane。
func lowPriorityRequest(kind string) bool {
	return kind == "battle.ticket.install" ||
		kind == "battle.ticket.status.query" ||
		kind == "battle_qualification_snapshot_request"
}

// Close 终止 pipes 并让 pending call 观察 closed failure。
func (session *Session) Close() error {
	if session == nil {
		return nil
	}
	session.fail(&SessionError{Kind: SessionErrorClosed})
	return nil
}

// Done 在 reader EOF、protocol failure 或 Close 后关闭。
func (session *Session) Done() <-chan struct{} {
	if session == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return session.terminal
}

// Err 返回首个 terminal failure。
func (session *Session) Err() error {
	if session == nil {
		return &SessionError{Kind: SessionErrorClosed}
	}
	return session.failure()
}

// readLoop 是 stdout 的唯一 reader，并在入队前验证 nonce/sequence。
func (session *Session) readLoop(reader *bufio.Reader) {
	for {
		frame, err := DecodeFrame(reader)
		if err != nil {
			kind := SessionErrorProtocol
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				kind = SessionErrorTransport
			}
			session.fail(&SessionError{Kind: kind, cause: err})
			return
		}
		session.sequenceMutex.Lock()
		expected := session.sequence + 1
		if frame.SessionNonce != session.nonce || frame.Sequence != expected {
			session.sequenceMutex.Unlock()
			session.fail(&SessionError{Kind: SessionErrorProtocol, cause: errors.New("session nonce or sequence mismatch")})
			return
		}
		session.sequence = frame.Sequence
		session.sequenceMutex.Unlock()
		select {
		case session.incoming <- frame:
		case <-session.terminal:
			return
		}
	}
}

// handleProposal 解码 closed proposal payload。
func (session *Session) handleProposal(frame Frame) error {
	var wire struct {
		AssignmentFingerprint string `json:"assignmentFingerprint"`
		EvidenceDigest        string `json:"evidenceDigest"`
		PayloadDigest         string `json:"payloadDigest"`
		ProposalFingerprint   string `json:"proposalFingerprint"`
		ResultID              string `json:"resultId"`
		ResultKind            string `json:"resultKind"`
		SimulationInstanceID  string `json:"simulationInstanceId"`
		TickEnd               string `json:"tickEnd"`
		TickStart             string `json:"tickStart"`
	}
	if err := decodeClosedPayload(frame.Payload, &wire); err != nil {
		return err
	}
	assignment, err := NewDigest(wire.AssignmentFingerprint)
	if err != nil {
		return err
	}
	evidence, err := NewDigest(wire.EvidenceDigest)
	if err != nil {
		return err
	}
	payload, err := NewDigest(wire.PayloadDigest)
	if err != nil {
		return err
	}
	fingerprint, err := NewDigest(wire.ProposalFingerprint)
	if err != nil {
		return err
	}
	instanceID, err := NewSimulationInstanceID(wire.SimulationInstanceID)
	if err != nil {
		return err
	}
	tickStart, err := parseCanonicalUint64AllowZero(wire.TickStart)
	if err != nil {
		return err
	}
	tickEnd, err := parseCanonicalUint64AllowZero(wire.TickEnd)
	if err != nil {
		return err
	}
	proposal := ResultProposal{
		ResultID:              wire.ResultID,
		Kind:                  wire.ResultKind,
		AssignmentFingerprint: assignment,
		InstanceID:            instanceID,
		TickStart:             tickStart,
		TickEnd:               tickEnd,
		PayloadDigest:         payload,
		EvidenceDigest:        evidence,
		ProposalFingerprint:   fingerprint,
	}
	if err := proposal.Validate(); err != nil {
		return err
	}
	return session.proposals.OfferResult(proposal)
}

// fail 原子记录首个 terminal cause、关闭 pipes 并广播 completion。
func (session *Session) fail(cause error) {
	session.closeOnce.Do(func() {
		session.terminalMutex.Lock()
		session.terminalErr = cause
		session.terminalMutex.Unlock()
		_ = session.closer.Close()
		close(session.terminal)
	})
}

// failure 返回 terminal cause；active session 返回 nil。
func (session *Session) failure() error {
	session.terminalMutex.Lock()
	defer session.terminalMutex.Unlock()
	return session.terminalErr
}

// decodeClosedPayload 拒绝 unknown、duplicate、trailing 与非 canonical payload。
func decodeClosedPayload(payload json.RawMessage, target any) error {
	if err := rejectDuplicateMembers(payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytesReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode closed control payload: %w", err)
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if string(canonical) != string(payload) {
		return errors.New("control payload does not match closed canonical schema")
	}
	return nil
}

// bytesReader 隔离 payload reader 构造，避免 decode helper 保留底层 slice。
func bytesReader(payload []byte) io.Reader {
	return &readOnlyBytes{value: payload}
}

// readOnlyBytes 是不暴露 seek/mutation 的小型 payload reader。
type readOnlyBytes struct {
	// value 是尚未消费的 payload。
	value []byte
}

// Read 消费 payload 并符合 io.Reader。
func (reader *readOnlyBytes) Read(target []byte) (int, error) {
	if len(reader.value) == 0 {
		return 0, io.EOF
	}
	count := copy(target, reader.value)
	reader.value = reader.value[count:]
	return count, nil
}

// parseCanonicalUint64AllowZero 解析允许零值的规范 Tick 文本。
func parseCanonicalUint64AllowZero(value string) (uint64, error) {
	if value == "0" {
		return 0, nil
	}
	return parseCanonicalUint64(value)
}
