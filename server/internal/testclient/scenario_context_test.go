package testclient

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// TestScenarioContextClosesInReverseAndClearsActors 验证多 actor credential 隔离与逆序资源 cleanup。
func TestScenarioContextClosesInReverseAndClearsActors(t *testing.T) {
	scenario, err := NewScenarioContext(context.Background())
	if err != nil {
		t.Fatalf("new scenario: %v", err)
	}
	actor, err := scenario.AddActor()
	if err != nil {
		t.Fatalf("add actor: %v", err)
	}
	actor.AccessToken, _ = NewSecret("access")
	actor.RefreshToken, _ = NewSecret("refresh")
	var closed []int
	if err := scenario.Track(&recordingResource{id: 1, closed: &closed}); err != nil {
		t.Fatalf("track resource: %v", err)
	}
	if err := scenario.Track(&recordingResource{id: 2, closed: &closed}); err != nil {
		t.Fatalf("track resource: %v", err)
	}
	if err := scenario.Close(); err != nil {
		t.Fatalf("close scenario: %v", err)
	}
	if !reflect.DeepEqual(closed, []int{2, 1}) {
		t.Fatalf("close order=%v", closed)
	}
	if actor.AccessToken != nil || actor.RefreshToken != nil {
		t.Fatal("actor credentials were retained after cleanup")
	}
	if err := scenario.Track(&recordingResource{}); err == nil {
		t.Fatal("closed scenario accepted a resource")
	}
	if err := scenario.Close(); err != nil {
		t.Fatalf("repeat close: %v", err)
	}
}

// TestActorConnectionsRejectsStaleHandle 验证 replacement 后旧代际不能再写回 current target。
func TestActorConnectionsRejectsStaleHandle(t *testing.T) {
	connections := new(ActorConnections)
	first := &TCPClient{}
	second := &TCPClient{}
	firstHandle := connections.ReplaceTCP(first)
	secondHandle := connections.ReplaceTCP(second)
	if connections.IsCurrentTCP(firstHandle) {
		t.Fatal("stale TCP handle remained current")
	}
	if !connections.IsCurrentTCP(secondHandle) {
		t.Fatal("replacement TCP handle is not current")
	}
	if err := connections.Close(); err != nil {
		t.Fatalf("close connections: %v", err)
	}
}

// TestScenarioContextPreservesCleanupFailures 验证 cleanup 聚合错误且仍关闭其余资源。
func TestScenarioContextPreservesCleanupFailures(t *testing.T) {
	scenario, _ := NewScenarioContext(context.Background())
	var closed []int
	operationErr := errors.New("operation")
	firstCleanupErr := errors.New("first")
	secondCleanupErr := errors.New("second")
	_ = scenario.Track(&recordingResource{id: 1, closed: &closed, err: firstCleanupErr})
	_ = scenario.Track(&recordingResource{id: 2, closed: &closed, err: secondCleanupErr})
	resultErr := operationErr
	closeScenario(scenario, &resultErr)
	if !errors.Is(resultErr, operationErr) || !errors.Is(resultErr, firstCleanupErr) || !errors.Is(resultErr, secondCleanupErr) || !reflect.DeepEqual(closed, []int{2, 1}) {
		t.Fatalf("combined err=%v order=%v", resultErr, closed)
	}
}

// recordingResource 记录场景资源关闭顺序并可注入低敏错误。
type recordingResource struct {
	// id 是测试断言使用的稳定编号。
	id int
	// closed 收集所有 Close 调用顺序。
	closed *[]int
	// err 是 Close 返回的可选测试错误。
	err error
}

// Close 记录编号并返回注入错误。
func (resource *recordingResource) Close() error {
	if resource.closed != nil {
		*resource.closed = append(*resource.closed, resource.id)
	}
	return resource.err
}
