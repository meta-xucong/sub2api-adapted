package service

import (
	"encoding/json"
	"net/http"

	"github.com/tidwall/gjson"
)

// Deduplication is safe only for equivalent results. Silently keeping the first
// conflicting result can hide a tool failure or replay the wrong observation.
// This validates replay input; it does not claim exactly-once tool execution.
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
