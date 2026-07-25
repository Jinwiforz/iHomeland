// Package battleticket 使用共享 standalone Redis 实现 BattleTicket digest/handle-only issuance。
//
// Adapter 借用 client/keyspace，不保存 raw ticket secret、proof key 或 derivation key，不启动
// goroutine，也不拥有 HTTP、C++ child 或 target lifecycle。Redis 丢失后旧 issuance 不恢复。
package battleticket
