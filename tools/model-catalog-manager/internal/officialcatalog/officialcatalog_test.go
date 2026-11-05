package officialcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fixtureDoer struct {
	respond  func(*http.Request) (*http.Response, error)
	requests []*http.Request
}

func (d *fixtureDoer) Do(r *http.Request) (*http.Response, error) {
	d.requests = append(d.requests, r.Clone(r.Context()))
	return d.respond(r)
}

func fixtureResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func fixedNow() time.Time { return time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) }

func testKeys() map[string][]byte {
	return map[string][]byte{"anthropic": []byte("a-test-key"), "openai": []byte("o-test-key"), "deepseek": []byte("d-test-key"), "alibaba": []byte("b-test-key"), "xai": []byte("x-test-key")}
}

func fixtureResponder(t *testing.T) *fixtureDoer {
	t.Helper()
	return &fixtureDoer{respond: func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Fragment != "" {
			t.Fatalf("non-fixed or unsafe URL reached transport: %s", req.URL.Redacted())
		}
		switch req.URL.Host {
		case "api.anthropic.com":
			if req.URL.Path != "/v1/models" || req.Header.Get("anthropic-version") != "2023-06-01" || req.Header.Get("x-api-key") != "a-test-key" {
				t.Fatalf("Anthropic request contract mismatch: path=%s version=%q", req.URL.Path, req.Header.Get("anthropic-version"))
			}
			q := req.URL.Query()
			if q.Get("limit") != "1000" || strings.Join(q["lifecycle"], ",") != "active,deprecated,retired" {
				t.Fatalf("Anthropic lifecycle filters were not explicit: %v", q)
			}
			if q.Get("after_id") == "" {
				return fixtureResponse(200, readFixture(t, "anthropic-page-1.json")), nil
			}
			if q.Get("after_id") == "claude-old" {
				return fixtureResponse(200, readFixture(t, "anthropic-page-2.json")), nil
			}
			t.Fatalf("unexpected Anthropic cursor: %q", q.Get("after_id"))
		case "api.openai.com":
			if req.URL.Path != "/v1/models" || req.Header.Get("Authorization") != "Bearer o-test-key" {
				t.Fatalf("OpenAI request contract mismatch")
			}
			return fixtureResponse(200, readFixture(t, "openai.json")), nil
		case "api.deepseek.com":
			if req.URL.Path != "/models" || req.Header.Get("Authorization") != "Bearer d-test-key" {
				t.Fatalf("DeepSeek request contract mismatch")
			}
			return fixtureResponse(200, readFixture(t, "deepseek.json")), nil
		case "ws-test.cn-beijing.maas.aliyuncs.com":
			if req.URL.Path != "/api/v1/models" || req.Header.Get("Authorization") != "Bearer b-test-key" || req.URL.Query().Get("page_size") != "100" {
				t.Fatalf("Alibaba request contract mismatch")
			}
			if req.URL.Query().Get("page_no") == "1" {
				return fixtureResponse(200, readFixture(t, "alibaba-page-1.json")), nil
			}
			if req.URL.Query().Get("page_no") == "2" {
				return fixtureResponse(200, readFixture(t, "alibaba-page-2.json")), nil
			}
			t.Fatalf("unexpected Alibaba page: %s", req.URL.RawQuery)
		case "api.x.ai":
			if req.Header.Get("Authorization") != "Bearer x-test-key" {
				t.Fatalf("xAI request missing expected authentication")
			}
			switch req.URL.Path {
			case "/v1/models":
				return fixtureResponse(200, readFixture(t, "xai-models.json")), nil
			case "/v1/image-generation-models":
				return fixtureResponse(200, readFixture(t, "xai-images.json")), nil
			case "/v1/video-generation-models":
				return fixtureResponse(200, readFixture(t, "xai-videos.json")), nil
			}
		}
		t.Fatalf("request escaped the fixed endpoint allowlist: %s", req.URL.Redacted())
		return nil, errors.New("unexpected request")
	}}
}

func TestAllOfficialConnectorsParseDocumentedShapesAndPagination(t *testing.T) {
	inv := inventoryFor("claude-live", "claude-old", "claude-retired", "gpt-upcoming", "gpt-past", "deepseek-flash", "qwen-offline", "grok-latest", "grok-imagine-video", "shared-model", "unmatched")
	doer := fixtureResponder(t)
	keys := testKeys()
	report, err := Review(context.Background(), inv, keys, Options{AlibabaRegion: "beijing", AlibabaWorkspace: "ws-test"}, doer, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(doer.requests) != 9 {
		t.Fatalf("expected 9 fixture requests (two Anthropic pages, three xAI lists, two Alibaba pages, OpenAI, DeepSeek): got %d", len(doer.requests))
	}
	for _, key := range keys {
		for _, value := range key {
			if value != 0 {
				t.Fatal("OEM credential buffer was not cleared after review")
			}
		}
	}
	for _, result := range report.ProviderResults {
		if result.Status != "available" {
			t.Fatalf("%s fetch status = %s (%s)", result.Provider, result.Status, result.ErrorCode)
		}
	}
	assertObservation(t, report, "claude-old", "official_deprecated_candidate", "anthropic", "exact_id")
	assertObservation(t, report, "claude-retired", "official_retired_candidate", "anthropic", "exact_id")
	assertObservation(t, report, "gpt-upcoming", "official_shutdown_date_upcoming", "openai", "exact_id")
	assertObservation(t, report, "gpt-past", "official_shutdown_date_passed_candidate", "openai", "exact_id")
	assertObservation(t, report, "deepseek-flash", "official_reference_match", "deepseek", "exact_id")
	assertObservation(t, report, "qwen-offline", "official_offline_time_candidate", "alibaba", "exact_id")
	assertObservation(t, report, "grok-latest", "official_alias_candidate", "xai", "official_alias_candidate")
	assertObservation(t, report, "unmatched", "unknown", "", "")
	claude := findObservation(t, report, "claude-live")
	if claude.Platform != "openai" || len(claude.Matches) != 1 || claude.Matches[0].Provider != "anthropic" {
		t.Fatalf("Sub2API platform was used as OEM attribution: %#v", claude)
	}
	shared := findObservation(t, report, "shared-model")
	if shared.Status != "multiple_oem_references" || !shared.MultipleOEMRefs || len(shared.Matches) != 2 {
		t.Fatalf("shared OEM references not retained/marked: %#v", shared)
	}
	for _, observation := range report.Observations {
		if observation.Warning != "same exact text only; route is unknown" {
			t.Fatalf("missing route warning on %#v", observation)
		}
		for _, match := range observation.Matches {
			if strings.Contains(match.Provider, "platform") {
				t.Fatalf("Sub2API platform was converted to OEM attribution: %#v", match)
			}
		}
	}
	var anthropicRequest *http.Request
	for _, request := range doer.requests {
		if request.URL.Host == "api.anthropic.com" {
			anthropicRequest = request
			break
		}
	}
	if anthropicRequest == nil || anthropicRequest.URL.Query().Get("after_id") != "" {
		t.Fatal("Anthropic fixture did not exercise first page")
	}
}

func TestExactCaseDateShapeAndDeepSeekNamesNeverMerge(t *testing.T) {
	inv := inventoryFor("MiniMax-M3", "minimax-m3", "deepseek-flash", "model-x", "model-x-20261012", "dated", "dated-2026-10-12")
	catalogs := []fetchedCatalog{{Models: []modelRecord{{Provider: "xai", ModelID: "MiniMax-M3", Status: "official_reference_match", Endpoints: []string{"/fixture"}}}}}
	report := Report{Observations: matchObservations(inv.Observations, catalogs, fixedNow()), NameShape: findNameShapeCandidates(inv)}
	upper := findObservation(t, report, "MiniMax-M3")
	lower := findObservation(t, report, "minimax-m3")
	if upper.Status != "official_reference_match" || lower.Status != "unknown" {
		t.Fatalf("matching was not exact and case-sensitive: upper=%s lower=%s", upper.Status, lower.Status)
	}
	if findObservation(t, report, "deepseek-flash").Status != "unknown" {
		t.Fatal("deepseek-flash was inferred as a different model")
	}
	if len(report.NameShape) != 2 {
		t.Fatalf("expected only two date-shaped candidate pairs, got %#v", report.NameShape)
	}
	for _, candidate := range report.NameShape {
		if candidate.Action != "none" || candidate.Equivalence != "unproven" || candidate.Label != "name_shape_candidate" {
			t.Fatalf("date shape escaped candidate-only status: %#v", candidate)
		}
	}
}

func TestInputWhitelistDigestAndOutputExcludeUnknownFieldsAndURLSecrets(t *testing.T) {
	const secret = "SENTINEL-DO-NOT-EMIT"
	body := `{"mode":"inspect","accounts":[{"account_id":9001,"platform":"openai","account_type":"apikey","api_protocol":"openai","source_fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_url":"https://user:` + secret + `@example.invalid/v1?token=` + secret + `#` + secret + `","provider_api_key":"` + secret + `","mappings":[{"public_id":"public-model","upstream_id":"upstream-model","custom_header":"` + secret + `"}]}]}`
	inventory, err := ParseInventory(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inventory.Digest, secret) || inventory.Digest == "" {
		t.Fatal("whitelist digest leaked input material or is missing")
	}
	changedUnknownFields := strings.ReplaceAll(body, secret, "DIFFERENT-UNKNOWN-VALUE")
	changedUnknownFields = strings.ReplaceAll(changedUnknownFields, "example.invalid", "other.invalid")
	changedInventory, err := ParseInventory(strings.NewReader(changedUnknownFields))
	if err != nil || changedInventory.Digest != inventory.Digest {
		t.Fatalf("unknown fields changed the whitelist digest: %q != %q (%v)", changedInventory.Digest, inventory.Digest, err)
	}
	if len(inventory.Observations) != 2 {
		t.Fatalf("mapping IDs were not both extracted: %#v", inventory.Observations)
	}
	report, err := Review(context.Background(), inventory, nil, Options{}, &fixtureDoer{respond: func(*http.Request) (*http.Response, error) {
		t.Fatal("missing credentials must not send a request")
		return nil, nil
	}}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "example.invalid") || strings.Contains(string(encoded), "token=") || strings.Contains(string(encoded), "user:") {
		t.Fatalf("report emitted unknown input or URL components: %s", encoded)
	}
	if report.Observations[0].Warning != "same exact text only; route is unknown" {
		t.Fatal("observation lacks route boundary")
	}
}

func TestSummaryMetadataDoesNotChangeCanonicalDigestOrOfficialReviewMatching(t *testing.T) {
	const base = `{"mode":"inspect","accounts":[{"account_id":9001,"platform":"openai","account_type":"apikey","api_protocol":"openai","source_fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_url":"https://secret.invalid/v1?token=SECRET-SENTINEL","mappings":[{"public_id":"public-model","upstream_id":"upstream-model"}]}]}`
	withMetadata := `{"mode":"inspect","created_at":"2026-10-12T11:00:00Z","account_scope":"all","account_scan_status":"complete","account_inventory_count":1,"account_inventory_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","target_build_fingerprint":"build-verified","accounts":[{"account_id":9001,"platform":"openai","account_type":"apikey","api_protocol":"openai","source_fingerprint":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_url":"https://secret.invalid/v1?token=SECRET-SENTINEL","mappings":[{"public_id":"public-model","upstream_id":"upstream-model"}]}]}`
	first, err := ParseInventory(strings.NewReader(base))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseInventory(strings.NewReader(withMetadata))
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("new metadata changed canonical digest: %s != %s", first.Digest, second.Digest)
	}
	const expectedDigest = "2b703fe766c08a08a63d48d308965423b52deed24dae4bc676d3726d71bed135"
	if second.Digest != expectedDigest {
		t.Fatalf("canonical digest changed: got %s", second.Digest)
	}
	if second.Metadata.AccountScope != "all" || second.Metadata.AccountScanStatus != "complete" || second.Metadata.AccountInventoryCount != 1 || second.Metadata.TargetBuildFingerprint != "build-verified" {
		t.Fatalf("whitelisted metadata was not exposed: %#v", second.Metadata)
	}
	if len(second.AccountRowIDs) != 1 || len(second.Mappings) != 1 || second.Mappings[0].PublicID != "public-model" || len(second.DiscoveryRecords) != 0 {
		t.Fatalf("safe mapping pairs were not extracted: %#v", second)
	}
	catalogs := []fetchedCatalog{{Models: []modelRecord{{Provider: "fixture", ModelID: "upstream-model", Status: "official_reference_match"}}}}
	firstObservations := matchObservations(first.Observations, catalogs, fixedNow())
	secondObservations := matchObservations(second.Observations, catalogs, fixedNow())
	firstJSON, err := json.Marshal(firstObservations)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(secondObservations)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("adding summary metadata changed official-review observations: %s != %s", firstJSON, secondJSON)
	}
}

func TestInvalidInputReportFailsClosedAndDoesNotEchoData(t *testing.T) {
	for name, body := range map[string]string{
		"bad mode":   `{"mode":"evil","accounts":[]}`,
		"trailing":   `{"mode":"inspect","accounts":[{"account_id":1}]} {}`,
		"invalid id": `{"mode":"inspect","accounts":[{"account_id":1,"mappings":[{"public_id":"","upstream_id":"x"}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseInventory(strings.NewReader(body))
			if err == nil {
				t.Fatal("invalid input was accepted")
			}
			if strings.Contains(err.Error(), "evil") || strings.Contains(err.Error(), "upstream") {
				t.Fatalf("input detail leaked from error: %v", err)
			}
		})
	}
}

func TestMissingCredentialsProduceUnknownEvidenceWithoutRequests(t *testing.T) {
	inv := inventoryFor("model-retired-looking")
	doer := &fixtureDoer{respond: func(*http.Request) (*http.Response, error) {
		t.Fatal("request was sent without an OEM key")
		return nil, nil
	}}
	report, err := Review(context.Background(), inv, map[string][]byte{}, Options{}, doer, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range report.ProviderResults {
		if provider.Status != "unavailable" || provider.ErrorCode != "missing_credentials" {
			t.Fatalf("missing key was not represented as unavailable: %#v", provider)
		}
	}
	if findObservation(t, report, "model-retired-looking").Status != "unknown" {
		t.Fatal("missing official evidence was interpreted as retired")
	}
}

func TestHTTPFailuresRedirectsOversizeMalformedAndBadShapesAreSanitized(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantCode   string
	}{
		{"http", `provider key=SECRET-BODY`, 500, "http_error"},
		{"redirect", ``, 302, "redirect_rejected"},
		{"malformed", `{not-json`, 200, "invalid_response"},
		{"wrong-shape", `{"object":"not-list","data":[{"id":"x"}]}`, 200, "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doer := &fixtureDoer{respond: func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "api.openai.com" {
					t.Fatalf("request host was not fixed: %s", req.URL.Host)
				}
				resp := fixtureResponse(tc.status, tc.body)
				if tc.status == 302 {
					resp.Header.Set("Location", "https://attacker.invalid/models")
				}
				return resp, nil
			}}
			keys := map[string][]byte{"openai": []byte("secret-key")}
			report, err := Review(context.Background(), inventoryFor("model-x"), keys, Options{}, doer, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			provider := providerResult(t, report, "openai")
			if provider.Status != "unavailable" || provider.ErrorCode != tc.wantCode {
				t.Fatalf("unexpected sanitized result: %#v", provider)
			}
			encoded, _ := json.Marshal(report)
			for _, sentinel := range []string{"secret-key", "SECRET-BODY", "Authorization", "attacker.invalid"} {
				if strings.Contains(string(encoded), sentinel) {
					t.Fatalf("secret/error body leaked in report (%s): %s", sentinel, encoded)
				}
			}
			if len(doer.requests) != 1 {
				t.Fatalf("redirect was followed or request count changed: %d", len(doer.requests))
			}
		})
	}
	t.Run("transport error string", func(t *testing.T) {
		const keySentinel = "KEY-DO-NOT-LEAK"
		const bodySentinel = "BODY-DO-NOT-LEAK"
		doer := &fixtureDoer{respond: func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("Authorization: Bearer " + keySentinel + "; response=" + bodySentinel)
		}}
		_, _, err := requestJSON(context.Background(), doer, openAIOrigin+"/v1/models", []byte(keySentinel), "Authorization", "bearer", false)
		if err == nil || err.Error() != "request_failed" {
			t.Fatalf("transport error was not mapped to a fixed error code: %v", err)
		}
		for _, sentinel := range []string{keySentinel, bodySentinel, "Authorization", "Bearer"} {
			if strings.Contains(err.Error(), sentinel) {
				t.Fatalf("transport error string leaked %q: %v", sentinel, err)
			}
		}
	})
	t.Run("oversized", func(t *testing.T) {
		doer := &fixtureDoer{respond: func(*http.Request) (*http.Response, error) {
			return fixtureResponse(200, strings.Repeat("x", MaxResponseBytes+1)), nil
		}}
		report, err := Review(context.Background(), inventoryFor("x"), map[string][]byte{"openai": []byte("k")}, Options{}, doer, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		if providerResult(t, report, "openai").ErrorCode != "response_too_large" {
			t.Fatalf("oversized body not rejected: %#v", report.ProviderResults)
		}
	})
}

func TestAnthropicPaginationLoopAlibabaTotalChangeAndRegionValidationFailClosed(t *testing.T) {
	t.Run("anthropic pagination loop", func(t *testing.T) {
		doer := &fixtureDoer{respond: func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Get("after_id") == "" {
				return fixtureResponse(200, `{"data":[{"id":"same","lifecycle":"active"}],"has_more":true,"last_id":"same"}`), nil
			}
			return fixtureResponse(200, `{"data":[{"id":"second","lifecycle":"active"}],"has_more":true,"last_id":"same"}`), nil
		}}
		report, err := Review(context.Background(), inventoryFor("same"), map[string][]byte{"anthropic": []byte("key")}, Options{}, doer, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		pr := providerResult(t, report, "anthropic")
		if pr.Status != "unavailable" || pr.ErrorCode != "pagination_invalid" {
			t.Fatalf("Anthropic loop was not blocked: %#v", pr)
		}
		if findObservation(t, report, "same").Status != "unknown" {
			t.Fatal("partial Anthropic page was treated as valid lifecycle evidence")
		}
	})
	t.Run("Alibaba changed total", func(t *testing.T) {
		doer := &fixtureDoer{respond: func(req *http.Request) (*http.Response, error) {
			if req.URL.Query().Get("page_no") == "1" {
				return fixtureResponse(200, `{"success":true,"output":{"total":2,"page_no":1,"models":[{"model":"m1"}]}}`), nil
			}
			return fixtureResponse(200, `{"success":true,"output":{"total":3,"page_no":2,"models":[{"model":"m2"}]}}`), nil
		}}
		report, err := Review(context.Background(), inventoryFor("m1", "m2"), map[string][]byte{"alibaba": []byte("key")}, Options{AlibabaRegion: "singapore"}, doer, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		pr := providerResult(t, report, "alibaba")
		if pr.Status != "unavailable" || pr.ErrorCode != "pagination_changed_total" {
			t.Fatalf("changed total was not blocked: %#v", pr)
		}
		if findObservation(t, report, "m1").Status != "unknown" {
			t.Fatal("incomplete Alibaba results were retained as evidence")
		}
	})
	t.Run("unsupported region", func(t *testing.T) {
		doer := &fixtureDoer{respond: func(*http.Request) (*http.Response, error) {
			t.Fatal("unsupported region triggered a request")
			return nil, nil
		}}
		report, err := Review(context.Background(), inventoryFor("m"), map[string][]byte{"alibaba": []byte("key")}, Options{AlibabaRegion: "https://attacker.invalid", AlibabaWorkspace: "ws"}, doer, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		pr := providerResult(t, report, "alibaba")
		if pr.Status != "unavailable" || pr.ErrorCode != "unsupported_region" {
			t.Fatalf("unsupported region not fail-closed: %#v", pr)
		}
	})
	t.Run("workspace injection", func(t *testing.T) {
		if _, code := alibabaBase(Options{AlibabaRegion: "beijing", AlibabaWorkspace: "ws.evil.invalid"}); code != "invalid_workspace" {
			t.Fatalf("workspace was not constrained: %q", code)
		}
		if isFixedURL("https://ws.evil.invalid/api/v1/models?page_no=1") {
			t.Fatal("caller-controlled origin passed allowlist")
		}
	})
	t.Run("DeepSeek unexpected pagination marker", func(t *testing.T) {
		doer := &fixtureDoer{respond: func(req *http.Request) (*http.Response, error) {
			return fixtureResponse(200, `{"object":"list","data":[{"id":"deepseek-v4-pro"}],"has_more":false}`), nil
		}}
		report, err := Review(context.Background(), inventoryFor("deepseek-v4-pro"), map[string][]byte{"deepseek": []byte("key")}, Options{}, doer, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		pr := providerResult(t, report, "deepseek")
		if pr.Status != "unavailable" || pr.ErrorCode != "unexpected_pagination" || findObservation(t, report, "deepseek-v4-pro").Status != "unknown" {
			t.Fatalf("unexpected pagination was not fail-closed: provider=%#v observation=%#v", pr, findObservation(t, report, "deepseek-v4-pro"))
		}
	})
}

func TestXAIEndpointFailureKeepsOtherEndpointEvidenceAndSanitizesBody(t *testing.T) {
	const sentinel = "XAI-ERROR-BODY-SECRET"
	doer := &fixtureDoer{respond: func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.x.ai" {
			t.Fatalf("xAI endpoint escaped fixed host: %s", req.URL.Redacted())
		}
		switch req.URL.Path {
		case "/v1/models":
			return fixtureResponse(200, readFixture(t, "xai-models.json")), nil
		case "/v1/image-generation-models":
			return fixtureResponse(200, readFixture(t, "xai-images.json")), nil
		case "/v1/video-generation-models":
			return fixtureResponse(503, sentinel), nil
		default:
			t.Fatalf("unexpected xAI endpoint %s", req.URL.Path)
			return nil, errors.New("unexpected endpoint")
		}
	}}
	report, err := Review(context.Background(), inventoryFor("grok-latest", "grok-imagine-image", "grok-imagine-video"), map[string][]byte{"xai": []byte("test-key")}, Options{}, doer, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	pr := providerResult(t, report, "xai")
	if pr.Status != "partial" || pr.ModelCount == 0 || len(pr.Endpoints) != 3 {
		t.Fatalf("partial xAI response lost its successful endpoints: %#v", pr)
	}
	if findObservation(t, report, "grok-latest").Status != "official_alias_candidate" || findObservation(t, report, "grok-imagine-image").Status != "official_reference_match" || findObservation(t, report, "grok-imagine-video").Status != "unknown" {
		t.Fatalf("xAI endpoint evidence was not isolated: %#v", report.Observations)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), sentinel) || providerResult(t, report, "xai").Endpoints[2].ErrorCode != "http_error" {
		t.Fatalf("xAI endpoint error body leaked or was not sanitized: %s", encoded)
	}
}

func TestNewHTTPClientDisablesProxyAndRejectsRedirects(t *testing.T) {
	client := NewHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatal("official lookup transport is not proxy-free")
	}
	if client.CheckRedirect == nil {
		t.Fatal("redirect handler is missing")
	}
	if err := client.CheckRedirect(&http.Request{URL: &url.URL{Scheme: "https", Host: "attacker.invalid"}}, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy did not reject follow: %v", err)
	}
}

func inventoryFor(ids ...string) Inventory {
	observations := make([]InputModel, 0, len(ids))
	for _, id := range ids {
		observations = append(observations, InputModel{AccountID: 9001, Platform: "openai", AccountType: "apikey", APIProtocol: "openai", ModelID: id, ObservedAs: "discovered_model"})
	}
	return Inventory{Mode: "inspect", Digest: strings.Repeat("a", 64), Observations: observations}
}

func assertObservation(t *testing.T, report Report, id, status, provider, matchKind string) {
	t.Helper()
	observation := findObservation(t, report, id)
	if observation.Status != status {
		t.Fatalf("%s status = %s, want %s: %#v", id, observation.Status, status, observation)
	}
	if provider == "" {
		if len(observation.Matches) != 0 {
			t.Fatalf("%s unexpectedly matched: %#v", id, observation.Matches)
		}
		return
	}
	for _, ref := range observation.Matches {
		if ref.Provider == provider && ref.MatchKind == matchKind {
			return
		}
	}
	t.Fatalf("%s lacks %s/%s reference: %#v", id, provider, matchKind, observation.Matches)
}

func findObservation(t *testing.T, report Report, id string) Observation {
	t.Helper()
	for _, observation := range report.Observations {
		if observation.ModelID == id {
			return observation
		}
	}
	t.Fatalf("observation %q not found", id)
	return Observation{}
}

func providerResult(t *testing.T, report Report, name string) ProviderResult {
	t.Helper()
	for _, provider := range report.ProviderResults {
		if provider.Provider == name {
			return provider
		}
	}
	t.Fatalf("provider result %q not found", name)
	return ProviderResult{}
}
