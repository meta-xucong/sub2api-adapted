package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// OpenAIGatewayUnifiedGrokMediaAdapter reuses the already audited Grok
// transport, including KIE and Wokey request shaping, credential failover,
// SSRF checks, and signed-content handling. The adapter only supplies a
// private Gin recorder because the legacy media service writes its response to
// a Gin context; no legacy public route is registered or changed here.
type OpenAIGatewayUnifiedGrokMediaAdapter struct {
	gateway *OpenAIGatewayService
}

var _ UnifiedGatewayGrokMediaAdapter = (*OpenAIGatewayUnifiedGrokMediaAdapter)(nil)
var _ UnifiedGatewayGrokMediaPoller = (*OpenAIGatewayUnifiedGrokMediaAdapter)(nil)
var _ UnifiedGatewayGrokMediaContentProvider = (*OpenAIGatewayUnifiedGrokMediaAdapter)(nil)

func NewOpenAIGatewayUnifiedGrokMediaAdapter(gateway *OpenAIGatewayService) *OpenAIGatewayUnifiedGrokMediaAdapter {
	return &OpenAIGatewayUnifiedGrokMediaAdapter{gateway: gateway}
}

func (a *OpenAIGatewayUnifiedGrokMediaAdapter) Forward(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
	if a == nil || a.gateway == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: OpenAI Grok media service is unavailable", ErrUnifiedGatewayUpstreamUnsupported)
	}
	endpoint, err := unifiedGatewayGrokMediaEndpoint(selection.Endpoint())
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	if endpoint == GrokMediaEndpointVideoStatus || endpoint == GrokMediaEndpointVideoContent {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: video lookup is not a generation endpoint", ErrUnifiedGatewayUpstreamUnsupported)
	}
	result, recorder, err := a.forwardWithRecorder(ctx, account, endpoint, request.RequestID, request.RawBody, unifiedGatewayRequestContentType(ctx))
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	body := recorder.Body.Bytes()
	if result == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Grok media service returned no result", ErrUnifiedGatewayUpstreamFailed)
	}
	requestID := firstNonEmpty(result.ResponseID, result.RequestID, request.RequestID)
	if endpoint == GrokMediaEndpointVideosGenerations {
		if requestID == "" {
			requestID = extractGrokMediaVideoRequestID(body)
		}
		if requestID == "" {
			return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: asynchronous Grok video response has no task ID", ErrUnifiedGatewayUpstreamFailed)
		}
		return UnifiedGatewayUpstreamResult{
			Pending:           true,
			UpstreamRequestID: requestID,
			ResponseBody:      append([]byte(nil), body...),
		}, nil
	}

	units := result.ImageCount
	if units <= 0 {
		units = extractOpenAIImagesBillableCountFromJSONBytes(body)
	}
	if units <= 0 {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Grok image response has no completed image", ErrUnifiedGatewayUsageMissing)
	}
	return UnifiedGatewayUpstreamResult{
		Delivered:         true,
		MeasuredUnits:     float64(units),
		UpstreamRequestID: requestID,
		ResponseBody:      append([]byte(nil), body...),
	}, nil
}

func (a *OpenAIGatewayUnifiedGrokMediaAdapter) Poll(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest, upstreamRequestID string) (UnifiedGatewayUpstreamResult, error) {
	if a == nil || a.gateway == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: OpenAI Grok media service is unavailable", ErrUnifiedGatewayUpstreamUnsupported)
	}
	upstreamRequestID = strings.TrimSpace(upstreamRequestID)
	if upstreamRequestID == "" {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: video task ID is required", ErrUnifiedGatewayInvalidRequest)
	}
	result, recorder, err := a.forwardWithRecorder(ctx, account, GrokMediaEndpointVideoStatus, upstreamRequestID, nil, "")
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	body := append([]byte(nil), recorder.Body.Bytes()...)
	if result == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Grok video status returned no result", ErrUnifiedGatewayUpstreamFailed)
	}
	if billed := unifiedGrokVideoBillingResult(account, body, result, upstreamRequestID); billed != nil {
		return UnifiedGatewayUpstreamResult{
			Delivered:         true,
			MeasuredUnits:     float64(unifiedGatewayMaxInt(billed.VideoCount, 1)),
			UpstreamRequestID: upstreamRequestID,
			ResponseBody:      body,
		}, nil
	}
	if unifiedGrokVideoStatusFailed(body) {
		return UnifiedGatewayUpstreamResult{
			UpstreamRequestID: upstreamRequestID,
			ResponseBody:      body,
		}, nil
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Grok video status response is invalid", ErrUnifiedGatewayUpstreamFailed)
	}
	return UnifiedGatewayUpstreamResult{
		Pending:           true,
		UpstreamRequestID: upstreamRequestID,
		ResponseBody:      body,
	}, nil
}

func (a *OpenAIGatewayUnifiedGrokMediaAdapter) Content(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest, upstreamRequestID string) (UnifiedGatewayContentResult, error) {
	if a == nil || a.gateway == nil {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: OpenAI Grok media service is unavailable", ErrUnifiedGatewayUpstreamUnsupported)
	}
	upstreamRequestID = strings.TrimSpace(upstreamRequestID)
	if upstreamRequestID == "" {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: video task ID is required", ErrUnifiedGatewayInvalidRequest)
	}
	result, recorder, err := a.forwardWithRecorder(ctx, account, GrokMediaEndpointVideoContent, upstreamRequestID, nil, "")
	if err != nil {
		return UnifiedGatewayContentResult{}, err
	}
	contentType := strings.TrimSpace(recorder.Header().Get("Content-Type"))
	if contentType == "" && result != nil {
		contentType = strings.TrimSpace(result.ResponseHeaders.Get("Content-Type"))
	}
	return UnifiedGatewayContentResult{
		StatusCode:  recorder.Code,
		ContentType: contentType,
		Body:        append([]byte(nil), recorder.Body.Bytes()...),
		Headers:     recorder.Header().Clone(),
	}, nil
}

func (a *OpenAIGatewayUnifiedGrokMediaAdapter) forwardWithRecorder(ctx context.Context, account *Account, endpoint GrokMediaEndpoint, requestID string, body []byte, contentType string) (*OpenAIForwardResult, *httptest.ResponseRecorder, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	method := http.MethodPost
	if endpoint.IsVideoLookupRequest() {
		method = http.MethodGet
	}
	request, err := http.NewRequestWithContext(ctx, method, "https://unified-gateway.invalid/internal", bytes.NewReader(body))
	if err != nil {
		return nil, recorder, err
	}
	if strings.TrimSpace(contentType) != "" {
		request.Header.Set("Content-Type", contentType)
	}
	ginContext.Request = request
	result, err := a.gateway.ForwardGrokMedia(ctx, ginContext, account, endpoint, strings.TrimSpace(requestID), body, contentType)
	if err != nil {
		return nil, recorder, err
	}
	if recorder.Code == 0 {
		recorder.Code = http.StatusOK
	}
	if recorder.Code < http.StatusOK || recorder.Code >= http.StatusMultipleChoices {
		return nil, recorder, &UnifiedGatewayUpstreamHTTPError{StatusCode: recorder.Code, RequestID: requestID}
	}
	return result, recorder, nil
}

func unifiedGatewayGrokMediaEndpoint(raw string) (GrokMediaEndpoint, error) {
	endpoint, err := normalizeUnifiedGatewayUpstreamEndpoint(raw)
	if err != nil {
		return "", err
	}
	switch endpoint {
	case unifiedGatewayUpstreamEndpointImages:
		return GrokMediaEndpointImagesGenerations, nil
	case unifiedGatewayUpstreamEndpointImagesEdits:
		return GrokMediaEndpointImagesEdits, nil
	case unifiedGatewayUpstreamEndpointVideos:
		return GrokMediaEndpointVideosGenerations, nil
	default:
		return "", fmt.Errorf("%w: endpoint %q is not Grok media", ErrUnifiedGatewayUpstreamUnsupported, raw)
	}
}

func unifiedGrokVideoBillingResult(account *Account, body []byte, result *OpenAIForwardResult, requestID string) *OpenAIForwardResult {
	billingBody := body
	if account != nil && account.UsesWokeyVideoMultipart() {
		billingBody = normalizeWokeyVideoStatusForBilling(body)
	}
	if billed := ExtractGrokVideoBillingFromStatusBody(billingBody, nil, requestID); billed != nil {
		return billed
	}
	if result != nil && result.VideoCount > 0 && strings.EqualFold(strings.TrimSpace(gjson.GetBytes(billingBody, "status").String()), "done") {
		copyResult := *result
		return &copyResult
	}
	return nil
}

func unifiedGrokVideoStatusFailed(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String()))
	switch status {
	case "failed", "error", "expired", "cancelled", "canceled", "rejected":
		return true
	}
	return gjson.GetBytes(body, "error").Exists()
}

func unifiedGatewayMaxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
