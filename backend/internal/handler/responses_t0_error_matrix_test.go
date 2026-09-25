package handler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestT0ErrorsHTTP_UpstreamStatusMatrix(t *testing.T) {
	for _, provider := range []string{"chat", "anthropic", "native"} {
		for _, code := range []int{401, 403, 404, 429, 502, 503} {
			t.Run(fmt.Sprintf("%s/%d", provider, code), func(t *testing.T) {
				s, model, _, _ := newT0CompactHTTPServer(t, provider, fmt.Sprintf("status_%d", code))
				resp, data := t0ReadHTTP(t, s, "/v1/responses/compact", fmt.Sprintf(`{"model":%q,"input":"synthetic","stream":false}`, model))
				require.GreaterOrEqual(t, resp.StatusCode, 400, string(data))
				require.NotEmpty(t, gjson.GetBytes(data, "error.type").String(), string(data))
				require.NotEmpty(t, gjson.GetBytes(data, "error.code").String(), string(data))
				require.False(t, strings.Contains(string(data), "response.completed"))
			})
		}
	}
}
