// Package battleentry 编排公开 HTTPS BattleTicket admission。
//
// 本包只从权威 Session、PersonalWorld/VisitSession、placement、SimulationTarget、capacity
// 与 endpoint owner 收集事实；它不依赖 Gin、Redis、generated DTO、socket 或 C++ handle。
package battleentry
