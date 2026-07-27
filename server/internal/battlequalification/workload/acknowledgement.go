package workload

import "errors"

// AcknowledgementWindow 是一个完整 snapshot 新确认的 InputTick 闭区间。
type AcknowledgementWindow struct {
	// MappingGeneration 绑定当前 session 的 InputTick epoch。
	MappingGeneration uint64
	// FirstInputTick 是本次新确认的首个 InputTick；未推进时为零。
	FirstInputTick uint64
	// LastInputTick 是本次新确认的末个 InputTick；未推进时为零。
	LastInputTick uint64
}

// InputAcknowledgementTracker 只用显式 snapshot cursor 关联已发送输入。
//
// 它不读取 ServerTick、日志或 payload identity；一个 tracker 只属于一个 actor slot
// 和 mapping generation，successor 必须显式替换并从零开始。
type InputAcknowledgementTracker struct {
	// mappingGeneration 是 current session 的不可回退 epoch。
	mappingGeneration uint64
	// lastSentInputTick 是 workload 已发送的最大 InputTick。
	lastSentInputTick uint64
	// lastAcknowledgedInputTick 是完整 snapshot 已发布的连续前沿。
	lastAcknowledgedInputTick uint64
}

// NewInputAcknowledgementTracker 创建绑定非零 mapping generation 的 tracker。
func NewInputAcknowledgementTracker(mappingGeneration uint64) (*InputAcknowledgementTracker, error) {
	if mappingGeneration == 0 {
		return nil, errors.New("input acknowledgement mapping generation is zero")
	}
	return &InputAcknowledgementTracker{mappingGeneration: mappingGeneration}, nil
}

// RecordSent 记录已实际提交到客户端 transport 的 InputTick frontier。
func (tracker *InputAcknowledgementTracker) RecordSent(mappingGeneration, inputTick uint64) error {
	if tracker == nil || mappingGeneration != tracker.mappingGeneration ||
		inputTick == 0 || inputTick < tracker.lastSentInputTick {
		return errors.New("sent input acknowledgement identity is invalid")
	}
	tracker.lastSentInputTick = inputTick
	return nil
}

// Advance 接受完整 snapshot 的显式 cursor，并返回本次新增确认区间。
func (tracker *InputAcknowledgementTracker) Advance(
	mappingGeneration, lastProcessedInputTick uint64,
) (AcknowledgementWindow, error) {
	if tracker == nil || mappingGeneration != tracker.mappingGeneration ||
		lastProcessedInputTick < tracker.lastAcknowledgedInputTick ||
		lastProcessedInputTick > tracker.lastSentInputTick {
		return AcknowledgementWindow{}, errors.New("snapshot input acknowledgement is invalid")
	}
	if lastProcessedInputTick == tracker.lastAcknowledgedInputTick {
		return AcknowledgementWindow{MappingGeneration: mappingGeneration}, nil
	}
	window := AcknowledgementWindow{
		MappingGeneration: mappingGeneration,
		FirstInputTick:    tracker.lastAcknowledgedInputTick + 1,
		LastInputTick:     lastProcessedInputTick,
	}
	tracker.lastAcknowledgedInputTick = lastProcessedInputTick
	return window, nil
}

// ReplaceMapping 只接受 successor generation，并清空 predecessor frontier。
func (tracker *InputAcknowledgementTracker) ReplaceMapping(successor uint64) error {
	if tracker == nil || successor <= tracker.mappingGeneration {
		return errors.New("input acknowledgement successor generation is invalid")
	}
	tracker.mappingGeneration = successor
	tracker.lastSentInputTick = 0
	tracker.lastAcknowledgedInputTick = 0
	return nil
}
