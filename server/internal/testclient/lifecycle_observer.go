package testclient

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
)

var lifecycleScenarioRegistry = map[string]ScenarioFunction{
	"assignment-replacement":     runAssignmentReplacement,
	"child-crash-restart":        runChildCrashRestart,
	"go-restart":                 runGoRestart,
	"owner-grace-expiry":         runOwnerGraceExpiry,
	"session-epoch-invalidation": runWSSSessionInvalidation,
	"shutdown-drain-deadline":    runShutdownDrainDeadline,
	"visitor-leave":              runVisitLeave,
	"visitor-reconnect":          runVisitDisconnectReconnect,
}

// LifecycleObservation 保存公开协议场景在内存中的 predecessor/successor 投影。
//
// 调用方必须在生成单向摘要后调用 Clear；该类型不得直接序列化或写入日志。
type LifecycleObservation struct {
	// PredecessorProjection 是迁移前公开 session/binding/revision 的规范化投影。
	PredecessorProjection []byte
	// SuccessorProjection 是迁移后投影；终止迁移时为空。
	SuccessorProjection []byte
	// Terminated 表示迁移没有 successor。
	Terminated bool
	// PredecessorRejected 表示场景已观察到旧 connection/binding 不再可用。
	PredecessorRejected bool
}

// Clear 清零并释放生命周期投影。
func (observation *LifecycleObservation) Clear() {
	if observation == nil {
		return
	}
	clear(observation.PredecessorProjection)
	clear(observation.SuccessorProjection)
	observation.PredecessorProjection = nil
	observation.SuccessorProjection = nil
	observation.Terminated = false
	observation.PredecessorRejected = false
}

// LifecycleRecorder 是单场景公开协议 runner 的线程安全投影汇聚器。
type LifecycleRecorder struct {
	mutex       sync.Mutex
	predecessor []byte
	successor   []byte
	terminated  bool
	rejected    bool
}

// RunLifecycleScenario 执行 B0.6 public-protocol owner 的 closed lifecycle case。
func RunLifecycleScenario(
	ctx context.Context,
	caseID string,
	runtime *ScenarioRuntime,
) error {
	if ctx == nil || runtime == nil || runtime.Lifecycle == nil {
		return errors.New("lifecycle scenario invocation is invalid")
	}
	scenario, ok := lifecycleScenarioRegistry[caseID]
	if !ok || scenario == nil {
		return errors.New("lifecycle scenario is not registered")
	}
	return scenario(ctx, runtime)
}

// Observation 返回 owned projection 副本，并拒绝不完整或矛盾的迁移。
func (recorder *LifecycleRecorder) Observation() (LifecycleObservation, error) {
	if recorder == nil {
		return LifecycleObservation{}, errors.New("lifecycle recorder is unavailable")
	}
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if len(recorder.predecessor) == 0 || !recorder.rejected ||
		(recorder.terminated && len(recorder.successor) != 0) ||
		(!recorder.terminated && len(recorder.successor) == 0) {
		return LifecycleObservation{}, errors.New("lifecycle observation is incomplete")
	}
	return LifecycleObservation{
		PredecessorProjection: append([]byte(nil), recorder.predecessor...),
		SuccessorProjection:   append([]byte(nil), recorder.successor...),
		Terminated:            recorder.terminated,
		PredecessorRejected:   true,
	}, nil
}

// recordPredecessor 只允许场景设置一次迁移前投影。
func (recorder *LifecycleRecorder) recordPredecessor(parts ...string) error {
	projection, err := encodeLifecycleProjection(parts)
	if err != nil {
		return err
	}
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if len(recorder.predecessor) != 0 {
		clear(projection)
		return errors.New("lifecycle predecessor was already recorded")
	}
	recorder.predecessor = projection
	return nil
}

// recordSuccessor 只允许场景设置一次非终止 successor。
func (recorder *LifecycleRecorder) recordSuccessor(parts ...string) error {
	projection, err := encodeLifecycleProjection(parts)
	if err != nil {
		return err
	}
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.terminated || len(recorder.successor) != 0 {
		clear(projection)
		return errors.New("lifecycle successor was already recorded")
	}
	recorder.successor = projection
	recorder.rejected = true
	return nil
}

// recordTermination 提交无 successor 的终止迁移。
func (recorder *LifecycleRecorder) recordTermination() error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.terminated || len(recorder.successor) != 0 {
		return errors.New("lifecycle termination was already recorded")
	}
	recorder.terminated = true
	recorder.rejected = true
	return nil
}

// encodeLifecycleProjection 使用长度前缀消除字段拼接歧义。
func encodeLifecycleProjection(parts []string) ([]byte, error) {
	if len(parts) == 0 {
		return nil, errors.New("lifecycle projection is empty")
	}
	size := 0
	for _, part := range parts {
		if part == "" || len(part) > int(^uint32(0)) ||
			size > int(^uint32(0))-4-len(part) {
			return nil, errors.New("lifecycle projection component is invalid")
		}
		size += 4 + len(part)
	}
	projection := make([]byte, size)
	offset := 0
	for _, part := range parts {
		binary.BigEndian.PutUint32(projection[offset:offset+4], uint32(len(part)))
		offset += 4
		copy(projection[offset:offset+len(part)], part)
		offset += len(part)
	}
	return projection, nil
}
