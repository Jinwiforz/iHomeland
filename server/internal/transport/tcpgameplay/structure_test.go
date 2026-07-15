package tcpgameplay

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/contract"
)

// TestTCPGameplayHasNoForbiddenArchitectureDependencies 保护adapter不依赖Gin、业务storage、WSS或全局事件设施。
func TestTCPGameplayHasNoForbiddenArchitectureDependencies(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	listenOwners := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range file.Imports {
			value, _ := strconv.Unquote(imported.Path.Value)
			lower := strings.ToLower(value)
			for _, forbidden := range []string{"gin", "coder/websocket", "/internal/storage", "/transport/wscontrol", "eventbus", "service_locator"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("%s imports forbidden dependency %q", path, value)
				}
			}
		}
		if filepath.Base(path) == "server.go" {
			listenOwners++
		}
	}
	if listenOwners != 1 {
		t.Fatalf("TCP gameplay listener owner count = %d", listenOwners)
	}
}

// TestTCPGameplayCatalogContainsNoHeartbeatOrCrossChannelRoute 保护实现不私自发明heartbeat或复用WSS消息。
func TestTCPGameplayCatalogContainsNoHeartbeatOrCrossChannelRoute(t *testing.T) {
	t.Parallel()
	catalog := contractCatalogForStructure()
	for _, message := range catalog.Messages.Messages {
		if strings.Contains(strings.ToLower(message.Name), "heartbeat") {
			t.Fatalf("unregistered heartbeat message %d", message.ID)
		}
	}
	for _, route := range catalog.Routes.Routes {
		if route.Channel != "TLS_TCP" {
			t.Fatalf("cross-channel route %d uses %s", route.MessageID, route.Channel)
		}
	}
}

// contractCatalogForStructure 隔离结构测试对编译期TCP投影的读取。
func contractCatalogForStructure() contract.Catalog { return contract.TLSGameplayCatalog() }
