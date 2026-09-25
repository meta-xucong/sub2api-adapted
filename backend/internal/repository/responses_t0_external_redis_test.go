package repository

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Opt-in separate-process check against a disposable, loopback-only Redis.
func TestT0ResponsesIdempotencyExternalRedis(t *testing.T) {
	address := os.Getenv("T0_REDIS_ADDR")
	if address == "" {
		t.Skip("requires an isolated loopback Redis")
	}
	host, _, err := net.SplitHostPort(address)
	require.NoError(t, err)
	ip := net.ParseIP(host)
	require.NotNil(t, ip)
	require.True(t, ip.IsLoopback())
	rdb := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &gatewayCache{rdb: rdb}
	key := strings.Repeat("e", 64)
	claim := []byte(`{"owner":"synthetic-owner","state":"processing"}`)
	result := []byte(`{"state":"done","fixture":"cross-process"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch os.Getenv("T0_REDIS_PHASE") {
	case "write":
		_, acquired, err := cache.ClaimResponsesRequest(ctx, key, claim, time.Hour)
		require.NoError(t, err)
		require.True(t, acquired)
		completed, err := cache.CompleteResponsesRequest(ctx, key, []byte("wrong-owner"), result, time.Hour)
		require.NoError(t, err)
		require.False(t, completed)
		completed, err = cache.CompleteResponsesRequest(ctx, key, claim, result, time.Hour)
		require.NoError(t, err)
		require.True(t, completed)
	case "read":
		existing, acquired, err := cache.ClaimResponsesRequest(ctx, key, claim, time.Hour)
		require.NoError(t, err)
		require.False(t, acquired)
		require.Equal(t, result, existing)
		completed, err := cache.CompleteResponsesRequest(ctx, key, claim, []byte("overwrite"), time.Hour)
		require.NoError(t, err)
		require.False(t, completed)
		require.NoError(t, rdb.Del(ctx, responsesIdempotencyPrefix+key).Err())
	default:
		t.Fatal("T0_REDIS_PHASE must be write or read")
	}
}
