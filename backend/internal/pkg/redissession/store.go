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

var ErrNotConfigured = errors.New("redis session store not configured")

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
	raw, err := s.rdb.Get(ctx, s.dataKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, err
	}
	return true, nil
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
