package service

import (
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
)

// Failed streams only produce an accounting result after model output was
// delivered or an existing usage parser observed positive metering. Protocol
// metadata and error responses do not qualify. Keep the existing policy for
// delivered output without usage and never invent or normalize token counts.
func openAIStreamFailureHasBillableEvidence(usage OpenAIUsage, deliveredModelOutput bool) bool {
	return deliveredModelOutput || usage.InputTokens > 0 || usage.OutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0 ||
		usage.ImageInputTokens > 0 || usage.ImageCacheReadTokens > 0 || usage.ImageOutputTokens > 0
}

func ccStreamPayloadHasModelOutput(payload string) bool {
	nonemptyString := func(value gjson.Result) bool {
		return value.Type == gjson.String && value.String() != ""
	}
	for _, choice := range gjson.Get(payload, "choices").Array() {
		delta := choice.Get("delta")
		for _, field := range []string{"content", "reasoning_content", "reasoning", "reasoning_summary", "refusal"} {
			value := delta.Get(field)
			if nonemptyString(value) {
				return true
			}
		}
		for _, call := range delta.Get("tool_calls").Array() {
			if nonemptyString(call.Get("function.name")) || nonemptyString(call.Get("function.arguments")) {
				return true
			}
		}
		if nonemptyString(delta.Get("function_call.name")) || nonemptyString(delta.Get("function_call.arguments")) {
			return true
		}
	}
	return false
}

func anthropicCCEventHasModelOutput(event apicompat.AnthropicStreamEvent) bool {
	if event.Type == "content_block_start" && event.ContentBlock != nil {
		block := event.ContentBlock
		return block.Text != "" || block.Thinking != "" || block.Signature != "" || block.Data != "" ||
			(block.Type == "tool_use" && block.Name != "")
	}
	if event.Type == "content_block_delta" && event.Delta != nil {
		delta := event.Delta
		return delta.Text != "" || delta.Thinking != "" || delta.PartialJSON != "" || delta.Signature != ""
	}
	return false
}

func responsesCCEventHasModelOutput(event apicompat.ResponsesStreamEvent) bool {
	switch event.Type {
	case "response.output_text.delta", "response.reasoning_summary_text.delta", "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		return event.Delta != ""
	case "response.output_item.added":
		if event.Item == nil {
			return false
		}
		switch event.Item.Type {
		case "function_call", "custom_tool_call":
			return event.Item.Name != "" || event.Item.Arguments != "" || event.Item.Input != ""
		case "tool_search_call":
			return true // The named tool-search decision has already been announced.
		}
	}
	return false
}

// Preserve the downstream-disconnect signal independently of billing eligibility.
// The original error remains intact for errors.Is/As and endpoint diagnostics.
type openAIStreamClientDisconnectError struct{ cause error }

func (e *openAIStreamClientDisconnectError) Error() string { return e.cause.Error() }
func (e *openAIStreamClientDisconnectError) Unwrap() error { return e.cause }

func openAIUnbillableStreamError(err error, clientDisconnected bool) error {
	if err != nil && clientDisconnected {
		return &openAIStreamClientDisconnectError{cause: err}
	}
	return err
}

// IsOpenAIStreamClientDisconnect reports a downstream disconnect even when no
// billable forwarding result exists. It does not classify upstream failures.
func IsOpenAIStreamClientDisconnect(err error) bool {
	var disconnected *openAIStreamClientDisconnectError
	return errors.As(err, &disconnected)
}
