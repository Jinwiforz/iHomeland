package app

import "errors"

// ErrSignalShutdown 标识由受支持 OS signal 发起的正常受控关闭。
var ErrSignalShutdown = errors.New("signal shutdown requested")

// ResultKind 是 cmd/server 映射退出码所需的稳定运行结果。
type ResultKind string

const (
	// ResultClean 表示受控请求完成且全部资源在 deadline 内释放。
	ResultClean ResultKind = "clean"
	// ResultConfigError 表示配置在任何副作用前被拒绝。
	ResultConfigError ResultKind = "config_error"
	// ResultStartupError 表示组件初始化失败并已尝试回滚。
	ResultStartupError ResultKind = "startup_error"
	// ResultRuntimeError 表示必需任务或进程 context 异常终止。
	ResultRuntimeError ResultKind = "runtime_error"
	// ResultShutdownError 表示关闭失败或超过总 deadline。
	ResultShutdownError ResultKind = "shutdown_error"
)

// Result 保存对外稳定分类与仅供内部诊断的 cause。
type Result struct {
	// Kind 决定退出码与低基数 metrics 标签。
	Kind ResultKind
	// Err 保留内部 cause；clean 结果必须为空。
	Err error
}

// ExitCode 将运行结果映射为稳定进程码。
func (result Result) ExitCode() int {
	switch result.Kind {
	case ResultClean:
		return 0
	case ResultConfigError:
		return 2
	case ResultStartupError:
		return 3
	case ResultRuntimeError:
		return 4
	default:
		return 5
	}
}
