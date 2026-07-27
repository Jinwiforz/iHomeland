package protocolclient

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	// buildIdentityBytes 是 describe receipt 的 SHA-256 binary identity 宽度。
	buildIdentityBytes = 32
	// describePayloadBytes 包含 build identity、contract version 与 frame ceiling。
	describePayloadBytes = buildIdentityBytes + 1 + 4
	// startupRollbackTimeout 限制 identity/contract 失败后的 child 回收。
	startupRollbackTimeout = 5 * time.Second
)

var (
	// ErrIdentityMismatch 表示 child describe 与冻结 binary/contract 不一致。
	ErrIdentityMismatch = errors.New("battle protocol client identity mismatch")
	// ErrCredentialAlreadyDelivered 表示 supervisor 拒绝第二次 secret ingress。
	ErrCredentialAlreadyDelivered = errors.New("battle protocol client credential already delivered")
	// ErrClosed 表示 child 或 stdio session 已进入不可逆终态。
	ErrClosed = errors.New("battle protocol client supervisor is closed")
	// ErrCleanupTimeout 表示 owned child 未在 rollback hard deadline 内退出。
	ErrCleanupTimeout = errors.New("battle protocol client cleanup deadline exceeded")
)

// Config 冻结 exact executable 与 expected build identity。
type Config struct {
	// ExecutablePath 必须是当前 run 已验证的绝对 `.exe` 路径。
	ExecutablePath string
	// ExpectedBuildIdentity 是 build receipt 绑定的 32-byte digest。
	ExpectedBuildIdentity [buildIdentityBytes]byte
}

// Validate 在创建 pipe 或 process 前拒绝相对路径和零 identity。
func (config Config) Validate() error {
	if !filepath.IsAbs(config.ExecutablePath) ||
		filepath.Ext(config.ExecutablePath) != ".exe" {
		return ErrIdentityMismatch
	}
	var nonzero byte
	for _, value := range config.ExpectedBuildIdentity {
		nonzero |= value
	}
	if nonzero == 0 {
		return ErrIdentityMismatch
	}
	return nil
}

// stderrSignal 丢弃 child 文本，只保留饱和 byte count。
type stderrSignal struct {
	// bytes 是低敏“有/无 stderr”计数，不保存内容。
	bytes uint64
	// mutex 保护并发 Write。
	mutex sync.Mutex
}

// Write 丢弃 payload 并返回完整消费，避免 secret 进入 Go memory evidence。
func (signal *stderrSignal) Write(payload []byte) (int, error) {
	signal.mutex.Lock()
	defer signal.mutex.Unlock()
	remaining := ^uint64(0) - signal.bytes
	if uint64(len(payload)) >= remaining {
		signal.bytes = ^uint64(0)
	} else {
		signal.bytes += uint64(len(payload))
	}
	return len(payload), nil
}

// Supervisor 是单个 C++ client process、sequence 与 credential ingress 的 owner。
type Supervisor struct {
	// config 是启动前验证的 immutable identity。
	config Config
	// command 是 supervisor 唯一创建和终结的 child。
	command *exec.Cmd
	// stdin 是 request-only inherited pipe。
	stdin io.WriteCloser
	// stdout 是 receipt-only inherited pipe。
	stdout io.ReadCloser
	// stderr 只记录是否出现诊断，不保留文本。
	stderr *stderrSignal
	// cancel 在 protocol/deadline failure 时终结 owned child。
	cancel context.CancelFunc
	// waited 接收唯一 cmd.Wait 结果。
	waited chan error
	// nextSequence 是下一 request/receipt correlation。
	nextSequence uint64
	// credentialDelivered 防止第二次 session-start。
	credentialDelivered bool
	// closed 是不可逆 lifecycle 状态。
	closed bool
	// mutex 串行化 stdio 和 lifecycle。
	mutex sync.Mutex
}

// Start 启动 exact executable、发送 describe 并在返回前验证 identity。
//
// ctx 只约束启动操作；成功返回后的 child 生命周期由 Supervisor 独占，
// 调用方必须通过 Close 终结它，不能让场景 deadline 抢先破坏 cleanup 顺序。
func Start(ctx context.Context, config Config) (*Supervisor, error) {
	return startWithArguments(ctx, config, []string{"--stdio"}, nil)
}

// startWithArguments 允许 contract test 复用当前 test binary；production 只调用 Start。
func startWithArguments(
	ctx context.Context,
	config Config,
	arguments []string,
	environment []string,
) (*Supervisor, error) {
	if ctx == nil {
		return nil, ErrIdentityMismatch
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	processContext, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(processContext, config.ExecutablePath, arguments...)
	command.Dir = filepath.Dir(config.ExecutablePath)
	if len(environment) != 0 {
		command.Env = append(os.Environ(), environment...)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, err
	}
	stderr := &stderrSignal{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	supervisor := &Supervisor{
		config:       config,
		command:      command,
		stdin:        stdin,
		stdout:       stdout,
		stderr:       stderr,
		cancel:       cancel,
		waited:       make(chan error, 1),
		nextSequence: 1,
	}
	go func() {
		supervisor.waited <- command.Wait()
		close(supervisor.waited)
	}()
	if err := supervisor.describe(ctx); err != nil {
		return nil, errors.Join(err, supervisor.abortAndWait())
	}
	return supervisor, nil
}

// describe 验证 exact build identity、contract version 与 frame ceiling。
func (supervisor *Supervisor) describe(ctx context.Context) error {
	receipt, err := supervisor.exchange(ctx, KindDescribeRequest, nil)
	if err != nil {
		return err
	}
	defer clear(receipt.Payload)
	if receipt.Kind != KindDescribeReceipt ||
		len(receipt.Payload) != describePayloadBytes ||
		string(receipt.Payload[:buildIdentityBytes]) !=
			string(supervisor.config.ExpectedBuildIdentity[:]) ||
		receipt.Payload[buildIdentityBytes] != contractVersion ||
		binary.BigEndian.Uint32(receipt.Payload[buildIdentityBytes+1:]) != maximumFrameBytes {
		return ErrIdentityMismatch
	}
	return nil
}

// SessionEvent 是 child 完成真实 UDP handshake 后返回的 closed 结果。
type SessionEvent struct {
	// ClientSlot 是已建立 session 的 run-local slot。
	ClientSlot uint8
}

// StartSession 一次性交付 credential；任何路径都会清零 source 和 encoded payload。
func (supervisor *Supervisor) StartSession(ctx context.Context, start SessionStart) (SessionEvent, error) {
	supervisor.mutex.Lock()
	if supervisor.credentialDelivered {
		supervisor.mutex.Unlock()
		if start.Credential != nil {
			start.Credential.Clear()
		}
		return SessionEvent{}, ErrCredentialAlreadyDelivered
	}
	supervisor.credentialDelivered = true
	supervisor.mutex.Unlock()
	payload, err := encodeSessionStart(start)
	if err != nil {
		return SessionEvent{}, err
	}
	defer clear(payload)
	receipt, err := supervisor.exchange(ctx, KindSessionStartRequest, payload)
	if err != nil {
		return SessionEvent{}, err
	}
	defer clear(receipt.Payload)
	if receipt.Kind != KindSessionEvent {
		supervisor.abort()
		return SessionEvent{}, ErrUnexpectedReceipt
	}
	if len(receipt.Payload) == sessionFailurePayloadBytes &&
		receipt.Payload[0] == start.ClientSlot &&
		receipt.Payload[1] == sessionFailedEvent {
		failure := SessionStartFailure(receipt.Payload[2])
		supervisor.abort()
		if failure.Valid() {
			return SessionEvent{}, &SessionStartError{
				Failure: failure,
			}
		}
		return SessionEvent{}, ErrUnexpectedReceipt
	}
	if len(receipt.Payload) != sessionSuccessPayloadBytes ||
		receipt.Payload[0] != start.ClientSlot ||
		receipt.Payload[1] != sessionEstablishedEvent {
		supervisor.abort()
		return SessionEvent{}, ErrUnexpectedReceipt
	}
	return SessionEvent{ClientSlot: receipt.Payload[0]}, nil
}

// Exchange 发送非 secret workload/network/poll request并验证同 sequence receipt。
func (supervisor *Supervisor) Exchange(ctx context.Context, kind Kind, payload []byte) (Frame, error) {
	if kind == KindSessionStartRequest || kind == KindDescribeRequest ||
		kind == KindShutdownRequest {
		return Frame{}, ErrInvalidFrame
	}
	return supervisor.exchange(ctx, kind, payload)
}

// exchange 串行执行一个 request/receipt round trip。
func (supervisor *Supervisor) exchange(ctx context.Context, kind Kind, payload []byte) (Frame, error) {
	if ctx == nil {
		return Frame{}, ErrInvalidFrame
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return Frame{}, ErrInvalidFrame
	}
	supervisor.mutex.Lock()
	defer supervisor.mutex.Unlock()
	if supervisor.closed || supervisor.nextSequence == 0 {
		return Frame{}, ErrClosed
	}
	sequence := supervisor.nextSequence
	if err := writeFrame(supervisor.stdin, Frame{
		Kind: kind, Sequence: sequence, Deadline: deadline, Payload: payload,
	}, time.Now()); err != nil {
		supervisor.abortLocked()
		return Frame{}, err
	}
	type readResult struct {
		frame Frame
		err   error
	}
	resultChannel := make(chan readResult, 1)
	go func() {
		frame, err := readFrame(supervisor.stdout)
		resultChannel <- readResult{frame: frame, err: err}
	}()
	var result readResult
	select {
	case result = <-resultChannel:
	case <-ctx.Done():
		supervisor.abortLocked()
		return Frame{}, ctx.Err()
	}
	if result.err != nil || result.frame.Sequence != sequence {
		clear(result.frame.Payload)
		supervisor.abortLocked()
		if result.err != nil {
			return Frame{}, result.err
		}
		return Frame{}, ErrUnexpectedReceipt
	}
	if sequence == ^uint64(0) {
		supervisor.nextSequence = 0
	} else {
		supervisor.nextSequence++
	}
	return result.frame, nil
}

// Close 请求 child 正常关闭；deadline/协议失败会取消 owned process。
func (supervisor *Supervisor) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidFrame
	}
	supervisor.mutex.Lock()
	if supervisor.closed {
		supervisor.mutex.Unlock()
		return nil
	}
	supervisor.mutex.Unlock()
	receipt, err := supervisor.exchange(ctx, KindShutdownRequest, nil)
	if err == nil {
		if receipt.Kind != KindShutdownReceipt || len(receipt.Payload) != 0 {
			err = ErrUnexpectedReceipt
		}
		clear(receipt.Payload)
	}
	if err != nil {
		supervisor.abort()
	} else {
		supervisor.mutex.Lock()
		supervisor.closed = true
		_ = supervisor.stdin.Close()
		supervisor.mutex.Unlock()
	}
	select {
	case waitErr := <-supervisor.waited:
		_ = supervisor.stdout.Close()
		supervisor.cancel()
		if err != nil {
			return err
		}
		if waitErr != nil {
			return fmt.Errorf("battle protocol client exited unsuccessfully")
		}
		return nil
	case <-ctx.Done():
		supervisor.cancel()
		_ = supervisor.stdout.Close()
		return ctx.Err()
	}
}

// HadStderr 报告 child 是否写过诊断，不返回可能含敏感数据的原文。
func (supervisor *Supervisor) HadStderr() bool {
	supervisor.stderr.mutex.Lock()
	defer supervisor.stderr.mutex.Unlock()
	return supervisor.stderr.bytes != 0
}

// abort 幂等关闭 pipe 并取消 process。
func (supervisor *Supervisor) abort() {
	supervisor.mutex.Lock()
	defer supervisor.mutex.Unlock()
	supervisor.abortLocked()
}

// abortAndWait 在 startup rollback 中取消并等待唯一 child，禁止把异步清理留给 caller。
func (supervisor *Supervisor) abortAndWait() error {
	supervisor.abort()
	rollbackContext, cancel := context.WithTimeout(
		context.Background(),
		startupRollbackTimeout,
	)
	defer cancel()
	select {
	case <-supervisor.waited:
		_ = supervisor.stdout.Close()
		return nil
	case <-rollbackContext.Done():
		return ErrCleanupTimeout
	}
}

// abortLocked 在持锁状态终结 stdin/stdout 和 child context。
func (supervisor *Supervisor) abortLocked() {
	if supervisor.closed {
		return
	}
	supervisor.closed = true
	_ = supervisor.stdin.Close()
	_ = supervisor.stdout.Close()
	supervisor.cancel()
}
