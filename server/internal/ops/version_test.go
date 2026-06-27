package ops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadVersion(t *testing.T) {
	dir := t.TempDir()
	releasePath := filepath.Join(dir, "release.json")
	serverPath := filepath.Join(dir, "server.json")
	clientPath := filepath.Join(dir, "client.json")

	writeFile(t, releasePath, `{"release":"0.1.0","client":"0.1.0","server":"0.1.0","protocol":1}`)
	writeFile(t, serverPath, `{"name":"server","version":"0.1.0","buildNumber":2,"commit":"abc","buildTime":"2026-06-25 18:00:00"}`)
	writeFile(t, clientPath, `{"name":"client","version":"0.1.0","buildNumber":3,"commit":"def","buildTime":"2026-06-25 18:01:00"}`)

	got, err := LoadVersion(VersionPaths{
		Release: releasePath,
		Server:  serverPath,
		Client:  clientPath,
	})
	if err != nil {
		t.Fatalf("LoadVersion() error = %v", err)
	}

	if got.Release.Protocol != 1 {
		t.Fatalf("Release.Protocol = %d, want 1", got.Release.Protocol)
	}
	if got.Server.Name != "server" || got.Server.BuildNumber != 2 {
		t.Fatalf("Server = %+v", got.Server)
	}
	if got.Client.Name != "client" || got.Client.BuildNumber != 3 {
		t.Fatalf("Client = %+v", got.Client)
	}
}

func TestLoadVersionReportsMissingFile(t *testing.T) {
	_, err := LoadVersion(VersionPaths{
		Release: "missing-release.json",
		Server:  "missing-server.json",
		Client:  "missing-client.json",
	})
	if err == nil {
		t.Fatal("LoadVersion() error = nil, want error")
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
