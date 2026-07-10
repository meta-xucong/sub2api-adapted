package veyra

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	ErrTicketInvalid = errors.New("veyra ticket: invalid")
	ErrTicketExpired = errors.New("veyra ticket: expired")
	ErrTicketUsed    = errors.New("veyra ticket: already used")
)

type LoginTicket struct {
	Token     string
	UserID    int64
	Intent    string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

type TicketStore interface {
	Create(ctx context.Context, userID int64, intent string, ttl time.Duration) (*LoginTicket, error)
	Consume(ctx context.Context, token string, now time.Time) (*LoginTicket, error)
}

type MemoryTicketStore struct {
	mu      sync.Mutex
	tickets map[string]*LoginTicket
	now     func() time.Time
}

func NewMemoryTicketStore() *MemoryTicketStore {
	return &MemoryTicketStore{
		tickets: make(map[string]*LoginTicket),
		now:     time.Now,
	}
}

func (s *MemoryTicketStore) Create(_ context.Context, userID int64, intent string, ttl time.Duration) (*LoginTicket, error) {
	if s == nil || userID <= 0 || ttl <= 0 {
		return nil, ErrTicketInvalid
	}
	token, err := randomTicketToken()
	if err != nil {
		return nil, err
	}
	now := s.currentTime()
	ticket := &LoginTicket{
		Token:     token,
		UserID:    userID,
		Intent:    NormalizePortalIntent(intent),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(now)
	s.tickets[token] = ticket
	return cloneTicket(ticket), nil
}

func (s *MemoryTicketStore) Consume(_ context.Context, token string, now time.Time) (*LoginTicket, error) {
	if s == nil {
		return nil, ErrTicketInvalid
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrTicketInvalid
	}
	if now.IsZero() {
		now = s.currentTime()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.tickets[token]
	if !ok {
		return nil, ErrTicketInvalid
	}
	if ticket.UsedAt != nil {
		return nil, ErrTicketUsed
	}
	if !ticket.ExpiresAt.After(now) {
		delete(s.tickets, token)
		return nil, ErrTicketExpired
	}
	usedAt := now
	ticket.UsedAt = &usedAt
	delete(s.tickets, token)
	return cloneTicket(ticket), nil
}

func (s *MemoryTicketStore) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *MemoryTicketStore) cleanupLocked(now time.Time) {
	for token, ticket := range s.tickets {
		if ticket == nil || ticket.UsedAt != nil || !ticket.ExpiresAt.After(now) {
			delete(s.tickets, token)
		}
	}
}

func cloneTicket(ticket *LoginTicket) *LoginTicket {
	if ticket == nil {
		return nil
	}
	clone := *ticket
	if ticket.UsedAt != nil {
		used := *ticket.UsedAt
		clone.UsedAt = &used
	}
	return &clone
}

func randomTicketToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func TicketTTL(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = 120
	}
	if seconds > 600 {
		seconds = 600
	}
	return time.Duration(seconds) * time.Second
}
