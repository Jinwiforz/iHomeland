package httpapi

import (
	"testing"

	"github.com/jinwiforz/ihomeland/server/internal/visitsession"
	"github.com/jinwiforz/ihomeland/server/internal/worldadmission"
)

// TestVisitErrorMappingPreservesOperationSemantics 验证eligibility过期不会伪装成invite过期。
func TestVisitErrorMappingPreservesOperationSemantics(t *testing.T) {
	tests := []struct {
		// name 标识当前公开语义分支。
		name string
		// operation 区分invite mutation与membership eligibility。
		operation visitsession.Operation
		// code 是领域owner返回的封闭分类。
		code visitsession.ErrorCode
		// wantCode 是errors.json登记的公开数值码。
		wantCode uint32
	}{
		{name: "accept expired invite", operation: visitsession.OperationAcceptInvite, code: visitsession.ErrorCodeExpired, wantCode: 2102},
		{name: "resolve expired membership", operation: visitsession.OperationResolve, code: visitsession.ErrorCodeExpired, wantCode: 2107},
		{name: "resolve invalid membership state", operation: visitsession.OperationResolve, code: visitsession.ErrorCodeInvalidState, wantCode: 2107},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := mapVisitError(test.operation, test.code); got.Code != test.wantCode {
				t.Fatalf("mapVisitError() code=%d want=%d", got.Code, test.wantCode)
			}
		})
	}
}

// TestAdmissionErrorMappingIncludesStaleAssignment 验证签发阶段的陈旧assignment使用冻结公开错误。
func TestAdmissionErrorMappingIncludesStaleAssignment(t *testing.T) {
	got := mapAdmissionError(worldadmission.ErrorCodeStaleAssignment)
	if got.Status != 409 || got.Code != 2002 || got.MessageKey != "error.world.assignment_stale" {
		t.Fatalf("mapAdmissionError()=%#v", got)
	}
}
