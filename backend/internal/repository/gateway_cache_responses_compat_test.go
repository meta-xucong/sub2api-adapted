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

func TestGatewayCacheResponsesCompatStateSharedAcrossInstances(t *testing.T) {
	mr := miniredis.RunT(t)
	firstClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	secondClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	first, ok := NewGatewayCache(firstClient).(service.ResponsesCompatStateCache)
	require.True(t, ok)
	second, ok := NewGatewayCache(secondClient).(service.ResponsesCompatStateCache)
	require.True(t, ok)
	ctx := context.Background()

	if payload, err := second.GetResponsesCompatState(ctx, "state-key"); err != nil {
		t.Fatal(err)
	} else {
		require.Nil(t, payload)
	}
	payload := []byte(`{"response_id":"resp_shared","history_input":[{"type":"reasoning","summary":[{"text":"thinking"}]}]}`)
	require.NoError(t, first.SetResponsesCompatState(ctx, "state-key", payload, time.Minute))
	got, err := second.GetResponsesCompatState(ctx, "state-key")
	require.NoError(t, err)
	require.JSONEq(t, string(payload), string(got))
	require.NoError(t, second.DeleteResponsesCompatState(ctx, "state-key"))
	got, err = first.GetResponsesCompatState(ctx, "state-key")
	require.NoError(t, err)
	require.Nil(t, got)
}
