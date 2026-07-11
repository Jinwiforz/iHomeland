// Command contracttool 在不启动 listener 的前提下验证并重新生成协议治理 artifacts。
//
// 命令只接受一个明确动作并以退出码表达结果，供本地脚本和 CI 复用。它不会自动修复验证
// 失败，也不会连接数据库或网络 listener，因此契约阶段可以在隔离环境中稳定执行。
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
	"github.com/jinwiforz/ihomeland/server/internal/fixtures"
)

// main 将单个显式非交互命令映射为确定性的契约操作。
// flag 解析、仓库发现和错误输出集中在此处，contract 与 fixtures package 保持可直接测试。
func main() {
	root := flag.String("root", "", "absolute or relative repository root")
	openAPISchema := flag.String("openapi-schema", "", "path to the pinned official OpenAPI JSON Schema")
	flag.Parse()
	if flag.NArg() != 1 {
		fatal("usage: contracttool [-root path] [-openapi-schema path] <validate|fixtures|verify-fixtures>")
	}
	repositoryRoot, err := resolveRoot(*root)
	if err != nil {
		fatal(err.Error())
	}
	switch flag.Arg(0) {
	case "validate":
		err = contract.Validate(repositoryRoot, *openAPISchema)
	case "fixtures":
		err = fixtures.Generate(repositoryRoot)
	case "verify-fixtures":
		err = fixtures.Verify(repositoryRoot)
	default:
		err = fmt.Errorf("unknown command %q", flag.Arg(0))
	}
	if err != nil {
		fatal(err.Error())
	}
}

// resolveRoot 接受显式路径，未提供时从当前目录向上发现仓库根目录。
//
// 显式路径只负责转换为绝对路径，后续操作会验证所需文件；自动发现使用 versions.yaml 作为
// 稳定仓库标记，并在到达文件系统根目录后失败，避免误对上级目录执行生成。
func resolveRoot(candidate string) (string, error) {
	if candidate != "" {
		return filepath.Abs(candidate)
	}
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "versions.yaml")); err == nil {
			return current, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect repository marker in %s: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("repository root containing versions.yaml was not found")
		}
		current = parent
	}
}

// fatal 输出稳定诊断并向脚本与 CI 返回非零退出码。
// 该函数必然终止进程，只用于 main package 边界；可复用 package 必须返回 error 以保持可测试。
func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
