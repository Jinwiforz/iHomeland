package buildinfo

import (
	"strings"
	"testing"
)

// TestInfoValidate 固定版本响应长度与 RFC3339 构建时间边界。
func TestInfoValidate(t *testing.T) {
	valid := Info{Version: "0.0.0", Commit: "development", BuiltAt: "2026-07-11T00:00:00Z"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Info{
		{Version: "", Commit: "development"},
		{Version: "0.0.0", Commit: ""},
		{Version: "0.0.0", Commit: strings.Repeat("a", 65)},
		{Version: "0.0.0", Commit: "development", BuiltAt: "not-a-time"},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid build info should fail: %+v", invalid)
		}
	}
}
