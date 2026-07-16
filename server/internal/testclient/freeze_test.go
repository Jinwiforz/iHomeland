package testclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestContractFreezeDigest 验证提交摘要与公开契约源原始 bytes 完全一致。
func TestContractFreezeDigest(t *testing.T) {
	root := repositoryRoot(t)
	file, err := os.Open(filepath.Join(root, "shared", "contracts", "fixtures", "qualification", "freeze.json"))
	if err != nil {
		t.Fatalf("open freeze record: %v", err)
	}
	defer file.Close()
	record, err := LoadFreezeRecord(file)
	if err != nil {
		t.Fatalf("load freeze record: %v", err)
	}
	digest, paths, err := ContractFreezeDigest(root)
	if err != nil {
		t.Fatalf("compute freeze digest: %v", err)
	}
	if len(paths) < 10 {
		t.Fatalf("freeze input count=%d, want >=10", len(paths))
	}
	if digest != record.Digest {
		t.Fatalf("contract freeze drift: got %s want %s", digest, record.Digest)
	}
}

// TestContractFreezeDigestDetectsContentAndSetDrift 验证内容修改、文件增加和文件删除都改变摘要。
func TestContractFreezeDigestDetectsContentAndSetDrift(t *testing.T) {
	root := t.TempDir()
	mustWriteFixture(t, root, "shared/proto/a.proto", "one")
	mustWriteFixture(t, root, "shared/contracts/http/v1/openapi.yaml", "two")
	mustWriteFixture(t, root, "shared/contracts/registry/messages.json", "three")
	mustWriteFixture(t, root, "shared/contracts/fixtures/http/cases.json", "four")
	baseline, _, err := ContractFreezeDigest(root)
	if err != nil {
		t.Fatalf("baseline digest: %v", err)
	}
	mustWriteFixture(t, root, "shared/proto/a.proto", "changed")
	changed, _, err := ContractFreezeDigest(root)
	if err != nil || changed == baseline {
		t.Fatalf("content drift digest=%q err=%v", changed, err)
	}
	mustWriteFixture(t, root, "shared/proto/b.proto", "added")
	added, _, err := ContractFreezeDigest(root)
	if err != nil || added == changed {
		t.Fatalf("addition drift digest=%q err=%v", added, err)
	}
	if err := os.Remove(filepath.Join(root, "shared", "proto", "a.proto")); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}
	removed, _, err := ContractFreezeDigest(root)
	if err != nil || removed == added {
		t.Fatalf("deletion drift digest=%q err=%v", removed, err)
	}
	mustWriteFixture(t, root, "shared/contracts/fixtures/qualification/freeze.json", "ignored")
	ignored, _, err := ContractFreezeDigest(root)
	if err != nil || ignored != removed {
		t.Fatalf("freeze record affected digest=%q want=%q err=%v", ignored, removed, err)
	}
}

// TestLoadFreezeRecordRejectsInvalidInput 验证摘要格式、算法与额外字段不能静默通过。
func TestLoadFreezeRecordRejectsInvalidInput(t *testing.T) {
	validDigest := strings.Repeat("a", 64)
	tests := []string{
		`{"schemaVersion":2,"algorithm":"sha256-path-content-v1","digest":"` + validDigest + `"}`,
		`{"schemaVersion":1,"algorithm":"other","digest":"` + validDigest + `"}`,
		`{"schemaVersion":1,"algorithm":"sha256-path-content-v1","digest":"pending"}`,
		`{"schemaVersion":1,"algorithm":"sha256-path-content-v1","digest":"` + validDigest + `","extra":true}`,
	}
	for _, input := range tests {
		if _, err := LoadFreezeRecord(strings.NewReader(input)); err == nil {
			t.Fatalf("invalid freeze record was accepted: %s", input)
		}
	}
}

// mustWriteFixture 创建摘要测试所需的仓库相对文件。
func mustWriteFixture(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
