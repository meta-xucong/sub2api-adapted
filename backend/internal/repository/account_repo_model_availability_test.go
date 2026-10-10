package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestMergeCurrentAccountExtraKeysUsesLockedDatabaseValues(t *testing.T) {
	stale := map[string]any{
		service.UpstreamModelPolicyExtraKey: "manual",
		service.UpstreamModelAvailabilityExtraKey: map[string]any{
			"status": "old",
		},
		"unrelated": "preserved",
	}
	current := map[string][]byte{
		service.UpstreamModelPolicyExtraKey:       json.RawMessage(`"follow_upstream"`),
		service.UpstreamModelAvailabilityExtraKey: json.RawMessage(`{"status":"fresh"}`),
	}
	require.NoError(t, mergeCurrentAccountExtraKeys(stale, current))
	require.Equal(t, service.UpstreamModelPolicyFollow, stale[service.UpstreamModelPolicyExtraKey])
	require.Equal(t, map[string]any{"status": "fresh"}, stale[service.UpstreamModelAvailabilityExtraKey])
	require.Equal(t, "preserved", stale["unrelated"])
}

func TestMergeCurrentAccountExtraKeysRemovesValuesDeletedAfterStaleRead(t *testing.T) {
	stale := map[string]any{
		service.UpstreamModelPolicyExtraKey: "follow_upstream",
		service.UpstreamModelAvailabilityExtraKey: map[string]any{
			"status": "stale",
		},
	}
	require.NoError(t, mergeCurrentAccountExtraKeys(stale, map[string][]byte{
		service.UpstreamModelPolicyExtraKey:       []byte("null"),
		service.UpstreamModelAvailabilityExtraKey: nil,
	}))
	require.NotContains(t, stale, service.UpstreamModelPolicyExtraKey)
	require.NotContains(t, stale, service.UpstreamModelAvailabilityExtraKey)
}

func TestListModelAvailabilityCandidates_GroupQueryIgnoresTransientState(t *testing.T) {
	var capturedSQL string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcher{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)

	mock.ExpectQuery("model availability candidates").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	groupID := int64(42)
	accounts, err := repo.ListModelAvailabilityCandidates(
		context.Background(),
		&groupID,
		[]string{service.PlatformAnthropic},
		false,
	)
	require.NoError(t, err)
	require.Empty(t, accounts)
	require.NoError(t, mock.ExpectationsWereMet())

	normalized := normalizeSQLWhitespace(capturedSQL)
	_, whereClause, found := strings.Cut(normalized, " WHERE ")
	require.True(t, found, "expected WHERE clause in query: %s", normalized)
	whereClause, _, _ = strings.Cut(whereClause, " ORDER BY ")
	for _, configuredPredicate := range []string{"group_id", "status", "schedulable", "platform"} {
		require.Contains(t, whereClause, configuredPredicate)
	}
	for _, transientPredicate := range []string{
		"rate_limit_reset_at",
		"overload_until",
		"temp_unschedulable_until",
		"expires_at",
		"auto_pause_on_expired",
	} {
		require.NotContains(t, whereClause, transientPredicate, "configured-state diagnosis must not filter transient predicate %q", transientPredicate)
	}
}
