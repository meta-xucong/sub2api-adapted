package core

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOrderFiltersExactModelAndCapability(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	plan := Order(RouteRequest{Model: "GPT-5.6-LUNA", Capability: CapabilityResponses, NowUnix: 100, Seed: 1}, []LaneSnapshot{
		{LaneID: "luna", AccountID: 1, ModelPatterns: []string{"gpt-5.6-luna"}, Capabilities: map[Capability]bool{CapabilityResponses: true}},
		{LaneID: "sol", AccountID: 2, ModelPatterns: []string{"gpt-5.6-sol"}, Capabilities: map[Capability]bool{CapabilityResponses: true}},
		{LaneID: "chat-only", AccountID: 3, ModelPatterns: []string{"gpt-5.6-luna"}, Capabilities: map[Capability]bool{CapabilityChat: true}},
	}, policy)

	if got, want := plan.OrderedLaneIDs, []string{"luna"}; !equalStrings(got, want) {
		t.Fatalf("ordered lanes = %v, want %v", got, want)
	}
	if plan.SkipReasons["sol"] != "model_mismatch" {
		t.Fatalf("sol skip reason = %q, want model_mismatch", plan.SkipReasons["sol"])
	}
	if plan.SkipReasons["chat-only"] != "capability_mismatch" {
		t.Fatalf("chat-only skip reason = %q, want capability_mismatch", plan.SkipReasons["chat-only"])
	}
	if ExactModelKey(" GPT-5.6-LUNA ") != "gpt-5.6-luna" || !ModelMatches("gpt-5.6-*", "GPT-5.6-SOL") {
		t.Fatal("model canonicalization or prefix matching failed")
	}
}

func TestOrderSourceGroupExclusionAndConcurrency(t *testing.T) {
	policy := DefaultPolicy()
	policy.Enabled = true
	policy.SameSourceGroupAttempts = 1
	request := RouteRequest{
		Capability:           CapabilityChat,
		Seed:                 2,
		ExcludedSourceGroups: map[string]struct{}{"blocked": {}},
	}
	plan := Order(request, []LaneSnapshot{
		{LaneID: "busy-a", AccountID: 1, SourceGroup: "same", CurrentConcurrency: 1, SourceGroupMaxConcurrency: 1},
		{LaneID: "busy-b", AccountID: 2, SourceGroup: "same", SourceGroupMaxConcurrency: 1},
		{LaneID: "open", AccountID: 3, SourceGroup: "open", SourceGroupMaxConcurrency: 1},
		{LaneID: "excluded", AccountID: 4, SourceGroup: "blocked"},
	}, policy)

	if len(plan.OrderedLaneIDs) != 1 || plan.OrderedLaneIDs[0] != "open" {
		t.Fatalf("ordered lanes = %v, want [open]", plan.OrderedLaneIDs)
	}
	if plan.SkipReasons["busy-a"] != "source_group_concurrency_full" || plan.SkipReasons["busy-b"] != "source_group_concurrency_full" {
		t.Fatalf("source-group skip reasons = %v", plan.SkipReasons)
	}
	if plan.SkipReasons["excluded"] != "excluded_source_group" {
		t.Fatalf("excluded skip reason = %q", plan.SkipReasons["excluded"])
	}
	if NormalizeSourceGroup(LaneSnapshot{AccountID: 7}) != "account:7" {
		t.Fatal("account fallback source group was not normalized")
	}
}

func TestClassifierSeparatesModelAndConcurrency429(t *testing.T) {
	if got := ClassifyFailureDetails(http.StatusNotFound, CapabilityResponses, "model_not_found", "", false); got != FailureCapabilityError {
		t.Fatalf("model failure class = %q, want %q", got, FailureCapabilityError)
	}
	if got := ClassifyFailureDetails(http.StatusTooManyRequests, CapabilityChat, "too many concurrent requests", "", false); got != FailureConcurrencyLimited {
		t.Fatalf("concurrency failure class = %q, want %q", got, FailureConcurrencyLimited)
	}
	if got := ClassifyFailure(http.StatusTooManyRequests, CapabilityChat, false); got != FailureRateLimited {
		t.Fatalf("generic 429 failure class = %q, want %q", got, FailureRateLimited)
	}
}

func TestRateLimitPolicyParsesRetryAfterAndBoundsDelay(t *testing.T) {
	policy := DefaultRateLimitBackoffConfig()
	policy.JitterRatio = 0
	if got := policy.Delay(0, 30*time.Second, 1); got != 30*time.Second {
		t.Fatalf("retry-after delay = %s, want 30s", got)
	}
	if got := policy.Delay(4, 0, 1); got != 0 {
		t.Fatalf("delay after max attempts = %s, want 0", got)
	}
	now := time.Unix(100, 0).UTC()
	date := now.Add(12 * time.Second).Format(http.TimeFormat)
	if got := ParseRetryAfter(http.Header{"Retry-After": []string{date}}, now, time.Minute); got != 12*time.Second {
		t.Fatalf("HTTP-date retry-after = %s, want 12s", got)
	}
	if !IsConcurrencyRateLimit("Concurrency limit exceeded", "") || IsConcurrencyRateLimit("rate limit exceeded", "rate_limit_exceeded") {
		t.Fatal("concurrency marker classification failed")
	}
}

func TestParseRetryAfterUsesHeaderCaseAndZeroForMalformedValues(t *testing.T) {
	if got := ParseRetryAfter(http.Header{"retry-after": []string{"bad"}}, time.Now(), time.Minute); got != 0 {
		t.Fatalf("malformed retry-after = %s, want 0", got)
	}
	if got := ParseRetryAfter(nil, time.Now(), time.Minute); got != 0 {
		t.Fatalf("nil headers retry-after = %s, want 0", got)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Retry-After", "2")
	if got := ParseRetryAfter(request.Header, time.Unix(0, 0), time.Minute); got != 2*time.Second {
		t.Fatalf("delta retry-after = %s, want 2s", got)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
