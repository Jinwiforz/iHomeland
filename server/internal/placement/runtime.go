package placement

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	// MinimumLeaseTTL 防止配置让调度延迟立即制造 lease 抖动；production 默认值由 D0 决定。
	MinimumLeaseTTL = time.Second
	// MaximumLeaseTTL 限制故障 writer 最长残留窗口；更长策略必须通过独立设计评估。
	MaximumLeaseTTL = 5 * time.Minute
)

// Clock 为 assignment 创建、lease expiry 和条件操作提供受信绝对时间。
type Clock interface {
	// Now 返回当前绝对时间；实现必须允许并发调用，service 会转换为 UTC 并拒绝零值。
	Now() time.Time
}

// IDGenerator 创建不可由客户端预测或选择的 WorldInstance identity 材料。
type IDGenerator interface {
	// NewID 并发安全地返回不可预测安全 ASCII 材料；placement package 负责添加实体前缀。
	NewID() (string, error)
}

// RuntimeController 启动、排空和停止由 assignment identity 指定的服务端运行承载。
//
// 实现必须以 WorldInstanceID 幂等：重复 Start 不能创建第二个 runtime，重复 Stop 不能停止
// 较新 instance。Start 返回 nil 只表示 runtime ready，不授予 active 或写资格；这些事实仍
// 必须由 PlacementStore 提交。ctx 取消只停止等待，调用方会通过 store/current 决定恢复。
type RuntimeController interface {
	// Start 启动 snapshot 指定的 starting runtime 并等待 ready；实现不得自行发布 active。
	Start(ctx context.Context, snapshot AssignmentSnapshot) error
	// Drain 有界关闭完整 stamp 指定 runtime 的新输入并完成已接纳工作。
	Drain(ctx context.Context, stamp AssignmentStamp) error
	// Stop 停止完整 stamp 指定的 runtime；实现必须拒绝用旧 instance identity 停止 successor。
	Stop(ctx context.Context, stamp AssignmentStamp) error
}

// ResultDisposition 说明 assignment result 是本次启动还是既有 current 事实。
type ResultDisposition uint8

const (
	// ResultDispositionUnspecified 表示 result 不完整。
	ResultDispositionUnspecified ResultDisposition = iota
	// ResultDispositionStarted 表示本次调用明确推进 candidate 到 active。
	ResultDispositionStarted
	// ResultDispositionExisting 表示调用返回调用前已经 active 的 current assignment。
	ResultDispositionExisting
	// ResultDispositionReplayed 表示调用解析到相同稳定 identity 已经提交的 assignment。
	ResultDispositionReplayed
)

// AssignmentResult 表达 EnsureActive/Replace 已确认的 current active assignment。
//
// Replace 可能在 successor 已 active、但 predecessor runtime cleanup 失败时同时返回有效
// AssignmentResult 与 CleanupFailed error。调用方必须接纳 result 中的 current 事实，并把
// cleanup failure 交给 reconciliation，不能因 error 非 nil 恢复 predecessor 或丢弃 successor。
type AssignmentResult struct {
	// assignment 是经过 store current 验证的 active snapshot。
	assignment AssignmentSnapshot
	// disposition 说明本次调用是否启动或解析了既有事实。
	disposition ResultDisposition
	// cleanupFailed 表示 successor 已 active，但 predecessor runtime stop 未成功。
	cleanupFailed bool
}

// NewAssignmentResult 校验 active snapshot 与结果 disposition。
func NewAssignmentResult(assignment AssignmentSnapshot, disposition ResultDisposition, cleanupFailed bool) (AssignmentResult, error) {
	if !assignment.Valid() || assignment.Phase() != PhaseActive || disposition == ResultDispositionUnspecified {
		return AssignmentResult{}, errors.New("assignment result is incomplete")
	}
	return AssignmentResult{assignment: assignment, disposition: disposition, cleanupFailed: cleanupFailed}, nil
}

// Assignment 返回 store 已确认 current 的 active snapshot 值副本。
func (result AssignmentResult) Assignment() AssignmentSnapshot { return result.assignment }

// Disposition 返回本次调用对 active assignment 的推进语义。
func (result AssignmentResult) Disposition() ResultDisposition { return result.disposition }

// CleanupFailed 报告 predecessor runtime 是否仍需要 reconciliation 清理。
func (result AssignmentResult) CleanupFailed() bool { return result.cleanupFailed }

// Valid 报告 result 是否包含完整 active assignment 与 disposition。
func (result AssignmentResult) Valid() bool {
	return result.assignment.Valid() && result.assignment.Phase() == PhaseActive && result.disposition != ResultDispositionUnspecified
}

// LifecycleResult 表达 sleep 已提交但 runtime cleanup 可能失败的部分成功边界。
type LifecycleResult struct {
	// predecessor 是 revoke/replay 确认已失去 current 资格的 assignment。
	predecessor AssignmentSnapshot
	// placementCommitted 表示 revoke 已明确提交或被 replay 证明。
	placementCommitted bool
	// drainFailed 表示 revoke 前的 best-effort runtime drain 未成功。
	drainFailed bool
	// cleanupFailed 表示 runtime Stop 返回错误，不能据此恢复 predecessor fence。
	cleanupFailed bool
}

// NewLifecycleResult 构造 sleep 的 drain/placement/cleanup 三阶段结果。
func NewLifecycleResult(predecessor AssignmentSnapshot, placementCommitted bool, drainFailed bool, cleanupFailed bool) (LifecycleResult, error) {
	if !predecessor.Valid() || !placementCommitted {
		return LifecycleResult{}, errors.New("lifecycle result is incomplete")
	}
	return LifecycleResult{predecessor: predecessor, placementCommitted: placementCommitted, drainFailed: drainFailed, cleanupFailed: cleanupFailed}, nil
}

// Predecessor 返回已失去 current 资格的 assignment 值副本。
func (result LifecycleResult) Predecessor() AssignmentSnapshot { return result.predecessor }

// PlacementCommitted 报告 revoke 是否已被明确提交或 replay 证明。
func (result LifecycleResult) PlacementCommitted() bool { return result.placementCommitted }

// DrainFailed 报告 revoke 前的 bounded drain 是否失败或取消。
func (result LifecycleResult) DrainFailed() bool { return result.drainFailed }

// CleanupFailed 报告 runtime Stop 是否仍需 reconciliation 重试。
func (result LifecycleResult) CleanupFailed() bool { return result.cleanupFailed }

// Valid 报告 lifecycle result 是否包含已撤销 predecessor 和提交证据。
func (result LifecycleResult) Valid() bool {
	return result.predecessor.Valid() && result.placementCommitted
}

// ReplaceCommand 保存调用方预生成 successor identity 的 rebuild/migration 输入。
//
// 预生成 identity 是 commit-unknown 后的稳定重试锚点。Command 不携带 endpoint、PlayerID
// 或 PersonalWorld 内容；expected stamp 必须来自受信 placement context。
type ReplaceCommand struct {
	// expected 是 cutover 前必须仍为 current 的 predecessor stamp。
	expected AssignmentStamp
	// successorID 是响应丢失重试必须复用的新 WorldInstance identity。
	successorID WorldInstanceID
	// targetNodeID 是 rebuild/migration 的受信 RuntimeNode target。
	targetNodeID RuntimeNodeID
}

// NewReplaceCommand 校验 predecessor、successor 和 target node，禁止 instance 原地复活。
func NewReplaceCommand(expected AssignmentStamp, successorID WorldInstanceID, targetNodeID RuntimeNodeID) (ReplaceCommand, error) {
	if !expected.Valid() || !successorID.Valid() || !targetNodeID.Valid() || successorID == expected.InstanceID() {
		return ReplaceCommand{}, errors.New("replace command is incomplete or reuses predecessor")
	}
	return ReplaceCommand{expected: expected, successorID: successorID, targetNodeID: targetNodeID}, nil
}

// Expected 返回 cutover 必须完整比较的 predecessor stamp。
func (command ReplaceCommand) Expected() AssignmentStamp { return command.expected }

// SuccessorID 返回不确定重试必须复用的 WorldInstance identity。
func (command ReplaceCommand) SuccessorID() WorldInstanceID { return command.successorID }

// TargetNodeID 返回受信 rebuild/migration 节点 identity。
func (command ReplaceCommand) TargetNodeID() RuntimeNodeID { return command.targetNodeID }

// Valid 报告 replace command 是否经过完整构造且未复用 predecessor instance。
func (command ReplaceCommand) Valid() bool {
	return command.expected.Valid() && command.successorID.Valid() && command.targetNodeID.Valid() && command.successorID != command.expected.InstanceID()
}

// String 防止默认格式化展开 predecessor、successor 或目标节点 identity。
func (ReplaceCommand) String() string { return storeRequestPlaceholder }

// GoString 防止 `%#v` 展开 ReplaceCommand 私有字段。
func (ReplaceCommand) GoString() string { return storeRequestPlaceholder }

// LogValue 让结构化日志只记录稳定占位文本。
func (ReplaceCommand) LogValue() slog.Value { return slog.StringValue(storeRequestPlaceholder) }
