package service

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
)

const ResponsesCompactionEnabledExtraKey = "responses_compaction_enabled"

type responsesCompactionSchedulingContextKey struct{}

func WithResponsesCompactionScheduling(ctx context.Context) context.Context {
	return context.WithValue(ctx, responsesCompactionSchedulingContextKey{}, true)
}

func IsResponsesCompactionScheduling(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(responsesCompactionSchedulingContextKey{}).(bool)
	return enabled
}

func IsResponsesCompactionSchedulingRequest(body []byte, stream bool) bool {
	if stream && HasCompactionTriggerInInput(body) {
		return true
	}
	input := gjson.GetBytes(body, "input")
	return apicompat.ResponsesHasAnthropicCompactionState(json.RawMessage(input.Raw))
}
