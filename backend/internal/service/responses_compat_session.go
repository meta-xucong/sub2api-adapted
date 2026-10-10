package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const responsesCompatSessionCachePrefix = "responses:compat:"

// responsesCompatError carries a safe client-facing classification while
// retaining cache/parser details only for server-side diagnostics.
type responsesCompatError struct {
	status  int
	code    string
	message string
	param   string
	cause   error
}

func (e *responsesCompatError) Error() string { return e.message }
func (e *responsesCompatError) Unwrap() error { return e.cause }

func writeResponsesCompatError(c *gin.Context, err error) {
	wire := &responsesCompatError{
		status: http.StatusBadRequest, code: "invalid_request_error",
		message: "Invalid compatibility continuation input", param: "input",
	}
	var classified *responsesCompatError
	if errors.As(err, &classified) {
		wire = classified
	}
	errorType := "invalid_request_error"
	if wire.status >= http.StatusInternalServerError {
		errorType = "server_error"
	}
	MarkResponseCommitted(c)
	c.JSON(wire.status, gin.H{"error": gin.H{
		"type": errorType, "code": wire.code, "message": wire.message, "param": wire.param,
	}})
}

// ResponsesCompatStateCache is an optional shared-cache extension. Keeping it
// separate preserves compatibility with GatewayCache test doubles and callers.
type ResponsesCompatStateCache interface {
	GetResponsesCompatState(ctx context.Context, key string) ([]byte, error)
	SetResponsesCompatState(ctx context.Context, key string, payload []byte, ttl time.Duration) error
	DeleteResponsesCompatState(ctx context.Context, key string) error
}

// responsesCompatSessionState stores only the Responses request/output history
// needed to continue a third-party Chat Completions or Anthropic conversation.
// Native Responses and compact state are deliberately not handled here.
type responsesCompatSessionState struct {
	ResponseID    string                    `json:"response_id"`
	Model         string                    `json:"model,omitempty"`
	Instructions  string                    `json:"instructions,omitempty"`
	Tools         []apicompat.ResponsesTool `json:"tools,omitempty"`
	RequestInput  []json.RawMessage         `json:"request_input,omitempty"`
	HistoryInput  []json.RawMessage         `json:"history_input,omitempty"`
	OutputInput   []json.RawMessage         `json:"output_input,omitempty"`
	CreatedAtUnix int64                     `json:"created_at_unix,omitempty"`
	ExpiresAtUnix int64                     `json:"expires_at_unix,omitempty"`
}

type responsesCompatLocalBinding struct {
	State     responsesCompatSessionState
	ExpiresAt time.Time
}

func normalizeResponsesCompatSessionTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return time.Hour
	}
	return ttl
}

func responsesCompatSessionScope(c *gin.Context) string {
	return fmt.Sprintf("group:%d:key:%d", getOpenAIGroupIDFromContext(c), getAPIKeyIDFromContext(c))
}

func responsesCompatSessionLocalKey(c *gin.Context, responseID string) string {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return ""
	}
	return responsesCompatSessionScope(c) + "\x00" + responseID
}

func responsesCompatSessionCacheKey(c *gin.Context, responseID string) string {
	localKey := responsesCompatSessionLocalKey(c, responseID)
	if localKey == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(localKey))
	return responsesCompatSessionCachePrefix + hex.EncodeToString(digest[:])
}

func responsesCompatSessionExcluded(c *gin.Context) bool {
	return isOpenAIResponsesCompactPath(c) || isOpenAINativeCompactionV2(c)
}

func storeResponsesCompatLocalBinding(local *sync.Map, key string, binding responsesCompatLocalBinding) {
	if local == nil || key == "" {
		return
	}
	stored := &binding
	local.Store(key, stored)
	delay := time.Until(stored.ExpiresAt)
	if delay <= 0 {
		local.CompareAndDelete(key, stored)
		return
	}
	time.AfterFunc(delay, func() {
		raw, ok := local.Load(key)
		if !ok {
			return
		}
		current, ok := raw.(*responsesCompatLocalBinding)
		if ok && current != nil && !time.Now().Before(current.ExpiresAt) {
			local.CompareAndDelete(key, current)
		}
	})
}

func deleteResponsesCompatLocalBindingIfUnchanged(local *sync.Map, key string, observed any) bool {
	if local == nil || key == "" || observed == nil {
		return false
	}
	return local.CompareAndDelete(key, observed)
}

func saveResponsesCompatSession(
	ctx context.Context,
	c *gin.Context,
	cache GatewayCache,
	local *sync.Map,
	state responsesCompatSessionState,
	ttl time.Duration,
) error {
	if local == nil || strings.TrimSpace(state.ResponseID) == "" {
		return nil
	}
	ttl = normalizeResponsesCompatSessionTTL(ttl)
	now := time.Now()
	state.ResponseID = strings.TrimSpace(state.ResponseID)
	if state.CreatedAtUnix == 0 {
		state.CreatedAtUnix = now.Unix()
	}
	if state.ExpiresAtUnix == 0 {
		state.ExpiresAtUnix = now.Add(ttl).Unix()
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal Responses compatibility session: %w", err)
	}
	var snapshot responsesCompatSessionState
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return fmt.Errorf("clone Responses compatibility session: %w", err)
	}
	localKey := responsesCompatSessionLocalKey(c, state.ResponseID)
	if localKey == "" {
		return nil
	}
	storeResponsesCompatLocalBinding(local, localKey, responsesCompatLocalBinding{State: snapshot, ExpiresAt: time.Now().Add(ttl)})
	shared, ok := cache.(ResponsesCompatStateCache)
	if !ok || shared == nil {
		return nil
	}
	return shared.SetResponsesCompatState(ctx, responsesCompatSessionCacheKey(c, state.ResponseID), payload, ttl)
}

func loadResponsesCompatSession(
	ctx context.Context,
	c *gin.Context,
	cache GatewayCache,
	local *sync.Map,
	responseID string,
) (*responsesCompatSessionState, error) {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return nil, nil
	}
	key := responsesCompatSessionLocalKey(c, responseID)
	if local != nil && key != "" {
		if raw, ok := local.Load(key); ok {
			binding, valid := raw.(*responsesCompatLocalBinding)
			if !valid || binding == nil || (!binding.ExpiresAt.IsZero() && time.Now().After(binding.ExpiresAt)) {
				deleteResponsesCompatLocalBindingIfUnchanged(local, key, raw)
			} else {
				state := binding.State
				if state.ResponseID != responseID {
					return nil, &responsesCompatError{
						status: http.StatusInternalServerError, code: "compat_session_corrupt",
						message: "Compatibility session identity is invalid",
						param:   "previous_response_id",
					}
				}
				return &state, nil
			}
		}
	}
	shared, ok := cache.(ResponsesCompatStateCache)
	if !ok || shared == nil {
		return nil, nil
	}
	payload, err := shared.GetResponsesCompatState(ctx, responsesCompatSessionCacheKey(c, responseID))
	if err != nil {
		return nil, &responsesCompatError{
			status: http.StatusServiceUnavailable, code: "compat_session_unavailable",
			message: "Compatibility session storage is temporarily unavailable",
			param:   "previous_response_id", cause: err,
		}
	}
	if len(payload) == 0 {
		return nil, nil
	}
	var state responsesCompatSessionState
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, &responsesCompatError{
			status: http.StatusInternalServerError, code: "compat_session_corrupt",
			message: "Compatibility session data is invalid",
			param:   "previous_response_id", cause: err,
		}
	}
	if state.ResponseID != responseID {
		return nil, &responsesCompatError{
			status: http.StatusInternalServerError, code: "compat_session_corrupt",
			message: "Compatibility session identity is invalid",
			param:   "previous_response_id",
		}
	}
	expiresAt := time.Unix(state.ExpiresAtUnix, 0)
	if state.ExpiresAtUnix <= 0 {
		expiresAt = time.Now().Add(time.Hour)
	}
	if time.Now().After(expiresAt) {
		return nil, nil
	}
	if local != nil && key != "" {
		storeResponsesCompatLocalBinding(local, key, responsesCompatLocalBinding{State: state, ExpiresAt: expiresAt})
	}
	return &state, nil
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
		return nil, fmt.Errorf("parse Responses input for compatibility replay: %w", err)
	}
	return items, nil
}

func responsesCompatOutputItems(outputs []apicompat.ResponsesOutput) ([]json.RawMessage, error) {
	items := make([]json.RawMessage, 0, len(outputs))
	for _, output := range outputs {
		var item map[string]any
		switch output.Type {
		case "message":
			item = map[string]any{"type": "message", "role": "assistant", "content": output.Content}
			if output.ID != "" {
				item["id"] = output.ID
			}
		case "reasoning":
			if len(output.Summary) == 0 {
				continue
			}
			item = map[string]any{"type": "reasoning", "summary": output.Summary}
		case "compaction", "compaction_summary":
			if len(output.Summary) == 0 {
				continue
			}
			item = map[string]any{
				"type": output.Type, "status": output.Status,
				"summary": output.Summary, "encrypted_content": output.EncryptedContent,
			}
			if output.ID != "" {
				item["id"] = output.ID
			}
		case "function_call":
			item = map[string]any{"type": "function_call", "call_id": output.CallID, "name": output.Name, "arguments": output.Arguments}
			if output.ID != "" {
				item["id"] = output.ID
			}
			if output.Namespace != "" {
				item["namespace"] = output.Namespace
			}
		case "custom_tool_call":
			item = map[string]any{"type": "custom_tool_call", "call_id": output.CallID, "name": output.Name, "input": output.Input}
		case "tool_search_call":
			item = map[string]any{"type": "tool_search_call", "call_id": output.CallID, "arguments": output.Arguments}
		default:
			continue
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		items = append(items, encoded)
	}
	return items, nil
}

func responsesCompatRawEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return bytes.Equal(bytes.TrimSpace(left), bytes.TrimSpace(right))
	}
	leftCanonical, leftErr := json.Marshal(leftValue)
	rightCanonical, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftCanonical, rightCanonical)
}

func responsesCompatPrefix(items, prefix []json.RawMessage) bool {
	if len(prefix) > len(items) {
		return false
	}
	for i := range prefix {
		if !responsesCompatRawEqual(items[i], prefix[i]) {
			return false
		}
	}
	return true
}

func responsesCompatItemCallID(item json.RawMessage) string {
	return strings.TrimSpace(gjson.GetBytes(item, "call_id").String())
}

// validateResponsesCompatToolOutputs mirrors the frozen T0 compatibility
// adapter: identical tool results may be deduplicated, but unknown or
// conflicting results must not be silently treated as equivalent.
func validateResponsesCompatToolOutputs(state *responsesCompatSessionState, current []json.RawMessage) error {
	if state == nil {
		return nil
	}
	knownCalls := make(map[string]bool)
	for _, items := range [][]json.RawMessage{state.HistoryInput, current} {
		for _, item := range items {
			if gjson.GetBytes(item, "type").String() == "function_call" {
				if id := responsesCompatItemCallID(item); id != "" {
					knownCalls[id] = true
				}
			}
		}
	}
	invalid := func(message string) error {
		return &responsesCompatError{status: http.StatusBadRequest, code: "invalid_tool_output", message: message, param: "input"}
	}
	seen := make(map[string]json.RawMessage)
	for index, items := range [][]json.RawMessage{state.HistoryInput, current} {
		for _, item := range items {
			if gjson.GetBytes(item, "type").String() != "function_call_output" {
				continue
			}
			callID := responsesCompatItemCallID(item)
			if index == 1 && !knownCalls[callID] {
				return invalid("Tool output does not match a function call in the compatibility history")
			}
			isError := gjson.GetBytes(item, "is_error")
			if isError.Exists() && isError.Type != gjson.True && isError.Type != gjson.False {
				return invalid("Tool output is_error must be a boolean when present")
			}
			if previous, ok := seen[callID]; ok {
				sameOutput := responsesCompatRawEqual(
					json.RawMessage(gjson.GetBytes(previous, "output").Raw),
					json.RawMessage(gjson.GetBytes(item, "output").Raw),
				)
				if !sameOutput || gjson.GetBytes(previous, "is_error").Bool() != isError.Bool() {
					return invalid("Conflicting tool outputs were supplied for the same call_id")
				}
			} else {
				seen[callID] = item
			}
		}
	}
	return nil
}

func responsesCompatResolveToolOutputCallIDs(state *responsesCompatSessionState, current []json.RawMessage) ([]json.RawMessage, error) {
	if state == nil || len(current) == 0 {
		return current, nil
	}
	callIDsByReference := make(map[string]string)
	collect := func(item json.RawMessage) {
		if strings.TrimSpace(gjson.GetBytes(item, "type").String()) != "function_call" {
			return
		}
		callID := responsesCompatItemCallID(item)
		if callID == "" {
			return
		}
		for _, reference := range []string{gjson.GetBytes(item, "id").String(), callID} {
			if reference = strings.TrimSpace(reference); reference != "" {
				callIDsByReference[reference] = callID
			}
		}
	}
	for _, item := range state.HistoryInput {
		collect(item)
	}
	for _, item := range current {
		collect(item)
	}
	resolved := append([]json.RawMessage(nil), current...)
	for index, item := range resolved {
		if strings.TrimSpace(gjson.GetBytes(item, "type").String()) != "function_call_output" || responsesCompatItemCallID(item) != "" {
			continue
		}
		reference := strings.TrimSpace(gjson.GetBytes(item, "item_reference").String())
		if reference == "" {
			reference = strings.TrimSpace(gjson.GetBytes(item, "item_id").String())
		}
		if reference == "" {
			reference = strings.TrimSpace(gjson.GetBytes(item, "tool_use_id").String())
		}
		if reference == "" {
			return nil, fmt.Errorf("function_call_output is missing call_id and item reference")
		}
		callID := callIDsByReference[reference]
		if callID == "" {
			return nil, fmt.Errorf("function_call_output item_reference %q does not match a previous function_call", reference)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(item, &object); err != nil {
			return nil, fmt.Errorf("parse function_call_output item: %w", err)
		}
		encodedCallID, err := json.Marshal(callID)
		if err != nil {
			return nil, err
		}
		object["call_id"] = encodedCallID
		resolved[index], err = json.Marshal(object)
		if err != nil {
			return nil, fmt.Errorf("marshal resolved function_call_output item: %w", err)
		}
	}
	return resolved, nil
}

func mergeResponsesCompatInput(state *responsesCompatSessionState, current []json.RawMessage) []json.RawMessage {
	if state == nil || len(state.HistoryInput) == 0 {
		return append([]json.RawMessage(nil), current...)
	}
	base := append([]json.RawMessage(nil), state.HistoryInput...)
	remainder := current
	if responsesCompatPrefix(current, state.RequestInput) {
		remainder = current[len(state.RequestInput):]
		if responsesCompatPrefix(remainder, state.OutputInput) {
			remainder = remainder[len(state.OutputInput):]
		}
	}
	knownCallIDs := make(map[string]struct{})
	knownItemIDs := make(map[string]struct{})
	for _, item := range base {
		if itemID := strings.TrimSpace(gjson.GetBytes(item, "id").String()); itemID != "" {
			knownItemIDs[itemID] = struct{}{}
		}
		if strings.TrimSpace(gjson.GetBytes(item, "type").String()) == "function_call_output" {
			if callID := responsesCompatItemCallID(item); callID != "" {
				knownCallIDs[callID] = struct{}{}
			}
		}
	}
	for _, item := range remainder {
		if itemID := strings.TrimSpace(gjson.GetBytes(item, "id").String()); itemID != "" {
			if _, exists := knownItemIDs[itemID]; exists {
				continue
			}
			knownItemIDs[itemID] = struct{}{}
		}
		if strings.TrimSpace(gjson.GetBytes(item, "type").String()) == "function_call_output" {
			if callID := responsesCompatItemCallID(item); callID != "" {
				if _, exists := knownCallIDs[callID]; exists {
					continue
				}
				knownCallIDs[callID] = struct{}{}
			}
		}
		base = append(base, item)
	}
	return base
}

func prepareResponsesCompatContinuation(ctx context.Context, c *gin.Context, cache GatewayCache, local *sync.Map, req *apicompat.ResponsesRequest) (bool, error) {
	if responsesCompatSessionExcluded(c) {
		return false, nil
	}
	if req == nil || strings.TrimSpace(req.PreviousResponseID) == "" {
		return false, nil
	}
	state, err := loadResponsesCompatSession(ctx, c, cache, local, req.PreviousResponseID)
	if err != nil {
		return false, err
	}
	if state == nil {
		return false, &responsesCompatError{
			status: http.StatusBadRequest, code: "previous_response_not_found",
			message: "Previous response is not available for this compatibility session",
			param:   "previous_response_id",
		}
	}
	current, err := responsesCompatRequestInputRaw(req.Input)
	if err != nil {
		return false, err
	}
	current, err = responsesCompatResolveToolOutputCallIDs(state, current)
	if err != nil {
		return false, err
	}
	if err := validateResponsesCompatToolOutputs(state, current); err != nil {
		return false, err
	}
	replayed, err := json.Marshal(mergeResponsesCompatInput(state, current))
	if err != nil {
		return false, fmt.Errorf("marshal replayed Responses input: %w", err)
	}
	req.Input = replayed
	if strings.TrimSpace(req.Instructions) == "" {
		req.Instructions = state.Instructions
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = state.Model
	}
	if len(req.Tools) == 0 && len(state.Tools) > 0 {
		req.Tools = append([]apicompat.ResponsesTool(nil), state.Tools...)
	}
	req.PreviousResponseID = ""
	return true, nil
}

func buildResponsesCompatSessionState(req *apicompat.ResponsesRequest, resp *apicompat.ResponsesResponse) (responsesCompatSessionState, error) {
	if req == nil || resp == nil || strings.TrimSpace(resp.ID) == "" {
		return responsesCompatSessionState{}, nil
	}
	requestInput, err := responsesCompatRequestInputRaw(req.Input)
	if err != nil {
		return responsesCompatSessionState{}, err
	}
	outputInput, err := responsesCompatOutputItems(resp.Output)
	if err != nil {
		return responsesCompatSessionState{}, err
	}
	history := append(append([]json.RawMessage(nil), requestInput...), outputInput...)
	return responsesCompatSessionState{
		ResponseID: resp.ID, Model: req.Model, Instructions: req.Instructions,
		Tools:        append([]apicompat.ResponsesTool(nil), req.Tools...),
		RequestInput: requestInput, HistoryInput: history, OutputInput: outputInput,
		CreatedAtUnix: time.Now().Unix(),
	}, nil
}

func (s *OpenAIGatewayService) prepareResponsesCompatContinuation(ctx context.Context, c *gin.Context, req *apicompat.ResponsesRequest) error {
	if responsesCompatSessionExcluded(c) {
		return nil
	}
	_, err := prepareResponsesCompatContinuation(ctx, c, s.cache, &s.responsesCompatSessions, req)
	return err
}

func (s *OpenAIGatewayService) saveResponsesCompatResponse(ctx context.Context, c *gin.Context, req *apicompat.ResponsesRequest, resp *apicompat.ResponsesResponse) error {
	if responsesCompatSessionExcluded(c) {
		return nil
	}
	state, err := buildResponsesCompatSessionState(req, resp)
	if err != nil || state.ResponseID == "" {
		return err
	}
	return saveResponsesCompatSession(ctx, c, s.cache, &s.responsesCompatSessions, state, s.openAIWSResponseStickyTTL())
}

func (s *OpenAIGatewayService) saveResponsesCompatResponseFromBody(ctx context.Context, c *gin.Context, body []byte, resp *apicompat.ResponsesResponse) error {
	var req apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return err
	}
	return s.saveResponsesCompatResponse(ctx, c, &req, resp)
}

func (s *GatewayService) prepareResponsesCompatContinuation(ctx context.Context, c *gin.Context, req *apicompat.ResponsesRequest) error {
	if responsesCompatSessionExcluded(c) {
		return nil
	}
	_, err := prepareResponsesCompatContinuation(ctx, c, s.cache, &s.responsesCompatSessions, req)
	return err
}

func (s *GatewayService) saveResponsesCompatResponse(ctx context.Context, c *gin.Context, req *apicompat.ResponsesRequest, resp *apicompat.ResponsesResponse) error {
	if responsesCompatSessionExcluded(c) {
		return nil
	}
	state, err := buildResponsesCompatSessionState(req, resp)
	if err != nil || state.ResponseID == "" {
		return err
	}
	return saveResponsesCompatSession(ctx, c, s.cache, &s.responsesCompatSessions, state, time.Hour)
}
