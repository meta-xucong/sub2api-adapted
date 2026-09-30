package veyra

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/redissession"
	"github.com/redis/go-redis/v9"
)

// RedisTicketStore keeps login tickets available across gateway instances and
// process restarts. It deliberately fails closed when Redis is unavailable;
// falling back to process-local memory would make a ticket issued by one
// instance unusable (or unexpectedly reusable) on another instance.
type RedisTicketStore struct {
	sessions *redissession.Store
}

func NewRedisTicketStore(rdb *redis.Client, prefix string, ttl time.Duration) *RedisTicketStore {
	return &RedisTicketStore{
		sessions: redissession.New(rdb, prefix, ttl),
	}
}

func (s *RedisTicketStore) Create(ctx context.Context, userID int64, intent string, ttl time.Duration) (*LoginTicket, error) {
	if s == nil || s.sessions == nil || userID <= 0 || ttl <= 0 {
		return nil, ErrTicketInvalid
	}
	token, err := randomTicketToken()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	ticket := &LoginTicket{
		Token:     token,
		UserID:    userID,
		Intent:    NormalizePortalIntent(intent),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if err := s.sessions.Set(ctx, token, ticket); err != nil {
		return nil, err
	}
	return cloneTicket(ticket), nil
}

func (s *RedisTicketStore) Consume(ctx context.Context, token string, expectedIntent string, now time.Time) (*LoginTicket, error) {
	if s == nil || s.sessions == nil {
		return nil, ErrTicketInvalid
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrTicketInvalid
	}
	var ticket LoginTicket
	found, err := s.sessions.Get(ctx, token, &ticket)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrTicketInvalid
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !ticket.ExpiresAt.After(now) {
		_ = s.sessions.Delete(ctx, token)
		return nil, ErrTicketExpired
	}
	if expected := NormalizePortalIntent(expectedIntent); expectedIntent != "" && ticket.Intent != expected {
		return nil, ErrTicketIntent
	}
	claimed, err := s.sessions.TryConsume(ctx, token)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, ErrTicketUsed
	}
	usedAt := now
	ticket.UsedAt = &usedAt
	return cloneTicket(&ticket), nil
}
