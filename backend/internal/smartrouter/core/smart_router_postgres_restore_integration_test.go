//go:build integration

package core_test

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestSmartRouterHealthRestoreThroughPostgresLedgerInChildProcess(t *testing.T) {
	dsn := os.Getenv("SUB2API_SMART_ROUTER_RESTORE_TEST_DSN")
	if dsn == "" {
		t.Skip("this probe is run by the parent repository integration test")
	}
	require.NotEmpty(t, dsn)
	laneID := os.Getenv("SUB2API_SMART_ROUTER_RESTORE_TEST_LANE")
	model := os.Getenv("SUB2API_SMART_ROUTER_RESTORE_TEST_MODEL")
	sourceGroup := os.Getenv("SUB2API_SMART_ROUTER_RESTORE_TEST_SOURCE_GROUP")
	accountID, err := strconv.ParseInt(os.Getenv("SUB2API_SMART_ROUTER_RESTORE_TEST_ACCOUNT"), 10, 64)
	require.NoError(t, err)

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, db.Ping())

	cfg := &config.Config{}
	cfg.Gateway.SmartRouter.Enabled = true
	svc := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, nil,
		cfg,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	svc.SetSmartRouterHealthLedger(repository.NewSmartRouterHealthRepository(db))
	account := &service.Account{
		ID:       accountID,
		Name:     "phase2-postgres-restore",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Extra: map[string]any{
			"smart_router": map[string]any{
				"lane_id":      laneID,
				"source_group": sourceGroup,
			},
		},
	}

	// The persisted row starts with two failures. A further transient compact
	// failure must be observed from that restored state, not from a fresh tracker.
	svc.ReportSmartRouterCompactCalibrationResult(account, model, nil, &service.UpstreamFailoverError{
		StatusCode: http.StatusServiceUnavailable,
	}, 150)

	states, err := repository.NewSmartRouterHealthRepository(db).LoadStates(context.Background())
	require.NoError(t, err)
	var restored *service.SmartRouterHealthState
	for i := range states {
		if states[i].LaneID == laneID && states[i].ModelFamily == model {
			state := states[i]
			restored = &state
			break
		}
	}
	require.NotNil(t, restored)
	require.Equal(t, accountID, restored.AccountID)
	require.Equal(t, sourceGroup, restored.SourceGroup)
	require.Equal(t, 3, restored.Snapshot.ConsecutiveFailures,
		"the service must load the persisted two-failure state before recording the child-process failure")
	t.Log("fresh-process repository-backed state restored and extended")
}
