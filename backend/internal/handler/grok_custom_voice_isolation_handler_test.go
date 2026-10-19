//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type grokVoiceHandlerRepo struct {
	grokMediaSlotRepo
	voices               map[string]service.GrokCustomVoice
	failRead             bool
	tempUnschedulableIDs []int64
}

func (r *grokVoiceHandlerRepo) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.tempUnschedulableIDs = append(r.tempUnschedulableIDs, id)
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].TempUnschedulableUntil = &until
			r.accounts[i].TempUnschedulableReason = reason
			return nil
		}
	}
	return errors.New("account not found")
}

func (r *grokVoiceHandlerRepo) CreateGrokCustomVoice(ctx context.Context, v *service.GrokCustomVoice) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, exists := r.voices[v.VoiceID]; exists {
		return errors.New("collision")
	}
	r.voices[v.VoiceID] = *v
	return nil
}
func (r *grokVoiceHandlerRepo) GetGrokCustomVoice(ctx context.Context, id string) (*service.GrokCustomVoice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.failRead {
		return nil, errors.New("private database detail")
	}
	v, ok := r.voices[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (r *grokVoiceHandlerRepo) ListGrokCustomVoices(context.Context, int64, int64, string, int) ([]service.GrokCustomVoice, error) {
	return nil, nil
}
func (r *grokVoiceHandlerRepo) DeleteGrokCustomVoice(_ context.Context, id string, user int64) error {
	if r.voices[id].UserID != user {
		return errors.New("not owned")
	}
	delete(r.voices, id)
	return nil
}
func (r *grokVoiceHandlerRepo) UpdateGrokCustomVoice(_ context.Context, v *service.GrokCustomVoice) error {
	if r.voices[v.VoiceID].UserID != v.UserID {
		return errors.New("not owned")
	}
	r.voices[v.VoiceID] = *v
	return nil
}

func newGrokVoiceIsolationHandler(t *testing.T) (*OpenAIGatewayHandler, *grokVoiceHandlerRepo, *grokMediaSlotsCache, *grokMediaSlotUpstream) {
	t.Helper()
	accounts := make([]service.Account, 3)
	for i := range accounts {
		accounts[i] = service.Account{ID: int64(i + 1), Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 50, Priority: i, GroupIDs: []int64{24}, Credentials: map[string]any{"api_key": "voice-test-key"}}
	}
	repo := &grokVoiceHandlerRepo{grokMediaSlotRepo: grokMediaSlotRepo{openAIImagesFailoverAccountRepo: openAIImagesFailoverAccountRepo{accounts: accounts}}, voices: map[string]service.GrokCustomVoice{
		"mine":    {VoiceID: "mine", UserID: 10, AccountID: 2, Profile: json.RawMessage(`{"voice_id":"mine"}`)},
		"foreign": {VoiceID: "foreign", UserID: 11, AccountID: 2, Profile: json.RawMessage(`{"voice_id":"foreign"}`)},
	}}
	slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
	concurrency := service.NewConcurrencyService(slots)
	upstream := &grokMediaSlotUpstream{call: func(*http.Request, int64) (*http.Response, error) {
		return voiceHandlerResponse(200, `{"voice_id":"mine"}`), nil
	}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.Scheduling.StickySessionWaitTimeout = 20 * time.Millisecond
	cfg.Gateway.Scheduling.StickySessionMaxWaiting = 3
	bindings := &grokMediaSlotBindings{}
	provider := service.NewGrokTokenProvider(repo, nil)
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, bindings, cfg, nil, concurrency, nil, nil, nil, upstream, nil, nil, provider, nil, nil, nil, nil, nil)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	return h, repo, slots, upstream
}
func voiceHandlerResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func grokVoiceIsolationContext(method, endpoint, body string) (*gin.Context, *httptest.ResponseRecorder) {
	c, w := grokMediaSlotContext(context.Background(), false)
	c.Request = httptest.NewRequest(method, "/v1/"+endpoint, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

func TestGrokVoiceHandlerPinsOwnedResourceAndNoFailover(t *testing.T) {
	for _, tc := range []struct {
		name, method, endpoint, body string
		status                       int
	}{
		{"read owned", http.MethodGet, "custom-voices/mine", "", 200},
		{"read owned upstream failure", http.MethodGet, "custom-voices/mine", "", 502},
		{"tts owned upstream failure", http.MethodPost, "tts", `{"voice_id":"mine","text":"hello"}`, 502},
		{"create upstream failure", http.MethodPost, "custom-voices", `{}`, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, repo, slots, upstream := newGrokVoiceIsolationHandler(t)
			var forwardedAccountID int64
			upstream.call = func(req *http.Request, id int64) (*http.Response, error) {
				forwardedAccountID = id
				if tc.endpoint != "custom-voices" {
					require.Equal(t, int64(2), id, "owned operations must use the recorded account")
				}
				require.Equal(t, "/v1/"+tc.endpoint, req.URL.Path)
				if tc.status != 200 {
					return voiceHandlerResponse(tc.status, `{"error":{"message":"temporarily unavailable"}}`), nil
				}
				return voiceHandlerResponse(200, `{"voice_id":"mine"}`), nil
			}
			c, w := grokVoiceIsolationContext(tc.method, tc.endpoint, tc.body)
			h.GrokVoice(c, tc.endpoint)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, 1, upstream.calls, "custom creation and pinned operations must not replay on another account")
			if tc.status == http.StatusOK {
				require.Empty(t, repo.tempUnschedulableIDs)
			} else {
				require.Equal(t, []int64{forwardedAccountID}, repo.tempUnschedulableIDs, "provider failure must retain the existing account cooldown")
				account, err := repo.GetByID(context.Background(), forwardedAccountID)
				require.NoError(t, err)
				require.NotNil(t, account.TempUnschedulableUntil)
				require.True(t, account.TempUnschedulableUntil.After(time.Now()))
				require.Equal(t, "grok upstream temporary error", account.TempUnschedulableReason)
			}
			slots.assertReleased(t)
		})
	}
}

func TestGrokVoiceHandlerRejectsBeforeUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, method, endpoint, body string
		status                       int
	}{
		{"foreign", http.MethodGet, "custom-voices/foreign", "", 404},
		{"unknown", http.MethodGet, "custom-voices/missing", "", 404},
		{"group removed", http.MethodGet, "custom-voices/mine", "", 404},
		{"deleted mapping", http.MethodGet, "custom-voices/mine", "", 404},
		{"disabled account", http.MethodGet, "custom-voices/mine", "", 503},
		{"store unavailable", http.MethodGet, "custom-voices/mine", "", 503},
		{"malformed JSON", http.MethodPost, "tts", `{"voice_id":`, 400},
		{"duplicate voice", http.MethodPost, "tts", `{"voice_id":"mine","voice_id":"foreign"}`, 400},
		{"invalid list limit", http.MethodGet, "custom-voices?limit=0", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, repo, slots, upstream := newGrokVoiceIsolationHandler(t)
			switch tc.name {
			case "group removed":
				repo.accounts[1].GroupIDs = []int64{99}
			case "deleted mapping":
				v := repo.voices["mine"]
				v.Deleted = true
				repo.voices["mine"] = v
			case "disabled account":
				repo.accounts[1].Status = service.StatusDisabled
				repo.accounts[1].Schedulable = false
			case "store unavailable":
				repo.failRead = true
			}
			c, w := grokVoiceIsolationContext(tc.method, tc.endpoint, tc.body)
			endpoint, _, _ := strings.Cut(tc.endpoint, "?")
			h.GrokVoice(c, endpoint)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.NotContains(t, w.Body.String(), "private database detail")
			require.Zero(t, upstream.calls)
			slots.assertReleased(t)
		})
	}
}

func TestGrokVoiceHandlerCatalogueFailureDoesNotForwardTTS(t *testing.T) {
	h, _, slots, upstream := newGrokVoiceIsolationHandler(t)
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		require.Equal(t, "/v1/tts/voices", req.URL.Path, "unverified voice must not reach the synthesis endpoint")
		return voiceHandlerResponse(http.StatusServiceUnavailable, `{"error":"private provider detail"}`), nil
	}
	c, w := grokVoiceIsolationContext(http.MethodPost, "tts", `{"voice_id":"unverified","text":"hello"}`)
	h.GrokVoice(c, "tts")
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.Equal(t, 1, upstream.calls)
	require.NotContains(t, w.Body.String(), "private provider detail")
	slots.assertReleased(t)
}

func TestGrokVoiceHandlerKnownForeignIDCannotBecomeBuiltin(t *testing.T) {
	h, _, slots, upstream := newGrokVoiceIsolationHandler(t)
	upstream.call = func(req *http.Request, _ int64) (*http.Response, error) {
		if req.URL.Path == "/v1/tts/voices" {
			return voiceHandlerResponse(http.StatusOK, `{"voices":[{"voice_id":"foreign"}]}`), nil
		}
		return voiceHandlerResponse(http.StatusBadRequest, `{"error":"must not reach synthesis"}`), nil
	}
	c, w := grokVoiceIsolationContext(http.MethodPost, "tts", `{"voice_id":"foreign","text":"hello"}`)
	h.GrokVoice(c, "tts")
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	require.Zero(t, upstream.calls, "known foreign ownership must win before any catalogue lookup")
	slots.assertReleased(t)
}
