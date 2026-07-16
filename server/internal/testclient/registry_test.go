package testclient

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// TestScenarioRegistryMatchesManifest 双向拒绝漏实现、隐藏 runner、nil runner 和重复 manifest ID。
func TestScenarioRegistryMatchesManifest(t *testing.T) {
	file, err := os.Open(filepath.Join(repositoryRoot(t), "shared", "contracts", "fixtures", "qualification", "manifest.json"))
	if err != nil {
		t.Fatalf("open qualification manifest: %v", err)
	}
	defer file.Close()
	manifest, err := LoadManifest(file)
	if err != nil {
		t.Fatalf("load qualification manifest: %v", err)
	}
	registry := Registry()
	registryIDs := make([]string, 0, len(registry))
	for id, function := range registry {
		if function == nil {
			t.Fatalf("scenario %q has nil runner", id)
		}
		registryIDs = append(registryIDs, id)
	}
	sort.Strings(registryIDs)
	if !reflect.DeepEqual(manifest.IDs(), registryIDs) {
		t.Fatalf("manifest IDs=%v registry IDs=%v", manifest.IDs(), registryIDs)
	}
	delete(registry, registryIDs[0])
	if len(Registry()) != len(manifest.Scenarios) {
		t.Fatal("Registry returned mutable global map")
	}
}
