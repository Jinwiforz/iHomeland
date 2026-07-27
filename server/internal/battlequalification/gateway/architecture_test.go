package gateway

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestGatewayImportScope 禁止 qualification gateway 引入 production transport/gameplay。
func TestGatewayImportScope(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate gateway package")
	}
	packageRoot := filepath.Dir(currentFile)
	files, err := filepath.Glob(filepath.Join(packageRoot, "*.go"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	allowed := map[string]struct{}{
		"bytes": {}, "container/heap": {}, "context": {}, "crypto/sha256": {},
		"encoding/hex": {}, "errors": {}, "fmt": {}, "go/parser": {},
		"go/token": {}, "math": {}, "math/bits": {}, "net": {}, "net/netip": {},
		"path/filepath": {}, "reflect": {}, "runtime": {}, "strconv": {},
		"strings": {}, "sync": {}, "sync/atomic": {}, "testing": {}, "time": {},
	}
	for _, path := range files {
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", filepath.Base(path), parseErr)
		}
		for _, imported := range parsed.Imports {
			name, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("unquote import: %v", unquoteErr)
			}
			if _, ok := allowed[name]; !ok {
				t.Fatalf("gateway import %q is outside closed stdlib allowlist", name)
			}
		}
	}
}

// TestMetadataHasNoSensitiveField 验证 evidence type 无 payload、secret、endpoint 或 IP 字段。
func TestMetadataHasNoSensitiveField(t *testing.T) {
	metadataType := reflect.TypeFor[Metadata]()
	for index := range metadataType.NumField() {
		name := strings.ToLower(metadataType.Field(index).Name)
		for _, forbidden := range []string{
			"payload", "secret", "ticket", "proof", "key", "endpoint", "address", "ip",
		} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("Metadata field %q contains forbidden token %q", name, forbidden)
			}
		}
	}
}
