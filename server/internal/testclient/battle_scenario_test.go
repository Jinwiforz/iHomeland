package testclient

import "testing"

// TestBattleAdmissionPlanClosedBounds 验证 workload 只接受 1..33 membership 与 1..9 battle actor。
func TestBattleAdmissionPlanClosedBounds(t *testing.T) {
	valid := []BattleAdmissionPlan{
		{MembershipCount: 1, BattleActorCount: 1},
		{MembershipCount: 5, BattleActorCount: 5, ResponseLossSlot: 1},
		{MembershipCount: 8, BattleActorCount: 8, ResponseLossSlot: 8},
		{MembershipCount: 9, BattleActorCount: 9},
		{MembershipCount: 33, BattleActorCount: 9},
	}
	for _, plan := range valid {
		if err := plan.validate(); err != nil {
			t.Fatalf("valid plan %+v: %v", plan, err)
		}
	}
	invalid := []BattleAdmissionPlan{
		{},
		{MembershipCount: 34, BattleActorCount: 1},
		{MembershipCount: 5, BattleActorCount: 6},
		{MembershipCount: 9, BattleActorCount: 10},
		{MembershipCount: 9, BattleActorCount: 9, ResponseLossSlot: 9},
	}
	for _, plan := range invalid {
		if err := plan.validate(); err == nil {
			t.Fatalf("invalid plan accepted: %+v", plan)
		}
	}
}

// TestBattleAdmissionRejectsIncompleteRuntime 验证不会在缺少公开 HTTP owner 时创建资源。
func TestBattleAdmissionRejectsIncompleteRuntime(t *testing.T) {
	if admission, err := PrepareBattleAdmission(
		t.Context(), &ScenarioRuntime{},
		BattleAdmissionPlan{MembershipCount: 1, BattleActorCount: 1},
	); err == nil || admission != nil {
		t.Fatal("incomplete battle admission runtime was accepted")
	}
}
