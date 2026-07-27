package main

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestImportAllowlist 禁止黑盒 harness 反向导入 production 实现。
func TestImportAllowlist(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate battlequalificationtool")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "*.go"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	modulePrefix := "github.com/jinwiforz/ihomeland/server/"
	allowedPrefixes := []string{
		modulePrefix + "internal/battlequalification/",
		modulePrefix + "internal/testclient",
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
			if !strings.HasPrefix(name, modulePrefix) {
				continue
			}
			allowed := false
			for _, prefix := range allowedPrefixes {
				allowed = allowed || strings.HasPrefix(name, prefix)
			}
			if !allowed {
				t.Fatalf("harness import %q is outside closed allowlist", name)
			}
		}
	}
}

// TestRunRejectsUnknownAction 验证 CLI 不会回退到隐式模式。
func TestRunRejectsUnknownAction(t *testing.T) {
	if err := run(nil); err == nil {
		t.Fatal("missing action was accepted")
	}
	if err := run([]string{"verify"}); err == nil {
		t.Fatal("unimplemented action was accepted")
	}
}
