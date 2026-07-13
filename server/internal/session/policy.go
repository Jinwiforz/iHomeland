package session

import (
	"errors"
	"time"
)

const (
	// minimumTicketTTL 防止 ticket 因调度抖动在正常握手前失效。
	minimumTicketTTL = time.Second
	// maximumTicketTTL 限制一次性 bearer credential 的暴露窗口。
	maximumTicketTTL = 5 * time.Minute
	// minimumAccessTTL 避免客户端持续刷新造成无意义负载。
	minimumAccessTTL = time.Minute
	// maximumAccessTTL 限制 access token 被盗后的直接可用窗口。
	maximumAccessTTL = 24 * time.Hour
	// minimumRefreshTTL 允许客户端在短时离线后恢复 session。
	minimumRefreshTTL = time.Hour
	// maximumRefreshTTL 限制 refresh lineage 与 tombstone 的保存周期。
	maximumRefreshTTL = 90 * 24 * time.Hour
	// minimumSessionTTL 必须覆盖允许的最短 refresh 生命周期。
	minimumSessionTTL = time.Hour
	// maximumSessionTTL 控制单次认证 lineage 的绝对最长寿命。
	maximumSessionTTL = 90 * 24 * time.Hour
)

// Policy 保存 session/token/ticket 的启动后只读安全预算。
//
// 真实部署配置在 store 与 adapter 接线时映射到该值；当前 package 不读取环境。
type Policy struct {
	// ticketTTL 限制一次性连接资格暴露窗口。
	ticketTTL time.Duration
	// accessTTL 限制 bearer access token 的在线验证窗口。
	accessTTL time.Duration
	// refreshTTL 限制 refresh lineage 的最长轮换间隔。
	refreshTTL time.Duration
	// sessionTTL 是 token、tombstone 与 session record 的总寿命上限。
	sessionTTL time.Duration
}

// NewPolicy 校验独立范围和严格 TTL 顺序，避免长寿命 ticket 或短于 token 的 session。
func NewPolicy(ticketTTL time.Duration, accessTTL time.Duration, refreshTTL time.Duration, sessionTTL time.Duration) (Policy, error) {
	if ticketTTL < minimumTicketTTL || ticketTTL > maximumTicketTTL {
		return Policy{}, errors.New("ticket TTL is outside the safe range")
	}
	if accessTTL < minimumAccessTTL || accessTTL > maximumAccessTTL {
		return Policy{}, errors.New("access TTL is outside the safe range")
	}
	if refreshTTL < minimumRefreshTTL || refreshTTL > maximumRefreshTTL {
		return Policy{}, errors.New("refresh TTL is outside the safe range")
	}
	if sessionTTL < minimumSessionTTL || sessionTTL > maximumSessionTTL {
		return Policy{}, errors.New("session TTL is outside the safe range")
	}
	if !(ticketTTL < accessTTL && accessTTL < refreshTTL && refreshTTL <= sessionTTL) {
		return Policy{}, errors.New("TTL order must satisfy ticket < access < refresh <= session")
	}
	return Policy{ticketTTL: ticketTTL, accessTTL: accessTTL, refreshTTL: refreshTTL, sessionTTL: sessionTTL}, nil
}

// TicketTTL 返回一次性 ticket 有效 duration。
func (policy Policy) TicketTTL() time.Duration { return policy.ticketTTL }

// AccessTTL 返回 access token 有效 duration。
func (policy Policy) AccessTTL() time.Duration { return policy.accessTTL }

// RefreshTTL 返回 refresh token 有效 duration。
func (policy Policy) RefreshTTL() time.Duration { return policy.refreshTTL }

// SessionTTL 返回 session record 总寿命。
func (policy Policy) SessionTTL() time.Duration { return policy.sessionTTL }

// Valid 报告 policy 是否经过成功构造。
func (policy Policy) Valid() bool {
	return policy.ticketTTL >= minimumTicketTTL && policy.ticketTTL <= maximumTicketTTL &&
		policy.accessTTL >= minimumAccessTTL && policy.accessTTL <= maximumAccessTTL &&
		policy.refreshTTL >= minimumRefreshTTL && policy.refreshTTL <= maximumRefreshTTL &&
		policy.sessionTTL >= minimumSessionTTL && policy.sessionTTL <= maximumSessionTTL &&
		policy.ticketTTL < policy.accessTTL && policy.accessTTL < policy.refreshTTL && policy.refreshTTL <= policy.sessionTTL
}
