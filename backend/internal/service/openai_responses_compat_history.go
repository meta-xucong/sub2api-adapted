package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const openAIResponsesCompatHistoryTTL = 24 * time.Hour

var errOpenAIResponsesCompatHistoryUnavailable = errors.New("previous_response_id is not available for this compatibility route")

type openAIResponsesCompatHistoryEntry struct {
	Items    []json.RawMessage
	StoredAt time.Time
}

type openAIResponsesCompatHistoryCache interface {
	SetOpenAIResponsesCompatHistory(context.Context, string, []byte, time.Duration) error
	GetOpenAIResponsesCompatHistory(context.Context, string) ([]byte, error)
}

// prepareOpenAIResponsesCompatContinuation expands a Responses continuation
// into the complete input expected by stateless Chat/Anthropic upstreams. The
// official native Responses route keeps previous_response_id and never enters
// this helper.
func (s *OpenAIGatewayService) prepareOpenAIResponsesCompatContinuation(req *apicompat.ResponsesRequest) error {
	if s == nil {
		return errOpenAIResponsesCompatHistoryUnavailable
	}
	return prepareOpenAIResponsesCompatContinuationWithStore(req, s.cache, &s.openaiResponsesCompatHistory)
}

func prepareOpenAIResponsesCompatContinuationWithStore(req *apicompat.ResponsesRequest, cache GatewayCache, history *sync.Map) error {
	if req == nil || strings.TrimSpace(req.PreviousResponseID) == "" {
		return nil
	}
	previousID := strings.TrimSpace(req.PreviousResponseID)
	var entry openAIResponsesCompatHistoryEntry
	loaded := false
	if sharedCache, ok := cache.(openAIResponsesCompatHistoryCache); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		payload, err := sharedCache.GetOpenAIResponsesCompatHistory(ctx, previousID)
		cancel()
		if err != nil {
			return fmt.Errorf("load shared Responses compatibility history: %w", err)
		}
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &entry); err != nil {
				return fmt.Errorf("decode shared Responses compatibility history: %w", err)
			}
			loaded = true
		}
	}
	if !loaded {
		if history == nil {
			return fmt.Errorf("%w: local history is unavailable", errOpenAIResponsesCompatHistoryUnavailable)
		}
		value, ok := history.Load(previousID)
		if !ok {
			return fmt.Errorf("%w: %s", errOpenAIResponsesCompatHistoryUnavailable, previousID)
		}
		entry, ok = value.(openAIResponsesCompatHistoryEntry)
		if !ok {
			return fmt.Errorf("%w: malformed history", errOpenAIResponsesCompatHistoryUnavailable)
		}
	}
	if time.Since(entry.StoredAt) > openAIResponsesCompatHistoryTTL {
		if history != nil {
			history.Delete(previousID)
		}
		return fmt.Errorf("%w: %s", errOpenAIResponsesCompatHistoryUnavailable, previousID)
	}

	current, err := responsesCompatInputItems(req.Input)
	if err != nil {
		return err
	}
	merged := cloneResponsesCompatItems(entry.Items)
	merged = append(merged, current...)
	req.Input, err = json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("marshal expanded Responses history: %w", err)
	}
	req.PreviousResponseID = ""
	return nil
}

// rememberOpenAIResponsesCompatHistory stores the effective request input plus
// the assistant output under the public resp_* ID. It is deliberately only
// used for compatibility routes whose upstream has no response store.
func (s *OpenAIGatewayService) rememberOpenAIResponsesCompatHistory(input json.RawMessage, response *apicompat.ResponsesResponse) {
	if s == nil {
		return
	}
	rememberOpenAIResponsesCompatHistoryWithStore(input, response, s.cache, &s.openaiResponsesCompatHistory)
}

func rememberOpenAIResponsesCompatHistoryWithStore(input json.RawMessage, response *apicompat.ResponsesResponse, cache GatewayCache, history *sync.Map) {
	if response == nil || strings.TrimSpace(response.ID) == "" || history == nil {
		return
	}
	items, err := responsesCompatInputItems(input)
	if err != nil {
		return
	}
	for _, output := range response.Output {
		raw, marshalErr := json.Marshal(output)
		if marshalErr == nil {
			items = append(items, raw)
		}
	}
	entry := openAIResponsesCompatHistoryEntry{
		Items:    cloneResponsesCompatItems(items),
		StoredAt: time.Now(),
	}
	history.Store(response.ID, entry)
	if sharedCache, ok := cache.(openAIResponsesCompatHistoryCache); ok {
		payload, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := sharedCache.SetOpenAIResponsesCompatHistory(ctx, response.ID, payload, openAIResponsesCompatHistoryTTL); err != nil {
			logger.L().Warn("openai.responses_compat_history_shared_cache_write_failed",
				zap.Error(err),
				zap.String("response_id_hash", openAIResponsesCompatHistoryKey(response.ID)),
			)
		}
	}
}

func openAIResponsesCompatHistoryKey(responseID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(responseID)))
	return hex.EncodeToString(sum[:])
}

func responsesCompatInputItems(raw json.RawMessage) ([]json.RawMessage, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		content, _ := json.Marshal(text)
		item, _ := json.Marshal(map[string]any{
			"type":    "message",
			"role":    "user",
			"content": json.RawMessage(content),
		})
		return []json.RawMessage{item}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parse Responses compatibility input: %w", err)
	}
	return cloneResponsesCompatItems(items), nil
}

func cloneResponsesCompatItems(items []json.RawMessage) []json.RawMessage {
	if len(items) == 0 {
		return nil
	}
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		out = append(out, append(json.RawMessage(nil), item...))
	}
	return out
}
