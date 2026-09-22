package veyra

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemoryTicketStoreConsumesOnce(t *testing.T) {
	store := NewMemoryTicketStore()
	now := time.Date(2026, 6, 11, 1, 2, 3, 0, time.UTC)
	store.now = func() time.Time { return now }

	ticket, err := store.Create(context.Background(), 42, "alchemy", time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(42), ticket.UserID)
	require.Equal(t, PortalIntentAlchemy, ticket.Intent)
	require.NotEmpty(t, ticket.Token)

	consumed, err := store.Consume(context.Background(), ticket.Token, now.Add(10*time.Second))
	require.NoError(t, err)
	require.Equal(t, ticket.Token, consumed.Token)
	require.NotNil(t, consumed.UsedAt)

	_, err = store.Consume(context.Background(), ticket.Token, now.Add(20*time.Second))
	require.ErrorIs(t, err, ErrTicketInvalid)
}

func TestMemoryTicketStoreExpires(t *testing.T) {
	store := NewMemoryTicketStore()
	now := time.Date(2026, 6, 11, 1, 2, 3, 0, time.UTC)
	store.now = func() time.Time { return now }

	ticket, err := store.Create(context.Background(), 42, "sub2api", time.Minute)
	require.NoError(t, err)

	_, err = store.Consume(context.Background(), ticket.Token, now.Add(time.Minute))
	require.ErrorIs(t, err, ErrTicketExpired)
}

func TestTicketTTL(t *testing.T) {
	require.Equal(t, 120*time.Second, TicketTTL(0))
	require.Equal(t, 30*time.Second, TicketTTL(30))
	require.Equal(t, 600*time.Second, TicketTTL(999))
}
