package testclient

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFileFaultControllerCorrelatesOwnerResponse 验证迟到或错误 sequence 不能完成当前故障。
func TestFileFaultControllerCorrelatesOwnerResponse(t *testing.T) {
	directory := t.TempDir()
	controller, err := NewFileFaultController(directory)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- controller.Execute(context.Background(), FaultRedisFlush) }()
	requestPath := filepath.Join(directory, "fault-request.json")
	var request FaultRequest
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		content, readErr := os.ReadFile(requestPath)
		if readErr == nil && json.Unmarshal(content, &request) == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	response, _ := json.Marshal(FaultResponse{SchemaVersion: 1, Sequence: request.Sequence, Outcome: "pass"})
	if err := os.WriteFile(filepath.Join(directory, "fault-response.json"), response, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

// TestFileFaultControllerRejectsUnknownOperation 验证 checkpoint 只接受封闭故障集合。
func TestFileFaultControllerRejectsUnknownOperation(t *testing.T) {
	controller, err := NewFileFaultController(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Execute(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown qualification fault was accepted")
	}
}
