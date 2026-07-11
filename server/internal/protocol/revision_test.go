package protocol

import "testing"

// TestSnapshotRevisionMustIncrease 保护客户端权威投影只能被严格更新的 aggregate revision 替换。
// 低值与相等值分别覆盖乱序 push 和重复投递；更高值确认正常状态推进不会被误拒绝。
func TestSnapshotRevisionMustIncrease(t *testing.T) {
	tests := []struct {
		// name 描述 incoming 相对 current 的顺序关系。
		name string
		// current 是客户端已经采用的权威 revision。
		current uint64
		// incoming 是准备覆盖本地投影的 snapshot revision。
		incoming uint64
		// wantError 表示该顺序必须被拒绝。
		wantError bool
	}{
		{name: "stale", current: 7, incoming: 6, wantError: true},
		{name: "duplicate", current: 7, incoming: 7, wantError: true},
		{name: "newer", current: 7, incoming: 8, wantError: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSnapshotRevision(test.current, test.incoming)
			if (err != nil) != test.wantError {
				t.Fatalf("ValidateSnapshotRevision(%d, %d) error = %v", test.current, test.incoming, err)
			}
		})
	}
}
