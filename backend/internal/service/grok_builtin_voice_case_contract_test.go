package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func grokBuiltinCaseCatalogue(t *testing.T, ids ...string) *customVoiceTestHTTP {
	t.Helper()
	voices := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		voices = append(voices, map[string]string{"voice_id": id})
	}
	data, err := json.Marshal(map[string]any{"voices": voices})
	require.NoError(t, err)
	return &customVoiceTestHTTP{do: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/v1/tts/voices", req.URL.Path)
		return customVoiceResponse(http.StatusOK, string(data)), nil
	}}
}

func TestGrokBuiltinVoiceCaseInsensitiveContract(t *testing.T) {
	for _, catalogueSize := range []int{2, 257} {
		t.Run(fmt.Sprint(catalogueSize), func(t *testing.T) {
			s, _, account, ctx := customVoiceFixture()
			ids := []string{"eve", "ara"}
			for len(ids) < catalogueSize {
				ids = append(ids, fmt.Sprintf("builtin-%d", len(ids)))
			}
			upstream := grokBuiltinCaseCatalogue(t, ids...)
			s.httpUpstream = upstream
			c, _ := voiceGin(ctx, http.MethodPost)
			bodies := []string{
				`{"voice_id":"Eve","text":"hello"}`,
				`{"voice_id":"EVE","text":"hello"}`,
				`{"voice_id":"eve","text":"hello"}`,
				`{"type":"session.update","session":{"voice":"Ara"}}`,
				`{"type":"session.update","session":{"voice":"ARA"}}`,
			}
			for round := 0; round < 2; round++ {
				for _, body := range bodies {
					pinned, err := s.GrokVoicePinnedAccount(ctx, "tts", []byte(body))
					require.NoError(t, err)
					require.Zero(t, pinned, "built-in aliases must not become custom affinity")
					require.NoError(t, s.validateGrokVoiceIDs(ctx, c, account, []byte(body)), body)
				}
			}
			if catalogueSize <= 256 {
				require.Equal(t, int32(1), upstream.calls.Load(), "cold and cached catalogues must share case semantics")
			} else {
				require.Equal(t, int32(2*len(bodies)), upstream.calls.Load(), "oversized catalogues remain uncached")
			}
		})
	}
}

func TestGrokBuiltinVoiceCaseAliasesDoNotBypassCustomMapping(t *testing.T) {
	for _, kind := range []string{"foreign exact", "deleted exact", "group denied exact", "foreign canonical", "deleted canonical", "group denied canonical", "owned canonical"} {
		t.Run(kind, func(t *testing.T) {
			s, repo, account, ctx := customVoiceFixture()
			id := "eve"
			if kind == "foreign exact" || kind == "deleted exact" || kind == "group denied exact" {
				id = "Eve"
			}
			putVoice(repo, id, 1, account.ID)
			switch kind {
			case "foreign exact", "foreign canonical":
				putVoice(repo, id, 2, account.ID)
			case "deleted exact", "deleted canonical":
				repo.deleted[id] = true
			case "group denied exact", "group denied canonical":
				account.GroupIDs = []int64{99}
			}
			upstream := grokBuiltinCaseCatalogue(t, "eve")
			s.httpUpstream = upstream
			c, _ := voiceGin(ctx, http.MethodPost)
			for _, body := range []string{`{"voice_id":"Eve"}`, `{"type":"session.update","session":{"voice":"Eve"}}`} {
				require.ErrorIs(t, s.validateGrokVoiceIDs(ctx, c, account, []byte(body)), ErrGrokVoiceNotFound)
			}
			if id == "Eve" {
				require.Zero(t, upstream.calls.Load(), "exact known denials must precede any catalogue lookup")
			}
		})
	}
}

func TestGrokBuiltinVoiceCachedAliasRechecksCanonicalMapping(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleted=%t", deleted), func(t *testing.T) {
			s, repo, account, ctx := customVoiceFixture()
			upstream := grokBuiltinCaseCatalogue(t, "eve")
			s.httpUpstream = upstream
			c, _ := voiceGin(ctx, http.MethodPost)
			body := []byte(`{"voice_id":"EVE"}`)
			require.NoError(t, s.validateGrokVoiceIDs(ctx, c, account, body))
			putVoice(repo, "eve", 2, account.ID)
			repo.deleted["eve"] = deleted
			require.ErrorIs(t, s.validateGrokVoiceIDs(ctx, c, account, body), ErrGrokVoiceNotFound)
			require.Equal(t, int32(1), upstream.calls.Load(), "catalogue caching must not cache ownership authorization")
		})
	}
}

func TestGrokBuiltinVoiceCaseHandlingKeepsCustomIDsExact(t *testing.T) {
	s, repo, account, ctx := customVoiceFixture()
	putVoice(repo, "Voice_A", 1, account.ID)
	upstream := grokBuiltinCaseCatalogue(t, "eve")
	s.httpUpstream = upstream
	c, _ := voiceGin(ctx, http.MethodPost)
	exact := []byte(`{"voice_id":"Voice_A"}`)
	pinned, err := s.GrokVoicePinnedAccount(ctx, "tts", exact)
	require.NoError(t, err)
	require.Equal(t, account.ID, pinned)
	require.NoError(t, s.validateGrokVoiceIDs(ctx, c, account, exact))
	require.Zero(t, upstream.calls.Load())
	require.ErrorIs(t, s.validateGrokVoiceIDs(ctx, c, account, []byte(`{"voice_id":"voice_a"}`)), ErrGrokVoiceNotFound)
	_, err = s.GrokVoicePinnedAccount(ctx, "custom-voices/voice_a", nil)
	require.ErrorIs(t, err, ErrGrokVoiceNotFound)
}

func TestGrokBuiltinVoiceAmbiguousCaseCatalogueFailsClosed(t *testing.T) {
	for _, size := range []int{2, 257} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s, _, account, ctx := customVoiceFixture()
			ids := []string{"eve", "EVE"}
			for len(ids) < size {
				ids = append(ids, fmt.Sprintf("builtin-%d", len(ids)))
			}
			s.httpUpstream = grokBuiltinCaseCatalogue(t, ids...)
			c, _ := voiceGin(ctx, http.MethodPost)
			for round := 0; round < 2; round++ {
				require.ErrorIs(t, s.validateGrokVoiceIDs(ctx, c, account, []byte(`{"voice_id":"Eve"}`)), ErrGrokVoiceNotFound)
			}
		})
	}
}

type grokBuiltinCanonicalReadErrorRepo struct{ *customVoiceTestRepo }

func (r *grokBuiltinCanonicalReadErrorRepo) GetGrokCustomVoice(ctx context.Context, id string) (*GrokCustomVoice, error) {
	if id == "eve" {
		return nil, errors.New("database unavailable")
	}
	return r.customVoiceTestRepo.GetGrokCustomVoice(ctx, id)
}

func TestGrokBuiltinVoiceCanonicalStoreErrorFailsClosed(t *testing.T) {
	s, repo, account, ctx := customVoiceFixture()
	s.accountRepo = &grokBuiltinCanonicalReadErrorRepo{repo}
	s.httpUpstream = grokBuiltinCaseCatalogue(t, "eve")
	c, _ := voiceGin(ctx, http.MethodPost)
	for round := 0; round < 2; round++ {
		require.ErrorIs(t, s.validateGrokVoiceIDs(ctx, c, account, []byte(`{"voice_id":"Eve"}`)), ErrGrokVoiceStoreUnavailable)
	}
}

func TestGrokBuiltinVoiceCaseFoldingIsASCIIOnly(t *testing.T) {
	s, _, account, ctx := customVoiceFixture()
	s.httpUpstream = grokBuiltinCaseCatalogue(t, "kepler", "sal")
	c, _ := voiceGin(ctx, http.MethodPost)
	require.NoError(t, s.validateGrokVoiceIDs(ctx, c, account, []byte(`{"voice_id":"KEPLER"}`)))
	for _, body := range []string{`{"voice_id":"Kepler"}`, `{"type":"session.update","session":{"voice":"ſal"}}`} {
		require.ErrorIs(t, s.validateGrokVoiceIDs(ctx, c, account, []byte(body)), ErrGrokVoiceNotFound)
	}
}
