package worldadmission

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

// TestProductionPackageHasNoInfrastructureWiring 保护 admission core 不接入 transport、storage 或后台生命周期。
func TestProductionPackageHasNoInfrastructureWiring(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		for _, imported := range file.Imports {
			value, _ := strconv.Unquote(imported.Path.Value)
			for _, forbidden := range []string{"gin", "websocket", "mysql", "redis", "/storage/", "/generated/", "/transport/", "/app/"} {
				if strings.Contains(strings.ToLower(value), forbidden) {
					t.Fatalf("%s imports forbidden dependency %q", path, value)
				}
			}
		}
		for _, declaration := range file.Decls {
			if general, ok := declaration.(*ast.GenDecl); ok && general.Tok == token.VAR {
				t.Fatalf("%s declares package mutable state", path)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.GoStmt:
				t.Fatalf("%s starts a goroutine", path)
			case *ast.CallExpr:
				selector, ok := value.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				packageName, ok := selector.X.(*ast.Ident)
				if ok && packageName.Name == "time" && (selector.Sel.Name == "NewTimer" || selector.Sel.Name == "NewTicker" || selector.Sel.Name == "After" || selector.Sel.Name == "AfterFunc") {
					t.Fatalf("%s starts an internal timer", path)
				}
			}
			return true
		})
	}
}

// TestProductionCompositionWiresAdmissionOnlyInPublicRuntime 保证credential owner只在唯一公开graph接线。
func TestProductionCompositionWiresAdmissionOnlyInPublicRuntime(t *testing.T) {
	t.Parallel()
	found := false
	for _, root := range []string{filepath.Join("..", "..", "cmd"), filepath.Join("..", "app")} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if parseErr != nil {
				return parseErr
			}
			for _, imported := range file.Imports {
				value, _ := strconv.Unquote(imported.Path.Value)
				if strings.Contains(value, "/internal/worldadmission") || strings.Contains(value, "/internal/storage/worldadmission") {
					if filepath.Base(path) != "public_runtime.go" {
						t.Fatalf("%s wires world admission outside public runtime", path)
					}
					found = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("public runtime is missing world admission production wiring")
	}
}
