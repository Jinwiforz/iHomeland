package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestRunTLSWritesTextSafeAdmissionKey 验证临时 key 保留 256-bit 熵且满足 file: secret 文本边界。
func TestRunTLSWritesTextSafeAdmissionKey(t *testing.T) {
	directory := t.TempDir()
	if err := runTLS([]string{"-directory", directory}); err != nil {
		t.Fatalf("runTLS() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(directory, "admission-key"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	decoded, err := hex.DecodeString(string(content))
	if err != nil || len(decoded) != 32 {
		t.Fatalf("admission key is not 32-byte hex: length=%d error=%v", len(decoded), err)
	}
}
