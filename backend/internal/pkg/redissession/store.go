// Package redissession provides a multi-instance OAuth session backend.
package redissession

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrNotConfigured  = errors.New("redis session store not configured")
	ErrExpired        = errors.New("redis session deadline expired")
	ErrPayloadChanged = errors.New("redis session payload changed")
)

// Store persists JSON sessions and single-use markers under one namespace.
type Store struct {
	rdb    *redis.Client
	prefix string
	ttl    time.Duration
}

func New(rdb *redis.Client, prefix string, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "oauth:session"
	}
	if !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	return &Store{rdb: rdb, prefix: prefix, ttl: ttl}
}

func (s *Store) dataKey(id string) string { return s.prefix + strings.TrimSpace(id) }
func (s *Store) usedKey(id string) string { return s.prefix + "used:" + strings.TrimSpace(id) }

func (s *Store) Set(ctx context.Context, id string, value any) error {
	if s == nil || s.rdb == nil {
		return ErrNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("session id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, s.dataKey(id), raw, s.ttl).Err()
}

func (s *Store) Get(ctx context.Context, id string, dest any) (bool, error) {
	raw, found, err := s.GetRaw(ctx, id)
	if err != nil || !found {
		return found, err
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, err
	}
	return true, nil
}

// GetRaw returns the stored bytes unchanged so a later claim can be bound to
// the exact payload the caller validated, including unknown JSON fields.
func (s *Store) GetRaw(ctx context.Context, id string) ([]byte, bool, error) {
	if s == nil || s.rdb == nil {
		return nil, false, ErrNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := s.rdb.Get(ctx, s.dataKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	if s == nil || s.rdb == nil {
		return ErrNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.rdb.Del(ctx, s.dataKey(id), s.usedKey(id)).Err()
}

// Keep the payload available for existing readers while atomically claiming it.
// A full configured TTL is a bounded tombstone; PTTL also covers sessions written
// by an instance with a longer TTL. Never let the marker expire before the data.
// This store uses a standalone redis.Client; these keys are not cluster-tagged.
var consumeScript = redis.NewScript(`
local remaining = redis.call('PTTL', KEYS[1])
if remaining <= 0 then
  return 0
end
local ttl = math.max(remaining, tonumber(ARGV[1]))
if redis.call('SET', KEYS[2], '1', 'NX', 'PX', ttl) then
  return 1
end
return 0
`)

// TryConsume returns true only for the first claim while the session exists.
func (s *Store) TryConsume(ctx context.Context, id string) (bool, error) {
	if s == nil || s.rdb == nil {
		return false, ErrNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	claimed, err := consumeScript.Run(ctx, s.rdb,
		[]string{s.dataKey(id), s.usedKey(id)}, max(s.ttl.Milliseconds(), 1)).Int64()
	return err == nil && claimed == 1, err
}

// Deadline-aware claims are separate from the existing OAuth claim contract.
// The application validates the JSON; Lua compares its original bytes rather
// than reparsing timestamps or trusting a payload replaced since that read.
var consumeBeforeScript = redis.NewScript(`
local payload = redis.call('GET', KEYS[1])
if not payload then
  return 0
end
if payload ~= ARGV[1] then
  return -2
end
local remaining = redis.call('PTTL', KEYS[1])
if remaining <= 0 then
  return 0
end
local now = redis.call('TIME')
local nowMillis = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
if nowMillis >= tonumber(ARGV[2]) then
  return -1
end
local ttl = math.max(remaining, tonumber(ARGV[3]))
if redis.call('SET', KEYS[2], '1', 'NX', 'PX', ttl) then
  return 1
end
return 0
`)

// TryConsumeBefore claims the validated payload only before its logical
// deadline, using Redis TIME in the same atomic operation as the used marker.
// Issuers and Redis must have synchronized wall clocks. UnixMilli rounds the
// deadline down, conservatively rejecting up to one millisecond early.
func (s *Store) TryConsumeBefore(ctx context.Context, id string, payload []byte, deadline time.Time) (bool, error) {
	if s == nil || s.rdb == nil {
		return false, ErrNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" || len(payload) == 0 {
		return false, nil
	}
	if deadline.IsZero() {
		return false, ErrExpired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	claimed, err := consumeBeforeScript.Run(ctx, s.rdb,
		[]string{s.dataKey(id), s.usedKey(id)}, payload, deadline.UnixMilli(), max(s.ttl.Milliseconds(), 1)).Int64()
	if err != nil {
		return false, err
	}
	switch claimed {
	case -1:
		return false, ErrExpired
	case -2:
		return false, ErrPayloadChanged
	default:
		return claimed == 1, nil
	}
}
