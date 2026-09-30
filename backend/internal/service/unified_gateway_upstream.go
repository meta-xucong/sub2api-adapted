package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	// UnifiedGatewayDefaultUpstreamTimeout bounds one synchronous adapter call
	// when the gateway configuration does not provide an OpenAI timeout.
	UnifiedGatewayDefaultUpstreamTimeout = 5 * time.Minute

	// UnifiedGatewayDefaultUpstreamRequestBodyMaxBytes prevents an untrusted
	// unified request from being copied without a bound before it reaches the
	// shared upstream transport.
	UnifiedGatewayDefaultUpstreamRequestBodyMaxBytes int64 = 8 << 20
)

var (
	// ErrUnifiedGatewayUpstreamUnsupported means the selected account or
	// endpoint has no reliable adapter contract. Callers must release any
	// reservation instead of treating this as a delivered response.
	ErrUnifiedGatewayUpstreamUnsupported = errors.New("unified gateway upstream protocol is unsupported")

	// ErrUnifiedGatewayUpstreamRequestBodyTooLarge is returned before a request
	// is handed to HTTPUpstream.
	ErrUnifiedGatewayUpstreamRequestBodyTooLarge = errors.New("unified gateway upstream request body is too large")

	// ErrUnifiedGatewayUpstreamResponseBodyTooLarge means the upstream response
	// exceeded the configured bounded-read limit.
	ErrUnifiedGatewayUpstreamResponseBodyTooLarge = errors.New("unified gateway upstream response body is too large")
)

// UnifiedGatewayContentResult is the bounded binary/content result used by
// media adapters after an asynchronous task reaches a terminal state.
// Headers and Body are owned by the caller after Content returns.
type UnifiedGatewayContentResult struct {
	StatusCode  int
	ContentType string
	Body        []byte
	Headers     http.Header
}

// UnifiedGatewayGrokMediaAdapter is the deliberately narrow seam for Grok
// Imagine image/video transport. The concrete media protocol is kept out of
// the OpenAI-compatible adapter because video generation can be asynchronous
// and may use a different host, authentication scheme, and response format.
type UnifiedGatewayGrokMediaAdapter interface {
	Forward(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error)
}

// UnifiedGatewayGrokMediaAdapterFunc adapts a function to
// UnifiedGatewayGrokMediaAdapter.
type UnifiedGatewayGrokMediaAdapterFunc func(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error)

func (f UnifiedGatewayGrokMediaAdapterFunc) Forward(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
	if f == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: nil Grok media adapter", ErrUnifiedGatewayUpstreamUnsupported)
	}
	return f(ctx, account, selection, request)
}

// UnifiedGatewayGrokMediaPoller is an optional extension for a media adapter
// that can query a provider job after Forward returned Pending.
type UnifiedGatewayGrokMediaPoller interface {
	Poll(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest, upstreamRequestID string) (UnifiedGatewayUpstreamResult, error)
}

// UnifiedGatewayGrokMediaContentProvider is an optional extension for a media
// adapter that can fetch the terminal binary/content representation of a job.
type UnifiedGatewayGrokMediaContentProvider interface {
	Content(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest, upstreamRequestID string) (UnifiedGatewayContentResult, error)
}

// UnifiedGatewayUpstreamHTTPError records a non-2xx response without copying
// an upstream error body into an error string. The body is intentionally not
// exposed here because it may contain provider diagnostics or credentials.
type UnifiedGatewayUpstreamHTTPError struct {
	StatusCode int
	RequestID  string
}

func (e *UnifiedGatewayUpstreamHTTPError) Error() string {
	if e == nil {
		return ErrUnifiedGatewayUpstreamFailed.Error()
	}
	if strings.TrimSpace(e.RequestID) == "" {
		return fmt.Sprintf("%s: upstream returned HTTP %d", ErrUnifiedGatewayUpstreamFailed, e.StatusCode)
	}
	return fmt.Sprintf("%s: upstream returned HTTP %d (request %s)", ErrUnifiedGatewayUpstreamFailed, e.StatusCode, e.RequestID)
}

func (e *UnifiedGatewayUpstreamHTTPError) Unwrap() error {
	return ErrUnifiedGatewayUpstreamFailed
}

// HTTPUnifiedGatewayUpstreamExecutor is the production-shaped executor for
// API-key OpenAI-compatible accounts. It is intentionally independent from
// the /v1 handlers: callers inject a repository, HTTPUpstream, and (optionally)
// a Grok media adapter, while tests can inject fakes through the same seams.
type HTTPUnifiedGatewayUpstreamExecutor struct {
	accountRepo               AccountRepository
	httpUpstream              HTTPUpstream
	openAIGateway             *OpenAIGatewayService
	anthropicGatewayService   *GatewayService
	geminiCompatService       *GeminiMessagesCompatService
	antigravityGatewayService *AntigravityGatewayService
	cfg                       *config.Config

	allowLocalhost       bool
	allowLocalhostHTTP   bool
	upstreamTimeout      time.Duration
	requestBodyMaxBytes  int64
	responseBodyMaxBytes int64
	grokMediaAdapter     UnifiedGatewayGrokMediaAdapter
}

var _ UnifiedGatewayUpstreamExecutor = (*HTTPUnifiedGatewayUpstreamExecutor)(nil)

// NewHTTPUnifiedGatewayUpstreamExecutor constructs the stable unified runtime
// executor contract. It does not perform network I/O. If either repository or
// transport is nil, Forward fails closed when called.
func NewHTTPUnifiedGatewayUpstreamExecutor(
	accountRepo AccountRepository,
	httpUpstream HTTPUpstream,
	openAIGateway *OpenAIGatewayService,
	cfg *config.Config,
) *HTTPUnifiedGatewayUpstreamExecutor {
	if accountRepo == nil && openAIGateway != nil {
		accountRepo = openAIGateway.accountRepo
	}
	if httpUpstream == nil && openAIGateway != nil {
		httpUpstream = openAIGateway.httpUpstream
	}
	if cfg == nil && openAIGateway != nil {
		cfg = openAIGateway.cfg
	}

	upstreamTimeout := UnifiedGatewayDefaultUpstreamTimeout
	requestBodyMaxBytes := UnifiedGatewayDefaultUpstreamRequestBodyMaxBytes
	responseBodyMaxBytes := config.DefaultUpstreamResponseReadMaxBytes
	if cfg != nil {
		if cfg.Gateway.OpenAIResponseHeaderTimeout > 0 {
			upstreamTimeout = time.Duration(cfg.Gateway.OpenAIResponseHeaderTimeout) * time.Second
		} else if cfg.Gateway.ResponseHeaderTimeout > 0 {
			upstreamTimeout = time.Duration(cfg.Gateway.ResponseHeaderTimeout) * time.Second
		}
		if cfg.Gateway.MaxBodySize > 0 {
			requestBodyMaxBytes = cfg.Gateway.MaxBodySize
		}
		if cfg.Gateway.UpstreamResponseReadMaxBytes > 0 {
			responseBodyMaxBytes = cfg.Gateway.UpstreamResponseReadMaxBytes
		}
	}

	return &HTTPUnifiedGatewayUpstreamExecutor{
		accountRepo:          accountRepo,
		httpUpstream:         httpUpstream,
		openAIGateway:        openAIGateway,
		cfg:                  cfg,
		allowLocalhost:       cfg != nil && cfg.Security.URLAllowlist.AllowPrivateHosts,
		allowLocalhostHTTP:   cfg != nil && cfg.Security.URLAllowlist.AllowPrivateHosts && cfg.Security.URLAllowlist.AllowInsecureHTTP,
		upstreamTimeout:      upstreamTimeout,
		requestBodyMaxBytes:  requestBodyMaxBytes,
		responseBodyMaxBytes: responseBodyMaxBytes,
	}
}

// SetGrokMediaAdapter attaches the optional Grok media transport. It is
// intended to be called during application construction, before requests are
// accepted. Leaving it unset makes Grok media fail closed.
func (e *HTTPUnifiedGatewayUpstreamExecutor) SetGrokMediaAdapter(adapter UnifiedGatewayGrokMediaAdapter) {
	if e == nil {
		return
	}
	e.grokMediaAdapter = adapter
}

// SetGeminiCompatService attaches the existing Gemini/AI-Studio compatibility
// transport.  The setter keeps the constructor stable for focused tests while
// allowing the application wire to reuse the provider-specific auth and
// response conversion that already powers the legacy gateway.
func (e *HTTPUnifiedGatewayUpstreamExecutor) SetGeminiCompatService(service *GeminiMessagesCompatService) {
	if e == nil {
		return
	}
	e.geminiCompatService = service
}

// SetAnthropicGatewayService attaches the existing OpenAI-to-Anthropic
// conversion transport.  It is deliberately optional and fail-closed so the
// isolated runtime cannot silently fall back to a raw protocol mismatch.
func (e *HTTPUnifiedGatewayUpstreamExecutor) SetAnthropicGatewayService(service *GatewayService) {
	if e == nil {
		return
	}
	e.anthropicGatewayService = service
}

// SetAntigravityGatewayService attaches the existing Antigravity OAuth
// compatibility transport.  It is deliberately optional and fail-closed.
func (e *HTTPUnifiedGatewayUpstreamExecutor) SetAntigravityGatewayService(service *AntigravityGatewayService) {
	if e == nil {
		return
	}
	e.antigravityGatewayService = service
}

// Forward resolves the account selected by the frozen route binding, rewrites
// the upstream model, and forwards one bounded OpenAI-compatible request.
// Grok media is delegated to the optional media seam; no production media
// protocol is guessed here.
func (e *HTTPUnifiedGatewayUpstreamExecutor) Forward(ctx context.Context, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if e == nil || e.accountRepo == nil || (e.httpUpstream == nil && e.openAIGateway == nil) {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: account repository and HTTPUpstream are required", ErrUnifiedGatewayUpstreamUnsupported)
	}
	if strings.TrimSpace(request.PublicModel) == "" {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: public model is required", ErrUnifiedGatewayInvalidRequest)
	}
	if len(request.RawBody) == 0 {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: request body is required", ErrUnifiedGatewayInvalidRequest)
	}
	if int64(len(request.RawBody)) > e.requestBodyMaxBytes {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: got %d bytes, limit %d", ErrUnifiedGatewayUpstreamRequestBodyTooLarge, len(request.RawBody), e.requestBodyMaxBytes)
	}

	endpoint, err := normalizeUnifiedGatewayUpstreamEndpoint(selection.Endpoint())
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	account, accountID, err := e.resolveSelectedAccount(ctx, selection)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}

	// Images can be served by both ordinary OpenAI-compatible accounts and
	// Grok accounts. Videos are always handled by the Grok media adapter. Do
	// not classify every image request as Grok, or a normal DALL-E-compatible
	// account would be rejected before the OpenAI transport is reached.
	if endpoint == unifiedGatewayUpstreamEndpointVideos ||
		(account.IsGrok() && isUnifiedGatewayGrokImageEndpoint(endpoint)) {
		if !account.IsGrok() {
			return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: endpoint %s requires a Grok account", ErrUnifiedGatewayUpstreamUnsupported, endpoint)
		}
		return e.forwardGrokMedia(ctx, account, selection, request)
	}
	// OpenAI subscription accounts (Plus/Pro OAuth or setup tokens) use the
	// existing, provider-specific ChatGPT transport. Reusing that audited
	// service behind a private recorder keeps the unified billing state machine
	// in charge while preserving OAuth headers, Codex transforms, and image
	// handling that cannot be reproduced by a generic API-key HTTP request.
	if account.IsOpenAI() && account.Type != AccountTypeAPIKey {
		return e.forwardOpenAILegacy(ctx, account, selection, request)
	}
	if account.Platform == PlatformGemini {
		return e.forwardGeminiCompatibility(ctx, account, selection, request)
	}
	if account.Platform == PlatformAntigravity {
		if account.Type == AccountTypeOAuth {
			return e.forwardAntigravityCompatibility(ctx, account, selection, request)
		}
		return e.forwardGeminiCompatibility(ctx, account, selection, request)
	}
	// Generic Anthropic API-key accounts expose native Messages upstream, but
	// the unified public contract remains OpenAI-shaped. Reuse the audited
	// GatewayService conversion path rather than sending OpenAI JSON directly
	// to /v1/messages.
	if account.Platform == PlatformAnthropic && account.Type == AccountTypeAPIKey {
		return e.forwardAnthropicCompatibility(ctx, account, selection, request)
	}
	// CN accounts configured for native Anthropic transport still use the
	// existing OpenAI↔Anthropic conversion path.  Their platform is marked as
	// OpenAI-compatible for scheduler/model purposes, but their base URL is an
	// Anthropic endpoint; sending them through the generic OpenAI HTTP adapter
	// would incorrectly append /chat/completions or /responses to that URL.
	if account.IsAnthropicProtocol() {
		if account.Type != AccountTypeAPIKey && account.Type != AccountTypeUpstream {
			return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Anthropic-protocol account type %q is unsupported", ErrUnifiedGatewayUpstreamUnsupported, account.Type)
		}
		return e.forwardOpenAILegacy(ctx, account, selection, request)
	}
	if !account.IsOpenAICompatible() {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: account platform %q is not OpenAI-compatible", ErrUnifiedGatewayUpstreamUnsupported, account.Platform)
	}
	if account.Type != AccountTypeAPIKey {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: account type %q is not an API-key account", ErrUnifiedGatewayUpstreamUnsupported, account.Type)
	}
	if endpoint == unifiedGatewayUpstreamEndpointChatCompletions || endpoint == unifiedGatewayUpstreamEndpointResponses {
		capability := OpenAIEndpointCapabilityChatCompletions
		if endpoint == unifiedGatewayUpstreamEndpointResponses {
			capability = OpenAIEndpointCapabilityResponses
		}
		if !account.SupportsOpenAIEndpointCapability(capability) {
			return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: account does not advertise %s", ErrUnifiedGatewayUpstreamUnsupported, endpoint)
		}
	}

	apiKey := strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
	if account.IsGrok() {
		apiKey = strings.TrimSpace(account.GetCredential("api_key"))
	}
	if apiKey == "" {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: API key is missing", ErrUnifiedGatewayUpstreamUnsupported)
	}

	upstreamModel := unifiedGatewayUpstreamModel(account, selection, request.PublicModel)
	contentType := unifiedGatewayRequestContentType(ctx)
	rewrittenBody, rewrittenContentType, err := rewriteUnifiedGatewayRequestBody(
		endpoint,
		request.RawBody,
		contentType,
		upstreamModel,
	)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	rewrittenBody, rewrittenContentType, upstreamEndpoint, err := e.adaptUnifiedGatewayProviderRequest(
		account,
		endpoint,
		upstreamModel,
		rewrittenBody,
		rewrittenContentType,
	)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	baseURL := unifiedGatewayOpenAIBaseURLForRequest(account, upstreamEndpoint)
	validatedBaseURL, local, err := e.validateBaseURL(ctx, baseURL)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	targetURL, err := unifiedGatewayTargetURL(account, upstreamEndpoint, validatedBaseURL)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}

	if int64(len(rewrittenBody)) > e.requestBodyMaxBytes {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: rewritten body is %d bytes, limit %d", ErrUnifiedGatewayUpstreamRequestBodyTooLarge, len(rewrittenBody), e.requestBodyMaxBytes)
	}

	requestCtx := WithHTTPUpstreamRedirectsDisabled(ctx)
	requestCtx = WithHTTPUpstreamProfile(requestCtx, HTTPUpstreamProfileOpenAI)
	if !local {
		requestCtx = WithHTTPUpstreamPublicHostsOnly(requestCtx)
	}
	timeout := e.timeoutForEndpoint(endpoint)
	if timeout > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(requestCtx, timeout)
		defer cancel()
	}

	upstreamRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, targetURL, bytes.NewReader(rewrittenBody))
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: build upstream request: %v", ErrUnifiedGatewayInvalidRequest, err)
	}
	upstreamRequest.Header.Set("Authorization", "Bearer "+apiKey)
	upstreamRequest.Header.Set("Content-Type", rewrittenContentType)
	upstreamRequest.Header.Set("Accept", "application/json")
	if userAgent := strings.TrimSpace(account.GetOpenAIUserAgent()); userAgent != "" {
		upstreamRequest.Header.Set("User-Agent", userAgent)
	}
	account.ApplyHeaderOverrides(upstreamRequest.Header)

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	response, err := e.httpUpstream.DoWithTLS(upstreamRequest, proxyURL, accountID, account.Concurrency, nil)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: upstream request: %w", ErrUnifiedGatewayUpstreamFailed, err)
	}
	if requestCtx.Err() != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: upstream request context: %w", ErrUnifiedGatewayUpstreamFailed, requestCtx.Err())
	}
	if response == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: HTTPUpstream returned a nil response", ErrUnifiedGatewayUpstreamFailed)
	}
	if response.Body == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: upstream response body is nil", ErrUnifiedGatewayUpstreamFailed)
	}
	defer response.Body.Close()

	responseBody, err := readUnifiedGatewayResponseBody(response.Body, response.ContentLength, e.responseBodyMaxBytes)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	if requestCtx.Err() != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: upstream response context: %w", ErrUnifiedGatewayUpstreamFailed, requestCtx.Err())
	}
	requestID := unifiedGatewayResponseRequestID(response.Header, responseBody)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return UnifiedGatewayUpstreamResult{}, &UnifiedGatewayUpstreamHTTPError{StatusCode: response.StatusCode, RequestID: requestID}
	}
	if response.StatusCode == http.StatusAccepted {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: HTTP 202 asynchronous responses are not supported by the OpenAI-compatible adapter", ErrUnifiedGatewayUpstreamUnsupported)
	}
	if len(responseBody) == 0 {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: upstream returned an empty successful response", ErrUnifiedGatewayUpstreamFailed)
	}

	units, err := unifiedGatewayMeasuredUnits(endpoint, selection, response.Header, responseBody)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	return UnifiedGatewayUpstreamResult{
		Delivered:         true,
		MeasuredUnits:     units,
		UpstreamRequestID: requestID,
		ResponseBody:      cloneUnifiedGatewayBytes(responseBody),
	}, nil
}

func (e *HTTPUnifiedGatewayUpstreamExecutor) forwardAnthropicCompatibility(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
	if e == nil || e.anthropicGatewayService == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Anthropic compatibility transport is not configured", ErrUnifiedGatewayUpstreamUnsupported)
	}
	endpoint, err := normalizeUnifiedGatewayUpstreamEndpoint(selection.Endpoint())
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	if endpoint != unifiedGatewayUpstreamEndpointChatCompletions && endpoint != unifiedGatewayUpstreamEndpointResponses {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Anthropic compatibility does not support %s", ErrUnifiedGatewayUpstreamUnsupported, endpoint)
	}
	// Keep the public model in the body. The cloned account carries the
	// immutable route binding as a one-request model mapping so the shared
	// converter maps it to the selected upstream model.
	compatAccount := unifiedGatewayCompatAccount(account, request.PublicModel, selection.UpstreamModel())
	contentType := unifiedGatewayRequestContentType(ctx)
	body, contentType, err := rewriteUnifiedGatewayRequestBody(endpoint, request.RawBody, contentType, request.PublicModel)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	requestCtx, cancel := e.contextWithTimeout(ctx, endpoint)
	defer cancel()
	privatePath := "/unified/v1/responses"
	if endpoint == unifiedGatewayUpstreamEndpointChatCompletions {
		privatePath = "/unified/v1/chat/completions"
	}
	privateContext, recorder, err := unifiedGatewayPrivateGinContext(requestCtx, http.MethodPost, privatePath, body, contentType, request)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	var forwardResult *ForwardResult
	if endpoint == unifiedGatewayUpstreamEndpointResponses {
		forwardResult, err = e.anthropicGatewayService.ForwardAsResponses(requestCtx, privateContext, compatAccount, body, nil)
	} else {
		forwardResult, err = e.anthropicGatewayService.ForwardAsChatCompletions(requestCtx, privateContext, compatAccount, body, nil)
	}
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Anthropic compatibility forward: %w", ErrUnifiedGatewayUpstreamFailed, err)
	}
	if e.responseBodyMaxBytes > 0 && int64(recorder.Body.Len()) > e.responseBodyMaxBytes {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Anthropic compatibility response exceeded limit %d", ErrUnifiedGatewayUpstreamResponseBodyTooLarge, e.responseBodyMaxBytes)
	}
	return unifiedGatewayForwardResultToUpstreamResult(selection, recorder, forwardResult)
}

// Poll completes an asynchronous Grok media job when the injected media
// adapter implements UnifiedGatewayGrokMediaPoller. The OpenAI-compatible
// adapter has no safe generic polling protocol, so it never fabricates a
// terminal result.
func (e *HTTPUnifiedGatewayUpstreamExecutor) Poll(ctx context.Context, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest, upstreamRequestID string) (UnifiedGatewayUpstreamResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	upstreamRequestID = strings.TrimSpace(upstreamRequestID)
	if upstreamRequestID == "" {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: upstream request ID is required for polling", ErrUnifiedGatewayInvalidRequest)
	}
	account, _, err := e.resolveSelectedAccount(ctx, selection)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	endpoint, err := normalizeUnifiedGatewayUpstreamEndpoint(selection.Endpoint())
	if err != nil || endpoint != unifiedGatewayUpstreamEndpointVideos {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: polling requires a Grok media endpoint", ErrUnifiedGatewayUpstreamUnsupported)
	}
	if !account.IsGrok() || e.grokMediaAdapter == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: no asynchronous media poller is configured", ErrUnifiedGatewayUpstreamUnsupported)
	}
	poller, ok := e.grokMediaAdapter.(UnifiedGatewayGrokMediaPoller)
	if !ok || poller == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Grok media adapter does not implement Poll", ErrUnifiedGatewayUpstreamUnsupported)
	}
	mediaCtx, cancel := e.contextWithTimeout(ctx, endpoint)
	defer cancel()
	result, err := poller.Poll(mediaCtx, account, selection, request, upstreamRequestID)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Grok media poll: %w", ErrUnifiedGatewayUpstreamFailed, err)
	}
	if mediaCtx.Err() != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Grok media poll context: %w", ErrUnifiedGatewayUpstreamFailed, mediaCtx.Err())
	}
	if e.responseBodyMaxBytes > 0 && int64(len(result.ResponseBody)) > e.responseBodyMaxBytes {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: polled response exceeded limit %d", ErrUnifiedGatewayUpstreamResponseBodyTooLarge, e.responseBodyMaxBytes)
	}
	return validateUnifiedGatewayAsyncResult(result)
}

// Content fetches terminal Grok media bytes through the optional content
// provider. A missing provider or an empty/non-2xx result fails closed.
func (e *HTTPUnifiedGatewayUpstreamExecutor) Content(ctx context.Context, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest, upstreamRequestID string) (UnifiedGatewayContentResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	upstreamRequestID = strings.TrimSpace(upstreamRequestID)
	if upstreamRequestID == "" {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: upstream request ID is required for media content", ErrUnifiedGatewayInvalidRequest)
	}
	account, _, err := e.resolveSelectedAccount(ctx, selection)
	if err != nil {
		return UnifiedGatewayContentResult{}, err
	}
	endpoint, err := normalizeUnifiedGatewayUpstreamEndpoint(selection.Endpoint())
	if err != nil || endpoint != unifiedGatewayUpstreamEndpointVideos {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: content requires a Grok media endpoint", ErrUnifiedGatewayUpstreamUnsupported)
	}
	if !account.IsGrok() || e.grokMediaAdapter == nil {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: no media content provider is configured", ErrUnifiedGatewayUpstreamUnsupported)
	}
	provider, ok := e.grokMediaAdapter.(UnifiedGatewayGrokMediaContentProvider)
	if !ok || provider == nil {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: Grok media adapter does not implement Content", ErrUnifiedGatewayUpstreamUnsupported)
	}
	mediaCtx, cancel := e.contextWithTimeout(ctx, endpoint)
	defer cancel()
	result, err := provider.Content(mediaCtx, account, selection, request, upstreamRequestID)
	if err != nil {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: Grok media content: %w", ErrUnifiedGatewayUpstreamFailed, err)
	}
	if mediaCtx.Err() != nil {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: Grok media content context: %w", ErrUnifiedGatewayUpstreamFailed, mediaCtx.Err())
	}
	if e.responseBodyMaxBytes > 0 && int64(len(result.Body)) > e.responseBodyMaxBytes {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: media content exceeded limit %d", ErrUnifiedGatewayUpstreamResponseBodyTooLarge, e.responseBodyMaxBytes)
	}
	return validateUnifiedGatewayContentResult(result)
}

const (
	unifiedGatewayUpstreamEndpointChatCompletions = "chat_completions"
	unifiedGatewayUpstreamEndpointResponses       = "responses"
	unifiedGatewayUpstreamEndpointImages          = "images_generations"
	unifiedGatewayUpstreamEndpointImagesEdits     = "images_edits"
	unifiedGatewayUpstreamEndpointVideos          = "videos"
)

func normalizeUnifiedGatewayUpstreamEndpoint(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.TrimRight(value, "/")
	switch value {
	case UnifiedGatewayEndpointChatCompletions, "chat/completions", "/v1/chat/completions", "chat-completions":
		return unifiedGatewayUpstreamEndpointChatCompletions, nil
	case UnifiedGatewayEndpointResponses, "/v1/responses", "/responses":
		return unifiedGatewayUpstreamEndpointResponses, nil
	case UnifiedGatewayEndpointImages, "images", "image", "images/generations", "/v1/images/generations":
		return unifiedGatewayUpstreamEndpointImages, nil
	case "images_edits", "image_edits", "images/edits", "/v1/images/edits":
		return unifiedGatewayUpstreamEndpointImagesEdits, nil
	case UnifiedGatewayEndpointVideos, "videos_generations", "video", "videos/generations", "/v1/videos/generations":
		return unifiedGatewayUpstreamEndpointVideos, nil
	default:
		return "", fmt.Errorf("%w: endpoint %q", ErrUnifiedGatewayUpstreamUnsupported, raw)
	}
}

func (e *HTTPUnifiedGatewayUpstreamExecutor) resolveSelectedAccount(ctx context.Context, selection UnifiedGatewayRouteSelection) (*Account, int64, error) {
	if e == nil || e.accountRepo == nil {
		return nil, 0, fmt.Errorf("%w: account repository is required", ErrUnifiedGatewayUpstreamUnsupported)
	}
	accountID := selection.Binding.AccountID
	if accountID <= 0 {
		return nil, 0, fmt.Errorf("%w: selected account ID is required", ErrUnifiedGatewayInvalidRequest)
	}
	account, err := e.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: resolve account %d: %v", ErrUnifiedGatewayUpstreamFailed, accountID, err)
	}
	if account == nil {
		return nil, 0, fmt.Errorf("%w: account %d was not found", ErrUnifiedGatewayUpstreamFailed, accountID)
	}
	if account.ID > 0 && account.ID != accountID {
		return nil, 0, fmt.Errorf("%w: resolved account ID %d does not match binding %d", ErrUnifiedGatewayInvalidRequest, account.ID, accountID)
	}
	return account, accountID, nil
}

// Eligible lets the runtime discard accounts that became unavailable after the
// immutable route catalog was published. The catalog remains the source of
// route identity and price, while the live account scheduler state is checked
// immediately before reserving a snapshot. This keeps a disabled, expired,
// rate-limited, overloaded, or quota-exhausted account from receiving traffic
// merely because its binding is still enabled.
func (e *HTTPUnifiedGatewayUpstreamExecutor) Eligible(ctx context.Context, selection UnifiedGatewayRouteSelection, _ UnifiedGatewayRequest) (bool, error) {
	account, _, err := e.resolveSelectedAccount(ctx, selection)
	if err != nil {
		return false, err
	}
	return account.IsSchedulable(), nil
}

func (e *HTTPUnifiedGatewayUpstreamExecutor) forwardOpenAILegacy(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
	if e == nil || e.openAIGateway == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: OpenAI subscription transport is not configured", ErrUnifiedGatewayUpstreamUnsupported)
	}
	endpoint, err := normalizeUnifiedGatewayUpstreamEndpoint(selection.Endpoint())
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	if account.IsAnthropicProtocol() && (endpoint == unifiedGatewayUpstreamEndpointImages || endpoint == unifiedGatewayUpstreamEndpointImagesEdits || endpoint == unifiedGatewayUpstreamEndpointVideos) {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: native Anthropic accounts do not support %s in the unified OpenAI adapter", ErrUnifiedGatewayUpstreamUnsupported, endpoint)
	}
	upstreamModel := unifiedGatewayUpstreamModel(account, selection, request.PublicModel)
	contentType := unifiedGatewayRequestContentType(ctx)
	body, contentType, err := rewriteUnifiedGatewayRequestBody(endpoint, request.RawBody, contentType, upstreamModel)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	body, contentType, upstreamEndpoint, err := e.adaptUnifiedGatewayProviderRequest(account, endpoint, upstreamModel, body, contentType)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}

	path := "/v1/chat/completions"
	switch upstreamEndpoint {
	case unifiedGatewayUpstreamEndpointResponses:
		path = "/v1/responses"
	case unifiedGatewayUpstreamEndpointImages:
		path = "/v1/images/generations"
	case unifiedGatewayUpstreamEndpointImagesEdits:
		path = "/v1/images/edits"
	case unifiedGatewayUpstreamEndpointChatCompletions:
	default:
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: OpenAI subscription endpoint %s is unsupported", ErrUnifiedGatewayUpstreamUnsupported, upstreamEndpoint)
	}
	requestCtx, cancel := e.contextWithTimeout(ctx, endpoint)
	defer cancel()
	privateRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, "https://unified-gateway.invalid"+path, bytes.NewReader(body))
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: build private OpenAI request: %v", ErrUnifiedGatewayInvalidRequest, err)
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json"
	}
	privateRequest.Header.Set("Content-Type", contentType)
	privateRequest.Header.Set("Accept", "application/json")
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = privateRequest
	groupID := request.AccessGroupID
	ginContext.Set("api_key", &APIKey{
		ID:      request.APIKeyID,
		UserID:  request.UserID,
		GroupID: &groupID,
		User:    &User{ID: request.UserID},
	})

	var forwardResult *OpenAIForwardResult
	switch upstreamEndpoint {
	case unifiedGatewayUpstreamEndpointChatCompletions:
		forwardResult, err = e.openAIGateway.ForwardAsChatCompletions(requestCtx, ginContext, account, body, "", "")
	case unifiedGatewayUpstreamEndpointResponses:
		forwardResult, err = e.openAIGateway.Forward(requestCtx, ginContext, account, body)
	case unifiedGatewayUpstreamEndpointImages, unifiedGatewayUpstreamEndpointImagesEdits:
		parsed, parseErr := e.parseUnifiedGatewayImagesRequest(upstreamEndpoint, body, contentType)
		if parseErr != nil {
			return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: parse OpenAI subscription image request: %v", ErrUnifiedGatewayInvalidRequest, parseErr)
		}
		forwardResult, err = e.openAIGateway.ForwardImages(requestCtx, ginContext, account, body, parsed, upstreamModel)
	}
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: OpenAI subscription forward: %w", ErrUnifiedGatewayUpstreamFailed, err)
	}
	if requestCtx.Err() != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: OpenAI subscription forward context: %v", ErrUnifiedGatewayUpstreamFailed, requestCtx.Err())
	}
	if forwardResult == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: OpenAI subscription forward returned no result", ErrUnifiedGatewayUpstreamFailed)
	}
	statusCode := recorder.Code
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return UnifiedGatewayUpstreamResult{}, &UnifiedGatewayUpstreamHTTPError{
			StatusCode: statusCode,
			RequestID:  unifiedGatewayResponseRequestID(forwardResult.UpstreamHeaders, recorder.Body.Bytes()),
		}
	}
	body = append([]byte(nil), recorder.Body.Bytes()...)
	if len(body) == 0 {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: OpenAI subscription returned an empty successful response", ErrUnifiedGatewayUpstreamFailed)
	}
	units := float64(forwardResult.Usage.InputTokens + forwardResult.Usage.ImageInputTokens + forwardResult.Usage.OutputTokens + forwardResult.Usage.ImageOutputTokens)
	if upstreamEndpoint == unifiedGatewayUpstreamEndpointImages || upstreamEndpoint == unifiedGatewayUpstreamEndpointImagesEdits {
		units = float64(forwardResult.ImageCount)
		if units <= 0 {
			units = float64(extractOpenAIImagesBillableCountFromJSONBytes(body))
		}
	}
	if units <= 0 {
		if unifiedGatewayPerRequestBilling(selection) {
			units = 1
		} else {
			return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: OpenAI subscription response did not contain positive usage", ErrUnifiedGatewayUsageMissing)
		}
	}
	requestID := firstNonEmptyString(
		unifiedGatewayResponseRequestID(forwardResult.UpstreamHeaders, body),
		unifiedGatewayResponseRequestID(forwardResult.ResponseHeaders, body),
		strings.TrimSpace(forwardResult.RequestID),
		strings.TrimSpace(forwardResult.ResponseID),
	)
	return UnifiedGatewayUpstreamResult{
		Delivered:         true,
		MeasuredUnits:     units,
		UpstreamRequestID: requestID,
		ResponseBody:      body,
	}, nil
}

func (e *HTTPUnifiedGatewayUpstreamExecutor) forwardGeminiCompatibility(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
	if e == nil || e.geminiCompatService == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Gemini compatibility transport is not configured", ErrUnifiedGatewayUpstreamUnsupported)
	}
	endpoint, err := normalizeUnifiedGatewayUpstreamEndpoint(selection.Endpoint())
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	if endpoint != unifiedGatewayUpstreamEndpointChatCompletions {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Gemini compatibility currently supports chat/completions only", ErrUnifiedGatewayUpstreamUnsupported)
	}
	// The existing Gemini adapter owns provider model mapping and whitelist
	// checks.  Keep the public model in the compatibility request and provide a
	// request-scoped mapping override when the unified binding explicitly names
	// a different upstream model.
	compatAccount := unifiedGatewayCompatAccount(account, request.PublicModel, selection.UpstreamModel())
	contentType := unifiedGatewayRequestContentType(ctx)
	body, contentType, err := rewriteUnifiedGatewayRequestBody(endpoint, request.RawBody, contentType, request.PublicModel)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	requestCtx, cancel := e.contextWithTimeout(ctx, endpoint)
	defer cancel()
	privateContext, recorder, err := unifiedGatewayPrivateGinContext(requestCtx, http.MethodPost, "/unified/v1/chat/completions", body, contentType, request)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	forwardResult, err := e.geminiCompatService.ForwardAsChatCompletions(requestCtx, privateContext, compatAccount, body)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Gemini compatibility forward: %w", ErrUnifiedGatewayUpstreamFailed, err)
	}
	return unifiedGatewayForwardResultToUpstreamResult(selection, recorder, forwardResult)
}

func (e *HTTPUnifiedGatewayUpstreamExecutor) forwardAntigravityCompatibility(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
	if e == nil || e.antigravityGatewayService == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Antigravity compatibility transport is not configured", ErrUnifiedGatewayUpstreamUnsupported)
	}
	endpoint, err := normalizeUnifiedGatewayUpstreamEndpoint(selection.Endpoint())
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	if endpoint != unifiedGatewayUpstreamEndpointChatCompletions && endpoint != unifiedGatewayUpstreamEndpointResponses {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Antigravity compatibility does not support %s", ErrUnifiedGatewayUpstreamUnsupported, endpoint)
	}
	compatAccount := unifiedGatewayCompatAccount(account, request.PublicModel, selection.UpstreamModel())
	contentType := unifiedGatewayRequestContentType(ctx)
	body, contentType, err := rewriteUnifiedGatewayRequestBody(endpoint, request.RawBody, contentType, request.PublicModel)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	requestCtx, cancel := e.contextWithTimeout(ctx, endpoint)
	defer cancel()
	privatePath := "/unified/v1/responses"
	if endpoint == unifiedGatewayUpstreamEndpointChatCompletions {
		privatePath = "/unified/v1/chat/completions"
	}
	privateContext, recorder, err := unifiedGatewayPrivateGinContext(requestCtx, http.MethodPost, privatePath, body, contentType, request)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	var forwardResult *ForwardResult
	if endpoint == unifiedGatewayUpstreamEndpointResponses {
		forwardResult, err = e.antigravityGatewayService.ForwardAsResponses(requestCtx, privateContext, compatAccount, body, nil)
	} else {
		forwardResult, err = e.antigravityGatewayService.ForwardAsChatCompletions(requestCtx, privateContext, compatAccount, body, nil)
	}
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Antigravity compatibility forward: %w", ErrUnifiedGatewayUpstreamFailed, err)
	}
	return unifiedGatewayForwardResultToUpstreamResult(selection, recorder, forwardResult)
}

func unifiedGatewayCompatAccount(account *Account, publicModel, upstreamModel string) *Account {
	if account == nil {
		return nil
	}
	publicModel = strings.TrimSpace(publicModel)
	upstreamModel = strings.TrimSpace(upstreamModel)
	if publicModel == "" || upstreamModel == "" {
		return account
	}
	clone := *account
	clone.Credentials = make(map[string]any, len(account.Credentials)+1)
	for key, value := range account.Credentials {
		clone.Credentials[key] = value
	}
	clone.Credentials["model_mapping"] = map[string]any{publicModel: upstreamModel}
	return &clone
}

func unifiedGatewayPrivateGinContext(ctx context.Context, method, path string, body []byte, contentType string, request UnifiedGatewayRequest) (*gin.Context, *httptest.ResponseRecorder, error) {
	privateRequest, err := http.NewRequestWithContext(ctx, method, "https://unified-gateway.invalid"+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: build private compatibility request: %v", ErrUnifiedGatewayInvalidRequest, err)
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json"
	}
	privateRequest.Header.Set("Content-Type", contentType)
	privateRequest.Header.Set("Accept", "application/json")
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = privateRequest
	groupID := request.AccessGroupID
	ginContext.Set("api_key", &APIKey{
		ID:      request.APIKeyID,
		UserID:  request.UserID,
		GroupID: &groupID,
		User:    &User{ID: request.UserID},
	})
	return ginContext, recorder, nil
}

func unifiedGatewayForwardResultToUpstreamResult(selection UnifiedGatewayRouteSelection, recorder *httptest.ResponseRecorder, forwardResult *ForwardResult) (UnifiedGatewayUpstreamResult, error) {
	if recorder == nil || forwardResult == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: compatibility transport returned no result", ErrUnifiedGatewayUpstreamFailed)
	}
	statusCode := recorder.Code
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	body := append([]byte(nil), recorder.Body.Bytes()...)
	requestID := firstNonEmptyString(
		unifiedGatewayResponseRequestID(recorder.Header(), body),
		unifiedGatewayResponseRequestID(forwardResult.UpstreamHeaders, body),
		strings.TrimSpace(forwardResult.RequestID),
	)
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return UnifiedGatewayUpstreamResult{}, &UnifiedGatewayUpstreamHTTPError{StatusCode: statusCode, RequestID: requestID}
	}
	if len(body) == 0 {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: compatibility transport returned an empty successful response", ErrUnifiedGatewayUpstreamFailed)
	}
	if unifiedGatewayPerRequestBilling(selection) {
		return UnifiedGatewayUpstreamResult{Delivered: true, MeasuredUnits: 1, UpstreamRequestID: requestID, ResponseBody: body}, nil
	}
	units := float64(forwardResult.Usage.InputTokens + forwardResult.Usage.OutputTokens + forwardResult.Usage.CacheCreationInputTokens + forwardResult.Usage.CacheReadInputTokens + forwardResult.Usage.ImageOutputTokens)
	if forwardResult.ImageCount > 0 {
		units = float64(forwardResult.ImageCount)
	}
	if !isFinitePositive(units) {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: compatibility response did not contain positive usage", ErrUnifiedGatewayUsageMissing)
	}
	return UnifiedGatewayUpstreamResult{Delivered: true, MeasuredUnits: units, UpstreamRequestID: requestID, ResponseBody: body}, nil
}

func (e *HTTPUnifiedGatewayUpstreamExecutor) forwardGrokMedia(ctx context.Context, account *Account, selection UnifiedGatewayRouteSelection, request UnifiedGatewayRequest) (UnifiedGatewayUpstreamResult, error) {
	if e == nil || e.grokMediaAdapter == nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Grok media adapter is not configured", ErrUnifiedGatewayUpstreamUnsupported)
	}
	endpoint, err := normalizeUnifiedGatewayUpstreamEndpoint(selection.Endpoint())
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, err
	}
	mediaCtx, cancel := e.contextWithTimeout(ctx, endpoint)
	defer cancel()
	result, err := e.grokMediaAdapter.Forward(mediaCtx, account, selection, request)
	if err != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Grok media forward: %w", ErrUnifiedGatewayUpstreamFailed, err)
	}
	if mediaCtx.Err() != nil {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: Grok media forward context: %w", ErrUnifiedGatewayUpstreamFailed, mediaCtx.Err())
	}
	if e.responseBodyMaxBytes > 0 && int64(len(result.ResponseBody)) > e.responseBodyMaxBytes {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: media forward response exceeded limit %d", ErrUnifiedGatewayUpstreamResponseBodyTooLarge, e.responseBodyMaxBytes)
	}
	return validateUnifiedGatewayAsyncResult(result)
}

func isUnifiedGatewayGrokMediaEndpoint(endpoint string) bool {
	return isUnifiedGatewayGrokImageEndpoint(endpoint) || endpoint == unifiedGatewayUpstreamEndpointVideos
}

func isUnifiedGatewayGrokImageEndpoint(endpoint string) bool {
	return endpoint == unifiedGatewayUpstreamEndpointImages || endpoint == unifiedGatewayUpstreamEndpointImagesEdits
}

func unifiedGatewayOpenAIBaseURL(account *Account, endpoint string) string {
	if account == nil {
		return ""
	}
	if account.IsGrok() {
		return strings.TrimSpace(account.GetGrokBaseURL())
	}
	if endpoint == unifiedGatewayUpstreamEndpointResponses && account.IsCNProvider() && account.UsesNativeCNResponses() {
		if baseURL := strings.TrimSpace(account.GetCNProtocolBaseURL(APIProtocolResponses)); baseURL != "" {
			return baseURL
		}
	}
	return strings.TrimSpace(account.GetOpenAIFormatBaseURL())
}

func unifiedGatewayTargetURL(account *Account, endpoint, baseURL string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return "", fmt.Errorf("%w: account base URL is missing", ErrUnifiedGatewayUpstreamUnsupported)
	}
	switch endpoint {
	case unifiedGatewayUpstreamEndpointChatCompletions:
		return buildOpenAIEndpointURL(baseURL, "/v1/chat/completions"), nil
	case unifiedGatewayUpstreamEndpointResponses:
		return buildOpenAIResponsesURLForPlatform(account.Platform, baseURL), nil
	case unifiedGatewayUpstreamEndpointImages:
		return buildOpenAIImagesURL(baseURL, openAIImagesGenerationsEndpoint), nil
	case unifiedGatewayUpstreamEndpointImagesEdits:
		return buildOpenAIImagesURL(baseURL, openAIImagesEditsEndpoint), nil
	default:
		return "", fmt.Errorf("%w: endpoint %s cannot be built as OpenAI-compatible HTTP", ErrUnifiedGatewayUpstreamUnsupported, endpoint)
	}
}

func unifiedGatewayOpenAIBaseURLForRequest(account *Account, endpoint string) string {
	if account != nil && (endpoint == unifiedGatewayUpstreamEndpointImages || endpoint == unifiedGatewayUpstreamEndpointImagesEdits) && isVolcengineArkOpenAIAccount(account) {
		if baseURL := strings.TrimSpace(volcengineArkImagesBaseURL(account)); baseURL != "" {
			return baseURL
		}
	}
	return unifiedGatewayOpenAIBaseURL(account, endpoint)
}

// adaptUnifiedGatewayProviderRequest applies only deterministic, provider
// documented transformations. The public endpoint and billing identity stay
// unchanged; upstreamEndpoint may differ when an Ark image edit is converted
// to the provider's generation-shaped request.
func (e *HTTPUnifiedGatewayUpstreamExecutor) adaptUnifiedGatewayProviderRequest(
	account *Account,
	endpoint string,
	upstreamModel string,
	body []byte,
	contentType string,
) ([]byte, string, string, error) {
	upstreamEndpoint := endpoint
	switch endpoint {
	case unifiedGatewayUpstreamEndpointChatCompletions:
		return body, contentType, upstreamEndpoint, nil
	case unifiedGatewayUpstreamEndpointResponses:
		filtered, err := filterOpenAIResponsesNoneReasoningEffortForAccount(account, body)
		if err != nil {
			return nil, "", "", fmt.Errorf("%w: normalize Responses reasoning: %v", ErrUnifiedGatewayInvalidRequest, err)
		}
		body = normalizeDeepSeekResponsesRequestBody(account, filtered)
		if !isVolcengineArkOpenAIAccount(account) {
			return body, contentType, upstreamEndpoint, nil
		}

		var responsesReq apicompat.ResponsesRequest
		if err := json.Unmarshal(body, &responsesReq); err != nil {
			return nil, "", "", fmt.Errorf("%w: parse Ark Responses request: %v", ErrUnifiedGatewayInvalidRequest, err)
		}
		if sanitizeVolcengineArkResponsesRequest(account, &responsesReq) {
			for _, field := range []string{"reasoning", "text"} {
				body, err = sjson.DeleteBytes(body, field)
				if err != nil {
					return nil, "", "", fmt.Errorf("%w: remove Ark Responses field %s: %v", ErrUnifiedGatewayInvalidRequest, field, err)
				}
			}
		}
		if responsesRequestHasInputImage(&responsesReq) {
			if restored := strings.TrimSpace(volcengineArkMultimodalEndpointModel(account, upstreamModel)); restored != "" && restored != upstreamModel {
				body, err = sjson.SetBytes(body, "model", restored)
				if err != nil {
					return nil, "", "", fmt.Errorf("%w: restore Ark multimodal model: %v", ErrUnifiedGatewayInvalidRequest, err)
				}
			}
		}
		return body, contentType, upstreamEndpoint, nil
	case unifiedGatewayUpstreamEndpointImages, unifiedGatewayUpstreamEndpointImagesEdits:
		if !isVolcengineArkOpenAIAccount(account) || !isVolcengineArkImageModel(upstreamModel) {
			return body, contentType, upstreamEndpoint, nil
		}
		parsed, err := e.parseUnifiedGatewayImagesRequest(endpoint, body, contentType)
		if err != nil {
			return nil, "", "", fmt.Errorf("%w: parse Ark image request: %v", ErrUnifiedGatewayInvalidRequest, err)
		}
		parsed.Model = strings.TrimSpace(upstreamModel)
		if adaptedBody, adaptedContentType, adaptedEndpoint, adapted, adaptErr := adaptVolcengineArkImagesToGeneration(account, parsed); adaptErr != nil {
			return nil, "", "", adaptErr
		} else if adapted {
			body = adaptedBody
			contentType = adaptedContentType
			upstreamEndpoint, err = normalizeUnifiedGatewayUpstreamEndpoint(adaptedEndpoint)
			if err != nil {
				return nil, "", "", fmt.Errorf("%w: normalize Ark image endpoint: %v", ErrUnifiedGatewayInvalidRequest, err)
			}
			parsed.Endpoint = openAIImagesGenerationsEndpoint
		}
		body, contentType, err = sanitizeVolcengineArkImagesRequest(account, body, contentType, parsed)
		if err != nil {
			return nil, "", "", err
		}
		return body, contentType, upstreamEndpoint, nil
	default:
		return body, contentType, upstreamEndpoint, nil
	}
}

func (e *HTTPUnifiedGatewayUpstreamExecutor) parseUnifiedGatewayImagesRequest(endpoint string, body []byte, contentType string) (*OpenAIImagesRequest, error) {
	parser := e.openAIGateway
	if parser == nil {
		parser = &OpenAIGatewayService{}
	}
	path := "/v1/images/generations"
	if endpoint == unifiedGatewayUpstreamEndpointImagesEdits {
		path = "/v1/images/edits"
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	if strings.TrimSpace(contentType) != "" {
		request.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = request
	return parser.ParseOpenAIImagesRequest(ginContext, body)
}

func unifiedGatewayUpstreamModel(account *Account, selection UnifiedGatewayRouteSelection, publicModel string) string {
	if model := strings.TrimSpace(selection.UpstreamModel()); model != "" {
		return model
	}
	if account != nil {
		if model := strings.TrimSpace(account.GetMappedModel(publicModel)); model != "" {
			return model
		}
	}
	return strings.TrimSpace(publicModel)
}

func rewriteUnifiedGatewayRequestBody(endpoint string, body []byte, contentType, model string) ([]byte, string, error) {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "application/json"
	}
	mediaType, _, mediaTypeErr := mime.ParseMediaType(contentType)
	if mediaTypeErr != nil {
		return nil, "", fmt.Errorf("%w: invalid request content type: %v", ErrUnifiedGatewayInvalidRequest, mediaTypeErr)
	}
	if endpoint == unifiedGatewayUpstreamEndpointChatCompletions || endpoint == unifiedGatewayUpstreamEndpointResponses {
		if !strings.EqualFold(mediaType, "application/json") || !gjson.ValidBytes(body) {
			return nil, "", fmt.Errorf("%w: %s requires a JSON request body", ErrUnifiedGatewayInvalidRequest, endpoint)
		}
		if strings.TrimSpace(model) == "" {
			return cloneUnifiedGatewayBytes(body), contentType, nil
		}
		rewritten, err := sjson.SetBytes(body, "model", model)
		if err != nil {
			return nil, "", fmt.Errorf("%w: rewrite model: %v", ErrUnifiedGatewayInvalidRequest, err)
		}
		return rewritten, contentType, nil
	}
	if endpoint == unifiedGatewayUpstreamEndpointImages || endpoint == unifiedGatewayUpstreamEndpointImagesEdits {
		if !strings.EqualFold(mediaType, "multipart/form-data") && !gjson.ValidBytes(body) {
			return nil, "", fmt.Errorf("%w: images requires a JSON or multipart request body", ErrUnifiedGatewayInvalidRequest)
		}
		rewritten, rewrittenContentType, err := rewriteOpenAIImagesModel(body, contentType, model)
		if err != nil {
			return nil, "", fmt.Errorf("%w: rewrite image request: %v", ErrUnifiedGatewayInvalidRequest, err)
		}
		return rewritten, rewrittenContentType, nil
	}
	return nil, "", fmt.Errorf("%w: endpoint %s has no request-body adapter", ErrUnifiedGatewayUpstreamUnsupported, endpoint)
}

func (e *HTTPUnifiedGatewayUpstreamExecutor) validateBaseURL(ctx context.Context, raw string) (string, bool, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false, fmt.Errorf("%w: invalid account base URL", ErrUnifiedGatewayInvalidRequest)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false, fmt.Errorf("%w: base URL must not contain userinfo, query, or fragment", ErrUnifiedGatewayInvalidRequest)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", false, fmt.Errorf("%w: base URL host is missing", ErrUnifiedGatewayInvalidRequest)
	}
	local := isUnifiedGatewayLocalhost(host)
	if local {
		if e == nil || !e.allowLocalhost {
			return "", false, fmt.Errorf("%w: localhost upstreams are disabled", ErrUnifiedGatewayUpstreamUnsupported)
		}
		if strings.EqualFold(parsed.Scheme, "http") && !e.allowLocalhostHTTP {
			return "", false, fmt.Errorf("%w: insecure localhost upstreams require an explicit test configuration", ErrUnifiedGatewayUpstreamUnsupported)
		}
		if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
			return "", false, fmt.Errorf("%w: invalid localhost URL scheme %q", ErrUnifiedGatewayInvalidRequest, parsed.Scheme)
		}
	} else {
		if !strings.EqualFold(parsed.Scheme, "https") {
			return "", false, fmt.Errorf("%w: non-localhost upstreams require HTTPS", ErrUnifiedGatewayUpstreamUnsupported)
		}
		if urlvalidator.IsBlockedHost(host) {
			return "", false, fmt.Errorf("%w: private or loopback upstream host is not allowed", ErrUnifiedGatewayUpstreamUnsupported)
		}
	}
	if parsed.Port() != "" {
		port, portErr := strconv.Atoi(parsed.Port())
		if portErr != nil || port <= 0 || port > 65535 {
			return "", false, fmt.Errorf("%w: invalid upstream port", ErrUnifiedGatewayInvalidRequest)
		}
	}

	var normalized string
	if e != nil && e.openAIGateway != nil && !local {
		normalized, err = e.openAIGateway.validateUpstreamBaseURL(trimmed)
	} else if e != nil && e.cfg != nil && e.cfg.Security.URLAllowlist.Enabled {
		normalized, err = urlvalidator.ValidateHTTPURL(trimmed, local && e.allowLocalhostHTTP, urlvalidator.ValidationOptions{
			AllowedHosts:     e.cfg.Security.URLAllowlist.UpstreamHosts,
			RequireAllowlist: true,
			AllowPrivate:     local,
		})
	} else {
		normalized, err = urlvalidator.ValidateHTTPURL(trimmed, local && e.allowLocalhostHTTP, urlvalidator.ValidationOptions{AllowPrivate: local})
	}
	if err != nil {
		return "", false, fmt.Errorf("%w: validate upstream base URL: %v", ErrUnifiedGatewayInvalidRequest, err)
	}
	// The legacy service validator can intentionally allow insecure HTTP via
	// config. Keep this adapter's public-host contract stricter.
	if parsedNormalized, parseErr := url.Parse(normalized); parseErr != nil || parsedNormalized.Scheme == "" {
		return "", false, fmt.Errorf("%w: normalized upstream base URL is invalid", ErrUnifiedGatewayInvalidRequest)
	} else if !local && !strings.EqualFold(parsedNormalized.Scheme, "https") {
		return "", false, fmt.Errorf("%w: normalized public upstream URL is not HTTPS", ErrUnifiedGatewayUpstreamUnsupported)
	}
	return normalized, local, nil
}

func isUnifiedGatewayLocalhost(host string) bool {
	normalized := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if normalized == "localhost" || strings.HasSuffix(normalized, ".localhost") {
		return true
	}
	if ip := net.ParseIP(normalized); ip != nil {
		return ip.IsLoopback() || (ip.To4() != nil && ip.To4()[0] == 127)
	}
	return false
}

func (e *HTTPUnifiedGatewayUpstreamExecutor) timeoutForEndpoint(endpoint string) time.Duration {
	if e == nil {
		return UnifiedGatewayDefaultUpstreamTimeout
	}
	if e.cfg != nil && (endpoint == unifiedGatewayUpstreamEndpointImages || endpoint == unifiedGatewayUpstreamEndpointImagesEdits) && e.cfg.Gateway.ImageUpstreamTimeoutSeconds > 0 {
		return time.Duration(e.cfg.Gateway.ImageUpstreamTimeoutSeconds) * time.Second
	}
	if e.cfg != nil && endpoint == unifiedGatewayUpstreamEndpointVideos && e.cfg.Gateway.GrokResponseHeaderTimeout > 0 {
		return time.Duration(e.cfg.Gateway.GrokResponseHeaderTimeout) * time.Second
	}
	if e.upstreamTimeout > 0 {
		return e.upstreamTimeout
	}
	return UnifiedGatewayDefaultUpstreamTimeout
}

func (e *HTTPUnifiedGatewayUpstreamExecutor) contextWithTimeout(ctx context.Context, endpoint string) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := e.timeoutForEndpoint(endpoint)
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

type unifiedGatewayRequestContentTypeContextKey struct{}

// WithUnifiedGatewayUpstreamContentType carries the original inbound content
// type for a request whose RawBody is multipart (notably image edits). The
// existing UnifiedGatewayRequest intentionally remains unchanged; JSON is the
// default when this helper is not used.
func WithUnifiedGatewayUpstreamContentType(ctx context.Context, contentType string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, unifiedGatewayRequestContentTypeContextKey{}, strings.TrimSpace(contentType))
}

func unifiedGatewayRequestContentType(ctx context.Context) string {
	if ctx != nil {
		if value, ok := ctx.Value(unifiedGatewayRequestContentTypeContextKey{}).(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "application/json"
}

func readUnifiedGatewayResponseBody(reader io.Reader, contentLength, maxBytes int64) ([]byte, error) {
	if reader == nil {
		return nil, fmt.Errorf("%w: response reader is nil", ErrUnifiedGatewayUpstreamFailed)
	}
	if maxBytes <= 0 {
		maxBytes = config.DefaultUpstreamResponseReadMaxBytes
	}
	if contentLength > maxBytes {
		return nil, fmt.Errorf("%w: content length %d exceeds limit %d", ErrUnifiedGatewayUpstreamResponseBodyTooLarge, contentLength, maxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read response body: %v", ErrUnifiedGatewayUpstreamFailed, err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%w: response exceeded limit %d", ErrUnifiedGatewayUpstreamResponseBodyTooLarge, maxBytes)
	}
	return body, nil
}

func unifiedGatewayResponseRequestID(header http.Header, body []byte) string {
	for _, key := range []string{"x-request-id", "xai-request-id", "request-id"} {
		if value := unifiedGatewayHeaderValue(header, key); value != "" {
			return value
		}
	}
	if isUnifiedGatewaySSEBody(header, body) {
		requestID := ""
		forEachOpenAISSEDataPayload(string(body), func(data []byte) {
			if requestID == "" {
				requestID = extractOpenAIResponseIDFromJSONBytes(data)
			}
		})
		return requestID
	}
	return extractOpenAIResponseIDFromJSONBytes(body)
}

// unifiedGatewayHeaderValue tolerates both canonical net/http headers and
// header maps assembled by adapters/tests with a non-canonical key spelling.
// The latter is legal as a map value even though Header.Get only performs a
// canonical lookup.
func unifiedGatewayHeaderValue(header http.Header, key string) string {
	if header == nil {
		return ""
	}
	if value := strings.TrimSpace(header.Get(key)); value != "" {
		return value
	}
	for actual, values := range header {
		if !strings.EqualFold(actual, key) {
			continue
		}
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func unifiedGatewayMeasuredUnits(endpoint string, selection UnifiedGatewayRouteSelection, header http.Header, body []byte) (float64, error) {
	isSSE := isUnifiedGatewaySSEBody(header, body)
	if unifiedGatewayResponseIsNonTerminalOrError(header, body) {
		return 0, fmt.Errorf("%w: upstream returned a non-terminal or error response", ErrUnifiedGatewayUpstreamFailed)
	}
	switch endpoint {
	case unifiedGatewayUpstreamEndpointChatCompletions, unifiedGatewayUpstreamEndpointResponses:
		if unifiedGatewayPerRequestBilling(selection) {
			if isSSE && !unifiedGatewaySSEHasPayload(body) {
				return 0, fmt.Errorf("%w: stream contained no semantic response payload", ErrUnifiedGatewayUpstreamFailed)
			}
			return 1, nil
		}
		var units float64
		if isSSE {
			forEachOpenAISSEDataPayload(string(body), func(data []byte) {
				if candidate, ok := unifiedGatewayTokenUnitsFromJSONBytes(data); ok && candidate > units {
					units = candidate
				}
			})
		} else if candidate, ok := unifiedGatewayTokenUnitsFromJSONBytes(body); ok {
			units = candidate
		}
		if units > 0 {
			return units, nil
		}
		if isSSE && !unifiedGatewaySSEHasPayload(body) {
			return 0, fmt.Errorf("%w: stream contained no semantic response payload", ErrUnifiedGatewayUpstreamFailed)
		}
		if unifiedGatewayPerRequestBilling(selection) {
			return 1, nil
		}
		return 0, fmt.Errorf("%w: %s response did not contain positive token usage", ErrUnifiedGatewayUsageMissing, endpoint)
	case unifiedGatewayUpstreamEndpointImages, unifiedGatewayUpstreamEndpointImagesEdits:
		count := 0
		if isSSE {
			counter := newOpenAIImageOutputCounter()
			counter.AddSSEBody(string(body))
			count = counter.Count()
		} else {
			count = extractOpenAIImagesBillableCountFromJSONBytes(body)
		}
		if count <= 0 {
			return 0, fmt.Errorf("%w: image response did not contain a completed image", ErrUnifiedGatewayUsageMissing)
		}
		return float64(count), nil
	default:
		return 0, fmt.Errorf("%w: endpoint %s has no usage extractor", ErrUnifiedGatewayUpstreamUnsupported, endpoint)
	}
}

func unifiedGatewayTokenUnitsFromJSONBytes(body []byte) (float64, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return 0, false
	}
	usage, ok := extractOpenAIUsageFromJSONBytes(body)
	if ok {
		units := usage.InputTokens + usage.ImageInputTokens + usage.OutputTokens + usage.ImageOutputTokens
		if units > 0 {
			return float64(units), true
		}
	}
	// Some compatible providers only return total_tokens. The existing usage
	// helper intentionally preserves the richer input/output fields, so keep a
	// narrow total_tokens fallback here for unified scalar billing.
	for _, path := range []string{"usage", "response.usage", "data.usage", "data.response.usage"} {
		candidate := gjson.GetBytes(body, path)
		if !candidate.Exists() || !candidate.IsObject() {
			continue
		}
		if total := candidate.Get("total_tokens").Int(); total > 0 {
			return float64(total), true
		}
	}
	return 0, false
}

func unifiedGatewayPerRequestBilling(selection UnifiedGatewayRouteSelection) bool {
	return selection.Target.RateBasis == UnifiedRateBasisPerRequest ||
		strings.EqualFold(strings.TrimSpace(selection.Target.BillingMode), string(BillingModePerRequest))
}

func isUnifiedGatewaySSEBody(header http.Header, body []byte) bool {
	return isEventStreamResponse(header) || bodyHasSSEFraming(body)
}

func unifiedGatewaySSEHasPayload(body []byte) bool {
	found := false
	forEachOpenAISSEDataPayload(string(body), func(data []byte) {
		if strings.TrimSpace(string(data)) != "" && strings.TrimSpace(string(data)) != "[DONE]" {
			found = true
		}
	})
	return found
}

func unifiedGatewayResponseIsNonTerminalOrError(header http.Header, body []byte) bool {
	check := func(data []byte) (hasError, nonTerminal, terminal bool) {
		if len(data) == 0 || !gjson.ValidBytes(data) {
			return false, false, false
		}
		for _, path := range []string{"error", "response.error", "data.error"} {
			if value := gjson.GetBytes(data, path); value.Exists() && value.Raw != "null" {
				return true, false, false
			}
		}
		for _, path := range []string{"status", "response.status", "data.status", "type"} {
			value := strings.ToLower(strings.TrimSpace(gjson.GetBytes(data, path).String()))
			switch value {
			case "queued", "pending", "processing", "in_progress", "in-progress", "submitted", "not_started", "not-started",
				"response.queued", "response.in_progress", "response.in-progress", "response.processing", "response.pending":
				nonTerminal = true
			case "failed", "error", "cancelled", "canceled", "response.failed", "response.error":
				return true, false, false
			case "completed", "complete", "succeeded", "success", "done", "response.completed", "response.done":
				terminal = true
			}
		}
		eventType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(data, "type").String()))
		if strings.HasSuffix(eventType, ".failed") || strings.HasSuffix(eventType, ".error") ||
			strings.HasSuffix(eventType, ".cancelled") || strings.HasSuffix(eventType, ".canceled") {
			return true, false, false
		}
		if strings.HasSuffix(eventType, ".completed") || strings.HasSuffix(eventType, ".done") {
			terminal = true
		}
		if units, ok := unifiedGatewayTokenUnitsFromJSONBytes(data); ok && units > 0 {
			terminal = true
		}
		return false, nonTerminal, terminal
	}
	if isUnifiedGatewaySSEBody(header, body) {
		hasError := false
		nonTerminal := false
		terminal := false
		forEachOpenAISSEDataPayload(string(body), func(data []byte) {
			payloadError, payloadPending, payloadTerminal := check(data)
			hasError = hasError || payloadError
			nonTerminal = nonTerminal || payloadPending
			terminal = terminal || payloadTerminal
		})
		return hasError || (nonTerminal && !terminal)
	}
	hasError, nonTerminal, _ := check(body)
	return hasError || nonTerminal
}

func validateUnifiedGatewayAsyncResult(result UnifiedGatewayUpstreamResult) (UnifiedGatewayUpstreamResult, error) {
	if result.Pending {
		if result.Delivered {
			return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: result cannot be both pending and delivered", ErrUnifiedGatewayUpstreamFailed)
		}
		if strings.TrimSpace(result.UpstreamRequestID) == "" {
			return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: pending result requires an upstream request ID", ErrUnifiedGatewayUpstreamFailed)
		}
		if result.MeasuredUnits < 0 || math.IsNaN(result.MeasuredUnits) || math.IsInf(result.MeasuredUnits, 0) {
			return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: pending result has invalid measured units", ErrUnifiedGatewayUpstreamFailed)
		}
		result.UpstreamRequestID = strings.TrimSpace(result.UpstreamRequestID)
		result.ResponseBody = cloneUnifiedGatewayBytes(result.ResponseBody)
		return result, nil
	}
	if !result.Delivered {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: media adapter returned neither delivered nor pending", ErrUnifiedGatewayUpstreamFailed)
	}
	if result.MeasuredUnits <= 0 || math.IsNaN(result.MeasuredUnits) || math.IsInf(result.MeasuredUnits, 0) {
		return UnifiedGatewayUpstreamResult{}, fmt.Errorf("%w: delivered media result has no positive measured units", ErrUnifiedGatewayUsageMissing)
	}
	result.UpstreamRequestID = strings.TrimSpace(result.UpstreamRequestID)
	result.ResponseBody = cloneUnifiedGatewayBytes(result.ResponseBody)
	return result, nil
}

func validateUnifiedGatewayContentResult(result UnifiedGatewayContentResult) (UnifiedGatewayContentResult, error) {
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return UnifiedGatewayContentResult{}, &UnifiedGatewayUpstreamHTTPError{StatusCode: result.StatusCode, RequestID: unifiedGatewayHeaderValue(result.Headers, "x-request-id")}
	}
	if len(result.Body) == 0 {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: media content body is empty", ErrUnifiedGatewayUpstreamFailed)
	}
	if strings.TrimSpace(result.ContentType) == "" {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: media content type is missing", ErrUnifiedGatewayUpstreamFailed)
	}
	result.ContentType = strings.TrimSpace(result.ContentType)
	if strings.ContainsAny(result.ContentType, "\r\n") {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: media content type contains forbidden characters", ErrUnifiedGatewayUpstreamFailed)
	}
	if _, _, err := mime.ParseMediaType(result.ContentType); err != nil {
		return UnifiedGatewayContentResult{}, fmt.Errorf("%w: media content type is invalid: %v", ErrUnifiedGatewayUpstreamFailed, err)
	}
	result.Body = cloneUnifiedGatewayBytes(result.Body)
	result.Headers = cloneHeader(result.Headers)
	return result, nil
}

func cloneUnifiedGatewayBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	return append([]byte(nil), value...)
}
