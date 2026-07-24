package process

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/simulationcontrol"
)

const (
	// createNewProcessGroup 防止发送给 Go parent group 的 Ctrl-Break 越过受控 shutdown 协议终止 child。
	createNewProcessGroup = 0x00000200
)

// DiagnosticSink 接收已经截断和去控制字符的 child stderr 行。
type DiagnosticSink interface {
	// ObserveSimulationDiagnostic 只允许记录低敏稳定诊断。
	ObserveSimulationDiagnostic(string)
}

// Config 固定启动精确 simulation child 所需的本机证据。
type Config struct {
	// BinaryPath 是 ihomeland-sim-server 的绝对路径。
	BinaryPath string
	// BinarySHA256 是 binary 内容的预期 digest。
	BinarySHA256 simulationcontrol.Digest
	// QualificationReceiptPath 是 B0.3 gate receipt 的绝对路径。
	QualificationReceiptPath string
	// QualificationReceiptSHA256 是 receipt 内容的预期 digest。
	QualificationReceiptSHA256 simulationcontrol.Digest
	// RequestTimeout 是每个已发送 frame turn 的 hard deadline。
	RequestTimeout time.Duration
	// ShutdownTimeout 是等待精确 child exit 的 hard deadline。
	ShutdownTimeout time.Duration
	// StderrLineLimit 是单条低敏诊断最大 bytes。
	StderrLineLimit int
}

// Validate 拒绝相对路径、缺失 digest 和 silent deadline defaults。
func (config Config) Validate() error {
	if !filepath.IsAbs(config.BinaryPath) ||
		!filepath.IsAbs(config.QualificationReceiptPath) ||
		!config.BinarySHA256.Valid() ||
		!config.QualificationReceiptSHA256.Valid() ||
		config.RequestTimeout <= 0 || config.ShutdownTimeout <= 0 ||
		config.StderrLineLimit < 64 || config.StderrLineLimit > 4096 {
		return errors.New("simulation process config is invalid")
	}
	return nil
}

// Owner 唯一持有 child process、pipes、session 与 wait goroutine。
type Owner struct {
	// command 持有精确 OS process handle。
	command *exec.Cmd
	// session 是 controller 使用的私有协议 owner。
	session *simulationcontrol.Session
	// stdin 用于正常关闭写端。
	stdin io.WriteCloser
	// done 在 cmd.Wait 返回后关闭。
	done chan struct{}
	// waitMutex 保护 process exit 结果。
	waitMutex sync.Mutex
	// waitErr 保存 child exit status。
	waitErr error
	// shutdownTimeout 限制 owner 等待。
	shutdownTimeout time.Duration
}

// Start 校验 binary/receipt digest 后创建无 listener 的继承 stdio child。
func Start(config Config, nonce simulationcontrol.Digest, proposals simulationcontrol.ProposalHandler, diagnostics DiagnosticSink) (*Owner, error) {
	if config.Validate() != nil || !nonce.Valid() || proposals == nil || diagnostics == nil {
		return nil, errors.New("simulation process start input is invalid")
	}
	if err := verifyFile(config.BinaryPath, config.BinarySHA256); err != nil {
		return nil, fmt.Errorf("simulation binary verification failed: %w", err)
	}
	if err := verifyFile(config.QualificationReceiptPath, config.QualificationReceiptSHA256); err != nil {
		return nil, fmt.Errorf("simulation qualification verification failed: %w", err)
	}
	command := exec.Command(config.BinaryPath, "--control-stdio")
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup,
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open simulation stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open simulation stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("open simulation stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start simulation child: %w", err)
	}
	pipes := &pipeCloser{stdin: stdin, stdout: stdout}
	session, err := simulationcontrol.NewSession(
		stdout,
		stdin,
		pipes,
		nonce,
		config.RequestTimeout,
		proposals,
	)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, err
	}
	owner := &Owner{
		command:         command,
		session:         session,
		stdin:           stdin,
		done:            make(chan struct{}),
		shutdownTimeout: config.ShutdownTimeout,
	}
	go pumpStderr(stderr, config.StderrLineLimit, diagnostics)
	go owner.wait()
	return owner, nil
}

// Session 返回 controller bootstrap 使用的唯一 session。
func (owner *Owner) Session() *simulationcontrol.Session {
	if owner == nil {
		return nil
	}
	return owner.session
}

// ProcessID 返回仅供受控本机生命周期/验收关联的 OS process ID。
func (owner *Owner) ProcessID() int {
	if owner == nil || owner.command == nil || owner.command.Process == nil {
		return 0
	}
	return owner.command.Process.Pid
}

// Done 在精确 child exit 后关闭。
func (owner *Owner) Done() <-chan struct{} {
	if owner == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return owner.done
}

// Err 返回 child exit 结果；运行中返回 nil。
func (owner *Owner) Err() error {
	if owner == nil {
		return errors.New("simulation process owner is nil")
	}
	owner.waitMutex.Lock()
	defer owner.waitMutex.Unlock()
	return owner.waitErr
}

// Terminate 在正常 shutdown owner 未完成时只终止精确 child process。
func (owner *Owner) Terminate(ctx context.Context) error {
	if owner == nil || ctx == nil {
		return errors.New("simulation process terminate input is invalid")
	}
	select {
	case <-owner.done:
		return owner.Err()
	default:
	}
	if err := owner.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("terminate simulation child: %w", err)
	}
	timer := time.NewTimer(owner.shutdownTimeout)
	defer timer.Stop()
	select {
	case <-owner.done:
		return owner.Err()
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("simulation child exit deadline exceeded")
	}
}

// wait 回收 OS process handle 并发布唯一 exit result。
func (owner *Owner) wait() {
	err := owner.command.Wait()
	owner.waitMutex.Lock()
	owner.waitErr = err
	owner.waitMutex.Unlock()
	_ = owner.session.Close()
	close(owner.done)
}

// pipeCloser 同时关闭 parent 持有的 stdin/stdout，不触碰其他进程 handle。
type pipeCloser struct {
	// stdin 是 parent 写端。
	stdin io.Closer
	// stdout 是 parent 读端。
	stdout io.Closer
	// once 保证 close 幂等。
	once sync.Once
	// err 保存首次 close 的组合结果。
	err error
}

// Close 关闭两个 pipe handle。
func (closer *pipeCloser) Close() error {
	closer.once.Do(func() {
		closer.err = errors.Join(closer.stdin.Close(), closer.stdout.Close())
	})
	return closer.err
}

// verifyFile 对 regular file 内容执行 exact SHA-256。
func verifyFile(path string, expected simulationcontrol.Digest) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("verified path is not regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != expected.String() {
		return errors.New("verified file digest drifted")
	}
	return nil
}

// pumpStderr 按行截断、移除控制字符并避免反压 child。
func pumpStderr(reader io.ReadCloser, limit int, sink DiagnosticSink) {
	defer reader.Close()
	buffer := make([]byte, 4096)
	var pending strings.Builder
	emit := func() {
		if pending.Len() == 0 {
			return
		}
		value := pending.String()
		if len(value) > limit {
			value = value[:limit]
		}
		value = strings.Map(func(character rune) rune {
			if character == '\t' || character >= 0x20 && character != 0x7f {
				return character
			}
			return -1
		}, value)
		sink.ObserveSimulationDiagnostic(value)
		pending.Reset()
	}
	for {
		count, err := reader.Read(buffer)
		for _, character := range string(buffer[:count]) {
			if character == '\n' {
				emit()
				continue
			}
			if pending.Len() < limit*2 {
				pending.WriteRune(character)
			}
		}
		if err != nil {
			emit()
			return
		}
	}
}
