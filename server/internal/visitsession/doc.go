// Package visitsession 实现个人世界临时访客资格的领域模型与应用边界。
//
// 该包只拥有邀请、访客 membership、临时连接绑定、断线 grace、到期与安全返回结果。
// PersonalWorld、placement assignment、认证 session、admission credential 和网络连接仍由
// 各自模块管理。包内不启动 goroutine 或 timer，也不依赖 transport、generated protocol、
// MySQL 或 Redis；正式 adapter 完成前不得把测试 store 接入生产 Composition Root。
package visitsession
