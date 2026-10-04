package service

import "strings"

// UsesGrokVideosCreatePath preserves the legacy account-level override used by
// Wokey-compatible relays. It is limited to the create endpoint and does not
// change ordinary xAI image/status URL construction.
func (a *Account) UsesGrokVideosCreatePath() bool {
	if a == nil || !a.IsGrok() {
		return false
	}
	return strings.EqualFold(strings.Trim(strings.TrimSpace(a.GetCredential("grok_video_create_path")), "/"), "videos")
}

func (a *Account) UsesKIEJobsVideoAPI() bool {
	return a != nil && a.GrokVideoTransport() == GrokVideoTransportKIEJobs
}

// SupportsGrokMediaEndpoint keeps KIE's intentionally narrow native contract
// from receiving image/edit/extension requests that it cannot translate.
func (a *Account) SupportsGrokMediaEndpoint(endpoint GrokMediaEndpoint) bool {
	if !a.UsesKIEJobsVideoAPI() {
		return true
	}
	switch endpoint {
	case GrokMediaEndpointVideosGenerations, GrokMediaEndpointVideoStatus, GrokMediaEndpointVideoContent:
		return true
	default:
		return false
	}
}
