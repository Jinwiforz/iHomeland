// Package account 提供 Account owner 的 production MySQL repository 与 password hasher。
//
// 该包借用共享 storage runtime，不拥有 pool、listener 或 lifecycle。Plaintext password
// 只能在 Argon2id 当前调用中短暂存在；repository 只保存自描述 PHC hash，并且默认错误、
// 日志和 metrics 不得包含 username、identity、SQL、credential 或完整持久值。
package account
