package contract

import (
	"path/filepath"
	"testing"
)

// TestLookupWSSPushAcceptsFrozenProfiles 验证9个公开control push都能投影为严格只出不进路由。
func TestLookupWSSPushAcceptsFrozenProfiles(t *testing.T) {
	t.Parallel()
	catalog, err := Load(testRepositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint32]string{
		500: "ihomeland.control.v1.MaintenancePush", 501: "ihomeland.control.v1.ForcedLogoutPush",
		502: "ihomeland.control.v1.QueueStatusPush", 503: "ihomeland.control.v1.EndpointUpdatePush",
		504: "ihomeland.control.v1.SessionInvalidatedPush", 2003: "ihomeland.world.v1.WorldAssignmentChangedPush",
		2100: "ihomeland.visit.v1.VisitInvitePush", 2101: "ihomeland.visit.v1.VisitOwnerAvailabilityPush",
		2102: "ihomeland.visit.v1.VisitClosedNoticePush",
	}
	for id, protobufName := range want {
		route, lookupErr := catalog.LookupWSSPush(id)
		if lookupErr != nil {
			t.Fatalf("LookupWSSPush(%d) error = %v", id, lookupErr)
		}
		if route.Protobuf != protobufName || route.Channel != "WSS" || route.AuthScope != "CONTROL" || route.MaxSize == 0 {
			t.Fatalf("LookupWSSPush(%d) = %+v", id, route)
		}
	}
}

// TestWSSPushCatalogMatchesRegistry 防止编译期runtime投影与唯一registry事实漂移。
func TestWSSPushCatalogMatchesRegistry(t *testing.T) {
	t.Parallel()
	source, err := Load(testRepositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	runtime := WSSPushCatalog()
	if len(runtime.Messages.Messages) != 9 || len(runtime.Routes.Routes) != 9 {
		t.Fatalf("runtime WSS catalog count drifted: messages=%d routes=%d", len(runtime.Messages.Messages), len(runtime.Routes.Routes))
	}
	for _, message := range runtime.Messages.Messages {
		want, err := source.LookupWSSPush(message.ID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := runtime.LookupWSSPush(message.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("runtime WSS profile %d drifted: got=%+v want=%+v", message.ID, got, want)
		}
	}
}

// TestLookupWSSPushRejectsWrongProfiles 防止unknown、TLS/TCP或方向语义漂移被publisher接受。
func TestLookupWSSPushRejectsWrongProfiles(t *testing.T) {
	t.Parallel()
	catalog, err := Load(testRepositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint32{0, 2000, 2103} {
		if _, err := catalog.LookupWSSPush(id); err == nil {
			t.Fatalf("LookupWSSPush(%d) unexpectedly succeeded", id)
		}
	}
	mutated := catalog
	for index := range mutated.Messages.Messages {
		if mutated.Messages.Messages[index].ID == 500 {
			mutated.Messages.Messages[index].Direction = "CLIENT_TO_SERVER"
		}
	}
	if _, err := mutated.LookupWSSPush(500); err == nil {
		t.Fatal("direction drift was accepted")
	}
}

// testRepositoryRoot 返回contract package测试对应的仓库根目录。
func testRepositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
