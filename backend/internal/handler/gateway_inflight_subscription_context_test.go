package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestReserveInflightBalance_SubscriptionPreservesRouteDecision(t *testing.T) {
	estimator := &routePricingReservationEstimator{decision: routePricingTestDecision()}
	apiKey := &service.APIKey{Group: &service.Group{SubscriptionType: service.SubscriptionTypeSubscription}}
	ctx, done, err := reserveInflightBalanceCtx(context.Background(), nil, estimator, apiKey, &service.UserSubscription{}, tokenInflightEstimate("m", nil))
	require.NoError(t, err)
	defer done()
	require.True(t, estimator.prepared)
	decision, ok := service.UnifiedGatewayRoutePricingDecisionFromContext(ctx)
	require.True(t, ok, "subscription requests must retain the prepared pricing decision")
	require.True(t, decision.Allowed)
	require.Equal(t, estimator.decision.Revision, decision.Revision)
	require.Equal(t, estimator.decision.Entries, decision.Entries)
}
