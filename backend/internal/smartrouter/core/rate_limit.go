package core

import (
	"strings"
)

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
