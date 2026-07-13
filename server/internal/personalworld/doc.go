// Package personalworld 实现个人持久世界的身份、生命周期和 revision 规则。
//
// 该包只拥有 PersonalWorld 的持久元数据，不依赖 transport、generated protocol、
// WorldInstance、VisitSession 或 storage adapter。调用方必须从可信认证边界取得
// account.PlayerID；客户端 payload 不能覆盖 immutable owner。Production repository
// 由后续 storage change 提供，本包不包含 memory fallback。
package personalworld
