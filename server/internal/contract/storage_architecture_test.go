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

// TestAccountSessionStorageAdaptersRemainBorrowedInfrastructure 保护adapter不反向依赖transport或创建第二份资源。
func TestAccountSessionStorageAdaptersRemainBorrowedInfrastructure(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, packageName := range []string{"account", "session"} {
		paths, globErr := filepath.Glob(filepath.Join(root, "server", "internal", "storage", packageName, "*.go"))
		if globErr != nil {
			t.Fatal(globErr)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if parseErr != nil {
				t.Fatalf("parse %s: %v", path, parseErr)
			}
			aliases := make(map[string]string)
			for _, imported := range file.Imports {
				value, unquoteErr := strconv.Unquote(imported.Path.Value)
				if unquoteErr != nil {
					t.Fatalf("unquote import in %s: %v", path, unquoteErr)
				}
				for _, forbidden := range []string{"/generated/", "/transport/", "gin-gonic", "websocket"} {
					if strings.Contains(strings.ToLower(value), forbidden) {
						t.Fatalf("%s imports forbidden protocol/transport dependency %q", path, value)
					}
				}
				name := filepath.Base(value)
				if imported.Name != nil {
					name = imported.Name.Name
				}
				aliases[name] = value
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.GoStmt:
					t.Fatalf("%s starts an unowned goroutine", path)
				case *ast.CallExpr:
					selector, ok := value.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					qualifier, ok := selector.X.(*ast.Ident)
					if !ok {
						return true
					}
					importPath := aliases[qualifier.Name]
					if importPath == "database/sql" && (selector.Sel.Name == "Open" || selector.Sel.Name == "OpenDB") {
						t.Fatalf("%s creates an independent MySQL pool", path)
					}
					if importPath == "github.com/redis/go-redis/v9" && selector.Sel.Name == "NewClient" {
						t.Fatalf("%s creates an independent Redis client", path)
					}
				}
				return true
			})
		}
	}
}

// TestCompositionRootDoesNotWireAccountSessionBusinessGraph 保护当前change只应用migration而不提前开放业务能力。
func TestCompositionRootDoesNotWireAccountSessionBusinessGraph(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{filepath.Join(root, "server", "internal", "app"), filepath.Join(root, "server", "cmd", "server")} {
		paths, globErr := filepath.Glob(filepath.Join(directory, "*.go"))
		if globErr != nil {
			t.Fatal(globErr)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if parseErr != nil {
				t.Fatalf("parse %s: %v", path, parseErr)
			}
			for _, imported := range file.Imports {
				value, _ := strconv.Unquote(imported.Path.Value)
				if strings.Contains(value, "/internal/storage/account") || strings.Contains(value, "/internal/storage/session") {
					t.Fatalf("%s wires account/session business adapter before public transport change", path)
				}
			}
		}
	}
}
