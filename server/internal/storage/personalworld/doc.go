// Package personalworld 实现 PersonalWorld 持久事实的 production MySQL adapter。
//
// 该包借用 storage/mysql 拥有的共享 *sql.DB，不创建或关闭 pool，也不参与 lifecycle。
// 所有 row 必须经领域 constructor 恢复；SQL、参数、identity 与幂等材料不得进入默认错误。
package personalworld
