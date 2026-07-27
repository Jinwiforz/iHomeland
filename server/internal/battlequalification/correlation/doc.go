// Package correlation 生成 B0.6 四源 evidence 使用的 run-local 低敏关联键。
//
// 本包只保留 client slot、代际、workload phase 与 keyed digest；PlayerID、完整
// AssignmentStamp、SimulationInstanceID、remote endpoint 和 credential 都不得进入输出。
package correlation
