package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newGrokCustomVoiceTestRepo(t *testing.T) (service.GrokCustomVoiceRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	// Exercise the production constructor's dynamic type, not just a test fake.
	repo, ok := NewAccountRepository(nil, db, nil).(service.GrokCustomVoiceRepository)
	require.True(t, ok, "production account repository must persist custom voice ownership")
	return repo, mock
}

func TestGrokCustomVoiceRepositoryCreateRejectsCollision(t *testing.T) {
	repo, mock := newGrokCustomVoiceTestRepo(t)
	voice := &service.GrokCustomVoice{VoiceID: "voice-1", UserID: 7, AccountID: 19, Profile: json.RawMessage(`{"name":"test"}`)}
	query := regexp.QuoteMeta(`INSERT INTO grok_custom_voices (voice_id, user_id, account_id, profile) VALUES ($1, $2, $3, $4::jsonb)`)
	mock.ExpectExec(query).WithArgs("voice-1", int64(7), int64(19), string(voice.Profile)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.CreateGrokCustomVoice(context.Background(), voice))
	collision := errors.New("duplicate key")
	voice.UserID = 8
	mock.ExpectExec(query).WithArgs("voice-1", int64(8), int64(19), string(voice.Profile)).WillReturnError(collision)
	require.ErrorIs(t, repo.CreateGrokCustomVoice(context.Background(), voice), collision)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGrokCustomVoiceRepositoryReadOwnership(t *testing.T) {
	repo, mock := newGrokCustomVoiceTestRepo(t)
	columns := []string{"voice_id", "user_id", "account_id", "profile"}
	query := regexp.QuoteMeta(`SELECT voice_id, user_id, account_id, profile, (deleted_at IS NOT NULL) FROM grok_custom_voices WHERE voice_id = $1`)
	mock.ExpectQuery(query).WithArgs("voice-1").WillReturnRows(sqlmock.NewRows(append(columns, "deleted")).AddRow("voice-1", 7, 19, []byte(`{"name":"test"}`), false))
	voice, err := repo.GetGrokCustomVoice(context.Background(), "voice-1")
	require.NoError(t, err)
	require.Equal(t, int64(7), voice.UserID)
	require.Equal(t, int64(19), voice.AccountID)
	require.JSONEq(t, `{"name":"test"}`, string(voice.Profile))
	require.False(t, voice.Deleted)
	mock.ExpectQuery(query).WithArgs("missing").WillReturnRows(sqlmock.NewRows(append(columns, "deleted")))
	voice, err = repo.GetGrokCustomVoice(context.Background(), "missing")
	require.NoError(t, err)
	require.Nil(t, voice)
	mock.ExpectQuery(query).WithArgs("deleted").WillReturnRows(sqlmock.NewRows(append(columns, "deleted")).AddRow("deleted", 7, 19, []byte(`{}`), true))
	voice, err = repo.GetGrokCustomVoice(context.Background(), "deleted")
	require.NoError(t, err)
	require.NotNil(t, voice)
	require.True(t, voice.Deleted)
	require.Equal(t, int64(7), voice.UserID)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT v.voice_id, v.user_id, v.account_id, v.profile FROM grok_custom_voices v JOIN accounts a ON a.id = v.account_id JOIN account_groups ag ON ag.account_id = a.id WHERE v.user_id = $1 AND ag.group_id = $2 AND v.voice_id > $3 AND a.platform = $4 AND a.deleted_at IS NULL AND v.deleted_at IS NULL ORDER BY v.voice_id LIMIT $5`)).WithArgs(int64(7), int64(3), "", "grok", 101).WillReturnRows(sqlmock.NewRows(columns).AddRow("voice-1", 7, 19, []byte(`{}`)))
	voices, err := repo.ListGrokCustomVoices(context.Background(), 7, 3, "", 101)
	require.NoError(t, err)
	require.Len(t, voices, 1)
	require.Equal(t, int64(7), voices[0].UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGrokCustomVoiceRepositoryMutationOwnershipAndTombstones(t *testing.T) {
	repo, mock := newGrokCustomVoiceTestRepo(t)
	ctx := context.Background()
	deleteQuery := regexp.QuoteMeta(`UPDATE grok_custom_voices SET deleted_at = COALESCE(deleted_at, NOW()) WHERE voice_id = $1 AND user_id = $2`)
	for i := 0; i < 2; i++ {
		mock.ExpectExec(deleteQuery).WithArgs("voice-1", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.DeleteGrokCustomVoice(ctx, "voice-1", 7))
	}
	mock.ExpectExec(deleteQuery).WithArgs("voice-1", int64(8)).WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, repo.DeleteGrokCustomVoice(ctx, "voice-1", 8), sql.ErrNoRows)
	updateQuery := regexp.QuoteMeta(`UPDATE grok_custom_voices SET profile = $4::jsonb WHERE voice_id = $1 AND user_id = $2 AND account_id = $3 AND deleted_at IS NULL`)
	voice := &service.GrokCustomVoice{VoiceID: "voice-1", UserID: 7, AccountID: 19}
	mock.ExpectExec(updateQuery).WithArgs("voice-1", int64(7), int64(19), "{}").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateGrokCustomVoice(ctx, voice))
	// A deleted row or mismatched immutable owner/account cannot be updated.
	mock.ExpectExec(updateQuery).WithArgs("voice-1", int64(7), int64(19), "{}").WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, repo.UpdateGrokCustomVoice(ctx, voice), sql.ErrNoRows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGrokCustomVoiceRepositoryInvalidInputs(t *testing.T) {
	repo, mock := newGrokCustomVoiceTestRepo(t)
	for _, voice := range []*service.GrokCustomVoice{nil, {}, {VoiceID: "v", UserID: 1}, {VoiceID: "v", UserID: 1, AccountID: 2, Profile: json.RawMessage(`{`)}} {
		require.Error(t, repo.CreateGrokCustomVoice(context.Background(), voice))
		require.Error(t, repo.UpdateGrokCustomVoice(context.Background(), voice))
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGrokCustomVoiceRepositoryStorageErrorsFailClosed(t *testing.T) {
	repo, mock := newGrokCustomVoiceTestRepo(t)
	ctx := context.Background()
	storageErr := errors.New("storage unavailable")
	mock.ExpectQuery("SELECT .* FROM grok_custom_voices WHERE voice_id").WithArgs("v").WillReturnError(storageErr)
	voice, err := repo.GetGrokCustomVoice(ctx, "v")
	require.ErrorIs(t, err, storageErr)
	require.Nil(t, voice)
	mock.ExpectQuery("SELECT .* FROM grok_custom_voices v JOIN accounts").WithArgs(int64(7), int64(3), "", "grok", 101).WillReturnRows(sqlmock.NewRows([]string{"voice_id", "user_id", "account_id", "profile"}).AddRow("v", 7, 19, []byte(`{}`)).RowError(0, storageErr))
	_, err = repo.ListGrokCustomVoices(ctx, 7, 3, "", 101)
	require.ErrorIs(t, err, storageErr)
	mock.ExpectExec("UPDATE grok_custom_voices SET deleted_at").WithArgs("v", int64(7)).WillReturnError(storageErr)
	require.ErrorIs(t, repo.DeleteGrokCustomVoice(ctx, "v", 7), storageErr)
	mock.ExpectExec("UPDATE grok_custom_voices SET profile").WithArgs("v", int64(7), int64(19), "{}").WillReturnResult(sqlmock.NewErrorResult(storageErr))
	require.ErrorIs(t, repo.UpdateGrokCustomVoice(ctx, &service.GrokCustomVoice{VoiceID: "v", UserID: 7, AccountID: 19}), storageErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGrokCustomVoiceRepositoryBoundedList(t *testing.T) {
	repo, mock := newGrokCustomVoiceTestRepo(t)
	query := regexp.QuoteMeta(`SELECT v.voice_id, v.user_id, v.account_id, v.profile FROM grok_custom_voices v JOIN accounts a ON a.id = v.account_id JOIN account_groups ag ON ag.account_id = a.id WHERE v.user_id = $1 AND ag.group_id = $2 AND v.voice_id > $3 AND a.platform = $4 AND a.deleted_at IS NULL AND v.deleted_at IS NULL ORDER BY v.voice_id LIMIT $5`)
	mock.ExpectQuery(query).WithArgs(int64(7), int64(3), "voice-50", "grok", 11).WillReturnRows(sqlmock.NewRows([]string{"voice_id", "user_id", "account_id", "profile"}))
	voices, err := repo.ListGrokCustomVoices(context.Background(), 7, 3, "voice-50", 11)
	require.NoError(t, err)
	require.Empty(t, voices)
	for _, limit := range []int{-1, 0, 1002} {
		_, err := repo.ListGrokCustomVoices(context.Background(), 7, 3, "", limit)
		require.Error(t, err)
	}
	_, err = repo.ListGrokCustomVoices(context.Background(), 7, 0, "", 11)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGrokCustomVoiceRepositoryRejectsOversizedMetadata(t *testing.T) {
	repo, mock := newGrokCustomVoiceTestRepo(t)
	voice := &service.GrokCustomVoice{VoiceID: "v", UserID: 7, AccountID: 19, Profile: json.RawMessage(`{"name":"` + strings.Repeat("x", 8192) + `"}`)}
	require.ErrorContains(t, repo.CreateGrokCustomVoice(context.Background(), voice), "metadata size limit")
	require.ErrorContains(t, repo.UpdateGrokCustomVoice(context.Background(), voice), "metadata size limit")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGrokCustomVoiceRepositoryRejectsMalformedIDs(t *testing.T) {
	repo, mock := newGrokCustomVoiceTestRepo(t)
	for _, id := range []string{"..", ".", "%2F", "%2e%2e", "mine ", " mine", "a/b", "a?b", "音色", strings.Repeat("a", 129)} {
		voice := &service.GrokCustomVoice{VoiceID: id, UserID: 7, AccountID: 19, Profile: json.RawMessage(`{}`)}
		require.Error(t, repo.CreateGrokCustomVoice(context.Background(), voice), id)
		require.Error(t, repo.UpdateGrokCustomVoice(context.Background(), voice), id)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}
