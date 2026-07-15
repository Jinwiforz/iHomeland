package app

import (
	"strings"
	"testing"
)

// TestGameplayMutationIDsTranslateNamespaces 验证transport前缀不会进入VisitSession identifier正文。
func TestGameplayMutationIDsTranslateNamespaces(t *testing.T) {
	t.Parallel()
	binding, command, err := gameplayMutationIDs("tcp_0123456789abcdef0123456789abcdef", strings.Repeat("ab", 16))
	if err != nil || !binding.Valid() || !command.Valid() {
		t.Fatalf("gameplayMutationIDs binding=%v command=%v err=%v", binding, command, err)
	}
	if _, _, err := gameplayMutationIDs("invalid", strings.Repeat("ab", 16)); err == nil {
		t.Fatal("connection identity without tcp_ namespace was accepted")
	}
}
