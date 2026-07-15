package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestWebSocketControlKeepsTransportBoundary 防止control adapter依赖storage或业务owner。
func TestWebSocketControlKeepsTransportBoundary(t *testing.T) {
	t.Parallel()
	root := testRepositoryRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "server", "internal", "transport", "wscontrol", "*.go"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("locate websocket control package: paths=%d err=%v", len(paths), err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, imported := range file.Imports {
			value, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatal(unquoteErr)
			}
			for _, forbidden := range []string{"/internal/storage/", "/internal/account", "/internal/personalworld", "/internal/placement", "/internal/visitsession", "/internal/worldadmission", "/internal/worldentry", "gin-gonic"} {
				if strings.Contains(strings.ToLower(value), forbidden) {
					t.Fatalf("%s imports forbidden dependency %q", path, value)
				}
			}
		}
	}
}

// TestProductionCompositionUsesRealWebSocketInvalidator 防止正式graph退回无连接占位实现。
func TestProductionCompositionUsesRealWebSocketInvalidator(t *testing.T) {
	t.Parallel()
	root := testRepositoryRoot(t)
	path := filepath.Join(root, "server", "internal", "app", "public_runtime.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var foundRegistry bool
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if ok && packageName.Name == "wscontrol" && selector.Sel.Name == "NewRegistry" {
			foundRegistry = true
		}
		if ok && packageName.Name == "session" && selector.Sel.Name == "NoActiveRealtimeConnections" {
			t.Fatal("production graph uses placeholder realtime invalidator")
		}
		return true
	})
	if !foundRegistry {
		t.Fatal("production graph does not construct websocket control registry")
	}
}
