package app

import (
	"errors"
	"sync/atomic"
)

// ReadinessState 是只允许向关闭方向推进的进程可服务状态。
type ReadinessState uint32

const (
	// ReadinessStarting 表示配置有效但必需组件尚未全部启动。
	ReadinessStarting ReadinessState = iota
	// ReadinessReady 表示进程可以接收公开业务。
	ReadinessReady
	// ReadinessDraining 表示进程已撤销就绪且正在释放资源。
	ReadinessDraining
	// ReadinessStopped 表示生命周期已经结束。
	ReadinessStopped
)

// String 返回诊断 API 使用的稳定小写状态名。
func (state ReadinessState) String() string {
	switch state {
	case ReadinessStarting:
		return "starting"
	case ReadinessReady:
		return "ready"
	case ReadinessDraining:
		return "draining"
	case ReadinessStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// Readiness 使用原子状态使诊断 handler 不需要获取 lifecycle 锁。
type Readiness struct {
	// state 使用 uint32 原子保存枚举，避免诊断读与关闭写产生锁竞争。
	state atomic.Uint32
}

// NewReadiness 创建 starting 状态的 readiness owner。
func NewReadiness() *Readiness { return new(Readiness) }

// State 返回一次原子状态快照。
func (readiness *Readiness) State() ReadinessState {
	return ReadinessState(readiness.state.Load())
}

// DiagnosticState 返回诊断 handler 所需的稳定名称与 ready 判断，不暴露状态迁移能力。
func (readiness *Readiness) DiagnosticState() (string, bool) {
	state := readiness.State()
	return state.String(), state == ReadinessReady
}

// MarkReady 只允许 starting 进入 ready。
func (readiness *Readiness) MarkReady() error {
	if !readiness.state.CompareAndSwap(uint32(ReadinessStarting), uint32(ReadinessReady)) {
		return errors.New("readiness can enter ready only from starting")
	}
	return nil
}

// BeginDraining 从 starting 或 ready 单向进入 draining；重复调用保持幂等。
func (readiness *Readiness) BeginDraining() error {
	for {
		current := readiness.State()
		switch current {
		case ReadinessStarting, ReadinessReady:
			if readiness.state.CompareAndSwap(uint32(current), uint32(ReadinessDraining)) {
				return nil
			}
		case ReadinessDraining:
			return nil
		default:
			return errors.New("stopped readiness cannot begin draining")
		}
	}
}

// MarkStopped 只允许 draining 进入 stopped，并允许关闭清理重复确认。
func (readiness *Readiness) MarkStopped() error {
	if readiness.State() == ReadinessStopped {
		return nil
	}
	if !readiness.state.CompareAndSwap(uint32(ReadinessDraining), uint32(ReadinessStopped)) {
		return errors.New("readiness can stop only from draining")
	}
	return nil
}
