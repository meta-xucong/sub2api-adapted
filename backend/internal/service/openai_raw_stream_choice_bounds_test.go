//go:build unit

package service

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRawChatCompletionsRequestedChoices(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int64
	}{
		{`{}`, 1},
		{`{"n":1}`, 1},
		{`{"n":2}`, 2},
		{`{"n":1099511627776}`, 1099511627776},
		{`{"n":0}`, 1},
		{`{"n":-1}`, 1},
		{`{"n":"2"}`, 1},
		{`{"n":null}`, 1},
	} {
		require.Equal(t, tc.want, rawChatCompletionsRequestedChoices([]byte(tc.body)), tc.body)
	}
	state := openAIRawStreamTerminalState{expectedChoices: rawChatCompletionsRequestedChoices([]byte(`{"n":1099511627776}`))}
	require.Nil(t, state.choices, "a large explicit n must not preallocate memory")
}

func TestOpenAIRawStreamChoiceIndexesBoundState(t *testing.T) {
	for _, index := range []string{"-1", "1", "1.5", `"0"`, "null", "9223372036854775808"} {
		t.Run(index, func(t *testing.T) {
			var state openAIRawStreamTerminalState
			err := state.ObserveDataLine(`{"choices":[{"index":` + index + `,"delta":{}}]}`)
			require.Error(t, err)
			require.Nil(t, state.choices, "reject indexes before allocating map state")
		})
	}
	var state openAIRawStreamTerminalState
	require.NoError(t, state.ObserveDataLine(`{"choices":[{"delta":{}}]}`), "omitted indexes retain choice-zero compatibility")
	for index := 1; index <= 4096; index++ {
		require.Error(t, state.ObserveDataLine(fmt.Sprintf(`{"choices":[{"index":%d,"delta":{}}]}`, index)))
	}
	require.Len(t, state.choices, 1)
	require.Equal(t, 1, state.unfinishedChoices)
}

func TestOpenAIRawStreamChoiceCompletionIncremental(t *testing.T) {
	const count = 4096
	state := openAIRawStreamTerminalState{expectedChoices: count}
	for index := 0; index < count; index++ {
		require.NoError(t, state.ObserveDataLine(fmt.Sprintf(`{"choices":[{"index":%d,"delta":{}}]}`, index)))
		require.Equal(t, index+1, state.unfinishedChoices)
	}
	for index := 0; index < count; index++ {
		finished := fmt.Sprintf(`{"choices":[{"index":%d,"finish_reason":"stop"}]}`, index)
		require.NoError(t, state.ObserveDataLine(finished))
		require.NoError(t, state.ObserveDataLine(finished), "duplicate finish must not decrement twice")
		require.NoError(t, state.ObserveDataLine(fmt.Sprintf(`{"choices":[{"index":%d,"delta":{}}]}`, index)), "later deltas do not reopen a finished choice")
		require.Equal(t, count-index-1, state.unfinishedChoices)
	}
	require.Len(t, state.choices, count)
	require.True(t, state.Terminated())

	// Preserve the existing policy: completion concerns observed choices, not
	// every requested slot. Do not turn this allocation repair into new policy.
	partialSet := openAIRawStreamTerminalState{expectedChoices: 2}
	require.NoError(t, partialSet.ObserveDataLine(`{"choices":[{"index":0,"finish_reason":"stop"}]}`))
	require.True(t, partialSet.Terminated())
}

func TestGatewayStreamIntegrityRawChoiceIndexFailureDoesNotReplay(t *testing.T) {
	for _, metered := range []bool{false, true} {
		c, rec := integrityContext()
		usage := ""
		if metered {
			usage = `,"usage":{"prompt_tokens":7,"completion_tokens":3}`
		}
		payload := `data: {"choices":[{"index":1,"delta":{"content":"invalid choice"}}]` + usage + "}\n\ndata: [DONE]\n\n"
		result, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, integrityResponse(io.NopCloser(strings.NewReader(payload))), rawChatCompletionsTestAccount(), "m", "m", "m", nil, nil, time.Now(), openAISilentRefusalMinRequestBodyBytes)
		require.Error(t, err)
		var failover *UpstreamFailoverError
		require.False(t, errors.As(err, &failover))
		require.Empty(t, rec.Body.String(), "an invalid choice must not release withheld bytes")
		if metered {
			require.NotNil(t, result)
			require.Equal(t, 7, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
		} else {
			require.Nil(t, result, "unmetered withheld failures are not billable")
		}
	}
}

func TestForwardAsRawChatCompletionsRequestedChoicesReachTerminalGuard(t *testing.T) {
	const payload = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one\"},\"finish_reason\":\"stop\"},{\"index\":1,\"delta\":{\"content\":\"two\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	for _, requestedTwo := range []bool{false, true} {
		c, rec := integrityContext()
		request := `{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true`
		if requestedTwo {
			request += `,"n":2`
		}
		request += "}"
		upstream := &httpUpstreamRecorder{resp: integrityResponse(io.NopCloser(strings.NewReader(payload)))}
		svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
		_, err := svc.forwardAsRawChatCompletions(c.Request.Context(), c, rawChatCompletionsTestAccount(), []byte(request), "")
		if requestedTwo {
			require.NoError(t, err)
			require.Equal(t, int64(2), rawChatCompletionsRequestedChoices(upstream.lastBody))
			require.Contains(t, rec.Body.String(), "data: [DONE]")
		} else {
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			require.NotContains(t, rec.Body.String(), "data: [DONE]")
		}
	}
}
