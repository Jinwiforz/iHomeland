// Package httpapi 实现冻结OpenAPI的公开HTTP transport与显式listener生命周期。
//
// Gin只在本包内负责路由和middleware组合；领域、application与storage均不依赖Gin或HTTP。
package httpapi

import "time"

// IdempotencyMode 描述operation的冻结重试语义。
type IdempotencyMode uint8

const (
	// IdempotencyUnspecified 禁止登记route。
	IdempotencyUnspecified IdempotencyMode = iota
	// IdempotencySafe 表示只读安全operation。
	IdempotencySafe
	// IdempotencyIdempotent 表示无额外key即可重复提交。
	IdempotencyIdempotent
	// IdempotencyNonIdempotent 表示每次提交都是新尝试。
	IdempotencyNonIdempotent
	// IdempotencyKeyRequired 表示必须提供受限Idempotency-Key。
	IdempotencyKeyRequired
)

// Operation 是route与执行策略的唯一集中登记项。
type Operation struct {
	// ID 与OpenAPI operationId精确一致。
	ID string
	// Method 是大写HTTP method。
	Method string
	// Path 是Gin参数模板，与OpenAPI path语义一致。
	Path string
	// Authenticated 决定是否执行Bearer认证。
	Authenticated bool
	// BodyLimit 是请求体最大bytes；零表示必须无body。
	BodyLimit int64
	// Timeout 是application调用总预算。
	Timeout time.Duration
	// Idempotency 固定重试语义。
	Idempotency IdempotencyMode
}

// Operations 返回不可变副本，调用方不能修改runtime route策略。
func Operations() []Operation { return append([]Operation(nil), operations...) }

// operations 是runtime路由与执行预算的私有唯一事实，初始化后只读且只通过Operations返回副本。
var operations = []Operation{
	{ID: "getVersion", Method: "GET", Path: "/v1/version", BodyLimit: 0, Timeout: 3 * time.Second, Idempotency: IdempotencySafe},
	{ID: "getBootstrapConfig", Method: "GET", Path: "/v1/config", BodyLimit: 0, Timeout: 3 * time.Second, Idempotency: IdempotencySafe},
	{ID: "registerAccount", Method: "POST", Path: "/v1/auth/register", BodyLimit: 4096, Timeout: 10 * time.Second, Idempotency: IdempotencyNonIdempotent},
	{ID: "loginAccount", Method: "POST", Path: "/v1/auth/login", BodyLimit: 4096, Timeout: 10 * time.Second, Idempotency: IdempotencyNonIdempotent},
	{ID: "refreshSession", Method: "POST", Path: "/v1/auth/refresh", BodyLimit: 4096, Timeout: 5 * time.Second, Idempotency: IdempotencyNonIdempotent},
	{ID: "logoutSession", Method: "POST", Path: "/v1/auth/logout", Authenticated: true, BodyLimit: 0, Timeout: 5 * time.Second, Idempotency: IdempotencyIdempotent},
	{ID: "issueConnectionTicket", Method: "POST", Path: "/v1/session/tickets", Authenticated: true, BodyLimit: 2048, Timeout: 5 * time.Second, Idempotency: IdempotencyNonIdempotent},
	{ID: "getWorldBootstrap", Method: "GET", Path: "/v1/world/bootstrap", Authenticated: true, BodyLimit: 0, Timeout: 5 * time.Second, Idempotency: IdempotencySafe},
	{ID: "acceptVisitInvite", Method: "POST", Path: "/v1/visits/:visitSessionId/invites/:inviteId/accept", Authenticated: true, BodyLimit: 4096, Timeout: 5 * time.Second, Idempotency: IdempotencyKeyRequired},
	{ID: "issueWorldAdmission", Method: "POST", Path: "/v1/world/admissions", Authenticated: true, BodyLimit: 4096, Timeout: 5 * time.Second, Idempotency: IdempotencyKeyRequired},
	{ID: "issueBattleTicket", Method: "POST", Path: "/v1/battle/tickets", Authenticated: true, BodyLimit: 2048, Timeout: 5 * time.Second, Idempotency: IdempotencyKeyRequired},
}
