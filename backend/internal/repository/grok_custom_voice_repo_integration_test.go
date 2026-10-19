//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// These tests use committed fixtures and independent connections so they cover
// database visibility and uniqueness, rather than a transaction-local fake.
type grokVoiceSQLFixture struct {
	t          *testing.T
	ctx        context.Context
	prefix     string
	users      []*service.User
	groups     []*service.Group
	accountIDs []int64
	voiceIDs   []string
	repos      []service.GrokCustomVoiceRepository
}

func newGrokVoiceSQLFixture(t *testing.T) *grokVoiceSQLFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	f := &grokVoiceSQLFixture{t: t, ctx: ctx, prefix: fmt.Sprintf("voice-sql-%d-", time.Now().UnixNano())}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, query := range []struct {
			sql string
			ids any
		}{
			{"DELETE FROM grok_custom_voices WHERE voice_id = ANY($1)", pq.Array(f.voiceIDs)},
			{"DELETE FROM account_groups WHERE account_id = ANY($1)", pq.Array(f.accountIDs)},
			{"DELETE FROM accounts WHERE id = ANY($1)", pq.Array(f.accountIDs)},
		} {
			_, err := integrationDB.ExecContext(ctx, query.sql, query.ids)
			require.NoError(t, err, "remove owned voice fixture")
		}
		for _, group := range f.groups {
			_, err := integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", group.ID)
			require.NoError(t, err)
		}
		for _, user := range f.users {
			_, err := integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
			require.NoError(t, err)
		}
	})
	for i := 0; i < 2; i++ {
		f.users = append(f.users, mustCreateUser(t, testEntClient(t), &service.User{Email: fmt.Sprintf("%s%d@example.test", f.prefix, i)}))
		f.groups = append(f.groups, mustCreateGroup(t, testEntClient(t), &service.Group{Name: fmt.Sprintf("%sgroup-%d", f.prefix, i), Platform: service.PlatformGrok}))
		db, err := sql.Open("postgres", integrationDSN)
		require.NoError(t, err)
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, db.Close()) })
		repo, ok := NewAccountRepository(integrationEntClient, db, nil).(service.GrokCustomVoiceRepository)
		require.True(t, ok, "production constructor must expose durable voice ownership")
		f.repos = append(f.repos, repo)
	}
	return f
}

func (f *grokVoiceSQLFixture) account(platform string, groups ...int) *service.Account {
	f.t.Helper()
	a := mustCreateAccount(f.t, testEntClient(f.t), &service.Account{Name: fmt.Sprintf("%saccount-%d", f.prefix, len(f.accountIDs)), Platform: platform, Type: service.AccountTypeAPIKey})
	f.accountIDs = append(f.accountIDs, a.ID)
	for _, group := range groups {
		_, err := integrationDB.ExecContext(f.ctx, "INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2)", a.ID, f.groups[group].ID)
		require.NoError(f.t, err)
	}
	return a
}

func (f *grokVoiceSQLFixture) voice(suffix string, user int, account *service.Account) *service.GrokCustomVoice {
	f.t.Helper()
	v := &service.GrokCustomVoice{VoiceID: f.prefix + suffix, UserID: f.users[user].ID, AccountID: account.ID, Profile: json.RawMessage(`{"name":"original"}`)}
	f.voiceIDs = append(f.voiceIDs, v.VoiceID)
	require.NoError(f.t, f.repos[0].CreateGrokCustomVoice(f.ctx, v))
	return v
}

func (f *grokVoiceSQLFixture) list(repo, user, group int, cursor string, limit int) []string {
	f.t.Helper()
	voices, err := f.repos[repo].ListGrokCustomVoices(f.ctx, f.users[user].ID, f.groups[group].ID, cursor, limit)
	require.NoError(f.t, err)
	ids := make([]string, 0, len(voices))
	for _, voice := range voices {
		require.Equal(f.t, f.users[user].ID, voice.UserID)
		ids = append(ids, voice.VoiceID)
	}
	return ids
}

func requireGrokVoiceUniqueViolation(t *testing.T, err error) {
	t.Helper()
	var pgErr *pq.Error
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, pq.ErrorCode("23505"), pgErr.Code)
}

func TestGrokCustomVoicePostgresIsolationPaginationAndMembership(t *testing.T) {
	f := newGrokVoiceSQLFixture(t)
	shared := f.account(service.PlatformGrok, 0, 1)
	firstOnly := f.account(service.PlatformGrok, 0)
	secondOnly := f.account(service.PlatformGrok, 1)
	wrongPlatform := f.account(service.PlatformOpenAI, 0)
	deletedAccount := f.account(service.PlatformGrok, 0)
	first := f.voice("10", 0, shared)
	second := f.voice("20", 0, firstOnly)
	third := f.voice("30", 0, firstOnly)
	foreignUser := f.voice("11-foreign-user", 1, shared)
	otherGroup := f.voice("12-other-group", 0, secondOnly)
	f.voice("13-wrong-platform", 0, wrongPlatform)
	f.voice("14-deleted-account", 0, deletedAccount)
	deletedVoice := f.voice("15-deleted-voice", 0, shared)
	_, err := integrationDB.ExecContext(f.ctx, "UPDATE accounts SET deleted_at = NOW() WHERE id = $1", deletedAccount.ID)
	require.NoError(t, err)
	require.NoError(t, f.repos[0].DeleteGrokCustomVoice(f.ctx, deletedVoice.VoiceID, deletedVoice.UserID))

	require.Equal(t, []string{first.VoiceID, second.VoiceID}, f.list(1, 0, 0, "", 2))
	require.Equal(t, []string{third.VoiceID}, f.list(1, 0, 0, second.VoiceID, 2))
	require.Empty(t, f.list(0, 0, 0, third.VoiceID, 2))
	require.Equal(t, []string{first.VoiceID, otherGroup.VoiceID}, f.list(1, 0, 1, "", 1001))
	require.Equal(t, []string{foreignUser.VoiceID}, f.list(0, 1, 0, "", 1001))

	// Membership changes are immediately visible to another repository instance.
	_, err = integrationDB.ExecContext(f.ctx, "DELETE FROM account_groups WHERE account_id = $1 AND group_id = $2", shared.ID, f.groups[0].ID)
	require.NoError(t, err)
	require.Equal(t, []string{second.VoiceID, third.VoiceID}, f.list(1, 0, 0, "", 1001))
	require.Empty(t, f.list(1, 1, 0, "", 1001))
	_, err = integrationDB.ExecContext(f.ctx, "UPDATE accounts SET deleted_at = NOW() WHERE id = $1", firstOnly.ID)
	require.NoError(t, err)
	require.Empty(t, f.list(1, 0, 0, "", 1001))
}

func TestGrokCustomVoicePostgresImmutableOwnershipAcrossInstances(t *testing.T) {
	f := newGrokVoiceSQLFixture(t)
	a := f.account(service.PlatformGrok, 0)
	b := f.account(service.PlatformGrok, 0)
	original := f.voice("owner", 0, a)
	read, err := f.repos[1].GetGrokCustomVoice(f.ctx, original.VoiceID)
	require.NoError(t, err)
	require.Equal(t, original.UserID, read.UserID)
	require.Equal(t, original.AccountID, read.AccountID)
	require.False(t, read.Deleted)
	require.JSONEq(t, string(original.Profile), string(read.Profile))

	for _, mutate := range []struct {
		name    string
		userID  int64
		account int64
	}{
		{"different_user", f.users[1].ID, a.ID},
		{"different_account", f.users[0].ID, b.ID},
		{"different_user_and_account", f.users[1].ID, b.ID},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			v := *original
			v.UserID, v.AccountID, v.Profile = mutate.userID, mutate.account, json.RawMessage(`{"name":"stolen"}`)
			require.ErrorIs(t, f.repos[1].UpdateGrokCustomVoice(f.ctx, &v), sql.ErrNoRows)
			requireGrokVoiceUniqueViolation(t, f.repos[1].CreateGrokCustomVoice(f.ctx, &v))
		})
	}
	require.ErrorIs(t, f.repos[1].DeleteGrokCustomVoice(f.ctx, original.VoiceID, f.users[1].ID), sql.ErrNoRows)
	read, err = f.repos[0].GetGrokCustomVoice(f.ctx, original.VoiceID)
	require.NoError(t, err)
	require.Equal(t, original.UserID, read.UserID)
	require.Equal(t, original.AccountID, read.AccountID)
	require.JSONEq(t, string(original.Profile), string(read.Profile))
	original.Profile = json.RawMessage(`{"name":"updated","languages":["en","zh"]}`)
	require.NoError(t, f.repos[1].UpdateGrokCustomVoice(f.ctx, original))
	read, err = f.repos[0].GetGrokCustomVoice(f.ctx, original.VoiceID)
	require.NoError(t, err)
	require.JSONEq(t, string(original.Profile), string(read.Profile))
}

func TestGrokCustomVoicePostgresConcurrentCollisionHasSingleOwner(t *testing.T) {
	f := newGrokVoiceSQLFixture(t)
	accounts := []*service.Account{f.account(service.PlatformGrok, 0), f.account(service.PlatformGrok, 1)}
	id := f.prefix + "concurrent-collision"
	f.voiceIDs = append(f.voiceIDs, id)
	const contenders = 8
	type result struct {
		voice service.GrokCustomVoice
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, contenders)
	for i := 0; i < contenders; i++ {
		go func(i int) {
			<-start
			owner := i % 2
			v := service.GrokCustomVoice{VoiceID: id, UserID: f.users[owner].ID, AccountID: accounts[owner].ID, Profile: json.RawMessage(fmt.Sprintf(`{"contender":%d}`, i))}
			results <- result{v, f.repos[owner].CreateGrokCustomVoice(f.ctx, &v)}
		}(i)
	}
	close(start)
	completed := make([]result, 0, contenders)
	for i := 0; i < contenders; i++ {
		completed = append(completed, <-results)
	}
	var winner *service.GrokCustomVoice
	winners, collisions := 0, 0
	for _, r := range completed {
		if r.err == nil {
			winners++
			v := r.voice
			winner = &v
		} else {
			collisions++
			requireGrokVoiceUniqueViolation(t, r.err)
		}
	}
	require.Equal(t, 1, winners)
	require.Equal(t, contenders-1, collisions)
	for _, repo := range f.repos {
		v, err := repo.GetGrokCustomVoice(f.ctx, id)
		require.NoError(t, err)
		require.Equal(t, winner.UserID, v.UserID)
		require.Equal(t, winner.AccountID, v.AccountID)
		require.JSONEq(t, string(winner.Profile), string(v.Profile))
	}
}

func TestGrokCustomVoicePostgresConcurrentDeleteCannotResurrect(t *testing.T) {
	f := newGrokVoiceSQLFixture(t)
	a := f.account(service.PlatformGrok, 0)
	v := f.voice("delete-race", 0, a)
	const operations = 8
	type result struct {
		deleted bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, operations)
	for i := 0; i < operations; i++ {
		go func(i int) {
			<-start
			if i%2 == 0 {
				results <- result{true, f.repos[0].DeleteGrokCustomVoice(f.ctx, v.VoiceID, v.UserID)}
				return
			}
			update := *v
			update.Profile = json.RawMessage(fmt.Sprintf(`{"update":%d}`, i))
			results <- result{false, f.repos[1].UpdateGrokCustomVoice(f.ctx, &update)}
		}(i)
	}
	close(start)
	completed := make([]result, 0, operations)
	for i := 0; i < operations; i++ {
		completed = append(completed, <-results)
	}
	for _, r := range completed {
		if r.deleted {
			require.NoError(t, r.err)
		} else if r.err != nil {
			require.ErrorIs(t, r.err, sql.ErrNoRows)
		}
	}
	var deletedAt, retriedAt time.Time
	require.NoError(t, integrationDB.QueryRowContext(f.ctx, "SELECT deleted_at FROM grok_custom_voices WHERE voice_id = $1", v.VoiceID).Scan(&deletedAt))
	for _, repo := range f.repos {
		read, err := repo.GetGrokCustomVoice(f.ctx, v.VoiceID)
		require.NoError(t, err)
		require.NotNil(t, read)
		require.True(t, read.Deleted)
		require.Equal(t, v.UserID, read.UserID)
		require.Equal(t, v.AccountID, read.AccountID)
		require.ErrorIs(t, repo.UpdateGrokCustomVoice(f.ctx, v), sql.ErrNoRows)
		requireGrokVoiceUniqueViolation(t, repo.CreateGrokCustomVoice(f.ctx, v))
		require.NoError(t, repo.DeleteGrokCustomVoice(f.ctx, v.VoiceID, v.UserID))
	}
	require.NoError(t, integrationDB.QueryRowContext(f.ctx, "SELECT deleted_at FROM grok_custom_voices WHERE voice_id = $1", v.VoiceID).Scan(&retriedAt))
	require.True(t, deletedAt.Equal(retriedAt), "idempotent deletion must preserve the original tombstone time")
	require.Empty(t, f.list(1, 0, 0, "", 1001))
}

func TestGrokCustomVoicePostgresTombstoneSurvivesOwnerAccountDeletion(t *testing.T) {
	f := newGrokVoiceSQLFixture(t)
	a := f.account(service.PlatformGrok, 0)
	b := f.account(service.PlatformGrok, 1)
	v := f.voice("retired", 0, a)
	require.NoError(t, f.repos[0].DeleteGrokCustomVoice(f.ctx, v.VoiceID, v.UserID))
	_, err := integrationDB.ExecContext(f.ctx, "DELETE FROM account_groups WHERE account_id = $1", a.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(f.ctx, "DELETE FROM accounts WHERE id = $1", a.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(f.ctx, "DELETE FROM users WHERE id = $1", v.UserID)
	require.NoError(t, err)
	// Reapplying migrations must neither erase ownership nor make IDs reusable.
	require.NoError(t, ApplyMigrations(f.ctx, integrationDB))
	var owner, account int64
	var deletedAt time.Time
	require.NoError(t, integrationDB.QueryRowContext(f.ctx, "SELECT user_id, account_id, deleted_at FROM grok_custom_voices WHERE voice_id = $1", v.VoiceID).Scan(&owner, &account, &deletedAt))
	require.Equal(t, v.UserID, owner)
	require.Equal(t, v.AccountID, account)
	require.False(t, deletedAt.IsZero())
	claim := &service.GrokCustomVoice{VoiceID: v.VoiceID, UserID: f.users[1].ID, AccountID: b.ID}
	requireGrokVoiceUniqueViolation(t, f.repos[1].CreateGrokCustomVoice(f.ctx, claim))
	require.ErrorIs(t, f.repos[1].UpdateGrokCustomVoice(f.ctx, claim), sql.ErrNoRows)
	require.ErrorIs(t, f.repos[1].DeleteGrokCustomVoice(f.ctx, v.VoiceID, claim.UserID), sql.ErrNoRows)
	read, err := f.repos[1].GetGrokCustomVoice(f.ctx, v.VoiceID)
	require.NoError(t, err)
	require.NotNil(t, read)
	require.True(t, read.Deleted)
	require.Equal(t, v.UserID, read.UserID)
	require.Equal(t, v.AccountID, read.AccountID)
}

func TestGrokCustomVoicePostgresMigrationConstraints(t *testing.T) {
	f := newGrokVoiceSQLFixture(t)
	a := f.account(service.PlatformGrok, 0)
	for _, tc := range []struct {
		name    string
		voiceID string
		userID  int64
		account int64
	}{
		{"blank_id", " ", f.users[0].ID, a.ID},
		{"path_separator_id", f.prefix + "a/b", f.users[0].ID, a.ID},
		{"unicode_id", f.prefix + "é", f.users[0].ID, a.ID},
		{"overlength_id", strings.Repeat("x", 129), f.users[0].ID, a.ID},
		{"zero_user", f.prefix + "zero-user", 0, a.ID},
		{"negative_account", f.prefix + "negative-account", f.users[0].ID, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.voiceIDs = append(f.voiceIDs, tc.voiceID)
			// Direct SQL proves the migration enforces these constraints independently
			// of the repository's Go input validation.
			_, err := integrationDB.ExecContext(f.ctx, "INSERT INTO grok_custom_voices (voice_id, user_id, account_id) VALUES ($1, $2, $3)", tc.voiceID, tc.userID, tc.account)
			var pgErr *pq.Error
			require.ErrorAs(t, err, &pgErr)
			require.Equal(t, pq.ErrorCode("23514"), pgErr.Code)
		})
	}
	t.Run("profile_storage_bound", func(t *testing.T) {
		id := f.prefix + "oversized-profile"
		f.voiceIDs = append(f.voiceIDs, id)
		profile := `{"name":"` + strings.Repeat("x", 16384) + `"}`
		_, err := integrationDB.ExecContext(f.ctx, "INSERT INTO grok_custom_voices (voice_id, user_id, account_id, profile) VALUES ($1, $2, $3, $4::jsonb)", id, f.users[0].ID, a.ID, profile)
		var pgErr *pq.Error
		require.ErrorAs(t, err, &pgErr)
		require.Equal(t, pq.ErrorCode("23514"), pgErr.Code)
	})
	t.Run("empty_profile_defaults_to_object", func(t *testing.T) {
		v := &service.GrokCustomVoice{VoiceID: f.prefix + "default-profile", UserID: f.users[0].ID, AccountID: a.ID}
		f.voiceIDs = append(f.voiceIDs, v.VoiceID)
		require.NoError(t, f.repos[0].CreateGrokCustomVoice(f.ctx, v))
		read, err := f.repos[1].GetGrokCustomVoice(f.ctx, v.VoiceID)
		require.NoError(t, err)
		require.JSONEq(t, `{}`, string(read.Profile))
	})
}
