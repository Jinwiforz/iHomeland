package visitsession

import (
	"log/slog"
	"time"

	"github.com/jinwiforz/ihomeland/server/internal/account"
	"github.com/jinwiforz/ihomeland/server/internal/placement"
	"github.com/jinwiforz/ihomeland/server/internal/session"
)

// BattleEligibility 是 VisitSession owner 对当前 joined Visitor 的只读战斗资格。
//
// 该投影不包含 credential、endpoint 或 connection binding；它只证明调用时的权威
// membership、session lineage、assignment 与最早失效时间。BattleTicket owner 仍须
// 独立复核 current SimulationTarget、capacity 和 ticket install。
type BattleEligibility struct {
	visitorID  account.PlayerID
	sessionID  session.SessionID
	epoch      session.Epoch
	assignment placement.AssignmentStamp
	expiresAt  time.Time
	revision   Revision
}

// VisitorID 返回权威 membership 的 Visitor。
func (eligibility BattleEligibility) VisitorID() account.PlayerID { return eligibility.visitorID }

// SessionID 返回 membership 绑定的 session lineage。
func (eligibility BattleEligibility) SessionID() session.SessionID { return eligibility.sessionID }

// Epoch 返回 membership 绑定的 session 撤销屏障。
func (eligibility BattleEligibility) Epoch() session.Epoch { return eligibility.epoch }

// Assignment 返回 VisitSession 固定的完整 assignment。
func (eligibility BattleEligibility) Assignment() placement.AssignmentStamp {
	return eligibility.assignment
}

// ExpiresAt 返回资格的最早绝对失效时间。
func (eligibility BattleEligibility) ExpiresAt() time.Time { return eligibility.expiresAt }

// Revision 返回资格判断读取的 VisitSession CAS 版本。
func (eligibility BattleEligibility) Revision() Revision { return eligibility.revision }

// Valid 报告投影是否包含完整、封闭且可复核的资格事实。
func (eligibility BattleEligibility) Valid() bool {
	return eligibility.visitorID.Valid() && eligibility.sessionID.Valid() &&
		eligibility.epoch.Valid() && eligibility.assignment.Valid() &&
		!eligibility.expiresAt.IsZero() && eligibility.revision.Valid()
}

// String 防止默认格式化展开 actor、lineage 与 assignment。
func (BattleEligibility) String() string { return admissionPlaceholder }

// GoString 与 String 保持相同脱敏边界。
func (BattleEligibility) GoString() string { return admissionPlaceholder }

// LogValue 仅输出稳定占位文本。
func (BattleEligibility) LogValue() slog.Value { return slog.StringValue(admissionPlaceholder) }

// newBattleEligibility 只允许 Service 在完成权威读取后创建战斗资格。
func newBattleEligibility(
	visitorID account.PlayerID,
	sessionID session.SessionID,
	epoch session.Epoch,
	assignment placement.AssignmentStamp,
	expiresAt time.Time,
	revision Revision,
) BattleEligibility {
	return BattleEligibility{
		visitorID: visitorID, sessionID: sessionID, epoch: epoch,
		assignment: assignment, expiresAt: canonicalOptionalTime(expiresAt),
		revision: revision,
	}
}
