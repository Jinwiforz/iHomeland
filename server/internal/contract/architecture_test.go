package contract

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestWorldDomainPackagesRemainProtocolAgnostic 保护 domain/application core 不依赖生成协议或 transport adapter。
// 协议类型只能在 adapter 边界转换，不能反向成为 PersonalWorld、placement 或 VisitSession 的模型。
func TestWorldDomainPackagesRemainProtocolAgnostic(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, packageName := range []string{"personalworld", "placement", "visitsession"} {
		paths, err := filepath.Glob(filepath.Join(root, "server", "internal", packageName, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, imported := range file.Imports {
				value, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					t.Fatalf("unquote import in %s: %v", path, err)
				}
				for _, forbidden := range []string{"/generated/proto/", "/transport/", "gin-gonic", "websocket"} {
					if strings.Contains(strings.ToLower(value), forbidden) {
						t.Fatalf("%s imports forbidden protocol/transport dependency %q", path, value)
					}
				}
			}
		}
	}
}
