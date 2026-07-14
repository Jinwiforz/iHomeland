// Package session 使用共享 standalone Redis 实现 SessionStore 的原子安全运行态。
//
// 所有 token/ticket key 只包含不可逆 digest，value 使用 versioned Hash 并由 owner Lua
// scripts 在线性化点校验和迁移。该包不恢复 Redis flush 前的 session，不拥有 client、
// listener、goroutine 或 memory fallback，也不记录完整 key、value、credential 或 principal。
package session
