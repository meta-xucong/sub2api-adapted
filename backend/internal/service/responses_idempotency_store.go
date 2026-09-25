package service

import (
	"context"
	"time"
)

// ResponsesIdempotencyStore is deliberately separate from the generic write
// coordinator: a model dispatch must never be automatically reclaimed after
// an ambiguous failure. Keys are opaque hashes scoped by tenant and endpoint.
// Runtime response bytes are confidential state, not diagnostic evidence.
type ResponsesIdempotencyStore interface {
	ClaimResponsesRequest(ctx context.Context, key string, claim []byte, ttl time.Duration) (existing []byte, acquired bool, err error)
	CompleteResponsesRequest(ctx context.Context, key string, claim, result []byte, ttl time.Duration) (bool, error)
}

func (s *OpenAIGatewayService) ResponsesRequestStore() ResponsesIdempotencyStore {
	if s == nil {
		return nil
	}
	store, _ := s.cache.(ResponsesIdempotencyStore)
	return store
}
func (s *GatewayService) ResponsesRequestStore() ResponsesIdempotencyStore {
	if s == nil {
		return nil
	}
	store, _ := s.cache.(ResponsesIdempotencyStore)
	return store
}

type responsesRequestExecutionKey struct{}

// Accepted keyed requests drain independently of a lost client connection, but
// retain an execution deadline even through the upstream detach helpers.
func WithResponsesRequestExecution(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 15*time.Minute)
	return context.WithValue(ctx, responsesRequestExecutionKey{}, true), cancel
}
