package testclient

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var allowedExternalImports = []string{
	"github.com/coder/websocket",
	"google.golang.org/protobuf",
	"github.com/jinwiforz/ihomeland/server/internal/generated/proto/",
}

// TestImportBoundary 禁止资格客户端接触服务端内部对象图或未批准 runtime。
func TestImportBoundary(t *testing.T) {
	directory := filepath.Join(repositoryRoot(t), "server", "internal", "testclient")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read testclient directory: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", path, err)
			}
			if !allowedTestClientImport(importPath) {
				t.Errorf("%s imports forbidden package %q", entry.Name(), importPath)
			}
		}
	}
}

// TestPackageHasNoLinkname 拒绝绕过 import 边界进入服务端私有实现。
func TestPackageHasNoLinkname(t *testing.T) {
	directory := filepath.Join(repositoryRoot(t), "server", "internal", "testclient")
	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, directory, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse testclient package: %v", err)
	}
	for _, parsedPackage := range packages {
		for filename, file := range parsedPackage.Files {
			for _, group := range file.Comments {
				if strings.Contains(group.Text(), "go:linkname") {
					t.Errorf("%s uses forbidden go:linkname", filename)
				}
			}
			ast.Inspect(file, func(node ast.Node) bool { return node != nil })
		}
	}
}

// allowedTestClientImport 接受标准库和三类明确锁定的外部依赖。
func allowedTestClientImport(importPath string) bool {
	if !strings.Contains(importPath, ".") {
		return true
	}
	for _, prefix := range allowedExternalImports {
		if importPath == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(importPath, prefix) {
			return true
		}
	}
	return false
}
