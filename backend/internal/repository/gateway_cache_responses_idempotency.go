package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const responsesIdempotencyPrefix = "responses_idempotency:v1:"

var claimResponsesRequestScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current then return {0, current} end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return {1, ''}
`)
var completeResponsesRequestScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if not current or current ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return 1
`)

func validResponsesRequestStoreArgs(key string, payload []byte, ttl time.Duration) bool {
	if len(key) != 64 || len(payload) == 0 || ttl < time.Second {
		return false
	}
	for _, b := range key {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

func (c *gatewayCache) ClaimResponsesRequest(ctx context.Context, key string, claim []byte, ttl time.Duration) ([]byte, bool, error) {
	if c == nil || c.rdb == nil {
		return nil, false, errors.New("Responses request store unavailable")
	}
	if !validResponsesRequestStoreArgs(key, claim, ttl) {
		return nil, false, errors.New("invalid Responses request claim")
	}
	values, err := claimResponsesRequestScript.Run(ctx, c.rdb, []string{responsesIdempotencyPrefix + key}, claim, ttl.Milliseconds()).Slice()
	if err != nil {
		return nil, false, err
	}
	if len(values) != 2 {
		return nil, false, errors.New("invalid Responses claim result")
	}
	won, ok := values[0].(int64)
	payload, valid := values[1].(string)
	if !ok || !valid || (won != 0 && won != 1) {
		return nil, false, errors.New("invalid Responses claim result")
	}
	return []byte(payload), won == 1, nil
}

func (c *gatewayCache) CompleteResponsesRequest(ctx context.Context, key string, claim, result []byte, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, errors.New("Responses request store unavailable")
	}
	if !validResponsesRequestStoreArgs(key, result, ttl) || len(claim) == 0 {
		return false, errors.New("invalid Responses request completion")
	}
	n, err := completeResponsesRequestScript.Run(ctx, c.rdb, []string{responsesIdempotencyPrefix + key}, claim, result, ttl.Milliseconds()).Int()
	return n == 1, err
}

var _ service.ResponsesIdempotencyStore = (*gatewayCache)(nil)
