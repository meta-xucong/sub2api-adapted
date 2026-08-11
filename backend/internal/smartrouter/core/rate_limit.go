package core

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitBackoffConfig is the optional, request-scoped policy for an
// upstream 429. It deliberately does not encode a lane health penalty:
// concurrency exhaustion is usually temporary and is not proof that a lane
// is broken.
type RateLimitBackoffConfig struct {
	Enabled       bool
	Initial       time.Duration
	Max           time.Duration
	MaxAttempts   int
	JitterRatio   float64
	RetryAfterMax time.Duration
}

func DefaultRateLimitBackoffConfig() RateLimitBackoffConfig {
	return RateLimitBackoffConfig{
		Enabled:       true,
		Initial:       5 * time.Second,
		Max:           60 * time.Second,
		MaxAttempts:   4,
		JitterRatio:   0.25,
		RetryAfterMax: 90 * time.Second,
	}
}

func (c RateLimitBackoffConfig) Normalize() RateLimitBackoffConfig {
	d := DefaultRateLimitBackoffConfig()
	if c.Initial <= 0 {
		c.Initial = d.Initial
	}
	if c.Max <= 0 {
		c.Max = d.Max
	}
	if c.Max < c.Initial {
		c.Max = c.Initial
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = d.MaxAttempts
	}
	if c.JitterRatio < 0 || c.JitterRatio > 1 {
		c.JitterRatio = d.JitterRatio
	}
	if c.RetryAfterMax <= 0 {
		c.RetryAfterMax = d.RetryAfterMax
	}
	return c
}

// IsConcurrencyRateLimit distinguishes a temporary concurrency/busy response
// from quota or rate-limit exhaustion. It only matches explicit wording so a
// generic 429 keeps the existing longer health cooldown behavior.
func IsConcurrencyRateLimit(message string, code string) bool {
	lower := strings.ToLower(strings.TrimSpace(message + " " + code))
	for _, marker := range []string{
		"concurrency limit",
		"concurrent limit",
		"too many concurrent",
		"concurrent request",
		"maximum concurrent",
		"max_concurrency",
		"too many pending",
		"pending requests",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ParseRetryAfter returns a bounded duration from the upstream Retry-After
// header. Both delta-seconds and HTTP-date forms are accepted.
func ParseRetryAfter(headers http.Header, now time.Time, max time.Duration) time.Duration {
	if headers == nil {
		return 0
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	var delay time.Duration
	if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
		delay = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(raw); err == nil {
		if now.IsZero() {
			now = time.Now()
		}
		delay = at.Sub(now)
		if delay < 0 {
			delay = 0
		}
	}
	if max > 0 && delay > max {
		return max
	}
	return delay
}

// Delay computes a bounded exponential delay. Retry-After is honored but is
// still capped, and jitter prevents several callers from retrying together.
// attempt is zero-based and represents the number of 429s already seen for
// this request.
func (c RateLimitBackoffConfig) Delay(attempt int, retryAfter time.Duration, seed uint64) time.Duration {
	c = c.Normalize()
	if !c.Enabled || attempt < 0 || attempt >= c.MaxAttempts {
		return 0
	}
	if retryAfter > c.RetryAfterMax {
		retryAfter = c.RetryAfterMax
	}
	if retryAfter < 0 {
		retryAfter = 0
	}
	shift := attempt
	if shift > 6 {
		shift = 6
	}
	delay := c.Initial * time.Duration(1<<shift)
	if delay > c.Max {
		delay = c.Max
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > c.Max {
		delay = c.Max
	}
	if c.JitterRatio > 0 {
		if seed == 0 {
			seed = uint64(time.Now().UnixNano())
		}
		rng := newRNG(seed ^ uint64(attempt+1))
		jitter := (rng.nextFloat64()*2 - 1) * c.JitterRatio
		delay += time.Duration(float64(delay) * jitter)
	}
	if delay < time.Millisecond {
		return time.Millisecond
	}
	return delay
}
