package visitsession

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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

// TestQualificationHydrationCallersAreRestricted 限定production受信bridge只能由worldadmission owner调用。
func TestQualificationHydrationCallersAreRestricted(t *testing.T) {
	t.Parallel()
	trustedSource, err := filepath.Abs("admission.go")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.Walk(filepath.Clean(filepath.Join("..", "..")), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		absolutePath, absoluteErr := filepath.Abs(path)
		if absoluteErr != nil {
			return absoluteErr
		}
		if absolutePath == trustedSource {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		aliases := map[string]bool{}
		for _, imported := range file.Imports {
			value, _ := strconv.Unquote(imported.Path.Value)
			if value != "github.com/jinwiforz/ihomeland/server/internal/visitsession" {
				continue
			}
			alias := "visitsession"
			if imported.Name != nil {
				alias = imported.Name.Name
			}
			aliases[alias] = true
		}
		callsHydration := false
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch function := call.Fun.(type) {
			case *ast.Ident:
				if (function.Name == "HydrateJoinQualification" || function.Name == "HydrateReconnectQualification") && (file.Name.Name == "visitsession" || aliases["."]) {
					callsHydration = true
				}
			case *ast.SelectorExpr:
				identifier, ok := function.X.(*ast.Ident)
				if ok && aliases[identifier.Name] && (function.Sel.Name == "HydrateJoinQualification" || function.Sel.Name == "HydrateReconnectQualification") {
					callsHydration = true
				}
			}
			return true
		})
		if callsHydration && !strings.Contains(filepath.ToSlash(absolutePath), "/internal/worldadmission/") {
			t.Fatalf("%s calls trusted qualification hydration outside worldadmission owner", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
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
