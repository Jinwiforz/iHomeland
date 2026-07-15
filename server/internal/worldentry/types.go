// Package worldentry 编排公开HTTP进入PersonalWorld所需的三个跨owner用例。
//
// 本包不持有业务事实、credential store、socket或transport类型；所有结果均为显式
// client-safe projection，完整assignment与binding只在调用栈内短暂存在。
package worldentry

import (
	"errors"
	"log/slog"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/personalworld"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
)

// redactedValue 是world-entry敏感结果实现String、GoString与LogValue共享的稳定占位文本。
const redactedValue = "[REDACTED_WORLD_ENTRY]"

// AssignmentProjection 是可返回客户端的current active assignment子集。
type AssignmentProjection struct {
	// WorldID 是assignment所属PersonalWorld。
	WorldID personalworld.PersonalWorldID
	// InstanceID 是客户端关联当前实例使用的opaque identity。
	InstanceID string
	// Endpoint 是受验证配置提供的TLS/TCP公开地址。
	Endpoint session.Endpoint
	// Generation 是current assignment单调代次，不包含fencing token。
	Generation uint64
	// LeaseExpiresAt 是等于即失效的UTC微秒deadline。
	LeaseExpiresAt time.Time
}

// Valid 报告投影是否完整且没有暴露内部node/fence字段。
func (projection AssignmentProjection) Valid() bool {
	return projection.WorldID.Valid() && projection.InstanceID != "" && projection.Endpoint.Valid() && projection.Endpoint.Channel() == session.ChannelTLSTCP && projection.Generation > 0 && !projection.LeaseExpiresAt.IsZero()
}

// String 防止默认格式化展开world、instance与endpoint identity。
func (AssignmentProjection) String() string { return redactedValue }

// GoString 防止 `%#v` 绕过AssignmentProjection脱敏边界。
func (AssignmentProjection) GoString() string { return redactedValue }

// LogValue 只允许结构化日志记录稳定占位文本。
func (AssignmentProjection) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// BootstrapResult 是own-world持久事实与可选运行态投影。
type BootstrapResult struct {
	// World 是owner唯一primary world的持久snapshot。
	World personalworld.Snapshot
	// Assignment 只在current assignment active且lease有效时存在。
	Assignment AssignmentProjection
}

// Valid 报告bootstrap至少包含有效active world；assignment允许为空。
func (result BootstrapResult) Valid() bool {
	return result.World.Valid() && result.World.Lifecycle() == personalworld.LifecycleActive && (result.Assignment == AssignmentProjection{} || result.Assignment.Valid())
}

// String 防止默认格式化递归展开world与assignment identity。
func (BootstrapResult) String() string { return redactedValue }

// GoString 防止 `%#v` 绕过BootstrapResult脱敏边界。
func (BootstrapResult) GoString() string { return redactedValue }

// LogValue 只允许结构化日志记录稳定占位文本。
func (BootstrapResult) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// ReservationResult 是invite accept后可公开的reservation子集。
type ReservationResult struct {
	// VisitSessionID 是reservation所属aggregate。
	VisitSessionID visitsession.VisitSessionID
	// Revision 是accept提交后的aggregate revision。
	Revision visitsession.Revision
	// ExpiresAt 是等于即失效的UTC微秒deadline。
	ExpiresAt time.Time
}

// Valid 报告reservation投影是否完整。
func (result ReservationResult) Valid() bool {
	return result.VisitSessionID.Valid() && result.Revision.Valid() && !result.ExpiresAt.IsZero()
}

// String 防止默认格式化展开VisitSession identity与deadline。
func (ReservationResult) String() string { return redactedValue }

// GoString 防止 `%#v` 绕过ReservationResult脱敏边界。
func (ReservationResult) GoString() string { return redactedValue }

// LogValue 只允许结构化日志记录稳定占位文本。
func (ReservationResult) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// AdmissionTargetKind 是公开签发请求的封闭目标类型。
type AdmissionTargetKind uint8

const (
	// AdmissionTargetUnspecified 禁止进入application编排。
	AdmissionTargetUnspecified AdmissionTargetKind = iota
	// AdmissionTargetOwnWorld 表示认证玩家自己的primary world。
	AdmissionTargetOwnWorld
	// AdmissionTargetVisitWorld 表示已有reservation或reconnect membership。
	AdmissionTargetVisitWorld
)

// AdmissionTarget 保存解析后的目标；actor、role和purpose不从payload取得。
type AdmissionTarget struct {
	// Kind 选择own或visit分支。
	Kind AdmissionTargetKind
	// VisitSessionID 仅在visit分支存在。
	VisitSessionID visitsession.VisitSessionID
}

// Valid 报告目标字段组合是否封闭且完整。
func (target AdmissionTarget) Valid() bool {
	return (target.Kind == AdmissionTargetOwnWorld && !target.VisitSessionID.Valid()) || (target.Kind == AdmissionTargetVisitWorld && target.VisitSessionID.Valid())
}

// String 防止默认格式化展开目标VisitSession identity。
func (AdmissionTarget) String() string { return redactedValue }

// GoString 防止 `%#v` 绕过AdmissionTarget脱敏边界。
func (AdmissionTarget) GoString() string { return redactedValue }

// LogValue 只允许结构化日志记录稳定占位文本。
func (AdmissionTarget) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// AdmissionResult 是允许HTTP codec编码的credential与公开binding子集。
type AdmissionResult struct {
	// Credential 是唯一允许离开application边界的opaque bearer。
	Credential worldadmission.Credential
	// Endpoint 是credential唯一允许消费的TLS/TCP地址。
	Endpoint session.Endpoint
	// Role 由权威world或membership决定。
	Role worldadmission.Role
	// Purpose 由目标与membership state决定。
	Purpose worldadmission.Purpose
	// ExpiresAt 是等于即失效的UTC微秒deadline。
	ExpiresAt time.Time
}

// Valid 报告公开签发结果字段是否完整。
func (result AdmissionResult) Valid() bool {
	return result.Credential.Valid() && result.Endpoint.Valid() && result.Role.Valid() && result.Purpose.Valid() && !result.ExpiresAt.IsZero()
}

// String 防止默认格式化递归展开credential与内部identity。
func (AdmissionResult) String() string { return redactedValue }

// GoString 与String保持相同安全边界。
func (AdmissionResult) GoString() string { return redactedValue }

// LogValue 仅记录稳定占位文本。
func (AdmissionResult) LogValue() slog.Value { return slog.StringValue(redactedValue) }

// validateIdempotencyKey 在application边界复核有界安全ASCII，防止非HTTP调用绕过约束。
func validateIdempotencyKey(value string) error {
	if len(value) < 16 || len(value) > 128 {
		return errors.New("idempotency key length is invalid")
	}
	for _, character := range []byte(value) {
		if !idempotencyKeyCharacterAllowed(character) {
			return errors.New("idempotency key contains unsafe characters")
		}
	}
	return nil
}

// idempotencyKeyCharacterAllowed 与公开OpenAPI安全ASCII pattern保持精确一致。
func idempotencyKeyCharacterAllowed(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == ':' || character == '-'
}
