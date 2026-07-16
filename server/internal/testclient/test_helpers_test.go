package testclient

import (
	"path/filepath"
	"runtime"
	"testing"
)

// repositoryRoot 从当前源文件定位工作树根，不依赖调用方当前目录。
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
}
