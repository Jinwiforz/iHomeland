// Package placement 实现 WorldInstance placement 的 production MySQL/Redis adapter。
//
// 该包借用 storage runtime 拥有的共享 *sql.DB、*redis.Client 与 Keyspace，不关闭资源、
// 不启动 goroutine。MySQL 拥有不可回退 allocation；Redis 只拥有可失效 current assignment。
package placement
