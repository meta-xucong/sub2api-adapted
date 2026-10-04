package service

import (
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

// GrokVideoTransport identifies the wire contract used by a Grok video
// account.  It is deliberately scoped to the video media path; text, image,
// and audio requests continue to use their existing account routing.
type GrokVideoTransport string

const (
	// GrokVideoTransportOpenAICompat is the safe backwards-compatible default
	// for xAI-compatible relays and aggregators such as Subrouter.
	GrokVideoTransportOpenAICompat GrokVideoTransport = "openai_compat"
	// GrokVideoTransportWokeyMultipart is Wokey's /v1/videos multipart contract.
	GrokVideoTransportWokeyMultipart GrokVideoTransport = "wokey_multipart"
	// GrokVideoTransportKIEJobs is KIE's native createTask/recordInfo contract.
	GrokVideoTransportKIEJobs GrokVideoTransport = "kie_jobs"
)

// GrokVideoTransport resolves the account's video adapter.  Existing accounts
// with no transport marker retain the OpenAI-compatible path, while known
// provider hosts are detected automatically.  An explicit marker always wins
// so a future relay can opt into a registered adapter without changing the
// default behavior of older accounts.
func (a *Account) GrokVideoTransport() GrokVideoTransport {
	if a == nil || !a.IsGrok() {
		return ""
	}
	if transport, ok := normalizeGrokVideoTransport(a.GetCredential("grok_video_transport")); ok {
		return transport
	}
	return detectGrokVideoTransport(a.GetGrokMediaBaseURL())
}

// normalizeGrokVideoTransport accepts the historical values already used by
// the admin UI and a few descriptive aliases. Unknown values intentionally
// fall back to the OpenAI-compatible contract instead of disabling a route.
func normalizeGrokVideoTransport(raw string) (GrokVideoTransport, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "", false
	case "kie", "kie_jobs", "kie-jobs", "native_kie", "native-kie":
		return GrokVideoTransportKIEJobs, true
	case "wokey", "wokey_multipart", "wokey-multipart", "multipart":
		return GrokVideoTransportWokeyMultipart, true
	case "openai", "openai_compat", "openai-compatible", "subrouter", "xai", "default":
		return GrokVideoTransportOpenAICompat, true
	default:
		// Unknown profile names must not silently select a native endpoint. The
		// standard OpenAI-compatible route is the least surprising fallback.
		return GrokVideoTransportOpenAICompat, true
	}
}

func detectGrokVideoTransport(rawBaseURL string) GrokVideoTransport {
	if xai.IsWokeyAPIBaseURL(rawBaseURL) {
		return GrokVideoTransportWokeyMultipart
	}
	if isKnownKIEJobsBaseURL(rawBaseURL) {
		return GrokVideoTransportKIEJobs
	}
	return GrokVideoTransportOpenAICompat
}

// isKnownKIEJobsBaseURL is intentionally exact-host only. A suffix or
// substring match could route bearer credentials to an attacker-controlled
// look-alike domain. Custom KIE-compatible relays should set the explicit
// grok_video_transport=kie_jobs marker.
func isKnownKIEJobsBaseURL(rawBaseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil || parsed == nil || !strings.EqualFold(parsed.Scheme, "https") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(parsed.Hostname()), "api.kie.ai")
}

// UsesWokeyVideoMultipart reports whether the account needs Wokey's binary
// image projection. Keeping this beside the KIE predicate prevents each
// caller from reimplementing host/credential detection independently.
func (a *Account) UsesWokeyVideoMultipart() bool {
	return a != nil && a.GrokVideoTransport() == GrokVideoTransportWokeyMultipart
}
