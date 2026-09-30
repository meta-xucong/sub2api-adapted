package veyra

import (
	"context"
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

func TestRedisTicketStoreFailsClosedWithoutRedis(t *testing.T) {
	store := NewRedisTicketStore(nil, "veyra:test-ticket", time.Minute)
	_, err := store.Create(context.Background(), 42, "alchemy", time.Minute)
	require.Error(t, err)
}
