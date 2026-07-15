package core

import "testing"

func TestPolicyAttemptBudget_CompactUsesDedicatedBudget(t *testing.T) {
	policy := DefaultPolicy()
	if got := policy.AttemptBudget(CapabilityResponsesCompact); got != 0 {
		t.Fatalf("compact attempt budget = %d, want dynamic zero", got)
	}
	policy.MaxAttemptsCompact = 2
	if got := policy.AttemptBudget(CapabilityResponsesCompact); got != 2 {
		t.Fatalf("explicit compact attempt budget = %d, want 2", got)
	}
	if got := policy.AttemptBudget(CapabilityResponses); got != 3 {
		t.Fatalf("responses attempt budget = %d, want 3", got)
	}
}
