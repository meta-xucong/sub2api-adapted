package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// forwardResponsesCompactViaAnthropicMessages implements /responses/compact
// for Claude/Anthropic accounts. Anthropic Messages has no OpenAI compaction
// endpoint, so the request is lowered to a tool-free summary turn and the
// result is raised back as the same portable compatibility item used by the
// Chat Completions lane.
func (s *GatewayService) forwardResponsesCompactViaAnthropicMessages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*ForwardResult, error) {
	startTime := time.Now()
	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse compact request")
		return nil, fmt.Errorf("parse Responses compact request: %w", err)
	}
	if err := validateResponsesCompatCompactStream(c, responsesReq.Stream); err != nil {
		writeResponsesCompatError(c, err)
		return nil, err
	}
	if strings.TrimSpace(responsesReq.PreviousResponseID) != "" {
		if err := s.prepareResponsesCompatContinuation(ctx, c, &responsesReq); err != nil {
			writeResponsesCompatError(c, err)
			return nil, err
		}
	}
	canonicalReq := responsesReq
	originalModel := strings.TrimSpace(canonicalReq.Model)
	if originalModel == "" {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in compact request")
	}
	compactReq, err := buildResponsesCompatCompactRequest(&canonicalReq)
	if err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, err
	}

	anthropicReq, err := apicompat.ResponsesToAnthropicRequest(compactReq)
	if err != nil {
		writeResponsesError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, fmt.Errorf("convert Responses compact request to Anthropic: %w", err)
	}

	mappedModel := originalModel
	if account.Type == AccountTypeAPIKey || account.Type == AccountTypeServiceAccount {
		mappedModel = account.GetMappedModel(originalModel)
	}
	if mappedModel == originalModel && account.Platform == PlatformAnthropic && account.Type == AccountTypeServiceAccount {
		mappedModel = normalizeVertexAnthropicModelID(claude.NormalizeModelID(originalModel))
	} else if mappedModel == originalModel && account.Platform == PlatformAnthropic && account.Type != AccountTypeAPIKey {
		mappedModel = claude.NormalizeModelID(originalModel)
	}
	anthropicReq.Model = mappedModel
	anthropicReq.Stream = false
	anthropicReq.Tools = nil
	anthropicReq.ToolChoice = nil

	anthropicBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal Anthropic compact request: %w", err)
	}
	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}
	shouldMimicClaudeCode := account.IsOAuth()
	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, false)
	upstreamReq, _, err := s.buildUpstreamRequest(upstreamCtx, c, account, anthropicBody, token, tokenType, mappedModel, false, shouldMimicClaudeCode)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build Anthropic compact upstream request: %w", err)
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var resp *http.Response
	if s.tlsFPProfileService != nil {
		resp, err = s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
	} else {
		resp, err = s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, nil)
	}
	if err != nil {
		writeResponsesError(c, http.StatusBadGateway, "server_error", "Upstream request failed")
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, readErr := s.readUpstreamErrorBody(resp)
		if readErr != nil {
			logger.L().Warn("Responses compact Anthropic error body read failed", zap.Error(readErr), zap.Int64("account_id", account.ID))
		}
		upstreamMsg := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(respBody))
		if upstreamMsg == "" {
			upstreamMsg = fmt.Sprintf("Anthropic upstream returned status %d", resp.StatusCode)
		}
		if s.shouldFailoverUpstreamError(resp.StatusCode) {
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody}
		}
		writeResponsesError(c, mapUpstreamStatusCode(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("Anthropic compact upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, anthropicTooLargeError)
	if err != nil {
		return nil, fmt.Errorf("read Anthropic compact response: %w", err)
	}
	var anthropicResp apicompat.AnthropicResponse
	if err := json.Unmarshal(respBody, &anthropicResp); err != nil {
		writeResponsesError(c, http.StatusBadGateway, "server_error", "Failed to parse upstream response")
		return nil, fmt.Errorf("parse Anthropic compact response: %w", err)
	}
	responsesResp := apicompat.AnthropicToResponsesResponse(&anthropicResp)
	responsesResp.ID = normalizeResponsesCompatResponseID(responsesResp.ID)
	responsesResp.Model = originalModel
	responsesResp, err = responsesCompatCompactResponseFromResponses(responsesResp, originalModel)
	if err != nil {
		writeResponsesError(c, http.StatusBadGateway, "upstream_error", "Failed to build compatibility compaction response")
		return nil, err
	}
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	if err := writeResponsesCompatCompactResult(c, responsesResp); err != nil {
		writeResponsesCompatError(c, &responsesCompatError{status: http.StatusBadGateway, code: "compact_response_invalid", message: "Failed to encode compatibility compact response", cause: err})
		return nil, err
	}

	var usage ClaudeUsage
	mergeAnthropicUsage(&usage, anthropicResp.Usage)
	result := &ForwardResult{
		RequestID:               resp.Header.Get("x-request-id"),
		Usage:                   usage,
		Model:                   originalModel,
		UpstreamModel:           mappedModel,
		ReasoningEffort:         ExtractResponsesReasoningEffortFromBody(body),
		Stream:                  openAICompactClientWantsStream(c),
		Duration:                time.Since(startTime),
		responsesCompatResponse: responsesResp,
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	persistErr := s.saveResponsesCompatResponse(persistCtx, c, &canonicalReq, responsesResp)
	cancel()
	if persistErr != nil {
		logger.L().Warn("Responses compact Anthropic compatibility session persistence failed", zap.Error(persistErr), zap.String("response_id", responsesResp.ID))
	}
	return result, nil
}
