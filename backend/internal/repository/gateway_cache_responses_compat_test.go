package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheResponsesCompatStateIsSharedAcrossInstances(t *testing.T) {
	redisServer := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	first := NewGatewayCache(rdb)
	second := NewGatewayCache(rdb)

	firstState, ok := first.(service.ResponsesCompatStateCache)
	require.True(t, ok)
	secondState, ok := second.(service.ResponsesCompatStateCache)
	require.True(t, ok)

	ctx := context.Background()
	payload := []byte(`{"response_id":"resp_shared","history_input":[{"type":"message"}]}`)
	require.NoError(t, firstState.SetResponsesCompatState(ctx, "session-digest", payload, time.Minute))

	got, err := secondState.GetResponsesCompatState(ctx, "session-digest")
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(got))

	require.NoError(t, secondState.DeleteResponsesCompatState(ctx, "session-digest"))
	got, err = firstState.GetResponsesCompatState(ctx, "session-digest")
	require.NoError(t, err)
	require.Nil(t, got)
}
