package visitsession

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestProductionPackageHasNoEarlyInfrastructureWiring 保护 pure Go core 不接入 backend、transport、timer 或 mutable global。
func TestProductionPackageHasNoEarlyInfrastructureWiring(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imported := range file.Imports {
			value, _ := strconv.Unquote(imported.Path.Value)
			for _, forbidden := range []string{"gin", "websocket", "mysql", "redis", "/generated/", "party", "room", "activity"} {
				if strings.Contains(strings.ToLower(value), forbidden) {
					t.Fatalf("%s imports forbidden dependency %q", path, value)
				}
			}
		}
		for _, declaration := range file.Decls {
			if general, ok := declaration.(*ast.GenDecl); ok && general.Tok == token.VAR {
				t.Fatalf("%s declares package mutable state", path)
			}
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil && function.Name.IsExported() && function.Name.Name == "NewJoinQualification" {
				t.Fatalf("%s exposes forgeable join qualification", path)
			}
			if function, ok := declaration.(*ast.FuncDecl); ok && (function.Name.Name == "AuthorizeGameplay" || function.Name.Name == "AllowGameplay") {
				t.Fatalf("%s exposes a generic gameplay authorizer", path)
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

// TestVisitorControlPlanePermissions 证明 Visitor 不能 invite、kick 或 close。
func TestVisitorControlPlanePermissions(t *testing.T) {
	t.Parallel()
	fixture := newAggregateFixture(t, 1)
	visit := openFixture(t, fixture)
	if _, _, err := visit.CreateInvite(fixture.visitorA, mustInviteID(t, "vinv_permission"), fixture.visitorB.playerID, fixture.createdAt.Add(time.Minute), fixture.createdAt); !IsErrorCode(err, ErrorCodeForbidden) {
		t.Fatalf("visitor invite: %v", err)
	}
	if _, _, err := visit.Kick(fixture.visitorA, fixture.visitorB.playerID); !IsErrorCode(err, ErrorCodeForbidden) {
		t.Fatalf("visitor kick: %v", err)
	}
	if _, _, err := visit.Close(fixture.visitorA); !IsErrorCode(err, ErrorCodeForbidden) {
		t.Fatalf("visitor close: %v", err)
	}
}
