// Package visitsession 用共享 standalone Redis client 实现 VisitSessionStore。
//
// Adapter 只拥有 versioned key schema、codec 与 owner Lua 线性化规则，不拥有 Redis client、
// application service、deadline task、admission credential、listener 或 memory fallback。
package visitsession
