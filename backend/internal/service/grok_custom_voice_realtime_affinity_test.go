package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Realtime connects before receiving session.update, so voice validation may
// authorize only the account already selected for that connection. It must not
// silently forward an owned voice to a different provider account.
func TestGrokCustomVoiceRealtimeAccountAffinityBoundary(t *testing.T) {
	s, repo, selected, ctx := customVoiceFixture()
	putVoice(repo, "owned001", 1, selected.ID)
	otherID := selected.ID + 1
	repo.accounts[otherID] = &Account{ID: otherID, Platform: PlatformGrok, GroupIDs: []int64{20}}
	putVoice(repo, "owned002", 1, otherID)
	c, _ := voiceGin(ctx, http.MethodPost)
	require.NoError(t, s.validateGrokVoiceIDs(ctx, c, selected, []byte(`{"type":"session.update","session":{"voice":"owned001"}}`)))
	require.ErrorIs(t, s.validateGrokVoiceIDs(ctx, c, selected, []byte(`{"type":"session.update","session":{"voice":"owned002"}}`)), ErrGrokVoiceNotFound)
}
