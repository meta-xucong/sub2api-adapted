package core

import (
	"testing"
)

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
