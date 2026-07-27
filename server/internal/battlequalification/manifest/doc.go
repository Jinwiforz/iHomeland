// Package manifest 加载并交叉校验 B0.6 fault execution 与只读 B0.2 profile。
//
// 本包只把 tracked closed corpus 投影为运行时配置；它不提供默认值，也不允许调用方
// 覆盖 seed、fault 参数、queue、MTU、deadline 或 actor 数。
package manifest
