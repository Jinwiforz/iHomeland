//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const (
	// createNewProcessGroup 隔离测试子进程，防止 Ctrl-Break 同时终止 Go test 父进程。
	createNewProcessGroup = 0x00000200
	// ctrlBreakEvent 比 Ctrl-C 更适合发送给指定 process group，避免依赖前台窗口焦点。
	ctrlBreakEvent = 1
)

// prepareProcess 创建独立 Windows process group，使 Ctrl-Break 只发送给测试服务端。
func prepareProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// requestProcessShutdown 使用 Windows console API 模拟受控终止请求。
func requestProcessShutdown(process *os.Process) error {
	generateConsoleCtrlEvent := syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")
	result, _, callErr := generateConsoleCtrlEvent.Call(ctrlBreakEvent, uintptr(process.Pid))
	if result == 0 {
		return fmt.Errorf("GenerateConsoleCtrlEvent: %w", callErr)
	}
	return nil
}
