package service

import (
	"net"
	"net/url"
	"strings"
)

// unifiedGatewayUpstreamIdentity returns a display-safe grouping identity for
// the route-pricing admin view. Only the normalized URL origin is exposed.
func unifiedGatewayUpstreamIdentity(account *Account) (key, label string) {
	if account == nil {
		return "platform:unknown", "Unknown provider"
	}
	rawBaseURL := strings.TrimSpace(account.GetCredential("base_url"))
	if rawBaseURL != "" {
		if isWokeyBaseURL(rawBaseURL) {
			return "provider:wokey", "Wokey"
		}
		if origin, ok := unifiedGatewayURLOrigin(rawBaseURL); ok {
			return "url:" + origin, origin
		}
	}

	platform := strings.ToLower(strings.TrimSpace(string(account.Platform)))
	if platform == "" {
		platform = "unknown"
	}
	return "platform:" + platform, unifiedGatewayPlatformLabel(platform)
}

func unifiedGatewayURLOrigin(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Hostname() == "" {
		return "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	port := parsed.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		hostname = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		hostname = "[" + hostname + "]"
	}
	return scheme + "://" + hostname, true
}

func unifiedGatewayPlatformLabel(platform string) string {
	switch platform {
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "gemini":
		return "Gemini"
	case "grok":
		return "Grok"
	case "kimi":
		return "Kimi"
	case "zhipu":
		return "Zhipu"
	case "deepseek":
		return "DeepSeek"
	case "minimax":
		return "MiniMax"
	case "antigravity":
		return "Antigravity"
	case "typesafe":
		return "TypeSafe"
	default:
		if platform == "" {
			return "Unknown provider"
		}
		return strings.ToUpper(platform[:1]) + platform[1:]
	}
}
