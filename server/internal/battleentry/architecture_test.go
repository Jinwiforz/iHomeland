package battleentry

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPackageDoesNotDependOnTransportStorageOrGeneratedContracts(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if forbiddenBattleEntryImport(path) {
				t.Fatalf("%s imports forbidden application dependency %s", file, path)
			}
		}
	}
}

func forbiddenBattleEntryImport(path string) bool {
	return strings.Contains(path, "/transport/") ||
		strings.Contains(path, "/storage/") ||
		strings.Contains(path, "/generated/") ||
		strings.Contains(path, "github.com/gin-gonic/gin") ||
		strings.Contains(path, "github.com/redis/")
}
