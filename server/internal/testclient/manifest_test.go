package testclient

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestQualificationManifest 验证仓库提交的资格清单严格、唯一且全部 mandatory。
func TestQualificationManifest(t *testing.T) {
	file, err := os.Open(filepath.Join(repositoryRoot(t), "shared", "contracts", "fixtures", "qualification", "manifest.json"))
	if err != nil {
		t.Fatalf("open qualification manifest: %v", err)
	}
	defer file.Close()
	manifest, err := LoadManifest(file)
	if err != nil {
		t.Fatalf("load qualification manifest: %v", err)
	}
	for _, scenario := range manifest.Scenarios {
		if !scenario.Mandatory {
			t.Fatalf("Q0 scenario %q is not mandatory", scenario.ID)
		}
	}
	ids := manifest.IDs()
	if !reflect.DeepEqual(ids, append([]string(nil), ids...)) {
		t.Fatal("manifest IDs did not return a stable copy")
	}
}

// TestManifestValidationRejectsInvalidInput 覆盖未知字段、枚举、重复 ID、无界预算和额外 JSON。
func TestManifestValidationRejectsInvalidInput(t *testing.T) {
	valid := `{"schemaVersion":1,"qualificationVersion":"server-v1","scenarios":[{"id":"contract-freeze","group":"contract","mandatory":true,"phase":"contract","execution":"contract","timeoutMs":1,"expectedOutcome":"pass","evidence":"contract"}]}`
	tests := map[string]string{
		"unknown field":       strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":1,"secret":"x"`, 1),
		"duplicate id":        strings.Replace(valid, `]}`, `,{"id":"contract-freeze","group":"contract","mandatory":true,"phase":"contract","execution":"contract","timeoutMs":1,"expectedOutcome":"pass","evidence":"contract"}]}`, 1),
		"unknown group":       strings.Replace(valid, `"group":"contract"`, `"group":"other"`, 1),
		"unknown phase":       strings.Replace(valid, `"phase":"contract"`, `"phase":"other"`, 1),
		"unknown outcome":     strings.Replace(valid, `"expectedOutcome":"pass"`, `"expectedOutcome":"maybe"`, 1),
		"unknown evidence":    strings.Replace(valid, `"evidence":"contract"`, `"evidence":"other"`, 1),
		"unknown execution":   strings.Replace(valid, `"execution":"contract"`, `"execution":"other"`, 1),
		"execution mismatch":  strings.Replace(valid, `"execution":"contract"`, `"execution":"black_box"`, 1),
		"contract evidence":   strings.Replace(valid, `"evidence":"contract"`, `"evidence":"black_box"`, 1),
		"missing profile":     strings.Replace(strings.Replace(valid, `"group":"contract"`, `"group":"resource"`, 1), `"phase":"contract"`, `"phase":"resource"`, 1),
		"profile on contract": strings.Replace(valid, `"evidence":"contract"`, `"evidence":"contract","profile":{"attempts":1,"maxConcurrency":1,"maxBytes":1,"timeoutMs":1}`, 1),
		"unbounded timeout":   strings.Replace(valid, `"timeoutMs":1`, `"timeoutMs":0`, 1),
		"unstable id":         strings.Replace(valid, `"id":"contract-freeze"`, `"id":"../secret"`, 1),
		"trailing JSON":       valid + `{}`,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadManifest(strings.NewReader(input)); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

// FuzzLoadManifest 验证任意 JSON 输入只产生受控错误或完整有效 manifest。
func FuzzLoadManifest(f *testing.F) {
	f.Add(`{"schemaVersion":1,"qualificationVersion":"server-v1","scenarios":[{"id":"contract-freeze","group":"contract","mandatory":true,"phase":"contract","execution":"contract","timeoutMs":1,"expectedOutcome":"pass","evidence":"contract"}]}`)
	f.Add(`{}`)
	f.Fuzz(func(t *testing.T, input string) {
		manifest, err := LoadManifest(strings.NewReader(input))
		if err == nil {
			if validateErr := manifest.Validate(); validateErr != nil {
				t.Fatalf("loader returned invalid manifest: %v", validateErr)
			}
		}
	})
}
