package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGrokVoiceErrorHTTPStatus(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{service.ErrGrokVoiceNotFound, http.StatusNotFound},
		{fmt.Errorf("wrapped: %w", service.ErrGrokVoiceInvalidRequest), http.StatusBadRequest},
		{service.ErrGrokVoiceStoreUnavailable, http.StatusServiceUnavailable},
		{fmt.Errorf("provider secret detail"), http.StatusServiceUnavailable},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			h := &OpenAIGatewayHandler{}
			h.grokVoiceError(c, tc.err)
			require.Equal(t, tc.status, w.Code)
			require.NotContains(t, w.Body.String(), "secret detail")
		})
	}
}
