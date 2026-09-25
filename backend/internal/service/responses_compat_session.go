package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// ResponsesCompatStateCache is the optional shared-cache extension used by
// the Responses compatibility layer.  GatewayCache deliberately remains
// backwards-compatible with the many lightweight test doubles and with
// deployments that only need sticky-account state.
type ResponsesCompatStateCache interface {
	GetResponsesCompatState(ctx context.Context, key string) ([]byte, error)
	SetResponsesCompatState(ctx context.Context, key string, payload []byte, ttl time.Duration) error
	DeleteResponsesCompatState(ctx context.Context, key string) error
}

// responsesCompatSessionState is the canonical state needed to replay a
// third-party Responses turn through Chat Completions or Anthropic.  Input is
// kept as raw Responses items so unknown fields (item_id, item_reference,
// encrypted reasoning metadata) are not silently rewritten before the next
// adapter gets a chance to handle them.
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

// cloneResponsesCompatRequest snapshots raw request fields before a protocol
// adapter unmarshals another representation into the same request struct.
// json.RawMessage.UnmarshalJSON may reuse its existing backing array; a
// shallow request copy would therefore let Responses→Anthropic adaptation
// corrupt the canonical history used by previous_response_id replay.
func cloneResponsesCompatRequest(req apicompat.ResponsesRequest) apicompat.ResponsesRequest {
	clone := req
	clone.Input = append(json.RawMessage(nil), req.Input...)
	clone.ToolChoice = append(json.RawMessage(nil), req.ToolChoice...)
	clone.Include = append([]string(nil), req.Include...)
	clone.Tools = append([]apicompat.ResponsesTool(nil), req.Tools...)
	return clone
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

func normalizeResponsesCompatSessionTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return time.Hour
	}
	return ttl
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
	state.ResponseID = strings.TrimSpace(state.ResponseID)
	if state.CreatedAtUnix == 0 {
		state.CreatedAtUnix = time.Now().Unix()
	}
	if state.ExpiresAtUnix == 0 {
		state.ExpiresAtUnix = time.Now().Add(ttl).Unix()
	}

	// Copy through JSON before placing the value in the local map.  This keeps
	// the cache snapshot immutable even when the caller reuses request slices.
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal Responses compatibility session: %w", err)
	}
	var snapshot responsesCompatSessionState
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return fmt.Errorf("clone Responses compatibility session: %w", err)
	}
	key := responsesCompatSessionLocalKey(c, state.ResponseID)
	if key == "" {
		return nil
	}
	local.Store(key, responsesCompatLocalBinding{
		State:     snapshot,
		ExpiresAt: time.Now().Add(ttl),
	})

	shared, ok := cache.(ResponsesCompatStateCache)
	if !ok || shared == nil {
		return nil
	}
	cacheKey := responsesCompatSessionCacheKey(c, state.ResponseID)
	if cacheKey == "" {
		return nil
	}
	return shared.SetResponsesCompatState(ctx, cacheKey, encoded, ttl)
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
			binding, valid := raw.(responsesCompatLocalBinding)
			if !valid {
				local.Delete(key)
			} else if !binding.ExpiresAt.IsZero() && time.Now().After(binding.ExpiresAt) {
				local.Delete(key)
			} else {
				state := binding.State
				if state.ResponseID != responseID {
					return nil, &responsesCompatError{status: http.StatusInternalServerError, code: "compat_session_corrupt", message: "Compatibility session identity is invalid", param: "previous_response_id"}
				}
				return &state, nil
			}
		}
	}

	shared, ok := cache.(ResponsesCompatStateCache)
	if !ok || shared == nil {
		return nil, nil
	}
	cacheKey := responsesCompatSessionCacheKey(c, responseID)
	payload, err := shared.GetResponsesCompatState(ctx, cacheKey)
	if err != nil {
		return nil, &responsesCompatError{status: http.StatusServiceUnavailable, code: "compat_session_unavailable", message: "Compatibility session storage is temporarily unavailable", param: "previous_response_id", cause: err}
	}
	if len(payload) == 0 {
		return nil, nil
	}
	var state responsesCompatSessionState
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, &responsesCompatError{status: http.StatusInternalServerError, code: "compat_session_corrupt", message: "Compatibility session data is invalid", param: "previous_response_id", cause: err}
	}
	if state.ResponseID != responseID {
		return nil, &responsesCompatError{status: http.StatusInternalServerError, code: "compat_session_corrupt", message: "Compatibility session identity is invalid", param: "previous_response_id"}
	}
	expiresAt := time.Now().Add(time.Hour)
	if state.ExpiresAtUnix > 0 {
		expiresAt = time.Unix(state.ExpiresAtUnix, 0)
	}
	if time.Now().After(expiresAt) {
		return nil, nil
	}
	if local != nil && key != "" {
		local.Store(key, responsesCompatLocalBinding{
			State:     state,
			ExpiresAt: expiresAt,
		})
	}
	return &state, nil
}

func deleteResponsesCompatSession(ctx context.Context, c *gin.Context, cache GatewayCache, local *sync.Map, responseID string) error {
	key := responsesCompatSessionLocalKey(c, responseID)
	if local != nil && key != "" {
		local.Delete(key)
	}
	shared, ok := cache.(ResponsesCompatStateCache)
	if !ok || shared == nil {
		return nil
	}
	return shared.DeleteResponsesCompatState(ctx, responsesCompatSessionCacheKey(c, responseID))
}

// responsesCompatRequestInputRaw normalizes the Responses input into raw
// message/item objects without losing the original item fields.
func responsesCompatRequestInputRaw(input json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		item, err := json.Marshal(map[string]any{
			"type":    "message",
			"role":    "user",
			"content": text,
		})
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
			item = map[string]any{
				"type":    "message",
				"role":    "assistant",
				"content": output.Content,
			}
			if output.ID != "" {
				item["id"] = output.ID
			}
		case "reasoning":
			// Summary text is replayable reasoning. Encrypted reasoning is not
			// portable across Chat/Anthropic adapters and is intentionally not
			// copied as encrypted_content.
			if len(output.Summary) == 0 {
				continue
			}
			item = map[string]any{
				"type":    "reasoning",
				"summary": output.Summary,
			}
		case "compaction", "compaction_summary":
			// Keep the visible summary and the opaque item payload at the
			// Responses boundary.  Chat/Anthropic adapters replay the summary;
			// encrypted_content is retained only so a client can round-trip the
			// item without losing its wire shape.
			if len(output.Summary) == 0 {
				continue
			}
			item = map[string]any{
				"type":              output.Type,
				"status":            output.Status,
				"summary":           output.Summary,
				"encrypted_content": output.EncryptedContent,
			}
			if output.ID != "" {
				item["id"] = output.ID
			}
		case "function_call":
			item = map[string]any{
				"type":      "function_call",
				"call_id":   output.CallID,
				"name":      output.Name,
				"arguments": output.Arguments,
			}
			if output.ID != "" {
				item["id"] = output.ID
			}
			if output.Namespace != "" {
				item["namespace"] = output.Namespace
			}
		case "custom_tool_call":
			item = map[string]any{
				"type":    "custom_tool_call",
				"call_id": output.CallID,
				"name":    output.Name,
				"input":   output.Input,
			}
		case "tool_search_call":
			item = map[string]any{
				"type":      "tool_search_call",
				"call_id":   output.CallID,
				"arguments": output.Arguments,
			}
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
	var leftValue any
	var rightValue any
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
		for _, reference := range []string{
			gjson.GetBytes(item, "id").String(),
			gjson.GetBytes(item, "call_id").String(),
		} {
			reference = strings.TrimSpace(reference)
			if reference != "" {
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
			return nil, fmt.Errorf("marshal function_call_output call_id: %w", err)
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
			callID := responsesCompatItemCallID(item)
			if callID == "" {
				continue
			}
			knownCallIDs[callID] = struct{}{}
		}
	}
	for _, item := range remainder {
		if itemID := strings.TrimSpace(gjson.GetBytes(item, "id").String()); itemID != "" {
			if _, exists := knownItemIDs[itemID]; exists {
				continue
			}
			knownItemIDs[itemID] = struct{}{}
		}
		callID := responsesCompatItemCallID(item)
		if callID != "" && strings.TrimSpace(gjson.GetBytes(item, "type").String()) == "function_call_output" {
			if _, exists := knownCallIDs[callID]; exists {
				continue
			}
			knownCallIDs[callID] = struct{}{}
		}
		base = append(base, item)
	}
	return base
}

func prepareResponsesCompatContinuation(
	ctx context.Context,
	c *gin.Context,
	cache GatewayCache,
	local *sync.Map,
	req *apicompat.ResponsesRequest,
) (bool, error) {
	if req == nil || strings.TrimSpace(req.PreviousResponseID) == "" {
		return false, nil
	}
	previousID := strings.TrimSpace(req.PreviousResponseID)
	state, err := loadResponsesCompatSession(ctx, c, cache, local, previousID)
	if err != nil {
		return false, err
	}
	if state == nil {
		return false, &responsesCompatError{status: http.StatusBadRequest, code: "previous_response_not_found", message: "Previous response is not available for this compatibility session", param: "previous_response_id"}
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
	req.Input, err = json.Marshal(mergeResponsesCompatInput(state, current))
	if err != nil {
		return false, fmt.Errorf("marshal replayed Responses input: %w", err)
	}
	if strings.TrimSpace(req.Instructions) == "" {
		req.Instructions = state.Instructions
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = state.Model
	}
	if len(req.Tools) == 0 && len(state.Tools) > 0 {
		req.Tools = append([]apicompat.ResponsesTool(nil), state.Tools...)
	}
	// Chat Completions and Anthropic do not understand Responses state IDs. The
	// state has now been made explicit in input, so the opaque ID must not leak
	// into either upstream protocol.
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
		ResponseID:    resp.ID,
		Model:         req.Model,
		Instructions:  req.Instructions,
		Tools:         append([]apicompat.ResponsesTool(nil), req.Tools...),
		RequestInput:  requestInput,
		HistoryInput:  history,
		OutputInput:   outputInput,
		CreatedAtUnix: time.Now().Unix(),
	}, nil
}

func (s *OpenAIGatewayService) prepareResponsesCompatContinuation(ctx context.Context, c *gin.Context, req *apicompat.ResponsesRequest) error {
	if s == nil {
		return nil
	}
	_, err := prepareResponsesCompatContinuation(ctx, c, s.cache, &s.responsesCompatSessions, req)
	return err
}

func (s *OpenAIGatewayService) saveResponsesCompatResponse(ctx context.Context, c *gin.Context, req *apicompat.ResponsesRequest, resp *apicompat.ResponsesResponse) error {
	if s == nil || resp == nil {
		return nil
	}
	state, err := buildResponsesCompatSessionState(req, resp)
	if err != nil || strings.TrimSpace(state.ResponseID) == "" {
		return err
	}
	return saveResponsesCompatSession(ctx, c, s.cache, &s.responsesCompatSessions, state, s.openAIWSResponseStickyTTL())
}

func (s *OpenAIGatewayService) saveResponsesCompatResponseFromBody(ctx context.Context, c *gin.Context, body []byte, resp *apicompat.ResponsesResponse) error {
	if s == nil || resp == nil {
		return nil
	}
	var req apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("parse Responses request for compatibility session: %w", err)
	}
	return s.saveResponsesCompatResponse(ctx, c, &req, resp)
}

func (s *GatewayService) prepareResponsesCompatContinuation(ctx context.Context, c *gin.Context, req *apicompat.ResponsesRequest) error {
	if s == nil {
		return nil
	}
	_, err := prepareResponsesCompatContinuation(ctx, c, s.cache, &s.responsesCompatSessions, req)
	return err
}

func (s *GatewayService) saveResponsesCompatResponse(ctx context.Context, c *gin.Context, req *apicompat.ResponsesRequest, resp *apicompat.ResponsesResponse) error {
	if s == nil || resp == nil {
		return nil
	}
	state, err := buildResponsesCompatSessionState(req, resp)
	if err != nil || strings.TrimSpace(state.ResponseID) == "" {
		return err
	}
	return saveResponsesCompatSession(ctx, c, s.cache, &s.responsesCompatSessions, state, time.Hour)
}
