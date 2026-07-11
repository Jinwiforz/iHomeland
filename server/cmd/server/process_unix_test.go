//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// prepareProcess 创建独立 Unix process group，防止测试 signal 影响父进程。
func prepareProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// requestProcessShutdown 向测试服务端 process group 发送 SIGTERM。
func requestProcessShutdown(process *os.Process) error {
	return syscall.Kill(-process.Pid, syscall.SIGTERM)
}
