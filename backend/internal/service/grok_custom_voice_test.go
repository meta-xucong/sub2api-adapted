package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	coderws "github.com/coder/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type customVoiceTestRepo struct {
	AccountRepository
	mu              sync.Mutex
	voices          map[string]GrokCustomVoice
	deleted         map[string]bool
	accounts        map[int64]*Account
	failCreate      bool
	failDelete      bool
	requireDeadline bool
}

func (r *customVoiceTestRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.accounts[id], nil
}
func (r *customVoiceTestRepo) CreateGrokCustomVoice(ctx context.Context, v *GrokCustomVoice) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.requireDeadline {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			return errors.New("missing bounded persistence deadline")
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failCreate {
		return errors.New("offline")
	}
	if _, ok := r.voices[v.VoiceID]; ok {
		return errors.New("collision")
	}
	r.voices[v.VoiceID] = *v
	return nil
}
func (r *customVoiceTestRepo) GetGrokCustomVoice(_ context.Context, id string) (*GrokCustomVoice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.voices[id]
	if !ok {
		return nil, nil
	}
	v.Deleted = r.deleted[id]
	return &v, nil
}
func (r *customVoiceTestRepo) ListGrokCustomVoices(_ context.Context, user, group int64, cursor string, limit int) ([]GrokCustomVoice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []GrokCustomVoice
	for id, v := range r.voices {
		if v.UserID == user && !r.deleted[id] && id > cursor && grokVoiceAccountInGroup(r.accounts[v.AccountID], group) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VoiceID < out[j].VoiceID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (r *customVoiceTestRepo) DeleteGrokCustomVoice(ctx context.Context, id string, user int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.requireDeadline {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			return errors.New("missing bounded persistence deadline")
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failDelete {
		return errors.New("offline")
	}
	if r.voices[id].UserID != user {
		return errors.New("missing")
	}
	r.deleted[id] = true
	return nil
}
func (r *customVoiceTestRepo) UpdateGrokCustomVoice(ctx context.Context, v *GrokCustomVoice) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.requireDeadline {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			return errors.New("missing bounded persistence deadline")
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.voices[v.VoiceID]
	if !ok || r.deleted[v.VoiceID] || old.UserID != v.UserID || old.AccountID != v.AccountID {
		return errors.New("missing")
	}
	r.voices[v.VoiceID] = *v
	return nil
}

type customVoiceTestHTTP struct {
	HTTPUpstream
	calls atomic.Int32
	do    func(*http.Request) (*http.Response, error)
}

func (h *customVoiceTestHTTP) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	h.calls.Add(1)
	return h.do(r)
}
func customVoiceResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func customVoiceFixture() (*OpenAIGatewayService, *customVoiceTestRepo, *Account, context.Context) {
	a := &Account{ID: 10, Platform: PlatformGrok, Type: AccountTypeAPIKey, GroupIDs: []int64{20}, Credentials: map[string]any{"api_key": "fake-test-key"}}
	r := &customVoiceTestRepo{voices: map[string]GrokCustomVoice{}, deleted: map[string]bool{}, accounts: map[int64]*Account{10: a}}
	group := int64(20)
	return &OpenAIGatewayService{accountRepo: r}, r, a, WithGrokVoiceIdentity(context.Background(), 1, &group)
}
func voiceGin(ctx context.Context, method string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/custom-voices", nil).WithContext(ctx)
	return c, w
}
func putVoice(r *customVoiceTestRepo, id string, user, account int64) {
	r.voices[id] = GrokCustomVoice{VoiceID: id, UserID: user, AccountID: account, Profile: json.RawMessage(fmt.Sprintf(`{"voice_id":%q}`, id))}
}

func TestGrokCustomVoiceTenantIsolation(t *testing.T) {
	s, r, a, ctx := customVoiceFixture()
	putVoice(r, "private", 2, a.ID)
	for _, endpoint := range []string{"custom-voices/private", "custom-voices/private/audio", "custom-voices/unknown"} {
		_, err := s.GrokVoicePinnedAccount(ctx, endpoint, nil)
		require.ErrorIs(t, err, ErrGrokVoiceNotFound)
		c, _ := voiceGin(ctx, http.MethodGet)
		_, err = s.ForwardGrokVoice(ctx, c, a, endpoint, nil, "")
		require.ErrorIs(t, err, ErrGrokVoiceNotFound)
	}
	putVoice(r, "mine", 1, a.ID)
	id, err := s.GrokVoicePinnedAccount(ctx, "custom-voices/mine", nil)
	require.NoError(t, err)
	require.Equal(t, a.ID, id)
	a.GroupIDs = []int64{30}
	_, err = s.GrokVoicePinnedAccount(ctx, "custom-voices/mine", nil)
	require.ErrorIs(t, err, ErrGrokVoiceNotFound)
}
func TestGrokCustomVoiceListFiltersOwnerAndGroup(t *testing.T) {
	s, r, a, ctx := customVoiceFixture()
	putVoice(r, "mine", 1, a.ID)
	putVoice(r, "other", 2, a.ID)
	putVoice(r, "removed", 1, 30)
	r.accounts[30] = &Account{ID: 30, Platform: PlatformGrok, GroupIDs: []int64{99}}
	c, w := voiceGin(ctx, http.MethodGet)
	require.NoError(t, s.ListGrokCustomVoices(ctx, c))
	require.JSONEq(t, `{"voices":[{"voice_id":"mine"}],"pagination_token":null}`, w.Body.String())
}
func TestGrokCustomVoiceCreateDurableBeforeReply(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			s, r, a, ctx := customVoiceFixture()
			r.failCreate = fail
			h := &customVoiceTestHTTP{do: func(*http.Request) (*http.Response, error) {
				return customVoiceResponse(200, `{"voice_id":"newvoice","name":"mine"}`), nil
			}}
			s.httpUpstream = h
			c, w := voiceGin(ctx, http.MethodPost)
			_, err := s.ForwardGrokVoice(ctx, c, a, "custom-voices", []byte("fake multipart"), "multipart/form-data")
			if fail {
				require.Error(t, err)
				require.Empty(t, w.Body.String())
				require.False(t, c.Writer.Written())
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(1), r.voices["newvoice"].UserID)
				require.Equal(t, a.ID, r.voices["newvoice"].AccountID)
				require.Contains(t, w.Body.String(), "newvoice")
			}
			require.Equal(t, int32(1), h.calls.Load())
		})
	}
}
func TestGrokCustomVoiceCollisionNeverTransfersOwnership(t *testing.T) {
	s, r, a, ctx := customVoiceFixture()
	putVoice(r, "collision", 2, 99)
	s.httpUpstream = &customVoiceTestHTTP{do: func(*http.Request) (*http.Response, error) {
		return customVoiceResponse(200, `{"voice_id":"collision"}`), nil
	}}
	c, w := voiceGin(ctx, http.MethodPost)
	_, err := s.ForwardGrokVoice(ctx, c, a, "custom-voices", nil, "")
	require.Error(t, err)
	require.Empty(t, w.Body.String())
	require.Equal(t, int64(2), r.voices["collision"].UserID)
}
func TestGrokCustomVoiceDeleteRetryAfterPersistenceFailure(t *testing.T) {
	s, r, a, ctx := customVoiceFixture()
	putVoice(r, "mine", 1, a.ID)
	r.failDelete = true
	h := &customVoiceTestHTTP{do: func(*http.Request) (*http.Response, error) { return customVoiceResponse(200, `{"deleted":true}`), nil }}
	s.httpUpstream = h
	c, w := voiceGin(ctx, http.MethodDelete)
	_, err := s.ForwardGrokVoice(ctx, c, a, "custom-voices/mine", nil, "")
	require.Error(t, err)
	require.Empty(t, w.Body.String())
	require.False(t, r.deleted["mine"])
	r.failDelete = false
	h.do = func(*http.Request) (*http.Response, error) { return customVoiceResponse(404, `{"error":"gone"}`), nil }
	c, w = voiceGin(ctx, http.MethodDelete)
	_, err = s.ForwardGrokVoice(ctx, c, a, "custom-voices/mine", nil, "")
	require.NoError(t, err)
	require.True(t, r.deleted["mine"])
	require.JSONEq(t, `{"deleted":true}`, w.Body.String())
	_, err = s.GrokVoicePinnedAccount(ctx, "custom-voices/mine", nil)
	require.ErrorIs(t, err, ErrGrokVoiceNotFound)
}
func TestGrokCustomVoiceTTSAndRealtimeValidation(t *testing.T) {
	s, r, a, ctx := customVoiceFixture()
	putVoice(r, "mine", 1, a.ID)
	putVoice(r, "elsewhere", 1, 11)
	putVoice(r, "private", 2, a.ID)
	r.accounts[11] = &Account{ID: 11, Platform: PlatformGrok, GroupIDs: []int64{20}}
	h := &customVoiceTestHTTP{do: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/v1/tts/voices", req.URL.Path)
		return customVoiceResponse(200, `{"voices":[{"voice_id":"eve"},{"voice_id":"new-builtin"}]}`), nil
	}}
	s.httpUpstream = h
	c, _ := voiceGin(ctx, http.MethodPost)
	for _, body := range []string{`{}`, `{"voice_id":"mine"}`, `{"voice_id":"eve"}`, `{"voice_id":"new-builtin"}`, `{"type":"session.update","session":{"voice":"mine"}}`} {
		require.NoError(t, s.validateGrokVoiceIDs(ctx, c, a, []byte(body)), body)
	}
	for _, body := range []string{`{"voice_id":"private"}`, `{"voice_id":"unknown"}`, `{"voice_id":"elsewhere"}`, `{"type":"session.update","session":{"voice":"private"}}`, `{"voice_id":"eve","voice_id":"private"}`, `{"session":{"voice":"eve"},"session":{"voice":"private"}}`} {
		require.Error(t, s.validateGrokVoiceIDs(ctx, c, a, []byte(body)), body)
	}
	require.Equal(t, int32(1), h.calls.Load(), "catalogue should be cached")
	pinned, err := s.GrokVoicePinnedAccount(ctx, "tts", []byte(`{"voice_id":"mine"}`))
	require.NoError(t, err)
	require.Equal(t, a.ID, pinned)
}
func TestGrokVoiceJSONRejectsAmbiguity(t *testing.T) {
	for _, body := range []string{`[]`, `null`, `{`, `{} {}`, `{"voice":null}`, `{"voice":{"id":"private"}}`, `{"voice":""}`, `{"voice":"eve","voice":"private"}`, `{"session":{"voice":"eve"},"session":{}}`} {
		_, err := grokVoiceIDs([]byte(body))
		require.Error(t, err, body)
	}
	ids, err := grokVoiceIDs([]byte(`{"session":{"voice":"eve"},"data":[{"voice_id":"mine"}]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"eve"}, ids)
}
func TestGrokCustomVoiceConcurrentCreateAndDelete(t *testing.T) {
	s, r, a, ctx := customVoiceFixture()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := &GrokCustomVoice{VoiceID: "same", UserID: 1, AccountID: a.ID, Profile: json.RawMessage(`{"voice_id":"same"}`)}
			if r.CreateGrokCustomVoice(ctx, v) == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), accepted.Load())
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.ownedGrokVoice(ctx, "same")
			_ = r.DeleteGrokCustomVoice(ctx, "same", 1)
		}()
	}
	wg.Wait()
	_, err := s.ownedGrokVoice(ctx, "same")
	require.ErrorIs(t, err, ErrGrokVoiceNotFound)
	require.Error(t, r.CreateGrokCustomVoice(ctx, &GrokCustomVoice{VoiceID: "same", UserID: 2, AccountID: a.ID}))
}

func TestGrokCustomVoicePersistenceSurvivesClientCancellation(t *testing.T) {
	s, r, a, identity := customVoiceFixture()
	r.requireDeadline = true
	ctx, cancel := context.WithCancel(identity)
	cancel()
	c, _ := voiceGin(ctx, http.MethodPost)
	require.NoError(t, s.persistGrokVoiceResponse(ctx, c, a, "custom-voices", []byte(`{"voice_id":"persisted"}`)))
	require.Equal(t, int64(1), r.voices["persisted"].UserID)
	c.Request.Method = http.MethodPatch
	require.NoError(t, s.persistGrokVoiceResponse(ctx, c, a, "custom-voices/persisted", []byte(`{"voice_id":"persisted","name":"updated"}`)))
	c.Request.Method = http.MethodDelete
	require.NoError(t, s.persistGrokVoiceResponse(ctx, c, a, "custom-voices/persisted", nil))
	require.True(t, r.deleted["persisted"])
}

type customVoiceRealtimeFake struct {
	writes chan []byte
	reads  chan []byte
}

func (f *customVoiceRealtimeFake) WriteJSON(ctx context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case f.writes <- data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (f *customVoiceRealtimeFake) ReadMessage(ctx context.Context) ([]byte, error) {
	select {
	case data := <-f.reads:
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (f *customVoiceRealtimeFake) Ping(context.Context) error { return nil }
func (f *customVoiceRealtimeFake) Close() error               { return nil }
func TestGrokCustomVoiceRealtimeBlocksFrameAndContinues(t *testing.T) {
	s, r, a, identity := customVoiceFixture()
	putVoice(r, "mine", 1, a.ID)
	putVoice(r, "private", 2, a.ID)
	s.httpUpstream = &customVoiceTestHTTP{do: func(*http.Request) (*http.Response, error) {
		return customVoiceResponse(200, `{"voices":[{"voice_id":"eve"}]}`), nil
	}}
	fake := &customVoiceRealtimeFake{writes: make(chan []byte, 8), reads: make(chan []byte, 8)}
	observed := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := coderws.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		c, _ := gin.CreateTestContext(w)
		c.Request = req.WithContext(identity)
		audio, _ := s.ProxyGrokRealtimeConn(identity, c, conn, &GrokRealtimeUpstream{conn: fake, account: a})
		observed <- audio
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	fake.reads <- []byte(`{"type":"response.audio.delta","delta":"audio"}`)
	_, msg, err := client.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(msg), "response.audio.delta")
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"session.update","session":{"voice":"private"}}`)))
	_, msg, err = client.Read(ctx)
	require.NoError(t, err)
	require.Contains(t, string(msg), "voice_not_available")
	require.Empty(t, fake.writes, "private voice event must never reach provider")
	valid := []byte(`{"type":"session.update","session":{"voice":"mine"}}`)
	require.NoError(t, client.Write(ctx, coderws.MessageText, valid))
	select {
	case got := <-fake.writes:
		require.JSONEq(t, string(valid), string(got))
	case <-ctx.Done():
		t.Fatal("valid follow-up not forwarded")
	}
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case audio := <-observed:
		require.True(t, audio, "rejected voice update must preserve observed audio for existing billing")
	case <-ctx.Done():
		t.Fatal("relay failed to stop")
	}
}
func TestGrokCustomVoiceListPaginationAndInvalidLimit(t *testing.T) {
	s, r, a, ctx := customVoiceFixture()
	putVoice(r, "a", 1, a.ID)
	putVoice(r, "b", 1, a.ID)
	c, w := voiceGin(ctx, http.MethodGet)
	c.Request.URL.RawQuery = "limit=1"
	require.NoError(t, s.ListGrokCustomVoices(ctx, c))
	require.JSONEq(t, `{"voices":[{"voice_id":"a"}],"pagination_token":"a"}`, w.Body.String())
	c, w = voiceGin(ctx, http.MethodGet)
	c.Request.URL.RawQuery = "limit=1&pagination_token=a"
	require.NoError(t, s.ListGrokCustomVoices(ctx, c))
	require.JSONEq(t, `{"voices":[{"voice_id":"b"}],"pagination_token":null}`, w.Body.String())
	c, _ = voiceGin(ctx, http.MethodGet)
	c.Request.URL.RawQuery = "limit=1001"
	require.ErrorIs(t, s.ListGrokCustomVoices(ctx, c), ErrGrokVoiceInvalidRequest)
}

func TestGrokCustomVoiceKnownDeniedIDNeverUsesBuiltinFallback(t *testing.T) {
	for _, kind := range []string{"foreign", "deleted", "removed group"} {
		t.Run(kind, func(t *testing.T) {
			s, r, a, ctx := customVoiceFixture()
			putVoice(r, "eve", 1, a.ID)
			switch kind {
			case "foreign":
				putVoice(r, "eve", 2, a.ID)
			case "deleted":
				r.deleted["eve"] = true
			case "removed group":
				a.GroupIDs = []int64{99}
			}
			h := &customVoiceTestHTTP{do: func(*http.Request) (*http.Response, error) {
				return customVoiceResponse(200, `{"voices":[{"voice_id":"eve"}]}`), nil
			}}
			s.httpUpstream = h
			c, _ := voiceGin(ctx, http.MethodPost)
			require.ErrorIs(t, s.validateGrokVoiceIDs(ctx, c, a, []byte(`{"voice_id":"eve"}`)), ErrGrokVoiceNotFound)
			_, err := s.GrokVoicePinnedAccount(ctx, "tts", []byte(`{"voice_id":"eve"}`))
			require.ErrorIs(t, err, ErrGrokVoiceNotFound)
			require.Zero(t, h.calls.Load(), "known denied IDs must never be treated as public built-ins")
		})
	}
}

func TestGrokCustomVoiceIDBoundary(t *testing.T) {
	for _, id := range []string{"nlbqfwie", "Voice_123", "d8e69247-f8a0-4c6f-9520-9cd271184152", strings.Repeat("a", 128)} {
		require.True(t, ValidGrokCustomVoiceID(id), id)
	}
	for _, id := range []string{"", " ", "..", ".", "mine ", " mine", "%2F", "%2e%2e", "a/b", "a\\b", "a?b", "a#b", "音色", strings.Repeat("a", 129)} {
		require.False(t, ValidGrokCustomVoiceID(id), id)
		s, _, a, ctx := customVoiceFixture()
		c, _ := voiceGin(ctx, http.MethodGet)
		_, err := s.GrokVoicePinnedAccount(ctx, "custom-voices/"+id, nil)
		require.ErrorIs(t, err, ErrGrokVoiceNotFound)
		_, err = s.ForwardGrokVoice(ctx, c, a, "custom-voices/"+id, nil, "")
		if strings.Contains(id, "/") {
			require.Error(t, err)
		} else {
			require.ErrorIs(t, err, ErrGrokVoiceNotFound)
		}
	}
}
func TestGrokCustomVoiceResponseIdentityUnambiguous(t *testing.T) {
	for _, body := range []string{
		`[]`, `null`, `{"voice_id":null}`, `{"voice_id":123}`, `{"VOICE_ID":"mine"}`, `{"voice_id":"mine","voice_id":"other"}`, `{"voice_id":"mine","Voice_ID":"other"}`, `{"voice_id":"mine"} {}`, `{"voice_id":".."}`, `{"voice_id":"%2F"}`, `{"voice_id":" mine"}`, `{"voice_id":"mine "}`, `{"name":"missing identity"}`,
	} {
		t.Run(body, func(t *testing.T) {
			s, r, a, ctx := customVoiceFixture()
			s.httpUpstream = &customVoiceTestHTTP{do: func(*http.Request) (*http.Response, error) { return customVoiceResponse(200, body), nil }}
			c, w := voiceGin(ctx, http.MethodPost)
			_, err := s.ForwardGrokVoice(ctx, c, a, "custom-voices", nil, "")
			require.Error(t, err)
			require.Empty(t, r.voices)
			require.Empty(t, w.Body.String())
		})
	}
	body := `{"voice_id":"mine","metadata":{"voice_id":"unrelated"},"name":"first","name":"last"}`
	s, r, a, ctx := customVoiceFixture()
	s.httpUpstream = &customVoiceTestHTTP{do: func(*http.Request) (*http.Response, error) { return customVoiceResponse(200, body), nil }}
	c, w := voiceGin(ctx, http.MethodPost)
	_, err := s.ForwardGrokVoice(ctx, c, a, "custom-voices", nil, "")
	require.NoError(t, err)
	require.Equal(t, "mine", r.voices["mine"].VoiceID)
	require.Equal(t, body, w.Body.String())
}

func TestGrokCustomVoiceRealtimeRejectedAudioDoesNotCreateUsage(t *testing.T) {
	for _, priorAudio := range []bool{false, true} {
		for _, rejected := range []struct{ name, body string }{
			{"foreign voice", `{"type":"input_audio_buffer.append","audio":"YQ==","voice":"private"}`},
			{"malformed JSON", `{"type":"input_audio_buffer.append","audio":"YQ==",`},
			{"duplicate voice", `{"type":"input_audio_buffer.append","audio":"YQ==","voice":"mine","voice":"private"}`},
		} {
			t.Run(fmt.Sprintf("prior_audio_%t/%s", priorAudio, rejected.name), func(t *testing.T) {
				s, r, a, identity := customVoiceFixture()
				putVoice(r, "private", 2, a.ID)
				putVoice(r, "mine", 1, a.ID)
				fake := &customVoiceRealtimeFake{writes: make(chan []byte, 8), reads: make(chan []byte, 8)}
				observed := make(chan bool, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					conn, err := coderws.Accept(w, req, nil)
					if err != nil {
						return
					}
					defer func() { _ = conn.CloseNow() }()
					c, _ := gin.CreateTestContext(w)
					c.Request = req.WithContext(identity)
					audio, _ := s.ProxyGrokRealtimeConn(identity, c, conn, &GrokRealtimeUpstream{conn: fake, account: a})
					observed <- audio
				}))
				defer server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
				require.NoError(t, err)
				defer func() { _ = client.CloseNow() }()
				if priorAudio {
					valid := `{"type":"input_audio_buffer.append","audio":"YQ=="}`
					require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(valid)))
					select {
					case got := <-fake.writes:
						require.JSONEq(t, valid, string(got))
					case <-ctx.Done():
						t.Fatal("valid audio not forwarded")
					}
				}
				require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(rejected.body)))
				_, reply, err := client.Read(ctx)
				require.NoError(t, err)
				require.Contains(t, string(reply), "voice_not_available")
				require.Empty(t, fake.writes, "rejected audio-like event must not reach provider")
				require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
				select {
				case audio := <-observed:
					require.Equal(t, priorAudio, audio, "only valid authorized client audio or received audio may enable existing billing")
				case <-ctx.Done():
					t.Fatal("relay failed to stop")
				}
			})
		}
	}
}

type grokVoiceCredentialDeadlineCache struct {
	GrokTokenCache
	deadlines chan time.Duration
}

func (cache *grokVoiceCredentialDeadlineCache) GetAccessToken(ctx context.Context, _ string) (string, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return "", errors.New("credential lookup has no deadline")
	}
	cache.deadlines <- time.Until(deadline)
	return "voice-context-test-token", nil
}
func grokVoiceOAuthDeadlineFixture() (*OpenAIGatewayService, *Account, context.Context, *grokVoiceCredentialDeadlineCache) {
	s, r, a, identity := customVoiceFixture()
	a.Type = AccountTypeOAuth
	a.Status = StatusActive
	a.Schedulable = true
	// Keep this cache-hit fixture beyond the real one-hour refresh window.
	// Credential refresh configuration is intentionally outside these deadline tests.
	a.Credentials = map[string]any{"access_token": "voice-context-test-token", "refresh_token": "voice-context-test-refresh", "expires_at": time.Now().Add(2 * grokTokenRefreshSkew).UTC().Format(time.RFC3339)}
	cache := &grokVoiceCredentialDeadlineCache{deadlines: make(chan time.Duration, 4)}
	s.grokTokenProvider = NewGrokTokenProvider(r, cache)
	s.httpUpstream = &customVoiceTestHTTP{do: func(*http.Request) (*http.Response, error) {
		return customVoiceResponse(200, `{"voices":[{"voice_id":"eve"}]}`), nil
	}}
	return s, a, identity, cache
}
func TestGrokBuiltinVoiceBoundsCredentialAcquisition(t *testing.T) {
	s, a, identity, cache := grokVoiceOAuthDeadlineFixture()
	builtin, err := s.grokBuiltinVoice(identity, nil, a, "eve")
	require.NoError(t, err)
	require.True(t, builtin)
	select {
	case remaining := <-cache.deadlines:
		require.Positive(t, remaining)
		require.LessOrEqual(t, remaining, 10*time.Second)
	default:
		t.Fatal("credential lookup did not run")
	}
}
func TestGrokCustomVoiceRealtimeIgnoresReusedHandshakeGinBudget(t *testing.T) {
	s, a, identity, cache := grokVoiceOAuthDeadlineFixture()
	fake := &customVoiceRealtimeFake{writes: make(chan []byte, 4), reads: make(chan []byte, 4)}
	original := make(chan *gin.Context, 1)
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := coderws.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		c, _ := gin.CreateTestContext(w)
		c.Request = req.WithContext(identity)
		c.Set(grokCredentialFailoverDeadlineKey, time.Now().Add(-time.Hour))
		original <- c
		_, _ = s.ProxyGrokRealtimeConn(identity, c, conn, &GrokRealtimeUpstream{conn: fake, account: a})
		close(done)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()
	var old *gin.Context
	select {
	case old = <-original:
	case <-ctx.Done():
		t.Fatal("missing handshake context")
	}
	// Model Gin pool reuse while the independent relay still validates an event.
	// A race run must not observe any goroutine reading this old context.
	reused := make(chan struct{})
	go func() {
		defer close(reused)
		for i := 0; i < 1000; i++ {
			old.Keys = map[string]any{grokCredentialFailoverDeadlineKey: time.Now().Add(-time.Hour)}
			old.Request = httptest.NewRequest(http.MethodGet, "/reused", nil)
		}
	}()
	event := `{"type":"session.update","session":{"voice":"eve"}}`
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(event)))
	select {
	case got := <-fake.writes:
		require.JSONEq(t, event, string(got))
	case <-ctx.Done():
		t.Fatal("expired handshake budget blocked a new realtime voice event")
	}
	select {
	case remaining := <-cache.deadlines:
		require.Positive(t, remaining)
		require.LessOrEqual(t, remaining, 10*time.Second)
	default:
		t.Fatal("missing per-event credential deadline")
	}
	<-reused
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("relay did not stop")
	}
}
