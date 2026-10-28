package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

func addAnthropicCompactionBeta(req *http.Request) {
	var betaValues []string
	for key, values := range req.Header {
		if strings.EqualFold(key, "anthropic-beta") {
			betaValues = append(betaValues, values...)
		}
	}
	beta := strings.Join(betaValues, ", ")
	hasCompactionBeta := false
	for _, token := range strings.Split(beta, ",") {
		if strings.TrimSpace(token) == "compact-2026-09-04" {
			hasCompactionBeta = true
			break
		}
	}
	if !hasCompactionBeta {
		if beta != "" {
			beta += ", "
		}
		beta += "compact-2026-09-04"
	}
	setHeaderRaw(req.Header, "anthropic-beta", beta)
}

func (s *GatewayService) handleResponsesAnthropicCompactionStream(
	resp *http.Response,
	c *gin.Context,
	model string,
	startTime time.Time,
) (*ForwardResult, error) {
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	return handleResponsesAnthropicCompactionStream(resp, c, model, startTime, maxLineSize)
}

func handleResponsesAnthropicCompactionStream(
	resp *http.Response,
	c *gin.Context,
	model string,
	startTime time.Time,
	maxLineSize int,
) (*ForwardResult, error) {
	responseID := "resp_" + uuid.NewString()
	createdAt := time.Now().Unix()
	sequence := 0
	completedSent := false
	fail := func(code, message string) error {
		if err := writeResponsesAdapterFailure(c, responseID, model, createdAt, &sequence, &completedSent, code, message); err != nil {
			return err
		}
		return fmt.Errorf("%s", message)
	}
	response := func(status string, output []apicompat.ResponsesOutput) *apicompat.ResponsesResponse {
		return &apicompat.ResponsesResponse{ID: responseID, Object: "response", CreatedAt: createdAt, Model: model, Status: status, Output: output}
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	for _, eventType := range []string{"response.created", "response.in_progress"} {
		frame, err := apicompat.ResponsesEventToSSE(apicompat.ResponsesStreamEvent{Type: eventType, SequenceNumber: sequence, Response: response("in_progress", []apicompat.ResponsesOutput{})})
		if err != nil {
			return nil, err
		}
		sequence++
		if _, err := fmt.Fprint(c.Writer, frame); err != nil {
			return nil, err
		}
	}
	c.Writer.Flush()
	MarkResponseCommitted(c)

	var block json.RawMessage
	var usage ClaudeUsage
	sawMessageStop := false
	stopReason := ""
	requestID := resp.Header.Get("x-request-id")
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		if len(payload) == 0 || string(payload) == "[DONE]" {
			continue
		}
		var event apicompat.AnthropicStreamEvent
		if json.Unmarshal(payload, &event) != nil {
			continue
		}
		switch event.Type {
		case "ping":
			_, _ = fmt.Fprint(c.Writer, ": ping\n\n")
			c.Writer.Flush()
		case "message_start":
			if event.Message != nil {
				mergeAnthropicUsage(&usage, event.Message.Usage)
				mergeAnthropicCompactionUsage(&usage, json.RawMessage(gjson.GetBytes(payload, "message.usage").Raw))
			}
		case "content_block_start":
			if event.ContentBlock != nil && event.ContentBlock.Type == "compaction" {
				block = json.RawMessage(gjson.GetBytes(payload, "content_block").Raw)
			}
		case "message_delta":
			if event.Usage != nil {
				mergeAnthropicUsage(&usage, *event.Usage)
				mergeAnthropicCompactionUsage(&usage, json.RawMessage(gjson.GetBytes(payload, "usage").Raw))
			}
			if event.Delta != nil {
				stopReason = event.Delta.StopReason
			}
		case "message_stop":
			sawMessageStop = true
		}
	}
	if scanner.Err() != nil {
		return nil, fail("upstream_stream_error", "Upstream stream failed before completion")
	}
	if len(block) == 0 || !sawMessageStop || stopReason != "compaction" {
		return nil, fail("upstream_compaction_unavailable", "Upstream did not return a completed compaction block")
	}

	item := apicompat.ResponsesOutput{
		Type:             "compaction",
		ID:               "cmp_" + uuid.NewString(),
		Status:           "completed",
		EncryptedContent: apicompat.EncodeAnthropicCompactionEnvelope(block),
	}
	output := []apicompat.ResponsesOutput{item}
	events := []apicompat.ResponsesStreamEvent{
		{Type: "response.output_item.added", SequenceNumber: sequence, OutputIndex: 0, Item: &apicompat.ResponsesOutput{Type: item.Type, ID: item.ID, Status: "in_progress", EncryptedContent: item.EncryptedContent}},
		{Type: "response.output_item.done", SequenceNumber: sequence + 1, OutputIndex: 0, Item: &item},
		{Type: "response.completed", SequenceNumber: sequence + 2, Response: response("completed", output)},
	}
	for _, event := range events {
		frame, err := apicompat.ResponsesEventToSSE(event)
		if err != nil {
			return nil, err
		}
		if _, err := fmt.Fprint(c.Writer, frame); err != nil {
			return nil, err
		}
	}
	c.Writer.Flush()
	return &ForwardResult{
		RequestID:       requestID,
		UpstreamHeaders: resp.Header,
		Usage:           usage,
		Model:           model,
		Duration:        time.Since(startTime),
		Stream:          true,
	}, nil
}

func mergeAnthropicCompactionUsage(dst *ClaudeUsage, rawUsage json.RawMessage) {
	if dst == nil {
		return
	}
	var src struct {
		Iterations []struct {
			Type         string `json:"type"`
			InputTokens  int    `json:"input_tokens"`
			OutputTokens int    `json:"output_tokens"`
		} `json:"iterations"`
	}
	if json.Unmarshal(rawUsage, &src) != nil {
		return
	}
	var inputTokens, outputTokens int
	found := false
	for _, iteration := range src.Iterations {
		if iteration.Type != "compaction" {
			continue
		}
		inputTokens += iteration.InputTokens
		outputTokens += iteration.OutputTokens
		found = true
	}
	if found {
		// Anthropic reports compaction usage in iterations while top-level usage is zero.
		// Keep it internal to billing; the Responses compaction item has no usage payload.
		dst.InputTokens = inputTokens
		dst.OutputTokens = outputTokens
	}
}
