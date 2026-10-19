package veyra

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRedisTicketStoreSurvivesAnotherProcessAndConsumesOnce(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	storeA := NewRedisTicketStore(rdb, "veyra:test-ticket", time.Minute)
	storeB := NewRedisTicketStore(rdb, "veyra:test-ticket", time.Minute)

	ticket, err := storeA.Create(context.Background(), 42, "alchemy", time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, ticket.Token)

	consumed, err := storeB.Consume(context.Background(), ticket.Token, PortalIntentAlchemy, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, ticket.Token, consumed.Token)
	require.Equal(t, int64(42), consumed.UserID)
	require.NotNil(t, consumed.UsedAt)

	_, err = storeA.Consume(context.Background(), ticket.Token, PortalIntentAlchemy, time.Now().UTC())
	require.ErrorIs(t, err, ErrTicketUsed)
}

func TestRedisTicketStoreRejectsReplayAtFractionalExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	storeA := NewRedisTicketStore(rdb, "veyra:fractional", 120*time.Second)
	storeB := NewRedisTicketStore(rdb, "veyra:fractional", 120*time.Second)
	ticket, err := storeA.Create(context.Background(), 42, PortalIntentVideo, 120*time.Second)
	require.NoError(t, err)
	mr.FastForward(800 * time.Millisecond)
	_, err = storeA.Consume(context.Background(), ticket.Token, PortalIntentVideo, ticket.CreatedAt.Add(800*time.Millisecond))
	require.NoError(t, err)
	mr.FastForward(119*time.Second + 50*time.Millisecond)
	_, err = storeB.Consume(context.Background(), ticket.Token, PortalIntentVideo, ticket.CreatedAt.Add(119850*time.Millisecond))
	require.ErrorIs(t, err, ErrTicketUsed)
}

func TestRedisTicketStoreConcurrentExchangeConsumesExactlyOnce(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	stores := []*RedisTicketStore{
		NewRedisTicketStore(rdb, "veyra:concurrent", time.Minute),
		NewRedisTicketStore(rdb, "veyra:concurrent", time.Minute),
	}
	ticket, err := stores[0].Create(context.Background(), 42, PortalIntentVideo, time.Minute)
	require.NoError(t, err)
	results := make(chan error, 32)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(store *RedisTicketStore) {
			defer wg.Done()
			_, err := store.Consume(context.Background(), ticket.Token, PortalIntentVideo, ticket.CreatedAt.Add(time.Second))
			results <- err
		}(stores[i%len(stores)])
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrTicketUsed)
		}
	}
	require.Equal(t, 1, successes)
}

func TestRedisTicketStoreConsumeFailsClosedWhenRedisStops(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	store := NewRedisTicketStore(rdb, "veyra:unavailable", time.Minute)
	ticket, err := store.Create(context.Background(), 42, PortalIntentVideo, time.Minute)
	require.NoError(t, err)
	mr.Close()
	consumed, err := store.Consume(context.Background(), ticket.Token, PortalIntentVideo, ticket.CreatedAt.Add(time.Second))
	require.Error(t, err)
	require.Nil(t, consumed)
}

func TestRedisTicketStoreAllowedIntentsRejectsBeforeConsuming(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewRedisTicketStore(rdb, "veyra:test-ticket-allowed", time.Minute)

	ticket, err := store.Create(context.Background(), 42, PortalIntentVideo, time.Minute)
	require.NoError(t, err)
	_, err = store.ConsumeAllowed(context.Background(), ticket.Token, []string{PortalIntentAlchemy}, "", time.Now().UTC())
	require.ErrorIs(t, err, ErrTicketIntent)

	consumed, err := store.ConsumeAllowed(context.Background(), ticket.Token, []string{PortalIntentAlchemy, PortalIntentVideo}, "", time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, PortalIntentVideo, consumed.Intent)
}

func TestRedisTicketStoreFailsClosedWithoutRedis(t *testing.T) {
	store := NewRedisTicketStore(nil, "veyra:test-ticket", time.Minute)
	_, err := store.Create(context.Background(), 42, "alchemy", time.Minute)
	require.Error(t, err)
}
