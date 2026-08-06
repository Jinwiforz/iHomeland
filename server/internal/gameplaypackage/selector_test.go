package gameplaypackage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testConfigIdentity     = "d6e3f016e4c29f4ad3916759e5ea059443fe37145dbe51621704180e50bc555b"
	testNavigationIdentity = "14165efe5b47caf6c7c8e2f9a4794c4d238aa4badeedc3d5badb41a9c2d5d19f"
	testPhysicsIdentity    = "64d53e1803d7cb14f4953ba1978175b162577eeb064716b2630cad0acf4de02e"
	testWireIdentity       = "9a40facbb23aafc556d38b403c8f8b1e264e0f2d9414e32da434b11d07554432"
	testMappingIdentity    = "9e78652d1d05524c2a67668b208f703120a214bfb45f053c94f6c3dc79595e3b"
)

// TestSelectFreezesProductionIdentity 验证 Go 只投影部署 identity 与 binding。
func TestSelectFreezesProductionIdentity(t *testing.T) {
	t.Parallel()
	request := productionRequest(t, productionRoot(t))
	selection, err := Select(request)
	if err != nil {
		t.Fatal(err)
	}
	if selection.RootPath != request.RootPath || selection.PackageID != request.PackageID ||
		selection.ConfigIdentity != testConfigIdentity || selection.NavigationIdentity != testNavigationIdentity ||
		selection.PhysicsIdentity != testPhysicsIdentity || selection.WireIdentity != testWireIdentity ||
		selection.MappingIdentity != testMappingIdentity {
		t.Fatalf("selection projection differs: %+v", selection)
	}
}

// TestSelectRejectsSourceAndClassificationDrift 覆盖 source bytes、closed root 与 fixture 隔离。
func TestSelectRejectsSourceAndClassificationDrift(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*testing.T, string){
		"source-drift": func(t *testing.T, root string) {
			path := filepath.Join(root, "presentation.json")
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source = append(source, ' ')
			if err := os.WriteFile(path, source, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"unknown-document": func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "unexpected.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"fixture-classification": func(t *testing.T, root string) {
			path := filepath.Join(root, "package.json")
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source = []byte(strings.Replace(string(source), `"qualification_state": "production"`, `"qualification_state": "governance-only"`, 1))
			if err := os.WriteFile(path, source, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := copyProductionRoot(t)
			mutate(t, root)
			if _, err := Select(productionRequest(t, root)); err == nil || strings.Contains(err.Error(), root) {
				t.Fatalf("drift error = %v", err)
			}
		})
	}
}

// TestSelectRejectsConfiguredBindingDrift 验证四类部署预期不能被 source 覆盖。
func TestSelectRejectsConfiguredBindingDrift(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*Request){
		"config":     func(value *Request) { value.ConfigIdentity = strings.Repeat("0", 64) },
		"navigation": func(value *Request) { value.NavigationIdentity = strings.Repeat("1", 64) },
		"physics":    func(value *Request) { value.PhysicsIdentity = strings.Repeat("2", 64) },
		"wire":       func(value *Request) { value.WireIdentity = strings.Repeat("3", 64) },
		"ticket-wire": func(value *Request) {
			value.BattleWireIdentity = strings.Repeat("4", 64)
		},
	} {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			request := productionRequest(t, productionRoot(t))
			mutate(&request)
			if _, err := Select(request); err == nil || strings.Contains(err.Error(), request.RootPath) {
				t.Fatalf("binding drift error = %v", err)
			}
		})
	}
}

// TestSelectPredecessorSuccessorAndRollback 验证 package 切换只生成完整 immutable 选择，不热更新旧快照。
func TestSelectPredecessorSuccessorAndRollback(t *testing.T) {
	t.Parallel()
	predecessorRoot := copyProductionRoot(t)
	successorRoot := copyProductionRoot(t)
	presentationPath := filepath.Join(successorRoot, "presentation.json")
	source, err := os.ReadFile(presentationPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(presentationPath, append(source, ' '), 0o600); err != nil {
		t.Fatal(err)
	}

	predecessorRequest := productionRequest(t, predecessorRoot)
	predecessorRequest.ConfigIdentity = identityForRoot(t, predecessorRoot)
	successorRequest := productionRequest(t, successorRoot)
	successorRequest.ConfigIdentity = identityForRoot(t, successorRoot)
	predecessor, err := Select(predecessorRequest)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := Select(successorRequest)
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := Select(predecessorRequest)
	if err != nil {
		t.Fatal(err)
	}
	if predecessor.ConfigIdentity == successor.ConfigIdentity || rollback != predecessor {
		t.Fatal("predecessor/successor/rollback selection did not remain atomic")
	}

	if err := os.WriteFile(presentationPath, append(source, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if successor.ConfigIdentity != successorRequest.ConfigIdentity {
		t.Fatal("active selection hot reloaded changed source bytes")
	}
	if _, err := Select(successorRequest); err == nil {
		t.Fatal("changed successor source reused an old expected identity")
	}
}

func productionRequest(t *testing.T, root string) Request {
	t.Helper()
	return Request{
		RootPath:           root,
		ArenaRootPath:      productionArenaRoot(t),
		PackageID:          "personal-world-combat-v1",
		ConfigIdentity:     testConfigIdentity,
		NavigationIdentity: testNavigationIdentity,
		PhysicsIdentity:    testPhysicsIdentity,
		WireIdentity:       testWireIdentity,
		ModelManifest:      "65e136d20dfa244db4ce42007cfe1c0411b7f807b635209704b6ef51e93d08b1",
		ProfileManifest:    "c7ff3d1f582625d18028c4ce20fb808b2e56e10084c4ccf61790b0c5f486c424",
		BattleWireIdentity: testWireIdentity,
	}
}

func productionArenaRoot(t *testing.T) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(repositoryRoot, "simulation", "content", "personal-world-combat-v1")
}

func productionRoot(t *testing.T) string {
	t.Helper()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(repositoryRoot, "shared", "contracts", "gameplay", "battle", "packages", "personal-world-combat-v1")
}

func copyProductionRoot(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "personal-world-combat-v1")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	for name := range requiredDocuments {
		source, err := os.ReadFile(filepath.Join(productionRoot(t), name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, name), source, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return target
}

func identityForRoot(t *testing.T, root string) string {
	t.Helper()
	documents := make(map[string]sourceDocument, len(requiredDocuments))
	for name, kind := range requiredDocuments {
		source, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		documents[name] = sourceDocument{Kind: kind, Digest: sha256Hex(source), Bytes: source}
	}
	var manifest packageDocument
	if err := decodeClosed(documents["package.json"].Bytes, &manifest); err != nil {
		t.Fatal(err)
	}
	return productionIdentity(manifest.GovernanceBinding.ManifestSHA256, documents)
}
