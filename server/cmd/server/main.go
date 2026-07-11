// Command server 启动 iHomeland Go 服务端唯一进程入口。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jinwiforz/ihomeland/server/internal/app"
	"github.com/jinwiforz/ihomeland/server/internal/buildinfo"
)

// forcedExitCode 表示第二次终止请求放弃继续等待 graceful shutdown。
const forcedExitCode = 6

// main 是唯一调用 os.Exit 的进程边界，确保深层 defer 与回滚始终执行。
func main() {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, signals))
}

// run 处理 bootstrap flag 和两阶段 signal，并按命令行语义隔离正常日志与错误诊断。
//
// output 承载可采集的结构化运行日志，errorOutput 只承载 flag、强制退出和最终非零结果；
// 分离 writer 让 IDE 与进程管理器不会把正常 INFO 误判为 stderr。
func run(arguments []string, output io.Writer, errorOutput io.Writer, signals <-chan os.Signal) int {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	configPath := flags.String("config", "", "path to the server YAML configuration")
	if err := flags.Parse(arguments); err != nil {
		return app.Result{Kind: app.ResultConfigError, Err: err}.ExitCode()
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errorOutput, "server does not accept positional arguments")
		return app.Result{Kind: app.ResultConfigError}.ExitCode()
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	// 单元素缓冲允许 app.Run 在主循环处理 signal 时完成退出，不会因结果暂时无人接收而阻塞清理。
	results := make(chan app.Result, 1)
	go func() {
		results <- app.Run(ctx, app.Options{ConfigPath: *configPath, Output: output, BuildInfo: buildinfo.Current()})
	}()

	shutdownRequested := false
	for {
		select {
		case result := <-results:
			if message := app.ResultMessage(result); message != "" {
				slog.New(slog.NewTextHandler(errorOutput, nil)).Error("server process failed", "result", result.Kind, "error", message)
			}
			return result.ExitCode()
		case <-signals:
			if shutdownRequested {
				slog.New(slog.NewTextHandler(errorOutput, nil)).Error("second termination signal received; forcing exit", "result", "forced")
				return forcedExitCode
			}
			shutdownRequested = true
			cancel(app.ErrSignalShutdown)
		}
	}
}
