package repository

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestResponsesRequestClaimIsAtomicAndFenced(t *testing.T) {
	server := miniredis.RunT(t)
	a := redis.NewClient(&redis.Options{Addr: server.Addr()})
	b := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	stores := []*gatewayCache{{rdb: a}, {rdb: b}}
	key := strings.Repeat("a", 64)
	claim := []byte(`{"state":"processing","owner":"first"}`)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, won, err := stores[i%2].ClaimResponsesRequest(context.Background(), key, claim, time.Hour)
			if err != nil {
				t.Error(err)
			}
			if won {
				winners.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, int32(1), winners.Load())
	done := []byte(`{"state":"done","response":"synthetic"}`)
	ok, err := stores[1].CompleteResponsesRequest(context.Background(), key, []byte("wrong-owner"), done, time.Hour)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = stores[0].CompleteResponsesRequest(context.Background(), key, claim, done, time.Hour)
	require.NoError(t, err)
	require.True(t, ok)
	existing, won, err := stores[1].ClaimResponsesRequest(context.Background(), key, claim, time.Hour)
	require.NoError(t, err)
	require.False(t, won)
	require.Equal(t, done, existing)
	// A stale completion must not overwrite a later owner after expiration.
	server.FastForward(2 * time.Hour)
	newer := []byte(`{"state":"processing","owner":"second"}`)
	_, won, err = stores[1].ClaimResponsesRequest(context.Background(), key, newer, time.Hour)
	require.NoError(t, err)
	require.True(t, won)
	ok, err = stores[0].CompleteResponsesRequest(context.Background(), key, claim, done, time.Hour)
	require.NoError(t, err)
	require.False(t, ok)
}
