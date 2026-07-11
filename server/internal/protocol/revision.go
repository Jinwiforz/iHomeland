package protocol

import "fmt"

// ValidateSnapshotRevision 在 snapshot 覆盖客户端新状态前拒绝过期或重复 revision。
//
// current 与 incoming 必须来自同一权威 aggregate 的单调 revision。该函数不允许相等值作为
// 幂等成功，因为调用方只有在观察到严格更新时才能替换本地权威投影。
func ValidateSnapshotRevision(current uint64, incoming uint64) error {
	if incoming <= current {
		return fmt.Errorf("snapshot revision %d is not newer than %d", incoming, current)
	}
	return nil
}
