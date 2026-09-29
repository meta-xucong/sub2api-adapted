package core

import (
	"net/http"
	"testing"
	"time"
)

func TestNormalizeCompactAttemptsIsFinite(t *testing.T) {
	if got := (Policy{MaxAttemptsCompact: 0}).Normalize().AttemptBudget(CapabilityResponsesCompact); got != 2 {
		t.Fatalf("compact attempt budget = %d, want 2", got)
	}
}

func TestParseRetryAfterOverflowIsBounded(t *testing.T) {
	max := 90 * time.Second
	headers := http.Header{"Retry-After": []string{"9223372036854775807"}}
	if got := ParseRetryAfter(headers, time.Now(), max); got != max {
		t.Fatalf("overflow retry-after = %s, want %s", got, max)
	}
}

func TestRateLimitDelayJitterNeverExceedsMax(t *testing.T) {
	cfg := RateLimitBackoffConfig{Enabled: true, Initial: 30 * time.Second, Max: time.Minute, MaxAttempts: 4, JitterRatio: 1}
	for seed := uint64(1); seed < 100; seed++ {
		got := cfg.Delay(1, 0, seed)
		if got <= 0 || got > time.Minute {
			t.Fatalf("seed %d produced delay %s outside (0,%s]", seed, got, time.Minute)
		}
	}
}

func TestCalibrationDueUsesShanghaiCalendar(t *testing.T) {
	policy := DefaultCalibrationPolicy()
	before := time.Date(2026, 9, 30, 3, 59, 59, 0, policy.Location)
	if CalibrationDue(before, before.Add(-24*time.Hour), policy) {
		t.Fatal("calibration became due before 04:00 Asia/Shanghai")
	}
	after := time.Date(2026, 9, 30, 4, 0, 0, 0, policy.Location)
	if !CalibrationDue(after, after.Add(-24*time.Hour), policy) {
		t.Fatal("calibration was not due at 04:00 Asia/Shanghai")
	}
}
