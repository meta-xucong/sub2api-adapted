package core

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitBackoffConfig is request scoped. It does not mutate lane health;
// a concurrency 429 is temporary evidence rather than proof of a bad lane.
type RateLimitBackoffConfig struct {
	Enabled       bool
	Initial       time.Duration
	Max           time.Duration
	MaxAttempts   int
	JitterRatio   float64
	RetryAfterMax time.Duration
}

func DefaultRateLimitBackoffConfig() RateLimitBackoffConfig {
	return RateLimitBackoffConfig{Enabled: true, Initial: 5 * time.Second, Max: 60 * time.Second, MaxAttempts: 4, JitterRatio: 0.25, RetryAfterMax: 90 * time.Second}
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

func IsConcurrencyRateLimit(message, code string) bool {
	lower := strings.ToLower(strings.TrimSpace(message + " " + code))
	for _, marker := range []string{"concurrency limit", "concurrent limit", "too many concurrent", "concurrent request", "maximum concurrent", "max_concurrency", "too many pending", "pending requests"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ParseRetryAfter accepts both delta-seconds and HTTP-date forms and bounds
// the result when max is positive.
func ParseRetryAfter(headers http.Header, now time.Time, max time.Duration) time.Duration {
	if headers == nil {
		return 0
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	var delay time.Duration
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
		// Convert only after checking the nanosecond multiplication. A large
		// Retry-After header must never wrap into a negative duration.
		if seconds > int64(math.MaxInt64)/int64(time.Second) {
			if max > 0 {
				return max
			}
			return 0
		}
		delay = time.Duration(seconds) * time.Second
	} else if strings.HasPrefix(raw, "-") {
		return 0
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
	factor := time.Duration(1 << shift)
	delay := c.Max
	if c.Initial <= c.Max/factor {
		delay = c.Initial * factor
	}
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
		jitter := float64(delay) * ((rng.nextFloat64()*2 - 1) * c.JitterRatio)
		if jitter > float64(c.Max-delay) {
			jitter = float64(c.Max - delay)
		}
		if jitter < -float64(delay) {
			jitter = -float64(delay)
		}
		delay += time.Duration(jitter)
	}
	if delay > c.Max {
		return c.Max
	}
	if delay < time.Millisecond {
		return time.Millisecond
	}
	return delay
}
