package handler

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestOpenAIImagesRequestCacheWaitsForInFlightAndCachesSuccess(t *testing.T) {
	cache := newOpenAIImagesRequestCache()
	now := time.Now()
	entry, owner, cached := cache.begin("image-key", now)
	if !owner {
		t.Fatal("first request should own in-flight entry")
	}
	if cached != nil {
		t.Fatal("first request should not receive cached response")
	}

	waitEntry, owner, cached := cache.begin("image-key", now.Add(time.Second))
	if owner {
		t.Fatal("duplicate request should wait for in-flight owner")
	}
	if cached != nil {
		t.Fatal("in-flight duplicate should not receive cached response before completion")
	}
	if waitEntry != entry {
		t.Fatal("duplicate request should receive the existing entry")
	}

	want := &openAIImagesCachedResponse{
		status: http.StatusOK,
		header: http.Header{"Content-Type": []string{"application/json"}},
		body:   []byte(`{"created":1,"data":[{"b64_json":"abc"}]}`),
	}
	cache.finishSuccess(entry, want, now.Add(2*time.Second))

	got, ok := cache.wait(context.Background(), waitEntry)
	if !ok {
		t.Fatal("waiting duplicate should receive completed response")
	}
	if got == nil || got.status != want.status || string(got.body) != string(want.body) {
		t.Fatalf("cached response mismatch: %#v", got)
	}

	_, owner, cached = cache.begin("image-key", now.Add(3*time.Second))
	if owner {
		t.Fatal("completed duplicate should not become owner")
	}
	if cached == nil || string(cached.body) != string(want.body) {
		t.Fatalf("completed duplicate should receive cached response, got %#v", cached)
	}
}

func TestOpenAIImagesRequestCacheFinishErrorWakesWaiters(t *testing.T) {
	cache := newOpenAIImagesRequestCache()
	entry, owner, _ := cache.begin("image-key", time.Now())
	if !owner {
		t.Fatal("first request should own in-flight entry")
	}

	waitEntry, owner, _ := cache.begin("image-key", time.Now())
	if owner {
		t.Fatal("duplicate request should wait")
	}
	cache.finishError(entry)

	got, ok := cache.wait(context.Background(), waitEntry)
	if ok || got != nil {
		t.Fatalf("failed in-flight request should not return cached response: ok=%v got=%#v", ok, got)
	}

	_, owner, cached := cache.begin("image-key", time.Now())
	if !owner || cached != nil {
		t.Fatal("new request should be allowed to own after failed in-flight entry")
	}
}
