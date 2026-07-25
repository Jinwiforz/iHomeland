// Package battleticket 定义 BattleTicket 的纯 Go 授权值、确定性密钥派生与幂等策略。
//
// 本包不依赖 HTTP、Gin、Redis、generated DTO、socket 或 C++ process handle。外层 application
// 必须先从权威 Session、PersonalWorld/VisitSession、placement 与 SimulationTarget 组装 Facts，
// 再把派生结果交给 storage 和 private control adapter。
package battleticket
