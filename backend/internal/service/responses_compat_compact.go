package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// responsesCompatCompactSummaryPrompt asks a Chat Completions provider for the
// portable part of a Responses compaction result. Provider-native encrypted
// state is not portable, so this lane never fabricates provider-native state.
const responsesCompatCompactSummaryPrompt = `Summarize the conversation so far for a successor assistant. Preserve the user's requests, decisions, constraints, important facts, tool results, errors, file paths, and unfinished work. Be concise but complete. Return only the summary text inside one <summary>...</summary> block; do not call tools and do not add commentary outside that block.`

const responsesCompatCompactEnvelopePrefix = "sub2api-compat-compact-v1:"

type responsesCompatCompactEnvelope struct {
	Version int    `json:"version"`
	Summary string `json:"summary"`
}

func responsesCompatRequestInputRaw(input json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		item, err := json.Marshal(map[string]any{"type": "message", "role": "user", "content": text})
		if err != nil {
			return nil, err
		}
		return []json.RawMessage{item}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, fmt.Errorf("parse Responses input for compact compatibility: %w", err)
	}
	return items, nil
}

func normalizeResponsesCompatResponseID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || ClassifyOpenAIPreviousResponseIDKind(id) == OpenAIPreviousResponseIDKindMessageID {
		return "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	return id
}

// buildResponsesCompatCompactRequest creates a normal, tool-free Responses
// turn that can be lowered to Chat Completions. The caller retains the
// unmodified request as the canonical session input for response-id replay.
func buildResponsesCompatCompactRequest(req *apicompat.ResponsesRequest) (*apicompat.ResponsesRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("responses compact request is nil")
	}
	items, err := responsesCompatRequestInputRaw(req.Input)
	if err != nil {
		return nil, err
	}
	prompt, err := json.Marshal(map[string]any{
		"type":    "message",
		"role":    "user",
		"content": []map[string]string{{"type": "input_text", "text": responsesCompatCompactSummaryPrompt}},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal Responses compact prompt: %w", err)
	}
	items = append(items, prompt)
	input, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("marshal Responses compact input: %w", err)
	}

	compact := *req
	compact.Input = input
	compact.Stream = false
	compact.Tools = nil
	compact.ToolChoice = nil
	compact.ParallelToolCalls = nil
	compact.Include = nil
	compact.Reasoning = nil
	compact.Text = nil
	store := false
	compact.Store = &store
	compact.PreviousResponseID = ""
	return &compact, nil
}

func responsesCompatCompactSummary(resp *apicompat.ResponsesResponse) string {
	if resp == nil {
		return ""
	}
	var parts []string
	for _, output := range resp.Output {
		switch output.Type {
		case "message":
			for _, part := range output.Content {
				if strings.TrimSpace(part.Text) != "" {
					parts = append(parts, part.Text)
				}
			}
		case "reasoning", "compaction", "compaction_summary":
			for _, summary := range output.Summary {
				if strings.TrimSpace(summary.Text) != "" {
					parts = append(parts, summary.Text)
				}
			}
		}
	}
	return normalizeResponsesCompatCompactSummary(strings.Join(parts, "\n"))
}

// responsesCompatCompactionHistoryInput is the portable replacement for the
// pre-compaction transcript. Native compaction payloads are opaque to a Chat
// Completions provider and the bridge intentionally skips a compaction item;
// retaining the original input would therefore make compact a no-op for
// subsequent previous_response_id requests. Keep one assistant message with
// the generated summary so the next stateless request replays bounded context.
func responsesCompatCompactionHistoryInput(resp *apicompat.ResponsesResponse) (json.RawMessage, error) {
	summary := responsesCompatCompactSummary(resp)
	if summary == "" {
		return nil, fmt.Errorf("compatibility compact history contains no summary text")
	}
	item := map[string]any{
		"type": "message",
		"role": "assistant",
		"content": []map[string]string{{
			"type": "output_text",
			"text": summary,
		}},
	}
	items, err := json.Marshal([]map[string]any{item})
	if err != nil {
		return nil, fmt.Errorf("marshal compatibility compact history: %w", err)
	}
	return items, nil
}

func normalizeResponsesCompatCompactSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if strings.HasPrefix(summary, "<summary>") && strings.HasSuffix(summary, "</summary>") {
		summary = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(summary, "<summary>"), "</summary>"))
	}
	return summary
}

func encodeResponsesCompatCompactEnvelope(summary string) (string, error) {
	payload, err := json.Marshal(responsesCompatCompactEnvelope{Version: 1, Summary: summary})
	if err != nil {
		return "", fmt.Errorf("marshal Responses compatibility compact envelope: %w", err)
	}
	return responsesCompatCompactEnvelopePrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

func responsesCompatCompactResponseFromChat(
	chatResp *apicompat.ChatCompletionsResponse,
	model string,
) (*apicompat.ResponsesResponse, error) {
	responsesResp := apicompat.ChatCompletionsResponseToResponses(chatResp, model, nil, nil, false, nil)
	return responsesCompatCompactResponseFromResponses(responsesResp, model)
}

func responsesCompatCompactResponseFromResponses(
	responsesResp *apicompat.ResponsesResponse,
	model string,
) (*apicompat.ResponsesResponse, error) {
	if responsesResp == nil {
		return nil, fmt.Errorf("compatibility compact response is nil")
	}
	if responsesResp.Status != "completed" {
		return nil, fmt.Errorf("compatibility compact response is incomplete")
	}
	summary := responsesCompatCompactSummary(responsesResp)
	if summary == "" {
		return nil, fmt.Errorf("compatibility compact response contains no summary text")
	}
	encryptedContent, err := encodeResponsesCompatCompactEnvelope(summary)
	if err != nil {
		return nil, err
	}
	responsesResp.ID = normalizeResponsesCompatResponseID(responsesResp.ID)
	responsesResp.Model = model
	responsesResp.Output = []apicompat.ResponsesOutput{{
		ID:               "cmp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Type:             "compaction",
		Status:           "completed",
		EncryptedContent: encryptedContent,
		Summary:          []apicompat.ResponsesSummary{{Type: "summary_text", Text: summary}},
	}}
	return responsesResp, nil
}

// writeResponsesCompatCompactResult preserves the caller's compact wire:
// unary clients receive JSON, while body-signal streaming clients receive the
// existing Responses SSE bridge.
func writeResponsesCompatCompactResult(c *gin.Context, resp *apicompat.ResponsesResponse) error {
	if openAICompactClientWantsStream(c) {
		payload, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		if !writeOpenAICompactSSEBridge(c, http.StatusOK, payload) {
			return fmt.Errorf("compact SSE bridge rejected response")
		}
		return nil
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusOK, resp)
	return nil
}
