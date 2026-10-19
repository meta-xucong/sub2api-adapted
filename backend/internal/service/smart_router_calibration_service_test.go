package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSafeSmartRouterProbeErrorOmitsUpstreamBody(t *testing.T) {
	secretEcho := `echoed prompt; Bearer sk-test-sensitive; https://provider.invalid/path?api_key=query-secret`
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "free form error", err: errors.New(secretEcho), want: "upstream_failure"},
		{name: "upstream status", err: &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, ResponseBody: []byte(secretEcho)}, want: "upstream_http_503"},
		{name: "deadline", err: context.DeadlineExceeded, want: string(smartrouter.FailureTimeout)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safeSmartRouterProbeError(tt.err)
			require.Equal(t, tt.want, got)
			require.NotContains(t, got, "echoed prompt")
			require.NotContains(t, got, "sk-test-sensitive")
			require.NotContains(t, got, "query-secret")
		})
	}
}

type smartRouterCalibrationProbeCall struct {
	capability smartrouter.Capability
	model      string
}

type smartRouterCalibrationProbeGatewayStub struct {
	mu    sync.Mutex
	calls []smartRouterCalibrationProbeCall
	err   error
}

func (g *smartRouterCalibrationProbeGatewayStub) record(capability smartrouter.Capability, model string) (int, int64, error) {
	g.mu.Lock()
	g.calls = append(g.calls, smartRouterCalibrationProbeCall{capability: capability, model: model})
	err := g.err
	g.mu.Unlock()
	return http.StatusOK, 12, err
}

func (g *smartRouterCalibrationProbeGatewayStub) RunSmartRouterImageCalibrationProbeForModel(_ context.Context, _ *Account, capability smartrouter.Capability, model string) (int, int64, error) {
	return g.record(capability, model)
}

func (g *smartRouterCalibrationProbeGatewayStub) RunSmartRouterResponsesCalibrationProbe(_ context.Context, _ *Account, model string) (int, int64, error) {
	return g.record(smartrouter.CapabilityResponses, model)
}

func (g *smartRouterCalibrationProbeGatewayStub) RunSmartRouterChatCalibrationProbe(_ context.Context, _ *Account, model string) (int, int64, error) {
	return g.record(smartrouter.CapabilityChat, model)
}

func (g *smartRouterCalibrationProbeGatewayStub) RunSmartRouterEmbeddingCalibrationProbe(_ context.Context, _ *Account, model string) (int, int64, error) {
	return g.record(smartrouter.CapabilityEmbedding, model)
}

func (g *smartRouterCalibrationProbeGatewayStub) RunSmartRouterCompactCalibrationProbe(_ context.Context, _ *Account, model string) (int, int64, error) {
	return g.record(smartrouter.CapabilityResponsesCompact, model)
}

func (g *smartRouterCalibrationProbeGatewayStub) snapshot() []smartRouterCalibrationProbeCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]smartRouterCalibrationProbeCall(nil), g.calls...)
}

type smartRouterCalibrationAccountRepoStub struct {
	AccountRepository
	mu        sync.Mutex
	accounts  []Account
	listCalls int
	updates   []map[string]any
}

func (r *smartRouterCalibrationAccountRepoStub) ListActive(ctx context.Context) ([]Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listCalls++
	return append([]Account(nil), r.accounts...), nil
}

func (r *smartRouterCalibrationAccountRepoStub) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, updates)
	return nil
}

func (r *smartRouterCalibrationAccountRepoStub) snapshot() (int, []map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listCalls, append([]map[string]any(nil), r.updates...)
}

func (r *smartRouterCalibrationAccountRepoStub) setAccounts(accounts []Account) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accounts = append([]Account(nil), accounts...)
}

type smartRouterCalibrationLedgerStub struct {
	SmartRouterHealthLedger
	mu       sync.Mutex
	evidence []SmartRouterCapabilityEvidence
	states   []SmartRouterHealthState
	results  []SmartRouterCalibrationResult
	finished int
	finishOK bool
}

func (l *smartRouterCalibrationLedgerStub) BeginCalibrationRun(_ context.Context, scheduledFor time.Time) (*SmartRouterCalibrationRun, bool, error) {
	return &SmartRouterCalibrationRun{ID: 7, ScheduledFor: scheduledFor}, true, nil
}

func (l *smartRouterCalibrationLedgerStub) RecordCalibrationResult(_ context.Context, _ int64, result SmartRouterCalibrationResult) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.results = append(l.results, result)
	return nil
}

func (l *smartRouterCalibrationLedgerStub) FinishCalibrationRun(_ context.Context, _ int64, success bool, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.finished++
	l.finishOK = success
	return nil
}

func (l *smartRouterCalibrationLedgerStub) ListCapabilityEvidence(context.Context) ([]SmartRouterCapabilityEvidence, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]SmartRouterCapabilityEvidence(nil), l.evidence...), nil
}

func (l *smartRouterCalibrationLedgerStub) LoadStates(context.Context) ([]SmartRouterHealthState, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]SmartRouterHealthState(nil), l.states...), nil
}

func (l *smartRouterCalibrationLedgerStub) resultsSnapshot() ([]SmartRouterCalibrationResult, int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]SmartRouterCalibrationResult(nil), l.results...), l.finished, l.finishOK
}

func smartRouterCalibrationTestAccount(id int64) Account {
	return Account{
		ID:          id,
		Name:        "calibration-test-lane",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "sk-test-only",
			"base_url":      "https://example.com/v1",
			"model_mapping": map[string]any{"gpt-5.5": "gpt-5.5"},
		},
		Extra: map[string]any{
			"smart_router": map[string]any{"capabilities": []any{"responses"}},
		},
	}
}

func TestSmartRouterCalibrationGenerationRequestIsMinimalAndTextOnly(t *testing.T) {
	body, contentType, endpoint, err := smartRouterCalibrationRequest(smartrouter.CapabilityImageGeneration)
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, openAIImagesGenerationsEndpoint, endpoint)

	var request map[string]any
	require.NoError(t, json.Unmarshal(body, &request))
	require.Equal(t, smartRouterCalibrationModel, request["model"])
	require.Equal(t, float64(1), request["n"])
	require.NotContains(t, request, "image")
	require.NotContains(t, request, "input_images")
}

func TestSmartRouterCalibrationEditRequestContainsTinyImageFixture(t *testing.T) {
	body, contentType, endpoint, err := smartRouterCalibrationRequest(smartrouter.CapabilityImageEdit)
	require.NoError(t, err)
	require.Equal(t, openAIImagesEditsEndpoint, endpoint)

	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	reader := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
	form, err := reader.ReadForm(128 * 1024)
	require.NoError(t, err)
	require.Equal(t, []string{smartRouterCalibrationModel}, form.Value["model"])
	require.Equal(t, []string{"1"}, form.Value["n"])
	require.Len(t, form.File["image"], 1)
	require.Equal(t, "smart-router-probe.png", form.File["image"][0].Filename)
}

func TestSmartRouterCalibrationRequestsUsePlannedImageModel(t *testing.T) {
	for _, capability := range []smartrouter.Capability{smartrouter.CapabilityImageGeneration, smartrouter.CapabilityImageEdit} {
		for _, tc := range []struct {
			name  string
			model string
			want  string
		}{
			{name: "exact model", model: "gpt-image-1-mini", want: "gpt-image-1-mini"},
			{name: "trimmed model", model: " gpt-image-1 ", want: "gpt-image-1"},
			{name: "default model", want: smartRouterCalibrationModel},
		} {
			t.Run(string(capability)+"/"+tc.name, func(t *testing.T) {
				body, contentType, endpoint, err := smartRouterCalibrationRequestForModel(capability, tc.model)
				require.NoError(t, err)
				if capability == smartrouter.CapabilityImageGeneration {
					require.Equal(t, openAIImagesGenerationsEndpoint, endpoint)
					require.Equal(t, "application/json", contentType)
					require.Equal(t, tc.want, gjson.GetBytes(body, "model").String())
					return
				}
				require.Equal(t, openAIImagesEditsEndpoint, endpoint)
				_, params, err := mime.ParseMediaType(contentType)
				require.NoError(t, err)
				form, err := multipart.NewReader(strings.NewReader(string(body)), params["boundary"]).ReadForm(128 * 1024)
				require.NoError(t, err)
				defer func() {
					if err := form.RemoveAll(); err != nil {
						t.Errorf("remove multipart form: %v", err)
					}
				}()
				require.Equal(t, []string{tc.want}, form.Value["model"])
				require.Len(t, form.File["image"], 1)
			})
		}
	}
}

func TestSmartRouterImageCalibrationProbeUsesExactModelForRequestAndReport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		capability smartrouter.Capability
		model      string
		endpoint   string
	}{
		{capability: smartrouter.CapabilityImageGeneration, model: "gpt-image-1", endpoint: openAIImagesGenerationsEndpoint},
		{capability: smartrouter.CapabilityImageEdit, model: "gpt-image-1-mini", endpoint: openAIImagesEditsEndpoint},
	} {
		t.Run(string(tc.capability), func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Gateway.SmartRouter.Enabled = true
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"created":1710000007,"data":[{"b64_json":"aGVsbG8="}]}`)),
			}}
			ledger := &smartRouterRuntimeTestLedger{}
			gateway := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, smartRouterHealthLedger: ledger}
			service := NewSmartRouterCalibrationService(nil, gateway, nil, cfg)
			account := smartRouterCalibrationTestAccount(730080)
			account.Credentials["model_mapping"] = map[string]any{tc.model: tc.model}
			account.Extra[smartRouterExtraKey] = map[string]any{"capabilities": []any{string(tc.capability)}}

			result := service.runProbe(context.Background(), &account, smartrouter.CalibrationProbe{
				LaneID: "account:730080", Capability: tc.capability, Model: tc.model, Reason: "model_recovery",
			})

			require.True(t, result.Success, result.ErrorSummary)
			require.Equal(t, http.StatusOK, result.StatusCode)
			require.Equal(t, tc.model, result.ModelFamily)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, tc.endpoint, upstream.lastReq.URL.Path)
			if tc.capability == smartrouter.CapabilityImageGeneration {
				require.Equal(t, tc.model, gjson.GetBytes(upstream.lastBody, "model").String())
			} else {
				_, params, err := mime.ParseMediaType(upstream.lastReq.Header.Get("Content-Type"))
				require.NoError(t, err)
				form, err := multipart.NewReader(strings.NewReader(string(upstream.lastBody)), params["boundary"]).ReadForm(128 * 1024)
				require.NoError(t, err)
				defer func() {
					if err := form.RemoveAll(); err != nil {
						t.Errorf("remove multipart form: %v", err)
					}
				}()
				require.Equal(t, []string{tc.model}, form.Value["model"])
			}
			require.Len(t, ledger.events, 1)
			require.Equal(t, "calibration", ledger.events[0].Source)
			require.Equal(t, tc.capability, ledger.events[0].Key.Capability)
			require.Equal(t, tc.model, ledger.events[0].Key.Model)
			require.True(t, ledger.events[0].Success)
		})
	}
}

func TestSmartRouterCalibrationKeepsCustomImageLanesAccountScoped(t *testing.T) {
	service := &SmartRouterCalibrationService{}
	accounts := []Account{smartRouterCalibrationTestAccount(730081), smartRouterCalibrationTestAccount(730082)}
	for index := range accounts {
		accounts[index].Credentials["model_mapping"] = map[string]any{"gpt-5.5": "gpt-5.5", "gpt-image-1": "gpt-image-1"}
		accounts[index].Extra[smartRouterExtraKey] = map[string]any{
			"lane_id": "shared", "capabilities": []any{"responses", "image_generation", "image_edit"},
		}
	}
	imageLanes, imageAccounts := service.imageCalibrationLanes(accounts)
	require.Len(t, imageLanes, 2)
	require.Len(t, imageAccounts, 2)
	require.Equal(t, "account:730081/image/shared", imageLanes[0].LaneID)
	require.Equal(t, "account:730082/image/shared", imageLanes[1].LaneID)
	for index, lane := range imageLanes {
		require.Equal(t, accounts[index].ID, imageAccounts[lane.LaneID].ID)
		require.True(t, lane.Capabilities[smartrouter.CapabilityImageGeneration])
		require.True(t, lane.Capabilities[smartrouter.CapabilityImageEdit])
		require.False(t, lane.Capabilities[smartrouter.CapabilityResponses])
		require.False(t, lane.Capabilities[smartrouter.CapabilityResponsesCompact])
		for _, probe := range smartRouterAutoEnrollmentProbes(&accounts[index]) {
			if probe.Capability == smartrouter.CapabilityImageGeneration || probe.Capability == smartrouter.CapabilityImageEdit {
				require.Equal(t, lane.LaneID, probe.LaneID)
			} else {
				require.Equal(t, "shared", probe.LaneID)
			}
		}
	}
	ordinaryLanes, _ := service.ordinaryCalibrationLanes(accounts)
	compactLanes, _ := service.compactCalibrationLanes(accounts)
	for _, lane := range append(ordinaryLanes, compactLanes...) {
		require.Equal(t, "shared", lane.LaneID)
		require.False(t, lane.Capabilities[smartrouter.CapabilityImageGeneration])
		require.False(t, lane.Capabilities[smartrouter.CapabilityImageEdit])
	}
}

func TestSmartRouterDailyImageCalibrationResolvesDuplicateCustomLanesByAccount(t *testing.T) {
	accounts := []Account{smartRouterCalibrationTestAccount(730081), smartRouterCalibrationTestAccount(730082)}
	for index := range accounts {
		accounts[index].Credentials["model_mapping"] = map[string]any{"gpt-5.5": "gpt-5.5", "gpt-image-1": "gpt-image-1"}
		accounts[index].Extra["openai_compact_supported"] = false
		accounts[index].Extra[smartRouterExtraKey] = map[string]any{
			"lane_id": "shared", "capabilities": []any{"responses", "image_generation", "image_edit"},
		}
	}
	// A nonimage custom ID may even match another account's qualified image
	// ID. Its account entry must not take over the image calibration probe.
	textAccount := smartRouterCalibrationTestAccount(730083)
	textAccount.Extra["openai_compact_supported"] = false
	textAccount.Extra[smartRouterExtraKey] = map[string]any{
		"lane_id": "account:730081/image/shared", "capabilities": []any{"responses"},
	}
	accounts = append(accounts, textAccount)
	now := time.Now()
	ledger := &smartRouterCalibrationLedgerStub{evidence: []SmartRouterCapabilityEvidence{{
		LaneID: "shared", GenerationKnown: true, EditKnown: true,
		GenerationLastSuccess: now, EditLastSuccess: now,
	}}}
	for _, account := range accounts[:2] {
		for _, capability := range []smartrouter.Capability{smartrouter.CapabilityImageGeneration, smartrouter.CapabilityImageEdit} {
			ledger.states = append(ledger.states, SmartRouterHealthState{
				LaneID: "shared", AccountID: account.ID, Capability: capability, ModelFamily: "gpt-image-1",
				Snapshot: smartrouter.HealthSnapshot{RecoveryStage: smartrouter.RecoveryCooling},
			})
		}
	}
	cfg := &config.Config{}
	service := &SmartRouterCalibrationService{
		cfg: cfg, gateway: &OpenAIGatewayService{cfg: cfg}, probeGateway: &smartRouterCalibrationProbeGatewayStub{},
		accountRepo: &smartRouterCalibrationAccountRepoStub{accounts: accounts}, ledger: ledger,
	}
	service.runCalibration()

	results, finished, success := ledger.resultsSnapshot()
	require.Equal(t, 1, finished)
	require.True(t, success)
	imageResults := make(map[int64][]SmartRouterCalibrationResult)
	for _, result := range results {
		if result.Capability == smartrouter.CapabilityImageGeneration || result.Capability == smartrouter.CapabilityImageEdit {
			imageResults[result.AccountID] = append(imageResults[result.AccountID], result)
		}
	}
	require.Len(t, imageResults, 2)
	for _, account := range accounts[:2] {
		accountResults := imageResults[account.ID]
		require.Len(t, accountResults, 4, "each account needs its own baseline and exact-model recovery probes")
		modelsByCapability := make(map[smartrouter.Capability][]string)
		for _, result := range accountResults {
			require.Equal(t, smartRouterImageLaneID(account.ID, "shared"), result.LaneID)
			modelsByCapability[result.Capability] = append(modelsByCapability[result.Capability], result.ModelFamily)
		}
		for _, capability := range []smartrouter.Capability{smartrouter.CapabilityImageGeneration, smartrouter.CapabilityImageEdit} {
			require.ElementsMatch(t, []string{smartRouterCalibrationModel, "gpt-image-1"}, modelsByCapability[capability])
		}
	}
}

func TestSmartRouterImageRecoveryProbeRequiresMatchingPersistedAccount(t *testing.T) {
	account := smartRouterCalibrationTestAccount(730081)
	account.Credentials["model_mapping"] = map[string]any{"gpt-image-1": "gpt-image-1"}
	account.Extra[smartRouterExtraKey] = map[string]any{"capabilities": []any{"image_edit"}}
	state := SmartRouterHealthState{
		LaneID: "shared", AccountID: account.ID, Capability: smartrouter.CapabilityImageEdit, ModelFamily: "gpt-image-1",
		Snapshot: smartrouter.HealthSnapshot{RecoveryStage: smartrouter.RecoveryCooling},
	}
	imageAccounts := map[string]*Account{"account:730081/image/shared": &account}
	probes := ensureSmartRouterModelRecoveryProbes(nil, []SmartRouterHealthState{state}, nil, imageAccounts)
	require.Equal(t, []smartrouter.CalibrationProbe{{
		LaneID: "account:730081/image/shared", Capability: state.Capability, Model: "gpt-image-1", Reason: "model_recovery_due",
	}}, probes)
	state.LaneID = probes[0].LaneID
	require.Equal(t, probes, ensureSmartRouterModelRecoveryProbes(nil, []SmartRouterHealthState{state}, nil, imageAccounts), "qualified persisted IDs must be idempotent")
	state.AccountID = 0
	require.Empty(t, ensureSmartRouterModelRecoveryProbes(nil, []SmartRouterHealthState{state}, nil, imageAccounts))
	state.AccountID = 730082
	require.Empty(t, ensureSmartRouterModelRecoveryProbes(nil, []SmartRouterHealthState{state}, nil, imageAccounts))
}

func TestSmartRouterDailyCalibrationRecoversExistingNonGPTImageState(t *testing.T) {
	for _, tc := range []struct {
		name       string
		capability smartrouter.Capability
		allowEdit  bool
		disabled   bool
		wantProbe  bool
	}{
		{name: "generation", capability: smartrouter.CapabilityImageGeneration, wantProbe: true},
		{name: "edit", capability: smartrouter.CapabilityImageEdit, allowEdit: true, wantProbe: true},
		{name: "old edit after opt out", capability: smartrouter.CapabilityImageEdit},
		{name: "disabled lane", capability: smartrouter.CapabilityImageGeneration, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const model = "gemini-2.5-flash-image"
			account := smartRouterCalibrationTestAccount(730084)
			account.Credentials["model_mapping"] = map[string]any{model: model}
			capabilities := []any{"image_generation"}
			if tc.allowEdit {
				capabilities = append(capabilities, "image_edit")
			}
			account.Extra[smartRouterExtraKey] = map[string]any{
				"lane_id": "gemini-only", "enabled": !tc.disabled, "capabilities": capabilities,
			}
			require.Empty(t, filterSmartRouterAccountsByModel([]Account{account}), "representative discovery must not broaden")
			require.False(t, smartRouterAutoEnrollmentEligible(&account), "ordinary auto-enrollment remains unchanged")
			cfg := &config.Config{}
			cfg.Gateway.SmartRouter.Enabled = true
			ledger := &smartRouterCalibrationLedgerStub{}
			runner := &smartRouterCalibrationProbeGatewayStub{}
			service := &SmartRouterCalibrationService{
				cfg: cfg, gateway: &OpenAIGatewayService{cfg: cfg}, probeGateway: runner,
				accountRepo: &smartRouterCalibrationAccountRepoStub{accounts: []Account{account}}, ledger: ledger,
			}
			service.runCalibration()
			require.Empty(t, runner.snapshot(), "an image account without existing recovery evidence must not gain a paid probe")
			ledger.states = []SmartRouterHealthState{{
				LaneID: "gemini-only", AccountID: account.ID, Capability: tc.capability, ModelFamily: model,
				Snapshot: smartrouter.HealthSnapshot{RecoveryStage: smartrouter.RecoveryCooling},
			}}
			service.runCalibration()
			results, _, success := ledger.resultsSnapshot()
			require.True(t, success)
			if !tc.wantProbe {
				require.Empty(t, runner.snapshot())
				require.Empty(t, results)
				return
			}
			require.Equal(t, []smartRouterCalibrationProbeCall{{capability: tc.capability, model: model}}, runner.snapshot())
			require.Len(t, results, 1)
			require.Equal(t, account.ID, results[0].AccountID)
			require.Equal(t, "account:730084/image/gemini-only", results[0].LaneID)
			require.Equal(t, model, results[0].ModelFamily)
			require.Equal(t, "model_recovery_due", results[0].Reason)
		})
	}
}

func TestSmartRouterImageRecoveryCanonicalStateSupersedesLegacyCooling(t *testing.T) {
	account := smartRouterCalibrationTestAccount(730085)
	account.Credentials["model_mapping"] = map[string]any{"gpt-image-1": "gpt-image-1"}
	account.Extra[smartRouterExtraKey] = map[string]any{"capabilities": []any{"image_generation"}}
	legacy := SmartRouterHealthState{
		LaneID: "shared", AccountID: account.ID, Capability: smartrouter.CapabilityImageGeneration, ModelFamily: "gpt-image-1",
		Snapshot: smartrouter.HealthSnapshot{RecoveryStage: smartrouter.RecoveryCooling},
	}
	canonical := legacy
	canonical.LaneID = "account:730085/image/shared"
	canonical.Snapshot.RecoveryStage = smartrouter.RecoveryNormal
	imageAccounts := map[string]*Account{canonical.LaneID: &account}
	for _, states := range [][]SmartRouterHealthState{{legacy, canonical}, {canonical, legacy}} {
		require.Empty(t, ensureSmartRouterModelRecoveryProbes(nil, states, nil, imageAccounts), "a recovered canonical row must not trigger another legacy recovery probe")
	}
}

func TestSmartRouterCompactProbeUsesOfficialCompactionItemRecognition(t *testing.T) {
	require.True(t, openAICompactProbeFoundCompactionItem([]byte(`{"output":[{"type":"compaction"}]}`)))
	require.True(t, openAICompactProbeFoundCompactionItem([]byte(`{"output":[{"type":"compaction_summary"}]}`)))
	require.True(t, openAICompactProbeFoundCompactionItem([]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"compaction\"}}\n\n")))
	require.False(t, openAICompactProbeFoundCompactionItem([]byte(`{"output":[{"type":"message"}]}`)))
}

func TestRunSmartRouterCompactCalibrationProbeForcesHTTPTransport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_probe\",\"status\":\"in_progress\",\"output\":[]}}\n\n" +
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"cmp_probe\",\"type\":\"compaction\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_probe\",\"status\":\"completed\",\"output\":[{\"id\":\"cmp_probe\",\"type\":\"compaction\"}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
				"data: [DONE]\n\n",
		)),
	}}
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
	account := &Account{
		ID:          730060,
		Name:        "ws-enabled-compact-lane",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test-only",
			"base_url": "https://example.com/v1",
		},
		Extra: map[string]any{
			"use_responses_api":                             true,
			"openai_apikey_responses_websockets_v2_enabled": true,
		},
	}

	statusCode, _, err := svc.RunSmartRouterCompactCalibrationProbe(context.Background(), account, "gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, statusCode)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
	require.Equal(t, "text/event-stream", upstream.requests[0].Header.Get("Accept"))
	require.Equal(t, true, gjson.GetBytes(upstream.bodies[0], "stream").Bool())
	require.Equal(t, "compaction_trigger", gjson.GetBytes(upstream.bodies[0], "input.1.type").String())
	require.Contains(t, upstream.requests[0].Header.Get("x-codex-beta-features"), "remote_compaction_v2")
}

func TestBuildSmartRouterCompactProbeExtraUpdatesDoesNotQuarantineTransientFailure(t *testing.T) {
	now := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	transient := buildSmartRouterCompactProbeExtraUpdates(SmartRouterCalibrationResult{
		StatusCode:   http.StatusServiceUnavailable,
		ErrorSummary: "service temporarily unavailable",
	}, now)
	require.NotContains(t, transient, "openai_compact_supported")

	unsupported := buildSmartRouterCompactProbeExtraUpdates(SmartRouterCalibrationResult{
		StatusCode:   http.StatusNotFound,
		ErrorSummary: "compact endpoint not found",
	}, now)
	require.NotContains(t, unsupported, "openai_compact_supported")

	success := buildSmartRouterCompactProbeExtraUpdates(SmartRouterCalibrationResult{Success: true, StatusCode: http.StatusOK}, now)
	require.NotContains(t, success, "openai_compact_supported", "Smart Router calibration must not change official compact eligibility")
	require.Equal(t, http.StatusOK, success["openai_compact_last_status"])
	require.Equal(t, "", success["openai_compact_last_error"])
}

func TestSmartRouterCompactProbeModelsExpandGPT5Variants(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.6-*":     "gpt-5.6-*",
				"gpt-5.5":       "gpt-5.5",
				"gpt-5.4":       "gpt-5.4",
				"gpt-image-2":   "gpt-image-2",
				"claude-sonnet": "claude-sonnet",
			},
		},
	}

	require.Equal(t, []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5", "gpt-5.4"}, smartRouterCompactProbeModelsForAccount(account, "gpt-5.4"))
}

func TestSmartRouterCompactProbeModelsUseDefaultsExactMappingsAndFallback(t *testing.T) {
	noMappings := &Account{Credentials: map[string]any{}}
	require.Equal(t,
		[]string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5", "gpt-5.4", "gpt-5.7"},
		smartRouterCompactProbeModelsForAccount(noMappings, "gpt-5.7"),
	)
	require.Equal(t,
		[]string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5", "gpt-5.4"},
		smartRouterCompactProbeModelsForAccount(noMappings, "gpt-5.4"),
	)

	account := &Account{Credentials: map[string]any{
		"model_mapping": map[string]any{
			"gpt-5.6-*":   "gpt-5.6-*",
			"gpt-5.5":     "gpt-5.5",
			"gpt-5.3":     "gpt-5.3",
			"gpt-image-2": "gpt-image-2",
			"claude-3":    "claude-3",
		},
		"compact_model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"},
	}}
	require.Equal(t,
		[]string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5", "gpt-5.4", "gpt-5.3"},
		smartRouterCompactProbeModelsForAccount(account, "gpt-5.7"),
	)

	noMatch := &Account{Credentials: map[string]any{"model_mapping": map[string]any{"claude-3": "claude-3"}}}
	require.Equal(t, []string{"gpt-5.7"}, smartRouterCompactProbeModelsForAccount(noMatch, "gpt-5.7"))
}

func TestEnsureCompactModelCalibrationProbesAddsExactModels(t *testing.T) {
	service := &SmartRouterCalibrationService{}
	account := &Account{
		ID:       730054,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.6-terra": "gpt-5.6-terra",
				"gpt-5.5":       "gpt-5.5",
			},
		},
	}
	lanes := []smartrouter.LaneSnapshot{{
		LaneID:    "account:730054",
		AccountID: 730054,
		Capabilities: map[smartrouter.Capability]bool{
			smartrouter.CapabilityResponsesCompact: true,
		},
	}}

	probes := service.ensureCompactModelCalibrationProbes(nil, lanes, map[string]*Account{"account:730054": account})
	require.ElementsMatch(t, []smartrouter.CalibrationProbe{
		{LaneID: "account:730054", Capability: smartrouter.CapabilityResponsesCompact, Model: "gpt-5.6-terra", Reason: "scheduled_model_compact_probe"},
		{LaneID: "account:730054", Capability: smartrouter.CapabilityResponsesCompact, Model: "gpt-5.5", Reason: "scheduled_model_compact_probe"},
	}, probes)
}

func TestCompactCalibrationLanesIncludeTextAndSkipImageOnlyAccounts(t *testing.T) {
	service := &SmartRouterCalibrationService{}
	accounts := []Account{
		{
			ID:       730052,
			Name:     "image-only-lane",
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"},
			},
			Extra: map[string]any{
				"smart_router": map[string]any{"capabilities": []any{"image_generation"}},
			},
		},
		{
			ID:       730053,
			Name:     "legacy-text-lane",
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"gpt-5.6-terra": "gpt-5.6-terra"},
			},
			Extra: map[string]any{
				"smart_router": map[string]any{"capabilities": []any{"chat", "responses"}},
			},
		},
	}

	lanes, accountsByLane := service.compactCalibrationLanes(accounts)
	require.Len(t, lanes, 1)
	require.Equal(t, int64(730053), lanes[0].AccountID)
	require.Equal(t, int64(730053), accountsByLane[lanes[0].LaneID].ID)
}

func TestSmartRouterCalibrationScheduleUsesShanghaiTime(t *testing.T) {
	utcNow := time.Date(2026, 7, 11, 20, 5, 0, 0, time.UTC)
	scheduledFor := smartRouterScheduledFor(utcNow, 4, 0)
	require.Equal(t, time.Date(2026, 7, 11, 20, 0, 0, 0, time.UTC), scheduledFor)

	service := &SmartRouterCalibrationService{}
	require.Equal(t, "0 4 * * *", service.smartRouterCalibrationCronExpression())
}

func TestNextSmartRouterCalibrationTimeUsesNextShanghaiBoundary(t *testing.T) {
	beforeBoundary := time.Date(2026, 7, 11, 19, 59, 0, 0, time.UTC)
	require.Equal(t, time.Date(2026, 7, 11, 20, 0, 0, 0, time.UTC), nextSmartRouterCalibrationTime(beforeBoundary).UTC())

	afterBoundary := time.Date(2026, 7, 11, 20, 1, 0, 0, time.UTC)
	require.Equal(t, time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC), nextSmartRouterCalibrationTime(afterBoundary).UTC())
}

func TestSmartRouterAutoEnrollmentKeepsT0CapabilityAndCompactExpansion(t *testing.T) {
	account := smartRouterCalibrationTestAccount(730070)
	account.Credentials["model_mapping"] = map[string]any{
		"gpt-5.6-*":   "gpt-5.6-*",
		"gpt-5.5":     "gpt-5.5",
		"gpt-image-2": "gpt-image-2",
	}
	account.Extra["smart_router"] = map[string]any{"capabilities": []any{
		"responses", "chat", "embedding", "image_generation", "image_edit",
	}}

	probes := smartRouterAutoEnrollmentProbes(&account)
	require.Equal(t, []smartrouter.Capability{
		smartrouter.CapabilityResponses,
		smartrouter.CapabilityChat,
		smartrouter.CapabilityEmbedding,
		smartrouter.CapabilityImageGeneration,
		smartrouter.CapabilityImageEdit,
		smartrouter.CapabilityResponsesCompact,
		smartrouter.CapabilityResponsesCompact,
		smartrouter.CapabilityResponsesCompact,
		smartrouter.CapabilityResponsesCompact,
	}, func() []smartrouter.Capability {
		capabilities := make([]smartrouter.Capability, len(probes))
		for i := range probes {
			capabilities[i] = probes[i].Capability
		}
		return capabilities
	}())
	require.Equal(t, []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"}, []string{
		probes[5].Model, probes[6].Model, probes[7].Model, probes[8].Model,
	})
	require.NotContains(t, []smartrouter.Capability{
		probes[0].Capability, probes[1].Capability, probes[2].Capability,
	}, smartrouter.CapabilityImageGeneration)
}

func TestSmartRouterRunProbeUsesMockGatewayForEachCapability(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Calibration.ProbeTimeoutSeconds = 3
	cfg.Gateway.OpenAICompactModel = "gpt-5.4"
	runner := &smartRouterCalibrationProbeGatewayStub{}
	service := &SmartRouterCalibrationService{cfg: cfg, probeGateway: runner}
	account := smartRouterCalibrationTestAccount(730071)
	probes := []smartrouter.CalibrationProbe{
		{LaneID: "lane", Capability: smartrouter.CapabilityResponses, Model: "gpt-5.5"},
		{LaneID: "lane", Capability: smartrouter.CapabilityChat, Model: "gpt-5.5"},
		{LaneID: "lane", Capability: smartrouter.CapabilityEmbedding, Model: "text-embedding-3-small"},
		{LaneID: "lane", Capability: smartrouter.CapabilityResponsesCompact, Model: "gpt-5.6-sol"},
		{LaneID: "lane", Capability: smartrouter.CapabilityImageGeneration},
		{LaneID: "lane", Capability: smartrouter.CapabilityImageEdit},
	}
	for _, probe := range probes {
		result := service.runProbe(context.Background(), &account, probe)
		require.True(t, result.Success)
		require.Equal(t, http.StatusOK, result.StatusCode)
	}

	calls := runner.snapshot()
	require.Len(t, calls, len(probes))
	for i, probe := range probes {
		require.Equal(t, probe.Capability, calls[i].capability)
	}
	require.Equal(t, "gpt-5.6-sol", calls[3].model)
	require.Equal(t, smartRouterCalibrationModel, calls[4].model)
	require.Equal(t, smartRouterCalibrationModel, calls[5].model)
}

func TestSmartRouterDailyCalibrationAddsCompactSupplementDespiteFreshEvidence(t *testing.T) {
	account := smartRouterCalibrationTestAccount(730072)
	account.Credentials["model_mapping"] = map[string]any{"gpt-5.6-sol": "gpt-5.6-sol"}
	now := time.Now().UTC()
	repo := &smartRouterCalibrationAccountRepoStub{accounts: []Account{account}}
	ledger := &smartRouterCalibrationLedgerStub{
		evidence: []SmartRouterCapabilityEvidence{{
			LaneID:               "account:730072",
			ResponsesKnown:       true,
			ResponsesLastSuccess: now,
			CompactKnown:         true,
			CompactLastSuccess:   now,
		}},
	}
	runner := &smartRouterCalibrationProbeGatewayStub{}
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Calibration.Hour = 4
	cfg.Gateway.SmartRouter.Calibration.Minute = 0
	cfg.Gateway.OpenAICompactModel = "gpt-5.4"
	service := &SmartRouterCalibrationService{
		accountRepo:  repo,
		gateway:      &OpenAIGatewayService{cfg: cfg},
		probeGateway: runner,
		ledger:       ledger,
		cfg:          cfg,
	}

	service.runCalibration()

	calls := runner.snapshot()
	require.Equal(t, []smartRouterCalibrationProbeCall{{capability: smartrouter.CapabilityResponsesCompact, model: "gpt-5.6-sol"}}, calls)
	results, finished, success := ledger.resultsSnapshot()
	require.Equal(t, 1, finished)
	require.True(t, success)
	require.Len(t, results, 1)
	require.Equal(t, "scheduled_model_compact_probe", results[0].Reason)
	require.Equal(t, "gpt-5.6-sol", results[0].ModelFamily)
}

func TestSmartRouterAutoEnrollmentProbesOnlyNewOrChangedFingerprints(t *testing.T) {
	account := smartRouterCalibrationTestAccount(730073)
	account.Extra["openai_compact_supported"] = false
	repo := &smartRouterCalibrationAccountRepoStub{accounts: []Account{account}}
	runner := &smartRouterCalibrationProbeGatewayStub{}
	cfg := &config.Config{}
	service := &SmartRouterCalibrationService{
		accountRepo:  repo,
		gateway:      &OpenAIGatewayService{cfg: cfg},
		probeGateway: runner,
		cfg:          cfg,
		enrollSeen:   make(map[int64]string),
	}

	service.runAutoEnrollment()
	require.Len(t, runner.snapshot(), 1)
	service.runAutoEnrollment()
	require.Len(t, runner.snapshot(), 1)

	account.Credentials["model_mapping"] = map[string]any{"gpt-5.5": "gpt-5.5", "gpt-5.4": "gpt-5.4"}
	repo.setAccounts([]Account{account})
	service.runAutoEnrollment()
	require.Len(t, runner.snapshot(), 2)
	_, updates := repo.snapshot()
	require.Len(t, updates, 2)
	status, ok := updates[1]["smart_router_enrollment"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ready", status["status"])
}

func TestSmartRouterAutoEnrollmentRechecksEligibilityAndLaneChanges(t *testing.T) {
	t.Run("schedulability restored", func(t *testing.T) {
		account := smartRouterCalibrationTestAccount(730074)
		account.Schedulable = false
		repo := &smartRouterCalibrationAccountRepoStub{accounts: []Account{account}}
		runner := &smartRouterCalibrationProbeGatewayStub{}
		cfg := &config.Config{}
		service := &SmartRouterCalibrationService{
			accountRepo:  repo,
			gateway:      &OpenAIGatewayService{cfg: cfg},
			probeGateway: runner,
			cfg:          cfg,
			enrollSeen:   make(map[int64]string),
		}

		service.runAutoEnrollment()
		require.Empty(t, runner.snapshot(), "an unschedulable account must not be probed")

		account.Schedulable = true
		repo.setAccounts([]Account{account})
		service.runAutoEnrollment()
		require.Len(t, runner.snapshot(), 2, "the eligibility transition must trigger enrollment")
	})

	t.Run("Smart Router switch and lane identity change", func(t *testing.T) {
		account := smartRouterCalibrationTestAccount(730075)
		extra, ok := account.Extra[smartRouterExtraKey].(map[string]any)
		require.True(t, ok)
		extra["enabled"] = false
		extra["lane_id"] = "lane-before"
		repo := &smartRouterCalibrationAccountRepoStub{accounts: []Account{account}}
		runner := &smartRouterCalibrationProbeGatewayStub{}
		cfg := &config.Config{}
		service := &SmartRouterCalibrationService{
			accountRepo:  repo,
			gateway:      &OpenAIGatewayService{cfg: cfg},
			probeGateway: runner,
			cfg:          cfg,
			enrollSeen:   make(map[int64]string),
		}

		service.runAutoEnrollment()
		require.Empty(t, runner.snapshot(), "a disabled Smart Router lane must not be probed")

		extra["enabled"] = true
		repo.setAccounts([]Account{account})
		service.runAutoEnrollment()
		require.Len(t, runner.snapshot(), 2, "enabling the Smart Router lane must trigger enrollment")

		extra["lane_id"] = "lane-after"
		repo.setAccounts([]Account{account})
		service.runAutoEnrollment()
		require.Len(t, runner.snapshot(), 4, "a changed lane identity must trigger enrollment")
	})
}

func TestSmartRouterCalibrationMasterOffDoesNotScanOrProbe(t *testing.T) {
	repo := &smartRouterCalibrationAccountRepoStub{}
	runner := &smartRouterCalibrationProbeGatewayStub{}
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = false
	cfg.Gateway.SmartRouter.Calibration.Enabled = true
	cfg.Gateway.SmartRouter.Calibration.AutoEnrollEnabled = true
	service := NewSmartRouterCalibrationService(repo, &OpenAIGatewayService{cfg: cfg}, &smartRouterCalibrationLedgerStub{}, cfg)
	service.probeGateway = runner

	service.Start()
	service.Stop()

	listCalls, _ := repo.snapshot()
	require.Zero(t, listCalls)
	require.Empty(t, runner.snapshot())
}

func TestSmartRouterAutoEnrollmentStopCancelsReconciliationLoop(t *testing.T) {
	repo := &smartRouterCalibrationAccountRepoStub{}
	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = true
	cfg.Gateway.SmartRouter.Calibration.Enabled = true
	cfg.Gateway.SmartRouter.Calibration.AutoEnrollEnabled = true
	cfg.Gateway.SmartRouter.Calibration.AutoEnrollIntervalSeconds = 1
	service := NewSmartRouterCalibrationService(repo, &OpenAIGatewayService{cfg: cfg}, &smartRouterCalibrationLedgerStub{}, cfg)
	service.Start()
	service.Stop()
	time.Sleep(1100 * time.Millisecond)

	listCalls, _ := repo.snapshot()
	require.Equal(t, 1, listCalls)
}
