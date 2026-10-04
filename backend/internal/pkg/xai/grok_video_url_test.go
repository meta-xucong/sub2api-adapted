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
	got, err := BuildWokeyVideosURL(WokeyAPIBaseURL)
	if err != nil || got != WokeyAPIBaseURL+"/videos" {
		t.Fatalf("Wokey video URL = %q, err=%v", got, err)
	}
}

func TestBuildVideosURLWithValidator(t *testing.T) {
	got, err := BuildVideosURLWithValidator("https://relay.example/v1", func(raw string) (string, error) {
		return raw, nil
	})
	if err != nil || got != "https://relay.example/v1/videos" {
		t.Fatalf("video URL = %q, err=%v", got, err)
	}
}
