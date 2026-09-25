package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/google/uuid"
)

// responsesCompatCompactSummaryPrompt asks a Chat Completions/Anthropic model
// for the portable part of a Responses compaction result.  The provider's
// encrypted reasoning state is not portable across providers, so this lane
// never pretends to manufacture provider-native encrypted content.
const responsesCompatCompactSummaryPrompt = `Summarize the conversation so far for a successor assistant. Preserve the user's requests, decisions, constraints, important facts, tool results, errors, file paths, and unfinished work. Be concise but complete. Return only the summary text inside one <summary>...</summary> block; do not call tools and do not add commentary outside that block.`

const responsesCompatCompactEnvelopePrefix = "sub2api-compat-compact-v1:"

type responsesCompatCompactEnvelope struct {
	Version int    `json:"version"`
	Summary string `json:"summary"`
}

// buildResponsesCompatCompactRequest creates a normal Chat-compatible
// Responses turn that asks the upstream model to summarize the current
// history.  The caller retains the unmodified request as the canonical
// session input used for response-id replay.
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
		case "reasoning":
			for _, summary := range output.Summary {
				if strings.TrimSpace(summary.Text) != "" {
					parts = append(parts, summary.Text)
				}
			}
		}
	}
	return normalizeResponsesCompatCompactSummary(strings.Join(parts, "\n"))
}

func normalizeResponsesCompatCompactSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if strings.HasPrefix(summary, "<summary>") && strings.HasSuffix(summary, "</summary>") {
		summary = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(summary, "<summary>"), "</summary>"))
	}
	return summary
}

func encodeResponsesCompatCompactEnvelope(summary string) (string, error) {
	payload, err := json.Marshal(responsesCompatCompactEnvelope{
		Version: 1,
		Summary: summary,
	})
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
	responseID := normalizeResponsesCompatResponseID(responsesResp.ID)
	responsesResp.ID = responseID
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
