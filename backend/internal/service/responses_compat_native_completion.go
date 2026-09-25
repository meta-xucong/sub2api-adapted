package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

func responsesCompatTerminalPayload(payload []byte) *apicompat.ResponsesResponse {
	switch gjson.GetBytes(payload, "type").String() {
	case "response.completed", "response.incomplete", "response.failed":
	default:
		return nil
	}
	var response apicompat.ResponsesResponse
	if json.Unmarshal([]byte(gjson.GetBytes(payload, "response").Raw), &response) != nil {
		return nil
	}
	return &response
}

func writeResponsesCompatAnthropicFailure(c *gin.Context, state *apicompat.AnthropicEventToResponsesState, disconnected bool, code string) {
	if state.CompletedSent {
		return
	}
	state.CompletedSent = true
	if disconnected {
		return
	}
	state.ResponseID = normalizeResponsesCompatResponseID(state.ResponseID)
	event := apicompat.ResponsesStreamEvent{Type: "response.failed", SequenceNumber: state.SequenceNumber, Response: &apicompat.ResponsesResponse{
		ID: state.ResponseID, Object: "response", CreatedAt: state.Created, Model: state.Model, Status: "failed", Output: []apicompat.ResponsesOutput{}, Error: &apicompat.ResponsesError{Code: code, Message: "The upstream response stream did not complete"},
	}}
	state.SequenceNumber++
	encoded, err := apicompat.ResponsesEventToSSE(event)
	if err != nil {
		return
	}
	MarkResponseCommitted(c)
	MarkOpsStreamError(c, code, "Incomplete upstream response stream", 502)
	_, _ = fmt.Fprint(c.Writer, encoded)
	c.Writer.Flush()
}

// CN Anthropic compaction uses its configured /messages URL, never a Chat URL.
func (s *OpenAIGatewayService) forwardResponsesCompactViaNativeAnthropic(ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
	started := time.Now()
	var canonical apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &canonical); err != nil {
		writeResponsesError(c, 400, "invalid_request_error", "Invalid compact request")
		return nil, err
	}
	markResponsesCompatCompactStream(c, canonical.Stream)
	if err := s.prepareResponsesCompatContinuation(ctx, c, &canonical); err != nil {
		writeResponsesCompatError(c, err)
		return nil, err
	}
	model := strings.TrimSpace(canonical.Model)
	if model == "" {
		writeResponsesError(c, 400, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("model is required")
	}
	summaryReq, err := buildResponsesCompatCompactRequest(&canonical)
	if err != nil {
		writeResponsesCompatError(c, err)
		return nil, err
	}
	request, err := apicompat.ResponsesToAnthropicRequest(summaryReq)
	if err != nil {
		writeResponsesCompatError(c, err)
		return nil, err
	}
	billingModel := resolveOpenAIForwardModel(account, model, "")
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	request.Model = upstreamModel
	request.Stream = false
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	target, err := s.nativeAnthropicTargetURL(account)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
	if token == "" {
		return nil, fmt.Errorf("missing upstream credential")
	}
	upstreamReq, _, err := s.buildNativeAnthropicUpstreamRequest(ctx, c, account, payload, token, target)
	if err != nil {
		return nil, err
	}
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	response, err := s.doOpenAIUpstream(upstreamReq, proxy, account)
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		data, msg := s.readOpenAIUpstreamError(response)
		if failover := s.failoverOpenAIUpstreamHTTPError(ctx, c, account, response, data, msg, upstreamModel); failover != nil {
			return nil, failover
		}
		writeResponsesError(c, mapUpstreamStatusCode(response.StatusCode), "upstream_error", msg)
		return nil, fmt.Errorf("upstream compact status %d", response.StatusCode)
	}
	data, err := ReadUpstreamResponseBody(response.Body, s.cfg, c, anthropicTooLargeError)
	if err != nil {
		return nil, err
	}
	var anthropic apicompat.AnthropicResponse
	if err := json.Unmarshal(data, &anthropic); err != nil {
		writeResponsesError(c, 502, "upstream_error", "Invalid upstream compact response")
		return nil, err
	}
	result, err := responsesCompatCompactResponseFromResponses(apicompat.AnthropicToResponsesResponse(&anthropic), model)
	if err != nil {
		writeResponsesError(c, 502, "upstream_error", "No complete portable summary was returned")
		return nil, err
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	s.bindHTTPResponseAccount(persistCtx, c, account, result.ID)
	persistErr := s.saveResponsesCompatResponse(persistCtx, c, &canonical, result)
	cancel()
	if persistErr != nil {
		logger.L().Warn("Native Anthropic compact persistence failed", zap.Error(persistErr))
	}
	if err := writeResponsesCompatCompactResult(c, result); err != nil {
		return nil, err
	}
	usage := ClaudeUsage{}
	mergeAnthropicUsage(&usage, anthropic.Usage)
	return &OpenAIForwardResult{RequestID: response.Header.Get("x-request-id"), ResponseID: result.ID, Model: model, BillingModel: billingModel, UpstreamModel: upstreamModel, UpstreamEndpoint: "/v1/messages", Stream: openAICompactClientWantsStream(c), Usage: claudeUsageToOpenAIUsage(&usage), Duration: time.Since(started), responsesCompatResponse: result}, nil
}
