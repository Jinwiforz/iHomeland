// Package battleticketcontrol 把 BattleTicket domain material 映射到唯一 simulation-control pipe。
//
// 本 adapter 不拥有 issuance、target freshness、capacity 或 HTTP credential；它只发送
// exact child install/status/revoke，并严格校验低敏 receipt。
package battleticketcontrol
