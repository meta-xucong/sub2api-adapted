package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"path"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// OperatorTestGuard prevents local maintenance scripts from accidentally using
// real customer API keys for gateway smoke tests. It is intentionally opt-in and
// scoped to trusted local/script-like requests so normal downstream traffic is
// unaffected.
func OperatorTestGuard(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg == nil || !cfg.Gateway.OperatorTestGuard.Enabled {
			c.Next()
			return
		}
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil {
			c.Next()
			return
		}
		guard := cfg.Gateway.OperatorTestGuard
		if !operatorGuardPathMatches(c.Request.URL.Path, guard.Paths) ||
			!operatorGuardUserAgentMatches(c.GetHeader("User-Agent"), guard.BlockedUserAgents) ||
			!operatorGuardClientMatches(c, guard.TrustedClientIPs) ||
			operatorGuardAPIKeyAllowed(apiKey, guard.AllowedUserEmails, guard.AllowedAPIKeyNames) {
			c.Next()
			return
		}

		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		AbortWithError(c, http.StatusForbidden, "OPERATOR_TEST_KEY_REQUIRED", "Local operator test requests must use a dedicated ops test API key")
	}
}

func operatorGuardPathMatches(requestPath string, patterns []string) bool {
	requestPath = strings.TrimSpace(requestPath)
	if requestPath == "" {
		return false
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if ok, _ := path.Match(pattern, requestPath); ok || requestPath == pattern {
			return true
		}
	}
	return false
}

func operatorGuardUserAgentMatches(userAgent string, patterns []string) bool {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	if ua == "" {
		return false
	}
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if strings.Contains(pattern, "*") {
			if ok, _ := path.Match(pattern, ua); ok {
				return true
			}
			continue
		}
		if strings.Contains(ua, pattern) {
			return true
		}
	}
	return false
}

func operatorGuardAPIKeyAllowed(apiKey *service.APIKey, allowedEmails, allowedKeyNames []string) bool {
	if apiKey == nil {
		return false
	}
	if apiKey.User != nil && operatorGuardStringAllowed(apiKey.User.Email, allowedEmails) {
		return true
	}
	return operatorGuardStringAllowed(apiKey.Name, allowedKeyNames)
}

func operatorGuardStringAllowed(value string, patterns []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if strings.Contains(pattern, "*") {
			if ok, _ := path.Match(pattern, value); ok {
				return true
			}
			continue
		}
		if value == pattern {
			return true
		}
	}
	return false
}

func operatorGuardClientMatches(c *gin.Context, trusted []string) bool {
	if len(trusted) == 0 {
		return false
	}
	for _, candidate := range operatorGuardClientCandidates(c) {
		if operatorGuardIPAllowed(candidate, trusted) {
			return true
		}
	}
	return false
}

func operatorGuardClientCandidates(c *gin.Context) []string {
	if c == nil || c.Request == nil {
		return nil
	}
	values := []string{
		c.ClientIP(),
		c.GetHeader("X-Real-IP"),
	}
	if host, _, err := net.SplitHostPort(c.Request.RemoteAddr); err == nil {
		values = append(values, host)
	} else {
		values = append(values, c.Request.RemoteAddr)
	}
	for _, part := range strings.Split(c.GetHeader("X-Forwarded-For"), ",") {
		values = append(values, strings.TrimSpace(part))
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func operatorGuardIPAllowed(candidate string, trusted []string) bool {
	ip, err := netip.ParseAddr(strings.TrimSpace(candidate))
	if err != nil {
		return false
	}
	for _, raw := range trusted {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(raw); err == nil {
			if prefix.Contains(ip) {
				return true
			}
			continue
		}
		allowed, err := netip.ParseAddr(raw)
		if err == nil && allowed == ip {
			return true
		}
	}
	return false
}
