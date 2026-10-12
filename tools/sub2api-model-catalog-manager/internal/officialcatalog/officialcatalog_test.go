package officialcatalog

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func specFor(t *testing.T, provider string) sourceSpec {
	t.Helper()
	spec, ok := sourceForProvider(provider)
	if !ok {
		t.Fatalf("source missing: %s", provider)
	}
	return spec
}

func TestOfficialLifecycleParsersUseFivePublicSources(t *testing.T) {
	now := time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct{ provider, fixture, model, status string }{
		{"openai", "openai-deprecations.html", "gpt-retiring-test", "announced_deprecated"},
		{"anthropic", "anthropic-deprecations.html", "claude-retiring-test", "announced_deprecated"},
		{"deepseek", "deepseek-updates.html", "deepseek-old-test", "retired_with_compatibility_route"},
		{"xai", "xai-release-notes.html", "grok-retiring-test", "retired_with_compatibility_route"},
	}
	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			parsed, err := parseSourcePage(specFor(t, tc.provider), specFor(t, tc.provider).rootURL, fixture(t, tc.fixture), now)
			if err != nil {
				t.Fatal(err)
			}
			if len(parsed.Evidence) == 0 {
				t.Fatal("expected explicit lifecycle evidence")
			}
			found := false
			for _, evidence := range parsed.Evidence {
				if evidence.ModelID == tc.model {
					found = evidence.Status == tc.status
					if evidence.SourceURL == "" || evidence.SourceTitle == "" || evidence.ContentSHA256 == "" || evidence.Excerpt == "" {
						t.Fatalf("incomplete source evidence: %+v", evidence)
					}
				}
			}
			if !found {
				t.Fatalf("missing expected evidence %s status=%s: %+v", tc.model, tc.status, parsed.Evidence)
			}
			for _, evidence := range parsed.Evidence {
				if evidence.ModelID == "gpt-successor-test" || evidence.ModelID == "grok-current-test" || evidence.ModelID == "deepseek-current-test" {
					t.Fatalf("replacement was incorrectly marked retired: %+v", evidence)
				}
			}
		})
	}
	rootSpec := specFor(t, "alibaba")
	root, err := parseSourcePage(rootSpec, rootSpec.rootURL, fixture(t, "alibaba-depreciation.html"), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Links) != 1 || len(root.Evidence) != 0 {
		t.Fatalf("Alibaba index should yield only bounded announcement links: %+v", root)
	}
	bulletinURL := alibabaBulletinURL("123456")
	child, err := parseSourcePage(rootSpec, bulletinURL, fixture(t, "alibaba-bulletin-response.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	gotAlibaba := make(map[string]LifecycleEvidence)
	alibabaReplacementFound := false
	for _, evidence := range child.Evidence {
		if _, exists := gotAlibaba[evidence.ModelID]; !exists || evidence.Replacement != "" {
			gotAlibaba[evidence.ModelID] = evidence
		}
		if evidence.ModelID == "qwen-old-test" && evidence.Replacement == "qwen-new-test" {
			alibabaReplacementFound = true
		}
	}
	for _, model := range []string{"qwen-old-test", "gte-rerank", "qwen-list-old-a", "qwen-list-old-b"} {
		if evidence, ok := gotAlibaba[model]; !ok || evidence.Status != "announced_deprecated" {
			t.Fatalf("Alibaba notice missed expected lifecycle model %q: %+v", model, child.Evidence)
		}
	}
	for _, replacement := range []string{"qwen-new-test", "qwen-list-successor"} {
		if _, ok := gotAlibaba[replacement]; ok {
			t.Fatalf("Alibaba recommended replacement was incorrectly marked retired: %s (%+v)", replacement, child.Evidence)
		}
	}
	if gotAlibaba["gte-rerank"].EffectiveAt != "2099-03-04" {
		t.Fatalf("Alibaba prose effective date mismatch: %+v", gotAlibaba["gte-rerank"])
	}
	if !alibabaReplacementFound {
		t.Fatalf("Alibaba explicit table replacement metadata was lost: %+v", child.Evidence)
	}
	for _, body := range [][]byte{
		[]byte(`{"data":{"success":false,"info":{"title":"残留标题","detailList":[{"contentHtml":"<table><tr><td>qwen-footer-link</td><td>2099-02-03</td><td>该模型将下线</td></tr></table>"}]}}`),
		[]byte(`{"data":{"success":true,"info":{"title":" ","detailList":[{"contentHtml":"<p>content</p>"}]}}`),
		[]byte(`{"data":{"success":true,"info":{"title":"标题","detailList":[]}}}`),
		[]byte(`{"data":{"success":true,"info":{"title":"标题","detailList":[{"contentHtml":"  "}]}}}`),
		[]byte(`{"data":{"success":true,"info":{"title":"标题","detailList":[{"contentHtml":"<p>残缺响应"}]}} trailing`),
	} {
		if _, err := parseSourcePage(rootSpec, bulletinURL, body, now); err == nil {
			t.Fatal("invalid/failed Alibaba renderer response was accepted")
		}
	}
	xaiChild, err := parseSourcePage(specFor(t, "xai"), "https://docs.x.ai/developers/migration/retirement-test", fixture(t, "xai-migration.html"), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(xaiChild.Evidence) != 1 || xaiChild.Evidence[0].ModelID != "grok-retiring-test" || xaiChild.Evidence[0].Status != "retired_with_compatibility_route" {
		t.Fatalf("xAI migration page parse mismatch: %+v", xaiChild.Evidence)
	}
}

func TestOpenAIParserOnlyReadsAPIDeprecationTable(t *testing.T) {
	spec := specFor(t, "openai")
	parsed, err := parseSourcePage(spec, spec.rootURL, fixture(t, "openai-deprecations-with-product-table.html"), time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool)
	for _, evidence := range parsed.Evidence {
		got[evidence.ModelID] = true
	}
	if !got["gpt-api-retiring-test"] {
		t.Fatalf("API deprecation row was not parsed: %+v", parsed.Evidence)
	}
	for _, id := range []string{"gpt-chatgpt-only-test", "gpt-chatgpt-successor-test"} {
		if got[id] {
			t.Fatalf("unrelated ChatGPT product table leaked into API lifecycle evidence: %q (%+v)", id, parsed.Evidence)
		}
	}
}

func TestAnthropicAndXAIKeepConflictingExactIDEvidence(t *testing.T) {
	now := time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		provider string
		url      string
		fixture  string
		model    string
		replace  string
	}{
		{provider: "anthropic", url: specFor(t, "anthropic").rootURL, fixture: "anthropic-deprecations.html", model: "claude-conflict-test", replace: "claude-replacement-test"},
		{provider: "xai", url: specFor(t, "xai").rootURL, fixture: "xai-release-notes.html", model: "grok-conflict-test", replace: "grok-replacement-b-test"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			parsed, err := parseSourcePage(specFor(t, tc.provider), tc.url, fixture(t, tc.fixture), now)
			if err != nil {
				t.Fatal(err)
			}
			var records []LifecycleEvidence
			foundReplacement := false
			for _, evidence := range parsed.Evidence {
				if evidence.ModelID == tc.model {
					records = append(records, evidence)
					foundReplacement = foundReplacement || evidence.Replacement == tc.replace
				}
			}
			if len(records) < 2 || !foundReplacement {
				t.Fatalf("conflicting exact-ID evidence was overwritten: records=%+v", records)
			}
			inv := Inventory{Mode: "inspect", Digest: "digest", Observations: []InputModel{{AccountID: 7, ModelID: tc.model, ObservedAs: "upstream_id"}}}
			snapshot := CatalogSnapshot{Provider: tc.provider, Complete: true, Evidence: parsed.Evidence}
			matched := matchObservations(inv.Observations, []CatalogSnapshot{snapshot})
			if len(matched) != 1 || matched[0].Status != "conflicting_official_lifecycle_evidence" {
				t.Fatalf("same-provider conflicts did not reach downstream conflict marker: %+v", matched)
			}
			for _, reference := range matched[0].Matches {
				if reference.Provider != tc.provider || !reference.Conflict {
					t.Fatalf("unexpected conflict reference: %+v", reference)
				}
			}
		})
	}
}

func TestParsersFailClosedOnMissingAnchorsAndUnknownIsNotActive(t *testing.T) {
	for _, spec := range sourceManifest {
		if _, err := parseSourcePage(spec, spec.rootURL, []byte("<html><title>Some unrelated page</title><body>gpt-test</body></html>"), time.Now()); err == nil {
			t.Errorf("%s parser accepted unrelated HTML", spec.provider)
		}
	}
	inventory := Inventory{Mode: "inspect", Digest: "test-digest", Observations: []InputModel{{AccountID: 4, ModelID: "not-mentioned-model", ObservedAs: "upstream_id"}}}
	report, err := BuildReviewFromStartup(inventory, StartupSyncResult{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Observations[0].Status; got != "unknown/no_official_notice_found" {
		t.Fatalf("no notice became %q", got)
	}
	if strings.Contains(strings.ToLower(report.Observations[0].Status), "active") {
		t.Fatal("absence of notice must never imply active")
	}
}

func TestExactMatchAndProviderScopedConflict(t *testing.T) {
	when := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	inv := Inventory{Mode: "inspect", Digest: "digest", Observations: []InputModel{{AccountID: 1, ModelID: "model-x", ObservedAs: "upstream_id"}, {AccountID: 1, ModelID: "Model-x", ObservedAs: "public_id"}}}
	openai := CatalogSnapshot{Provider: "openai", Complete: true, Evidence: []LifecycleEvidence{{Provider: "openai", ModelID: "model-x", Status: "retired", SourceURL: sourceManifest[0].rootURL, SourceTitle: "Deprecations", RetrievedAt: when, ContentSHA256: "a", Excerpt: "retired"}, {Provider: "openai", ModelID: "model-x", Status: "legacy", SourceURL: sourceManifest[0].rootURL, SourceTitle: "Deprecations", RetrievedAt: when, ContentSHA256: "b", Excerpt: "legacy"}}}
	deepseek := CatalogSnapshot{Provider: "deepseek", Complete: true, Evidence: []LifecycleEvidence{{Provider: "deepseek", ModelID: "model-x", Status: "legacy", SourceURL: sourceManifest[2].rootURL, SourceTitle: "Change Log", RetrievedAt: when, ContentSHA256: "c", Excerpt: "legacy"}}}
	observations := matchObservations(inv.Observations, []CatalogSnapshot{openai, deepseek})
	if observations[0].Status != "conflicting_official_lifecycle_evidence" || len(observations[0].Matches) != 3 {
		t.Fatalf("same-provider conflicting evidence was not retained: %+v", observations[0])
	}
	for _, ref := range observations[0].Matches {
		if ref.Provider == "deepseek" && ref.Conflict {
			t.Fatal("same ID from another provider must not create a conflict")
		}
	}
	if observations[1].Status != "unknown/no_official_notice_found" {
		t.Fatalf("match was not exact/case-sensitive: %+v", observations[1])
	}
}

type fakeDoer struct {
	mu                sync.Mutex
	handler           func(*http.Request) (*http.Response, error)
	requests          []*http.Request
	active, maxActive int
	delay             time.Duration
}

func (d *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.requests = append(d.requests, req.Clone(req.Context()))
	d.active++
	if d.active > d.maxActive {
		d.maxActive = d.active
	}
	d.mu.Unlock()
	if d.delay > 0 {
		time.Sleep(d.delay)
	}
	resp, err := d.handler(req)
	d.mu.Lock()
	d.active--
	d.mu.Unlock()
	return resp, err
}

func htmlResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}, "ETag": []string{"etag-test"}, "Last-Modified": []string{"Mon, 12 Oct 2026 00:00:00 GMT"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func jsonResponse(status int, body string) *http.Response {
	resp := htmlResponse(status, body)
	resp.Header.Set("Content-Type", "application/json; charset=utf-8")
	return resp
}

func fixtureRouter(t *testing.T, fail bool) *fakeDoer {
	return fixtureRouterWithOverrides(t, fail, nil)
}

func fixtureRouterWithOverrides(t *testing.T, fail bool, overrides map[string]string) *fakeDoer {
	t.Helper()
	bodies := map[string]string{
		"developers.openai.com/api/docs/deprecations":                 string(fixture(t, "openai-deprecations.html")),
		"platform.claude.com/docs/en/about-claude/model-deprecations": string(fixture(t, "anthropic-deprecations.html")),
		"api-docs.deepseek.com/updates/":                              string(fixture(t, "deepseek-updates.html")),
		"help.aliyun.com/zh/model-studio/model-depreciation":          string(fixture(t, "alibaba-depreciation.html")),
		"t.aliyun.com/abs/bulletin/bulletinDetail":                    string(fixture(t, "alibaba-bulletin-response.json")),
		"docs.x.ai/developers/release-notes":                          string(fixture(t, "xai-release-notes.html")),
		"docs.x.ai/developers/migration/retirement-test":              string(fixture(t, "xai-migration.html")),
	}
	for key, body := range overrides {
		bodies[key] = body
	}
	return &fakeDoer{delay: 5 * time.Millisecond, handler: func(req *http.Request) (*http.Response, error) {
		if fail {
			return htmlResponse(http.StatusServiceUnavailable, "unavailable"), nil
		}
		body, ok := bodies[req.URL.Host+req.URL.Path]
		if !ok {
			return nil, &url.Error{Op: "GET", URL: req.URL.String(), Err: os.ErrNotExist}
		}
		if req.URL.Host == "t.aliyun.com" && req.URL.RawQuery != "id=123456&language=zh&website=cn" {
			return htmlResponse(http.StatusBadRequest, "unexpected renderer query"), nil
		}
		if req.URL.Host == "t.aliyun.com" {
			return jsonResponse(http.StatusOK, body), nil
		}
		return htmlResponse(http.StatusOK, body), nil
	}}
}

func TestValidZeroEventOfficialPagesUpdateCompleteSnapshotsAndRemainUnknown(t *testing.T) {
	tests := []struct {
		provider string
		fixture  string
		key      string
		model    string
	}{
		{"openai", "openai-deprecations-zero-events.html", "developers.openai.com/api/docs/deprecations", "gpt-retiring-test"},
		{"anthropic", "anthropic-deprecations-zero-events.html", "platform.claude.com/docs/en/about-claude/model-deprecations", "claude-retiring-test"},
		{"deepseek", "deepseek-updates-zero-events.html", "api-docs.deepseek.com/updates/", "deepseek-old-test"},
		{"alibaba", "alibaba-bulletin-zero-events.json", "t.aliyun.com/abs/bulletin/bulletinDetail", "qwen-old-test"},
		{"xai", "xai-release-notes-zero-events.html", "docs.x.ai/developers/release-notes", "grok-retiring-test"},
	}
	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			spec := specFor(t, tc.provider)
			cache := filepath.Join(t.TempDir(), "cache")
			now := time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC)
			first := SyncStartup(context.Background(), StartupSyncOptions{CacheDir: cache, NewDoer: func() HTTPDoer { return fixtureRouter(t, false) }, Now: func() time.Time { return now }})
			var old ProviderSync
			for _, provider := range first.Providers {
				if provider.Provider == tc.provider {
					old = provider
				}
			}
			if old.Status != "updated" || old.EvidenceCount == 0 || old.Snapshot == nil {
				t.Fatalf("initial lifecycle snapshot missing for replacement test: %+v", old)
			}
			oldHash := old.Snapshot.ContentSHA256
			body := string(fixture(t, tc.fixture))
			second := SyncStartup(context.Background(), StartupSyncOptions{
				CacheDir: cache,
				NewDoer:  func() HTTPDoer { return fixtureRouterWithOverrides(t, false, map[string]string{tc.key: body}) },
				Now:      func() time.Time { return now.Add(time.Hour) },
			})
			var current ProviderSync
			for _, provider := range second.Providers {
				if provider.Provider == tc.provider {
					current = provider
				}
			}
			if current.Status != "updated" || current.ErrorCode != "" || current.EvidenceCount != 0 || !current.CachePersisted || current.Snapshot == nil || !current.Snapshot.Complete || len(current.Snapshot.Evidence) != 0 || current.Snapshot.ContentSHA256 == oldHash {
				t.Fatalf("valid zero-event source did not replace last-good with complete empty snapshot: %+v", current)
			}
			cached, err := readSourceSnapshot(cache, spec)
			if err != nil || !cached.Complete || len(cached.Evidence) != 0 || cached.ContentSHA256 != current.Snapshot.ContentSHA256 {
				t.Fatalf("complete zero-evidence snapshot was not persisted: snapshot=%+v err=%v", cached, err)
			}
			inventory := Inventory{Mode: "inspect", Digest: "zero-event-test-digest", Observations: []InputModel{{AccountID: 1, ModelID: tc.model, ObservedAs: "upstream_id"}}}
			report, err := BuildReviewFromStartup(inventory, second, func() time.Time { return now.Add(2 * time.Hour) })
			if err != nil || report.Observations[0].Status != "unknown/no_official_notice_found" || len(report.Observations[0].Matches) != 0 {
				t.Fatalf("model absent from a successful zero-event source must remain unknown: report=%+v err=%v", report.Observations, err)
			}
		})
	}
	for _, tc := range []struct {
		provider string
		fixture  string
		url      string
		anchor   string
	}{
		{"openai", "openai-deprecations-zero-events.html", specFor(t, "openai").rootURL, "Recommended replacement"},
		{"anthropic", "anthropic-deprecations-zero-events.html", specFor(t, "anthropic").rootURL, "Tentative retirement date"},
		{"xai", "xai-release-notes-zero-events.html", specFor(t, "xai").rootURL, "October"},
	} {
		t.Run("missing_structure_"+tc.provider, func(t *testing.T) {
			body := string(fixture(t, tc.fixture))
			if tc.provider == "xai" {
				body = strings.ReplaceAll(body, tc.anchor, "unexpected heading")
			} else {
				body = strings.Replace(body, tc.anchor, "unexpected heading", 1)
			}
			if _, err := parseSourcePage(specFor(t, tc.provider), tc.url, []byte(body), time.Now()); err == nil {
				t.Fatal("zero-event content with a missing required structure anchor was accepted")
			}
		})
	}
	xaiSpec := specFor(t, "xai")
	xaiMigration, err := parseSourcePage(xaiSpec, "https://docs.x.ai/developers/migration/retirement-test", fixture(t, "xai-migration-zero-events.html"), time.Now())
	if err != nil || len(xaiMigration.Evidence) != 0 {
		t.Fatalf("valid xAI migration page with no event must parse successfully and yield no evidence: %+v err=%v", xaiMigration, err)
	}
	invalidMigration := strings.Replace(string(fixture(t, "xai-migration-zero-events.html")), "Migration guide", "General documentation", 1)
	if _, err := parseSourcePage(xaiSpec, "https://docs.x.ai/developers/migration/retirement-test", []byte(invalidMigration), time.Now()); err == nil {
		t.Fatal("xAI migration page without its required heading was accepted")
	}
}

func TestStartupAnonymousFixedSourcesBoundsAndNoCredentialLookup(t *testing.T) {
	doer := fixtureRouter(t, false)
	envCalls, clientCalls := 0, 0
	result := SyncStartup(context.Background(), StartupSyncOptions{
		CacheDir:  filepath.Join(t.TempDir(), "cache"),
		LookupEnv: func(string) (string, bool) { envCalls++; return "must-not-be-read", true },
		NewDoer:   func() HTTPDoer { clientCalls++; return doer },
		Now:       time.Now,
	})
	if envCalls != 0 {
		t.Fatalf("OEM environment queried %d times", envCalls)
	}
	if clientCalls != 1 {
		t.Fatalf("HTTP client constructions=%d want 1", clientCalls)
	}
	if len(result.Providers) != 5 {
		t.Fatalf("sources=%d want 5", len(result.Providers))
	}
	doer.mu.Lock()
	requests, max := append([]*http.Request(nil), doer.requests...), doer.maxActive
	doer.mu.Unlock()
	if len(requests) != 7 {
		t.Fatalf("GET count=%d want 7 (five roots, one Alibaba notice and one xAI migration)", len(requests))
	}
	if max > 4 {
		t.Fatalf("concurrent requests=%d exceeds 4", max)
	}
	for _, req := range requests {
		for _, header := range []string{"Authorization", "X-Api-Key", "Cookie", "Proxy-Authorization"} {
			if req.Header.Get(header) != "" {
				t.Fatalf("anonymous request sent %s", header)
			}
		}
		if validateRequestAllowlist(req.URL.String()) != nil {
			t.Fatalf("request escaped source allowlist: %s", req.URL)
		}
		if req.URL.Host == "t.aliyun.com" {
			if req.Header.Get("Origin") != "https://www.aliyun.com" || req.Header.Get("Accept") != "application/json" {
				t.Fatalf("Alibaba renderer request headers mismatch: Origin=%q Accept=%q", req.Header.Get("Origin"), req.Header.Get("Accept"))
			}
		} else if req.Header.Get("Origin") != "" {
			t.Fatalf("Origin header was added to non-Alibaba source %s", req.URL.Host)
		}
	}
	for _, item := range result.Providers {
		if item.Status != "updated" || item.Snapshot == nil {
			t.Errorf("%s startup result: %+v", item.Provider, item)
		}
	}
	if got := result.Providers[3].EvidenceCount; got != 4 {
		t.Fatalf("Alibaba notice evidence=%d", got)
	}
}

func TestAlibabaRendererUsesAnonymousOriginRequestAgainstHttptestFixture(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requestCount++
		if req.Host != "t.aliyun.com" || req.URL.Path != "/abs/bulletin/bulletinDetail" || req.URL.RawQuery != "id=123456&language=zh&website=cn" {
			t.Errorf("unexpected rendered bulletin request: Host=%q URL=%s", req.Host, req.URL)
		}
		if req.Header.Get("Origin") != "https://www.aliyun.com" || req.Header.Get("Accept") != "application/json" {
			t.Errorf("required renderer headers missing: Origin=%q Accept=%q", req.Header.Get("Origin"), req.Header.Get("Accept"))
		}
		for _, header := range []string{"Authorization", "X-Api-Key", "Cookie", "Proxy-Authorization"} {
			if req.Header.Get(header) != "" {
				t.Errorf("anonymous renderer request sent %s", header)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "alibaba-bulletin-response.json"))
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	doer := &fakeDoer{handler: func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != alibabaBulletinURL("123456") {
			t.Fatalf("request did not use the single fixed renderer URL: %s", req.URL)
		}
		localRequest := req.Clone(req.Context())
		localURL := *req.URL
		localURL.Scheme, localURL.Host = serverURL.Scheme, serverURL.Host
		localRequest.URL = &localURL
		localRequest.Host = "t.aliyun.com"
		return server.Client().Do(localRequest)
	}}
	page, err := fetchAndParsePage(context.Background(), specFor(t, "alibaba"), alibabaBulletinURL("123456"), CachedPage{}, false, doer, &requestBudget{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if requestCount != 1 || page.Title != "百炼模型下线通知" || len(page.Evidence) != 4 {
		t.Fatalf("renderer fixture did not produce exact lifecycle evidence: requests=%d page=%+v", requestCount, page)
	}
	models := make(map[string]bool)
	for _, evidence := range page.Evidence {
		models[evidence.ModelID] = true
	}
	if !models["qwen-old-test"] || !models["gte-rerank"] || !models["qwen-list-old-a"] || !models["qwen-list-old-b"] || models["qwen-new-test"] || models["qwen-list-successor"] {
		t.Fatalf("renderer fixture evidence contains missing old models or a replacement: %+v", page.Evidence)
	}
}

func TestAlibabaRendererBusinessAndSchemaFailuresRetainLastGood(t *testing.T) {
	spec := specFor(t, "alibaba")
	url := alibabaBulletinURL("123456")
	now := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	body := fixture(t, "alibaba-bulletin-response.json")
	parsed, err := parseSourcePage(spec, url, body, now)
	if err != nil {
		t.Fatal(err)
	}
	prior := CachedPage{URL: url, Title: parsed.Title, ContentSHA256: sha256Hex(body), FetchedAt: now, Evidence: parsed.Evidence}
	snapshot := CatalogSnapshot{Provider: spec.provider, Pages: []CachedPage{prior}, Evidence: parsed.Evidence, FetchedAt: now, CheckedAt: now, Complete: true}
	snapshot.ContentSHA256, err = snapshotDigest(snapshot.Pages)
	if err != nil || validateSnapshot(spec, snapshot) != nil {
		t.Fatalf("last-good renderer snapshot fixture invalid: %v", err)
	}
	failedBodies := []string{
		`{"data":{"success":false,"info":{"title":"残留标题","detailList":[{"contentHtml":"<table><tr><td>qwen-false-test</td><td>2099-02-03</td><td>该模型将下线</td></tr></table>"}]}}`,
		`{"data":{"success":true,"info":{"title":" ","detailList":[{"contentHtml":"<p>residual</p>"}]}}`,
		`{"data":{"success":true,"info":{"title":"标题","detailList":[]}}}`,
		`{"data":{"success":true,"info":{"title":"标题","detailList":[{"contentHtml":"  "}]}}}`,
		`{"data":{"success":true,"info":{"title":"标题","detailList":[{"contentHtml":"<p>residual"}]}} trailing`,
	}
	for index, failedBody := range failedBodies {
		doer := &fakeDoer{handler: func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, failedBody), nil }}
		_, fetchErr := fetchAndParsePage(context.Background(), spec, url, prior, true, doer, &requestBudget{}, func() time.Time { return now.Add(time.Minute) })
		if failureCode(fetchErr) != "source_parse_error" {
			t.Fatalf("case %d was not rejected as parser failure: %v", index, fetchErr)
		}
		result := statusFromError(spec, &snapshot, failureCode(fetchErr))
		if result.Status != "stale" || result.Snapshot == nil || result.Snapshot.ContentSHA256 != snapshot.ContentSHA256 || result.Snapshot.Pages[0].ContentSHA256 != prior.ContentSHA256 {
			t.Fatalf("case %d did not retain the exact last-good snapshot: %+v", index, result)
		}
	}
	cache := t.TempDir()
	first := SyncStartup(context.Background(), StartupSyncOptions{CacheDir: cache, NewDoer: func() HTTPDoer { return fixtureRouter(t, false) }, Now: func() time.Time { return now }})
	if first.Providers[3].Status != "updated" {
		t.Fatalf("initial Alibaba snapshot was not cached: %+v", first.Providers[3])
	}
	before, err := readSourceSnapshot(cache, spec)
	if err != nil {
		t.Fatal(err)
	}
	failedDoer := fixtureRouter(t, false)
	validHandler := failedDoer.handler
	failedDoer.handler = func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "t.aliyun.com" {
			return jsonResponse(http.StatusOK, failedBodies[0]), nil
		}
		return validHandler(req)
	}
	second := SyncStartup(context.Background(), StartupSyncOptions{CacheDir: cache, NewDoer: func() HTTPDoer { return failedDoer }, Now: func() time.Time { return now.Add(2 * time.Minute) }})
	if second.Providers[3].Status != "stale" || second.Providers[3].ErrorCode != "source_parse_error" {
		t.Fatalf("failed renderer response did not report stale parser error: %+v", second.Providers[3])
	}
	after, err := readSourceSnapshot(cache, spec)
	if err != nil || after.ContentSHA256 != before.ContentSHA256 || !after.CheckedAt.Equal(before.CheckedAt) {
		t.Fatalf("failed renderer response replaced cached last-good: before=%+v after=%+v err=%v", before, after, err)
	}
	redirectDoer := fixtureRouter(t, false)
	redirectHandler := redirectDoer.handler
	redirectDoer.handler = func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "t.aliyun.com" {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{alibabaBulletinURL("654321")}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return redirectHandler(req)
	}
	third := SyncStartup(context.Background(), StartupSyncOptions{CacheDir: cache, NewDoer: func() HTTPDoer { return redirectDoer }, Now: func() time.Time { return now.Add(3 * time.Minute) }})
	if third.Providers[3].Status != "stale" || third.Providers[3].ErrorCode != "redirect_rejected" || third.Providers[3].Snapshot == nil || third.Providers[3].Snapshot.ContentSHA256 != before.ContentSHA256 {
		t.Fatalf("cross-notice redirect did not retain stale last-good evidence: %+v", third.Providers[3])
	}
	afterRedirect, err := readSourceSnapshot(cache, spec)
	if err != nil || afterRedirect.ContentSHA256 != before.ContentSHA256 {
		t.Fatalf("cross-notice redirect overwrote the cached official notice snapshot: %+v err=%v", afterRedirect, err)
	}
	redirectDoer.mu.Lock()
	redirectRequests := append([]*http.Request(nil), redirectDoer.requests...)
	redirectDoer.mu.Unlock()
	for _, request := range redirectRequests {
		if request.URL.Host == "t.aliyun.com" && request.URL.RawQuery == "id=654321&language=zh&website=cn" {
			t.Fatalf("cross-notice redirect target was requested and could be misattributed: %s", request.URL)
		}
	}
}

func TestAlibabaProseLifecycleAnchorExtractsOnlyPrecedingModel(t *testing.T) {
	evidence := alibabaProseEvidence("将于 2099 年 3 月 4 日对 gte-rerank 模型进行下线处理。我们推荐替换为 qwen3-rerank。", "https://example.invalid", "test", time.Now(), "digest")
	if len(evidence) != 1 || evidence[0].ModelID != "gte-rerank" || evidence[0].EffectiveAt != "2099-03-04" {
		t.Fatalf("Alibaba prose lifecycle extraction mismatch: %+v", evidence)
	}
}

func TestCredentialedProxyRejectedBeforeClientOrRequest(t *testing.T) {
	proxy, err := url.Parse("http://user:secret@127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	clientCalls, envCalls, resolverCalls := 0, 0, 0
	result := SyncStartup(context.Background(), StartupSyncOptions{
		CacheDir:      filepath.Join(t.TempDir(), "cache"),
		LookupEnv:     func(string) (string, bool) { envCalls++; return "not-read", true },
		proxyResolver: func(*http.Request) (*url.URL, error) { resolverCalls++; return proxy, nil },
		NewDoer: func() HTTPDoer {
			clientCalls++
			return &fakeDoer{handler: func(*http.Request) (*http.Response, error) {
				t.Fatal("request sent despite credentialed proxy")
				return nil, nil
			}}
		},
	})
	if resolverCalls == 0 || clientCalls != 0 || envCalls != 0 {
		t.Fatalf("proxy credential preflight crossed the request/client boundary: resolver=%d clients=%d env=%d", resolverCalls, clientCalls, envCalls)
	}
	for _, provider := range result.Providers {
		if provider.Status != "unavailable" || provider.ErrorCode != "proxy_configuration_rejected" {
			t.Fatalf("credentialed proxy was not rejected: %+v", provider)
		}
	}
	if _, err := resolveAnonymousProxy(&http.Request{}, func(*http.Request) (*url.URL, error) { return proxy, nil }); err == nil {
		t.Fatal("proxy resolver accepted userinfo")
	}
}

func TestConfiguredProxyValidationCoversOnlyFixedSourceHosts(t *testing.T) {
	seen := map[string]bool{}
	if err := validateConfiguredProxy(func(req *http.Request) (*url.URL, error) {
		if req.Method != http.MethodGet || req.URL.Scheme != "https" || validateRequestAllowlist(req.URL.String()) != nil {
			t.Fatalf("proxy preflight escaped fixed official source set: %s %s", req.Method, req.URL)
		}
		seen[req.URL.Host] = true
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"developers.openai.com", "platform.claude.com", "api-docs.deepseek.com", "help.aliyun.com", "t.aliyun.com", "docs.x.ai"} {
		if !seen[host] {
			t.Errorf("proxy preflight omitted fixed official host %s", host)
		}
	}
}

func validateRequestAllowlist(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	for _, spec := range sourceManifest {
		if validateSourceURL(spec, raw) == nil {
			return nil
		}
	}
	return errors.New("source request outside allowlist: " + u.Host + u.Path)
}

func TestRedirectAndChildURLAllowlistBudget(t *testing.T) {
	spec := specFor(t, "openai")
	for _, raw := range []string{
		"http://developers.openai.com/api/docs/deprecations",
		"https://user@developers.openai.com/api/docs/deprecations",
		"https://developers.openai.com:443/api/docs/deprecations",
		"https://developers.openai.com/api/docs/deprecations?x=1",
		"https://evil.example/api/docs/deprecations",
	} {
		if validateSourceURL(spec, raw) == nil {
			t.Errorf("unsafe source URL accepted: %s", raw)
		}
	}
	doer := &fakeDoer{handler: func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://evil.example/steal"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}}
	budget := &requestBudget{}
	if _, err := fetchAndParsePage(context.Background(), spec, spec.rootURL, CachedPage{}, false, doer, budget, time.Now); failureCode(err) != "redirect_rejected" {
		t.Fatalf("cross-host redirect error=%v", err)
	}
	if budget.count() != 1 {
		t.Fatalf("rejected redirect must not issue target GET; count=%d want 1", budget.count())
	}

	redirects := &fakeDoer{handler: func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": []string{req.URL.String()}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}}
	budget = &requestBudget{}
	if _, err := fetchAndParsePage(context.Background(), spec, spec.rootURL, CachedPage{}, false, redirects, budget, time.Now); failureCode(err) != "redirect_limit_exceeded" {
		t.Fatalf("redirect limit error=%v", err)
	}
	if budget.count() != 3 {
		t.Fatalf("two-hop redirect should make initial+2 requests: %d", budget.count())
	}

	alibaba := specFor(t, "alibaba")
	links := make([]string, MaxAlibabaNotices+1)
	for i := range links {
		links[i] = "https://www.aliyun.com/notice/" + strconv.Itoa(1000+i)
	}
	if _, err := validateChildren(alibaba, alibaba.rootURL, links); failureCode(err) != "source_link_limit_exceeded" {
		t.Fatalf("Alibaba limit error=%v", err)
	}
	badLinks := []string{"https://evil.example/notice/123"}
	if _, err := validateChildren(alibaba, alibaba.rootURL, badLinks); failureCode(err) != "source_link_rejected" {
		t.Fatalf("unsafe candidate link error=%v", err)
	}
	for _, invalid := range []string{
		"https://www.aliyun.com/notice/not-a-number",
		"https://www.aliyun.com/notice/123456/extra",
		"https://www.aliyun.com/notice/?unexpected=1",
	} {
		if _, err := validateChildren(alibaba, alibaba.rootURL, []string{invalid}); failureCode(err) != "source_link_rejected" {
			t.Errorf("non-canonical Alibaba notice link was not rejected: %s err=%v", invalid, err)
		}
	}
	childURLs, err := validateChildren(alibaba, alibaba.rootURL, []string{"https://www.aliyun.com/notice/123456", "https://www.aliyun.com/notice/123456"})
	if err != nil || len(childURLs) != 1 || childURLs[0] != alibabaBulletinURL("123456") {
		t.Fatalf("official index links were not deduplicated and converted to the fixed renderer endpoint: urls=%v err=%v", childURLs, err)
	}
	childURLs, err = validateChildren(alibaba, alibaba.rootURL, []string{"https://www.aliyun.com/notice/", "https://www.aliyun.com/notice/123456"})
	if err != nil || len(childURLs) != 1 || childURLs[0] != alibabaBulletinURL("123456") {
		t.Fatalf("generic official notice index link was not ignored: urls=%v err=%v", childURLs, err)
	}
	for _, raw := range []string{
		"https://www.aliyun.com/notice/123456",
		"https://t.aliyun.com/abs/bulletin/bulletinDetail?id=123456&language=zh&website=cn&extra=x",
		"https://t.aliyun.com/abs/bulletin/bulletinDetail?id=123456&language=en&website=cn",
		"https://t.aliyun.com/abs/bulletin/bulletinDetail?id=123456&website=cn&language=zh",
		"https://t.aliyun.com/abs/bulletin/bulletinDetail?id=123456&language=zh&website=cn#fragment",
	} {
		if validateSourceURL(alibaba, raw) == nil {
			t.Errorf("Alibaba renderer accepted non-canonical URL: %s", raw)
		}
	}
	validRenderer := alibabaBulletinURL("123456")
	if err := validateSourceURL(alibaba, validRenderer); err != nil {
		t.Fatalf("canonical Alibaba renderer URL rejected: %v", err)
	}
	changedQueryRedirect := &fakeDoer{handler: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://t.aliyun.com/abs/bulletin/bulletinDetail?id=123456&language=zh&website=cn&extra=x"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}}
	budget = &requestBudget{}
	if _, err := fetchAndParsePage(context.Background(), alibaba, validRenderer, CachedPage{}, false, changedQueryRedirect, budget, time.Now); failureCode(err) != "redirect_rejected" {
		t.Fatalf("renderer redirect that changed query was not rejected: %v", err)
	}
	if budget.count() != 1 {
		t.Fatalf("invalid renderer redirect issued a second request: %d", budget.count())
	}
	crossNoticeIDRedirect := &fakeDoer{handler: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{alibabaBulletinURL("654321")}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}}
	budget = &requestBudget{}
	if _, err := fetchAndParsePage(context.Background(), alibaba, validRenderer, CachedPage{}, false, crossNoticeIDRedirect, budget, time.Now); failureCode(err) != "redirect_rejected" {
		t.Fatalf("renderer redirect changed official notice ID but was accepted: %v", err)
	}
	if budget.count() != 1 {
		t.Fatalf("cross-notice redirect issued a second GET: %d", budget.count())
	}
	xai := specFor(t, "xai")
	if err := validateSourceURL(xai, "https://docs.x.ai/developers/migration/Valid-Case"); err == nil {
		t.Fatal("uppercase migration slug accepted")
	}
	if err := validateSourceURL(xai, "https://docs.x.ai/developers/migration/valid-case?token=x"); err == nil {
		t.Fatal("migration query accepted")
	}
	budget = &requestBudget{}
	for i := 0; i < MaxHTTPGets; i++ {
		if !budget.reserve() {
			t.Fatalf("budget stopped early at %d", i)
		}
	}
	if budget.reserve() {
		t.Fatal("87 GET budget exceeded")
	}
}

func TestLogicalDocumentsAndRedirectsStopAtGlobal87GETLimit(t *testing.T) {
	logicalURLs := make([]string, 0, 1+1+1+1+MaxAlibabaNotices+1+MaxXAIMigrations)
	for _, provider := range []string{"openai", "anthropic", "deepseek"} {
		logicalURLs = append(logicalURLs, specFor(t, provider).rootURL)
	}
	for _, provider := range []string{"alibaba", "xai"} {
		spec := specFor(t, provider)
		logicalURLs = append(logicalURLs, spec.rootURL)
		childCount := spec.maxChildren
		links := make([]string, childCount)
		for i := range links {
			if provider == "alibaba" {
				links[i] = "https://www.aliyun.com/notice/" + strconv.Itoa(1000+i)
			} else {
				links[i] = "https://docs.x.ai/developers/migration/migration-" + strconv.Itoa(i)
			}
		}
		children, err := validateChildren(spec, spec.rootURL, links)
		if err != nil || len(children) != childCount {
			t.Fatalf("%s logical child URL set=%d err=%v", provider, len(children), err)
		}
		logicalURLs = append(logicalURLs, children...)
	}
	if len(logicalURLs) != 29 {
		t.Fatalf("logical document upper bound=%d want 29", len(logicalURLs))
	}
	var requestCount int
	doer := &fakeDoer{handler: func(req *http.Request) (*http.Response, error) {
		requestCount++
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": []string{req.URL.String()}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}}
	budget := &requestBudget{}
	for _, raw := range logicalURLs {
		var spec sourceSpec
		found := false
		for _, candidate := range sourceManifest {
			if validateSourceURL(candidate, raw) == nil {
				spec, found = candidate, true
				break
			}
		}
		if !found {
			t.Fatalf("logical URL is outside source manifest: %s", raw)
		}
		_, err := fetchAndParsePage(context.Background(), spec, raw, CachedPage{}, false, doer, budget, time.Now)
		if failureCode(err) != "redirect_limit_exceeded" {
			t.Fatalf("redirect chain for %s did not stop at two redirects: %v", raw, err)
		}
	}
	if requestCount != MaxHTTPGets || budget.count() != MaxHTTPGets {
		t.Fatalf("worst-case requests=%d budget=%d want %d", requestCount, budget.count(), MaxHTTPGets)
	}
	if _, err := fetchAndParsePage(context.Background(), specFor(t, "openai"), specFor(t, "openai").rootURL, CachedPage{}, false, doer, budget, time.Now); failureCode(err) != "source_request_budget_exceeded" {
		t.Fatalf("request after global budget was not rejected: %v", err)
	}
	if requestCount != MaxHTTPGets {
		t.Fatalf("GET budget overflow sent request %d", requestCount)
	}
}

func TestFetchRejectsEmptyMalformedAndOversizedPagesAndRetainsLastGood(t *testing.T) {
	spec := specFor(t, "openai")
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	goodBody := fixture(t, "openai-deprecations.html")
	parsed, err := parseSourcePage(spec, spec.rootURL, goodBody, now)
	if err != nil {
		t.Fatal(err)
	}
	goodHash := sha256Hex(goodBody)
	page := CachedPage{URL: spec.rootURL, Title: parsed.Title, ContentSHA256: goodHash, FetchedAt: now, Links: parsed.Links, Evidence: parsed.Evidence}
	snapshot := CatalogSnapshot{Provider: spec.provider, Pages: []CachedPage{page}, Evidence: parsed.Evidence, FetchedAt: now, CheckedAt: now, Complete: true}
	snapshot.ContentSHA256, err = snapshotDigest(snapshot.Pages)
	if err != nil || validateSnapshot(spec, snapshot) != nil {
		t.Fatalf("test last-good fixture invalid: %v", err)
	}
	priorHash := snapshot.ContentSHA256
	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", "response_too_large_or_empty"},
		{"malformed", "<html><title>Unrelated</title><p>partial body</p>", "source_parse_error"},
		{"oversized", strings.Repeat("x", int(spec.maxBody)+1), "response_too_large_or_empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doer := &fakeDoer{handler: func(*http.Request) (*http.Response, error) { return htmlResponse(http.StatusOK, tc.body), nil }}
			_, fetchErr := fetchAndParsePage(context.Background(), spec, spec.rootURL, page, true, doer, &requestBudget{}, func() time.Time { return now })
			if got := failureCode(fetchErr); got != tc.want {
				t.Fatalf("failure code=%q want %q err=%v", got, tc.want, fetchErr)
			}
			result := statusFromError(spec, &snapshot, failureCode(fetchErr))
			if result.Status != "stale" || result.Snapshot == nil || result.Snapshot.ContentSHA256 != priorHash {
				t.Fatalf("bad page did not retain last-good snapshot: %+v", result)
			}
		})
	}
	links := strings.Repeat(`<a href="/api/docs/deprecations">x</a>`, MaxParsedLinks+1)
	if _, err := parseSourcePage(spec, spec.rootURL, []byte(strings.Replace(string(goodBody), "</body>", links+"</body>", 1)), now); err == nil {
		t.Fatal("page with too many links was accepted")
	}
}

type liveSmokeDoer struct {
	inner    HTTPDoer
	mu       sync.Mutex
	requests []*http.Request
	results  []liveSmokeHTTPResult
}

type liveSmokeHTTPResult struct {
	URL            string
	StatusCode     int
	TransportError bool
}

func (d *liveSmokeDoer) Do(req *http.Request) (*http.Response, error) {
	resp, err := d.inner.Do(req)
	d.mu.Lock()
	d.requests = append(d.requests, req.Clone(req.Context()))
	result := liveSmokeHTTPResult{URL: req.URL.String(), TransportError: err != nil}
	if resp != nil {
		result.StatusCode = resp.StatusCode
	}
	d.results = append(d.results, result)
	d.mu.Unlock()
	return resp, err
}

func TestLiveSmokeOfficialLifecycleSources(t *testing.T) {
	if os.Getenv("OFFICIAL_LIFECYCLE_LIVE_SMOKE") != "1" {
		t.Skip("set OFFICIAL_LIFECYCLE_LIVE_SMOKE=1 to perform anonymous GET smoke against the five fixed public sources")
	}
	observed := &liveSmokeDoer{inner: NewHTTPClient()}
	result := SyncStartup(context.Background(), StartupSyncOptions{
		CacheDir: t.TempDir(), NewDoer: func() HTTPDoer { return observed }, Now: time.Now,
	})
	parsedCount, parseErrorCount, unavailableCount := 0, 0, 0
	for _, provider := range result.Providers {
		parserStatus := liveParserStatus(provider)
		t.Logf("public source provider=%s http=%s parser=%s status=%s evidence=%d error=%s title=%q", provider.Provider, liveHTTPStatus(provider.Provider, observed), parserStatus, provider.Status, provider.EvidenceCount, provider.ErrorCode, provider.PageTitle)
		if provider.Provider == "alibaba" && provider.Snapshot != nil {
			byModel := make(map[string][]LifecycleEvidence)
			for _, page := range provider.Snapshot.Pages {
				t.Logf("public source detail provider=alibaba page=%s evidence=%d", page.URL, len(page.Evidence))
			}
			for _, evidence := range provider.Snapshot.Evidence {
				byModel[evidence.ModelID] = append(byModel[evidence.ModelID], evidence)
			}
			repeatedIDs, conflictingIDs, replacementHits := 0, 0, 0
			for modelID, evidenceList := range byModel {
				if len(evidenceList) > 1 {
					repeatedIDs++
					statuses := make(map[string]struct{})
					for _, evidence := range evidenceList {
						statuses[evidence.Status] = struct{}{}
					}
					if len(statuses) > 1 {
						conflictingIDs++
					}
					t.Logf("public evidence duplicate provider=alibaba model=%s records=%d statuses=%v evidence=%+v", modelID, len(evidenceList), statuses, evidenceList)
				}
			}
			for _, evidenceList := range provider.Snapshot.Evidence {
				if evidenceList.Replacement != "" {
					if _, ok := byModel[evidenceList.Replacement]; ok {
						replacementHits++
						t.Logf("public evidence replacement-also-listed provider=alibaba deprecated=%s replacement=%s source=%s", evidenceList.ModelID, evidenceList.Replacement, evidenceList.SourceURL)
					}
				}
			}
			t.Logf("public evidence audit provider=alibaba unique_model_ids=%d repeated_ids=%d conflicting_status_ids=%d replacement_ids_also_listed=%d", len(byModel), repeatedIDs, conflictingIDs, replacementHits)
		}
		switch parserStatus {
		case "pass":
			parsedCount++
		case "parse_error":
			parseErrorCount++
		case "not_reached_or_http_failure":
			unavailableCount++
		default:
			t.Errorf("source returned an unclassified parser state: provider=%s status=%s error=%s", provider.Provider, provider.Status, provider.ErrorCode)
		}
	}
	t.Logf("live source summary parsed=%d parse_error=%d unavailable_or_http_failure=%d total=%d", parsedCount, parseErrorCount, unavailableCount, len(result.Providers))
	observed.mu.Lock()
	defer observed.mu.Unlock()
	if len(observed.requests) == 0 || len(observed.requests) > MaxHTTPGets {
		t.Fatalf("live request count=%d outside 1..%d", len(observed.requests), MaxHTTPGets)
	}
	for _, req := range observed.requests {
		if validateRequestAllowlist(req.URL.String()) != nil {
			t.Errorf("live request escaped the fixed official allowlist: %s", req.URL)
		}
		for _, header := range []string{"Authorization", "X-Api-Key", "Cookie"} {
			if req.Header.Get(header) != "" {
				t.Errorf("anonymous live GET sent prohibited header %s", header)
			}
		}
	}
}

func liveHTTPStatus(provider string, observed *liveSmokeDoer) string {
	observed.mu.Lock()
	defer observed.mu.Unlock()
	statuses := make([]string, 0)
	for _, item := range observed.results {
		for _, spec := range sourceManifest {
			if spec.provider == provider && validateSourceURL(spec, item.URL) == nil {
				status := "transport_error"
				if item.StatusCode != 0 {
					status = fmt.Sprintf("%d", item.StatusCode)
				}
				statuses = append(statuses, status)
				break
			}
		}
	}
	if len(statuses) == 0 {
		return "no-request"
	}
	return strings.Join(statuses, ",")
}

func liveParserStatus(provider ProviderSync) string {
	if provider.Status == "updated" || provider.Status == "unchanged" {
		return "pass"
	}
	if provider.ErrorCode == "source_parse_error" {
		return "parse_error"
	}
	return "not_reached_or_http_failure"
}

func TestStartupFailurePreservesLastGoodAndOfflineDoesNotNetwork(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	first := SyncStartup(context.Background(), StartupSyncOptions{CacheDir: root, NewDoer: func() HTTPDoer { return fixtureRouter(t, false) }, Now: time.Now})
	for _, item := range first.Providers {
		if item.Status != "updated" {
			t.Fatalf("initial source sync failed: %+v", item)
		}
	}
	prior, err := readSourceSnapshot(root, specFor(t, "openai"))
	if err != nil {
		t.Fatal(err)
	}
	failedDoer := fixtureRouter(t, true)
	second := SyncStartup(context.Background(), StartupSyncOptions{CacheDir: root, NewDoer: func() HTTPDoer { return failedDoer }, Now: time.Now})
	for _, item := range second.Providers {
		if item.Status != "stale" || item.Snapshot == nil {
			t.Errorf("failed refresh did not retain last-good: %+v", item)
		}
	}
	after, err := readSourceSnapshot(root, specFor(t, "openai"))
	if err != nil {
		t.Fatal(err)
	}
	if prior.ContentSHA256 != after.ContentSHA256 || !prior.CheckedAt.Equal(after.CheckedAt) {
		t.Fatal("failed refresh replaced the last-good snapshot")
	}
	lookupCalls, clients := 0, 0
	offline := SyncStartup(context.Background(), StartupSyncOptions{Offline: true, CacheDir: root, LookupEnv: func(string) (string, bool) { lookupCalls++; return "x", true }, NewDoer: func() HTTPDoer { clients++; return failedDoer }})
	if lookupCalls != 0 || clients != 0 || len(failedDoer.requests) != 5 {
		t.Fatalf("offline path touched network or credentials: lookups=%d clients=%d requests=%d", lookupCalls, clients, len(failedDoer.requests))
	}
	for _, item := range offline.Providers {
		if item.Status != "offline_cached" {
			t.Errorf("offline cache status=%+v", item)
		}
	}
}

func TestConditionalGet304RequiresAndReusesGoodSnapshot(t *testing.T) {
	spec := specFor(t, "openai")
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	parsed, err := parseSourcePage(spec, spec.rootURL, fixture(t, "openai-deprecations.html"), now)
	if err != nil {
		t.Fatal(err)
	}
	body := fixture(t, "openai-deprecations.html")
	hash := sha256Hex(body)
	prior := CachedPage{URL: spec.rootURL, Title: parsed.Title, ContentSHA256: hash, FetchedAt: now, ETag: "etag-old", LastModified: "old-date", Evidence: parsed.Evidence}
	seenHeaders := false
	doer := &fakeDoer{handler: func(req *http.Request) (*http.Response, error) {
		seenHeaders = req.Header.Get("If-None-Match") == "etag-old" && req.Header.Get("If-Modified-Since") == "old-date"
		return &http.Response{StatusCode: http.StatusNotModified, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	}}
	got, err := fetchAndParsePage(context.Background(), spec, spec.rootURL, prior, true, doer, &requestBudget{}, func() time.Time { return now.Add(time.Minute) })
	if err != nil || !seenHeaders || got.ContentSHA256 != prior.ContentSHA256 || len(got.Evidence) != 1 {
		t.Fatalf("304 reuse failed: headers=%v got=%+v err=%v", seenHeaders, got, err)
	}
	if _, err := fetchAndParsePage(context.Background(), spec, spec.rootURL, CachedPage{}, false, doer, &requestBudget{}, time.Now); failureCode(err) != "invalid_not_modified_response" {
		t.Fatalf("304 without cached body accepted: %v", err)
	}
}

func TestPersistentSnapshotHasPrivatePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	result := SyncStartup(context.Background(), StartupSyncOptions{CacheDir: root, NewDoer: func() HTTPDoer { return fixtureRouter(t, false) }, Now: time.Now})
	if result.Providers[0].Status != "updated" {
		t.Fatalf("sync failed: %+v", result.Providers[0])
	}
	path := filepath.Join(root, sourceManifest[0].id, "snapshot.json")
	if err := securePrivateFile(path); err != nil {
		t.Fatalf("cache file security validation failed: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("cache is not private: %v", info.Mode().Perm())
		}
	}
}

func sha256Hex(body []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(body))
}
