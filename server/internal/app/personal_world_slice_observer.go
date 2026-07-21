package app

// personalWorldSliceObserver 只接收竖切资源数量与封闭低基数结果。
//
// 实现不得接受 PlayerID、VisitSessionID、invite、ConnectionID、IP、payload、credential、
// backend 错误文本或完整 AssignmentStamp。
type personalWorldSliceObserver interface {
	// SetWorldRuntimes 更新当前进程逻辑 WorldInstance 数量。
	SetWorldRuntimes(int)
	// SetSemanticDeadlines 更新单 worker 持有的语义 deadline 数量。
	SetSemanticDeadlines(int)
	// ObserveWorldLease 记录 renewed|retry|lost|expired|failed。
	ObserveWorldLease(string)
	// ObserveSemanticDeadline 记录固定 kind 与 executed|retry|stale|failed。
	ObserveSemanticDeadline(string, string)
	// ObserveVisitLifecycle 记录 connect|disconnect|assignment_invalidate|stale_open_reconcile 的封闭结果。
	ObserveVisitLifecycle(string, string)
	// ObserveVisitDelivery 记录固定 delivery kind 与稳定结果。
	ObserveVisitDelivery(string, string)
}
