package contract

import (
	"go/parser"
	"go/token"
	"io/fs"
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

// TestGinRemainsInsideHTTPTransport 防止路由框架渗入application、domain、storage或Composition Root。
func TestGinRemainsInsideHTTPTransport(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// 显式遍历仓库Go文件以覆盖未来目录。
	err = filepath.Walk(filepath.Join(root, "server"), func(path string, info fs.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imported := range file.Imports {
			value, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if value == "github.com/gin-gonic/gin" && !strings.Contains(filepath.ToSlash(path), "/internal/transport/httpapi/") {
				t.Fatalf("%s imports Gin outside HTTP transport", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
