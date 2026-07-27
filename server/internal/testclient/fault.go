package testclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// FaultServerRestart 是 server v1 资格使用的整体进程替换。
	FaultServerRestart = "server-restart"
	// FaultRedisFlush 清空可恢复 Redis 运行态。
	FaultRedisFlush = "redis-flush"
	// FaultRedisRestart 重启当前资格 run 拥有的 Redis。
	FaultRedisRestart = "redis-restart"
	// FaultMySQLRestart 重启当前资格 run 拥有的 MySQL。
	FaultMySQLRestart = "mysql-restart"
	// FaultAssignmentReplacement 替换 Go/C++ incarnation 并要求 assignment 前进。
	FaultAssignmentReplacement = "assignment-replacement"
	// FaultChildCrashRestart 终止精确 C++ child，并由外部 owner 恢复整个进程图。
	FaultChildCrashRestart = "child-crash-restart"
	// FaultGoRestart 替换精确 Go parent 及其 owned child。
	FaultGoRestart = "go-restart"
	// FaultShutdownDrainDeadline 触发 supervised failure 并验证有界 drain/shutdown。
	FaultShutdownDrainDeadline = "shutdown-drain-deadline"
)

var allowedFaults = map[string]struct{}{
	FaultServerRestart:         {},
	FaultRedisFlush:            {},
	FaultRedisRestart:          {},
	FaultMySQLRestart:          {},
	FaultAssignmentReplacement: {},
	FaultChildCrashRestart:     {},
	FaultGoRestart:             {},
	FaultShutdownDrainDeadline: {},
}

// FaultRequest 是 testclient 与 PowerShell owner 之间的低敏单槽请求。
type FaultRequest struct {
	// SchemaVersion 选择 checkpoint 文件结构代际。
	SchemaVersion int `json:"schemaVersion"`
	// Sequence 防止迟到 response 完成后续故障请求。
	Sequence uint64 `json:"sequence"`
	// Kind 是封闭 process/storage 故障名称。
	Kind string `json:"kind"`
}

// FaultResponse 是 owner 完成或拒绝故障动作后的低敏结果。
type FaultResponse struct {
	// SchemaVersion 选择 checkpoint 文件结构代际。
	SchemaVersion int `json:"schemaVersion"`
	// Sequence 必须与当前 request 精确相等。
	Sequence uint64 `json:"sequence"`
	// Outcome 只允许 pass 或 fail。
	Outcome string `json:"outcome"`
}

// FileFaultController 串行化当前资格 run 的封闭故障 checkpoint。
type FileFaultController struct {
	// directory 是 ignored qualification run 的绝对路径。
	directory string
	// mutex 防止同一 client process 并发覆盖单槽文件。
	mutex sync.Mutex
	// sequence 是当前 process 内严格递增的请求代际。
	sequence uint64
}

// NewFileFaultController 创建不执行任何故障的 checkpoint client。
func NewFileFaultController(directory string) (*FileFaultController, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("qualification fault directory must be absolute")
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return nil, errors.New("qualification fault directory is unavailable")
	}
	return &FileFaultController{directory: filepath.Clean(directory)}, nil
}

// Execute 请求唯一 owner 执行故障，并等待同 sequence 的有界响应。
func (controller *FileFaultController) Execute(ctx context.Context, kind string) error {
	if _, allowed := allowedFaults[kind]; controller == nil || ctx == nil || !allowed {
		return errors.New("qualification fault request is invalid")
	}
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	controller.sequence++
	request := FaultRequest{SchemaVersion: 1, Sequence: controller.sequence, Kind: kind}
	requestPath := filepath.Join(controller.directory, "fault-request.json")
	responsePath := filepath.Join(controller.directory, "fault-response.json")
	_ = os.Remove(responsePath)
	encoded, err := json.Marshal(request)
	if err != nil {
		return errors.New("encode qualification fault request failed")
	}
	temporary := requestPath + ".tmp"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o600); err != nil {
		return errors.New("write qualification fault request failed")
	}
	if err := os.Rename(temporary, requestPath); err != nil {
		_ = os.Remove(temporary)
		return errors.New("publish qualification fault request failed")
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		content, readErr := os.ReadFile(responsePath)
		if readErr == nil {
			var response FaultResponse
			if json.Unmarshal(content, &response) != nil || response.SchemaVersion != 1 || response.Sequence != request.Sequence || response.Outcome != "pass" && response.Outcome != "fail" {
				return errors.New("qualification fault response is invalid")
			}
			_ = os.Remove(requestPath)
			_ = os.Remove(responsePath)
			if response.Outcome != "pass" {
				return errors.New("qualification fault owner rejected operation")
			}
			return nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return errors.New("read qualification fault response failed")
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
		}
	}
}
