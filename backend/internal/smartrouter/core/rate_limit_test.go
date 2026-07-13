package core

import (
	"net/http"
	"testing"
	"time"
)

func TestRateLimitBackoffUsesWideExponentialWindow(t *testing.T) {
	policy := DefaultRateLimitBackoffConfig()
	policy.JitterRatio = 0
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second}
	for attempt, expected := range want {
		if got := policy.Delay(attempt, 0, 123); got != expected {
			t.Fatalf("attempt %d delay = %s, want %s", attempt, got, expected)
		}
	}
	if got := policy.Delay(4, 0, 123); got != 0 {
		t.Fatalf("delay after max attempts = %s, want 0", got)
	}
}

func TestRateLimitBackoffHonorsBoundedRetryAfterAndJitter(t *testing.T) {
	policy := DefaultRateLimitBackoffConfig()
	policy.JitterRatio = 0
	if got := policy.Delay(0, 30*time.Second, 123); got != 30*time.Second {
		t.Fatalf("retry-after delay = %s, want 30s", got)
	}
	if got := policy.Delay(0, 2*time.Minute, 123); got != policy.Max {
		t.Fatalf("bounded retry-after delay = %s, want %s", got, policy.Max)
	}

	policy.JitterRatio = 0.25
	got := policy.Delay(1, 0, 123)
	if got < 7500*time.Millisecond || got > 12500*time.Millisecond {
		t.Fatalf("jittered delay = %s, want within 25%% of 10s", got)
	}
}

func TestIsConcurrencyRateLimitSeparatesQuota429(t *testing.T) {
	if !IsConcurrencyRateLimit("Concurrency limit exceeded for user, please retry later", "") {
		t.Fatal("expected user concurrency marker")
	}
	if !IsConcurrencyRateLimit("Too many pending requests", "rate_limit_error") {
		t.Fatal("expected pending request marker")
	}
	if IsConcurrencyRateLimit("rate limit exceeded", "rate_limit_exceeded") {
		t.Fatal("quota/rate 429 must remain a normal rate limit")
	}
}

func TestParseRetryAfterSupportsSecondsAndHTTPDate(t *testing.T) {
	if got := ParseRetryAfter(http.Header{"Retry-After": []string{"12"}}, time.Unix(100, 0), time.Minute); got != 12*time.Second {
		t.Fatalf("seconds retry-after = %s, want 12s", got)
	}
	when := time.Unix(100, 0).UTC().Add(20 * time.Second).Format(http.TimeFormat)
	if got := ParseRetryAfter(http.Header{"Retry-After": []string{when}}, time.Unix(100, 0), time.Minute); got != 20*time.Second {
		t.Fatalf("date retry-after = %s, want 20s", got)
	}
}
