package handler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestT0NativeResponses_StatelessHistoryExpandedBeforeIDRemoval(t *testing.T) {
	for _, protocol := range []string{service.APIProtocolResponses, service.APIProtocolAdaptive} {
		t.Run(protocol, func(t *testing.T) {
			server := miniredis.RunT(t)
			options := t0HTTPOptions{Platform: service.PlatformDeepseek, Protocol: protocol, Model: "deepseek-v4-flash", Composite: true, Cache: t0RedisStore(t, server)}
			a, model, _, _ := newT0CompactHTTPServer(t, "native", "tool", options)
			options.Cache = t0RedisStore(t, server)
			b, _, observed, _ := newT0CompactHTTPServer(t, "native", "tool", options)
			resp, data := t0ReadHTTP(t, a, "/v1/responses", fmt.Sprintf(`{"model":%q,"input":"must preserve first user turn","instructions":"Skills stay visible","stream":false}`, model))
			require.Equal(t, 200, resp.StatusCode, string(data))
			id := gjson.GetBytes(data, "id").String()
			require.NotEmpty(t, id)
			resp, data = t0ReadHTTP(t, b, "/v1/responses", fmt.Sprintf(`{"model":%q,"previous_response_id":%q,"input":[{"type":"function_call_output","call_id":"call_fixture","output":"synthetic result"}],"stream":false}`, model, id))
			require.Equal(t, 200, resp.StatusCode, string(data))
			got := <-observed
			require.Contains(t, string(got.body), "must preserve first user turn")
			require.Contains(t, string(got.body), "Skills stay visible")
			require.Contains(t, string(got.body), "function_call_output")
			require.False(t, gjson.GetBytes(got.body, "previous_response_id").Exists())
			require.False(t, gjson.GetBytes(got.body, "store").Bool())
			require.True(t, strings.HasSuffix(got.path, "/responses"))
		})
	}
}
