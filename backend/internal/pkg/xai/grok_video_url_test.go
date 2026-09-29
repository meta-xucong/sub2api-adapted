//go:build unit

package xai

import "testing"

func TestWokeyVideoURLRequiresCanonicalProviderProfile(t *testing.T) {
	if !IsWokeyAPIBaseURL(WokeyAPIBaseURL) {
		t.Fatal("canonical Wokey base URL must be recognized")
	}
	if IsWokeyAPIBaseURL("https://api.wokey.ai.attacker.example/v1") {
		t.Fatal("Wokey lookalike host must not be recognized")
	}
	url, err := BuildWokeyVideosURL(WokeyAPIBaseURL)
	if err != nil || url != WokeyAPIBaseURL+"/videos" {
		t.Fatalf("Wokey video URL = %q, err=%v", url, err)
	}
}
