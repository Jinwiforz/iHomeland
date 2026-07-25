package httpapi

import (
	"context"
	"errors"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/battleticket"
	"github.com/jinwiforz/ihomeland/server/internal/session"
	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
	"github.com/jinwiforz/ihomeland/server/internal/worldentry"
)

// catalogError 是errors.json冻结条目的runtime表示。
type catalogError struct {
	// Status 是HTTP response status。
	Status int
	// Code 是errors.json登记的稳定数值错误码。
	Code uint32
	// MessageKey 是客户端本地化使用的稳定消息键。
	MessageKey string
	// Retryable 表示相同语义请求稍后是否可能成功。
	Retryable bool
}

// 以下条目是多个owner共享的errors.json冻结投影；不得按handler复制或附加backend文本。
var (
	validationError      = catalogError{Status: 400, Code: 200, MessageKey: "error.validation.failed"}
	unauthenticated      = catalogError{Status: 401, Code: 100, MessageKey: "error.auth.unauthenticated"}
	forbidden            = catalogError{Status: 403, Code: 101, MessageKey: "error.auth.forbidden"}
	invalidCredential    = catalogError{Status: 401, Code: 102, MessageKey: "error.auth.invalid_credentials"}
	usernameConflict     = catalogError{Status: 409, Code: 104, MessageKey: "error.account.username_taken"}
	dependencyError      = catalogError{Status: 503, Code: 500, MessageKey: "error.dependency.unavailable", Retryable: true}
	internalError        = catalogError{Status: 500, Code: 501, MessageKey: "error.internal", Retryable: true}
	rateLimited          = catalogError{Status: 429, Code: 400, MessageKey: "error.rate_limited", Retryable: true}
	battleTargetNotReady = catalogError{
		Status: 503, Code: 3000, MessageKey: "error.battle.target_not_ready", Retryable: true,
	}
	battleCapacityExceeded = catalogError{
		Status: 409, Code: 3001, MessageKey: "error.battle.capacity_exceeded",
	}
	battleTargetStale = catalogError{
		Status: 409, Code: 3002, MessageKey: "error.battle.target_stale",
	}
	battleIdempotencyConflict = catalogError{
		Status: 409, Code: 3003, MessageKey: "error.battle.idempotency_conflict",
	}
)

// mapApplicationError 集中把领域错误映射到errors.json，不回显cause或错误文本。
func mapApplicationError(err error) catalogError {
	if err == nil {
		return internalError
	}
	switch account.ErrorKindOf(err) {
	case account.ErrorKindValidation:
		return validationError
	case account.ErrorKindUsernameConflict:
		return usernameConflict
	case account.ErrorKindInvalidCredentials:
		return invalidCredential
	case account.ErrorKindDependencyUnavailable:
		return dependencyError
	}
	switch session.ErrorKindOf(err) {
	case session.ErrorKindInvalidArgument:
		return validationError
	case session.ErrorKindUnauthenticated, session.ErrorKindExpired:
		return unauthenticated
	case session.ErrorKindForbidden:
		return forbidden
	case session.ErrorKindDependencyUnavailable:
		return dependencyError
	}
	switch battleticket.ErrorCodeOf(err) {
	case battleticket.ErrorCodeInvalidArgument:
		return validationError
	case battleticket.ErrorCodeTargetNotReady:
		return battleTargetNotReady
	case battleticket.ErrorCodeCapacityExceeded:
		return battleCapacityExceeded
	case battleticket.ErrorCodeTargetStale:
		return battleTargetStale
	case battleticket.ErrorCodeIdempotencyConflict:
		return battleIdempotencyConflict
	case battleticket.ErrorCodeExpired:
		return unauthenticated
	case battleticket.ErrorCodeDependency, battleticket.ErrorCodeCommitUnknown:
		return dependencyError
	case battleticket.ErrorCodeDependencyDefect:
		return internalError
	}
	switch {
	case worldentry.IsErrorCode(err, worldentry.ErrorCodeValidation):
		return validationError
	case worldentry.IsErrorCode(err, worldentry.ErrorCodeWorldNotReady):
		return catalogError{Status: 409, Code: 2001, MessageKey: "error.world.not_ready", Retryable: true}
	case worldentry.IsErrorCode(err, worldentry.ErrorCodeAssignmentStale):
		return catalogError{Status: 409, Code: 2002, MessageKey: "error.world.assignment_stale"}
	case worldentry.IsErrorCode(err, worldentry.ErrorCodeDependency):
		return dependencyError
	case worldentry.IsErrorCode(err, worldentry.ErrorCodeDependencyDefect):
		return internalError
	}
	var visitFailure *visitsession.Error
	if errors.As(err, &visitFailure) {
		return mapVisitError(visitFailure.Operation(), visitFailure.Code())
	}
	var admissionFailure *worldadmission.Error
	if errors.As(err, &admissionFailure) {
		return mapAdmissionError(admissionFailure.Code())
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return dependencyError
	}
	return internalError
}

// mapVisitError 按operation语义映射VisitSession封闭错误码。
func mapVisitError(operation visitsession.Operation, code visitsession.ErrorCode) catalogError {
	if operation == visitsession.OperationResolve {
		switch code {
		case visitsession.ErrorCodeNotFound, visitsession.ErrorCodeExpired, visitsession.ErrorCodeInvalidState, visitsession.ErrorCodeForbidden:
			return catalogError{Status: 403, Code: 2107, MessageKey: "error.visit.membership_required"}
		}
	}
	switch code {
	case visitsession.ErrorCodeInvalidArgument:
		return validationError
	case visitsession.ErrorCodeNotFound:
		if operation == visitsession.OperationAcceptInvite {
			return catalogError{Status: 404, Code: 2101, MessageKey: "error.visit.invite_not_found"}
		}
		return catalogError{Status: 404, Code: 2100, MessageKey: "error.visit.not_found"}
	case visitsession.ErrorCodeStale:
		return catalogError{Status: 409, Code: 2002, MessageKey: "error.world.assignment_stale"}
	case visitsession.ErrorCodeExpired:
		return catalogError{Status: 410, Code: 2102, MessageKey: "error.visit.invite_expired"}
	case visitsession.ErrorCodeCapacity:
		return catalogError{Status: 409, Code: 2103, MessageKey: "error.visit.capacity_exceeded"}
	case visitsession.ErrorCodeInvalidState:
		return catalogError{Status: 409, Code: 2104, MessageKey: "error.visit.state_conflict"}
	case visitsession.ErrorCodeRevisionConflict:
		return catalogError{Status: 409, Code: 2105, MessageKey: "error.visit.revision_conflict"}
	case visitsession.ErrorCodeIdempotencyConflict:
		return catalogError{Status: 409, Code: 2106, MessageKey: "error.visit.idempotency_conflict"}
	case visitsession.ErrorCodeForbidden:
		return catalogError{Status: 403, Code: 2107, MessageKey: "error.visit.membership_required"}
	case visitsession.ErrorCodeDependency, visitsession.ErrorCodeCommitUnknown:
		return dependencyError
	case visitsession.ErrorCodeDependencyDefect:
		return internalError
	default:
		return internalError
	}
}

// mapAdmissionError 把WorldAdmission封闭错误码映射为公开错误目录。
func mapAdmissionError(code worldadmission.ErrorCode) catalogError {
	switch code {
	case worldadmission.ErrorCodeInvalidArgument:
		return validationError
	case worldadmission.ErrorCodeStaleAssignment:
		return catalogError{Status: 409, Code: 2002, MessageKey: "error.world.assignment_stale"}
	case worldadmission.ErrorCodeInvalid:
		return catalogError{Status: 401, Code: 2003, MessageKey: "error.world.admission_invalid"}
	case worldadmission.ErrorCodeExpired:
		return catalogError{Status: 401, Code: 2004, MessageKey: "error.world.admission_expired"}
	case worldadmission.ErrorCodeReplayed:
		return catalogError{Status: 409, Code: 2005, MessageKey: "error.world.admission_replayed"}
	case worldadmission.ErrorCodeIdempotencyConflict:
		return catalogError{Status: 409, Code: 2006, MessageKey: "error.world.idempotency_conflict"}
	case worldadmission.ErrorCodeDependency, worldadmission.ErrorCodeCommitUnknown:
		return dependencyError
	case worldadmission.ErrorCodeDependencyDefect:
		return internalError
	default:
		return internalError
	}
}
