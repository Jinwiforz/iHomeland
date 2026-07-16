package testclient

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var evidenceTestNamePattern = regexp.MustCompile(`^Test[A-Za-z0-9_]+$`)

// evidenceCatalog 是 PowerShell 资格入口和 Go 漂移测试共享的分层证据目录。
type evidenceCatalog struct {
	SchemaVersion int             `json:"schemaVersion"`
	Groups        []evidenceGroup `json:"groups"`
}

// evidenceGroup 将一组稳定场景映射到同一轮必须执行的 owner tests。
type evidenceGroup struct {
	ID          string            `json:"id"`
	ScenarioIDs []string          `json:"scenarioIds"`
	Packages    []evidencePackage `json:"packages"`
}

// evidencePackage 登记 server module 相对 package 及其稳定测试 identity。
type evidencePackage struct {
	Path  string   `json:"path"`
	Tests []string `json:"tests"`
}

// TestLayeredEvidenceCatalogIsComplete 拒绝分层场景、执行入口和 owner test identity 静默漂移。
func TestLayeredEvidenceCatalogIsComplete(t *testing.T) {
	root := repositoryRoot(t)
	catalog := loadEvidenceCatalog(t, root)
	manifest := loadRepositoryManifest(t, root)

	wantScenarios := make([]string, 0)
	for _, scenario := range manifest.Scenarios {
		if scenario.Execution == "layered" {
			wantScenarios = append(wantScenarios, scenario.ID)
		}
	}
	gotScenarios := make([]string, 0)
	seenGroups := make(map[string]bool)
	seenScenarios := make(map[string]bool)
	seenTests := make(map[string]bool)
	for _, group := range catalog.Groups {
		if !validStableID(group.ID) || seenGroups[group.ID] || len(group.ScenarioIDs) == 0 || len(group.Packages) == 0 {
			t.Fatalf("invalid or duplicate evidence group %q", group.ID)
		}
		seenGroups[group.ID] = true
		for _, scenarioID := range group.ScenarioIDs {
			if !validStableID(scenarioID) || seenScenarios[scenarioID] {
				t.Fatalf("invalid or duplicate evidence scenario %q", scenarioID)
			}
			seenScenarios[scenarioID] = true
			gotScenarios = append(gotScenarios, scenarioID)
		}
		for _, packageEntry := range group.Packages {
			if !validEvidencePackagePath(packageEntry.Path) || len(packageEntry.Tests) == 0 {
				t.Fatalf("invalid or empty evidence package %q", packageEntry.Path)
			}
			packagePath := strings.TrimPrefix(packageEntry.Path, "./")
			found := collectTestFunctions(t, filepath.Join(root, "server", filepath.FromSlash(packagePath)))
			for _, name := range packageEntry.Tests {
				identity := packageEntry.Path + "/" + name
				if !evidenceTestNamePattern.MatchString(name) || seenTests[identity] || !found[name] {
					t.Errorf("invalid, duplicate, or missing layered evidence test %s", identity)
				}
				seenTests[identity] = true
			}
		}
	}
	sort.Strings(wantScenarios)
	sort.Strings(gotScenarios)
	if fmt.Sprint(gotScenarios) != fmt.Sprint(wantScenarios) {
		t.Fatalf("layered evidence scenarios=%v, want %v", gotScenarios, wantScenarios)
	}
}

// loadEvidenceCatalog 使用严格 JSON 解码读取唯一分层证据源，拒绝未知字段和尾随值。
func loadEvidenceCatalog(t *testing.T, root string) evidenceCatalog {
	t.Helper()
	file, err := os.Open(filepath.Join(root, "shared", "contracts", "fixtures", "qualification", "evidence-manifest.json"))
	if err != nil {
		t.Fatalf("open evidence catalog: %v", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var catalog evidenceCatalog
	if err := decoder.Decode(&catalog); err != nil {
		t.Fatalf("decode evidence catalog: %v", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		t.Fatalf("decode evidence catalog trailer: %v", err)
	}
	if catalog.SchemaVersion != 1 || len(catalog.Groups) == 0 {
		t.Fatalf("unsupported or empty evidence catalog")
	}
	return catalog
}

// loadRepositoryManifest 读取与证据目录同版本的资格场景清单。
func loadRepositoryManifest(t *testing.T, root string) Manifest {
	t.Helper()
	file, err := os.Open(filepath.Join(root, "shared", "contracts", "fixtures", "qualification", "manifest.json"))
	if err != nil {
		t.Fatalf("open qualification manifest: %v", err)
	}
	defer file.Close()
	manifest, err := LoadManifest(file)
	if err != nil {
		t.Fatalf("load qualification manifest: %v", err)
	}
	return manifest
}

// validEvidencePackagePath 只接受 server module 内的 cmd/internal package，拒绝遍历和平台路径差异。
func validEvidencePackagePath(path string) bool {
	if strings.Contains(path, "\\") || strings.Contains(path, "..") {
		return false
	}
	return strings.HasPrefix(path, "./cmd/") || strings.HasPrefix(path, "./internal/")
}

// collectTestFunctions 只解析目标 package 的手写 `_test.go`，不执行或导入服务端实现。
func collectTestFunctions(t *testing.T, directory string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read evidence package: %v", err)
	}
	found := make(map[string]bool)
	files := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(files, filepath.Join(directory, entry.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parse evidence test file: %v", err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Recv == nil && strings.HasPrefix(function.Name.Name, "Test") {
				found[function.Name.Name] = true
			}
		}
	}
	return found
}
