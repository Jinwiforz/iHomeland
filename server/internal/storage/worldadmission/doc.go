// Package worldadmission 使用共享 standalone Redis 实现一次性 world admission 原子存储。
//
// Adapter 借用 client/keyspace，不保存 raw credential，不启动 goroutine，也不拥有 HTTP、TLS/TCP、
// placement 或 VisitSession 生命周期。Redis 丢失后旧 admission 永久失效。
package worldadmission
