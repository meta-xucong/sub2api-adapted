//go:build unit

package redissession

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestStoreRoundTripAndSingleUse(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := New(rdb, "oauth:test", time.Minute)
	ctx := context.Background()

	require.NoError(t, store.Set(ctx, "sid", map[string]string{"state": "state"}))
	var got map[string]string
	ok, err := store.Get(ctx, "sid", &got)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "state", got["state"])

	ok, err = store.TryConsume(ctx, "sid")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = store.TryConsume(ctx, "sid")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, store.Delete(ctx, "sid"))
	ok, err = store.Get(ctx, "sid", &got)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestStoreConsumeMarkerOutlivesFractionalDataTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	store := New(rdb, "oauth:fractional", 120*time.Second)
	ctx := context.Background()
	require.NoError(t, store.Set(ctx, "sid", map[string]string{"state": "test"}))
	mr.FastForward(800 * time.Millisecond)
	before := mr.TTL(store.dataKey("sid"))
	claimed, err := store.TryConsume(ctx, "sid")
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, before, mr.TTL(store.dataKey("sid")), "claim must not extend the payload")
	require.GreaterOrEqual(t, mr.TTL(store.usedKey("sid")), before)
	mr.FastForward(119*time.Second + 50*time.Millisecond)
	require.True(t, mr.Exists(store.dataKey("sid")), "payload remains valid for 150 ms")
	claimed, err = store.TryConsume(ctx, "sid")
	require.NoError(t, err)
	require.False(t, claimed, "a fractional-second boundary must not reopen a claim")
	var got map[string]string
	found, err := store.Get(ctx, "sid", &got)
	require.NoError(t, err)
	require.True(t, found, "existing Get-after-claim contract is preserved")
	require.Equal(t, "test", got["state"])
}

func TestStoreConcurrentConsumeAcrossInstances(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	stores := []*Store{New(rdb, "oauth:concurrent", time.Minute), New(rdb, "oauth:concurrent", time.Second)}
	require.NoError(t, stores[0].Set(context.Background(), "sid", map[string]string{"state": "test"}))
	results := make(chan bool, 32)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			claimed, err := store.TryConsume(context.Background(), "sid")
			if err != nil {
				t.Errorf("claim failed: %v", err)
			}
			results <- claimed
		}(stores[i%len(stores)])
	}
	wg.Wait()
	close(results)
	successes := 0
	for claimed := range results {
		if claimed {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	require.GreaterOrEqual(t, mr.TTL(stores[0].usedKey("sid")), mr.TTL(stores[0].dataKey("sid")))
}

func TestStoreConsumeRejectsMissingExpiredAndPersistentData(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	store := New(rdb, "oauth:invalid", time.Second)
	ctx := context.Background()
	require.NoError(t, store.Set(ctx, "expired", "test"))
	mr.FastForward(time.Second)
	require.NoError(t, rdb.Set(ctx, store.dataKey("persistent"), `"test"`, 0).Err())
	for _, id := range []string{"missing", "expired", "persistent", " "} {
		claimed, err := store.TryConsume(ctx, id)
		require.NoError(t, err, id)
		require.False(t, claimed, id)
		require.False(t, mr.Exists(store.usedKey(id)), id)
	}
}

func TestStoreConsumeFailsClosedOnRedisErrors(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	store := New(rdb, "oauth:errors", time.Minute)
	require.NoError(t, store.Set(context.Background(), "sid", "test"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	claimed, err := store.TryConsume(ctx, "sid")
	require.Error(t, err)
	require.False(t, claimed)
	require.False(t, mr.Exists(store.usedKey("sid")))
	mr.Close()
	claimed, err = store.TryConsume(context.Background(), "sid")
	require.Error(t, err)
	require.False(t, claimed)
}

func TestStoreConsumeBeforeUsesRedisDeadlineAndExactPayload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		offset  time.Duration
		changed bool
		wantErr error
	}{
		{name: "before deadline", offset: -time.Millisecond},
		{name: "at deadline", wantErr: ErrExpired},
		{name: "after deadline", offset: time.Second, wantErr: ErrExpired},
		{name: "changed payload", offset: -time.Second, changed: true, wantErr: ErrPayloadChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
			t.Cleanup(func() { _ = rdb.Close() })
			store := New(rdb, "deadline:test", time.Minute)
			ctx := context.Background()
			deadline := time.Date(2026, 10, 19, 12, 0, 30, 0, time.UTC)
			mr.SetTime(deadline.Add(-time.Second))
			original := []byte(`{ "extra": "preserved", "state": "original" }`)
			require.NoError(t, rdb.Set(ctx, store.dataKey("sid"), original, time.Minute).Err())
			raw, found, err := store.GetRaw(ctx, "sid")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, original, raw)
			if tc.changed {
				require.NoError(t, rdb.Set(ctx, store.dataKey("sid"), `{"state":"replacement"}`, time.Minute).Err())
			}
			// The payload remains physically live while its logical deadline passes.
			mr.SetTime(deadline.Add(tc.offset))
			claimed, err := store.TryConsumeBefore(ctx, "sid", raw, deadline)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.False(t, claimed)
				require.False(t, mr.Exists(store.usedKey("sid")))
			} else {
				require.NoError(t, err)
				require.True(t, claimed)
				require.GreaterOrEqual(t, mr.TTL(store.usedKey("sid")), mr.TTL(store.dataKey("sid")))
				claimed, err = store.TryConsumeBefore(ctx, "sid", raw, deadline)
				require.NoError(t, err)
				require.False(t, claimed)
			}
			require.True(t, mr.Exists(store.dataKey("sid")), "claim does not remove the payload")
		})
	}
}

func TestStoreConsumeBeforeRoundsDeadlineConservatively(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	store := New(rdb, "deadline:precision", time.Minute)
	ctx := context.Background()
	deadline := time.Date(2026, 10, 19, 12, 0, 30, 999999, time.UTC)
	mr.SetTime(deadline.Truncate(time.Millisecond))
	require.NoError(t, store.Set(ctx, "sid", "test"))
	raw, found, err := store.GetRaw(ctx, "sid")
	require.NoError(t, err)
	require.True(t, found)
	claimed, err := store.TryConsumeBefore(ctx, "sid", raw, deadline)
	require.ErrorIs(t, err, ErrExpired)
	require.False(t, claimed)
	require.False(t, mr.Exists(store.usedKey("sid")))
}

func TestStoreConsumeBeforeRejectsUnavailableOrUnboundedData(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	store := New(rdb, "deadline:invalid", time.Minute)
	ctx := context.Background()
	deadline := time.Now().Add(time.Minute)
	payload := []byte(`"test"`)
	require.NoError(t, rdb.Set(ctx, store.dataKey("persistent"), payload, 0).Err())
	for _, id := range []string{"missing", "persistent", " "} {
		claimed, err := store.TryConsumeBefore(ctx, id, payload, deadline)
		require.NoError(t, err, id)
		require.False(t, claimed, id)
		require.False(t, mr.Exists(store.usedKey(id)), id)
	}
	require.NoError(t, store.Set(ctx, "sid", "test"))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	claimed, err := store.TryConsumeBefore(canceled, "sid", payload, deadline)
	require.Error(t, err)
	require.False(t, claimed)
	require.False(t, mr.Exists(store.usedKey("sid")))
	mr.Close()
	claimed, err = store.TryConsumeBefore(ctx, "sid", payload, deadline)
	require.Error(t, err)
	require.False(t, claimed)
}
