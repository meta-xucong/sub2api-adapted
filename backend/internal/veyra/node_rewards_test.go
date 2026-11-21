package veyra

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type nodeRewardAccountStub struct {
	balanceAccountStub
	creditCalls int
}

func (*nodeRewardAccountStub) GetNodeRewardSettlement(_ context.Context, userID int64) (*service.VeyraNodeRewardResult, error) {
	return &service.VeyraNodeRewardResult{UserID: userID, RedeemedImages: 6}, nil
}

func (s *nodeRewardAccountStub) SettleNodeRewards(_ context.Context, userID, images int64) (*service.VeyraNodeRewardResult, error) {
	s.creditCalls++
	return &service.VeyraNodeRewardResult{UserID: userID, RedeemedImages: images, CreditedImages: images}, nil
}

func TestNodeRewardRoutesRequireBothCredentialsAndValidateCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	accounts := &nodeRewardAccountStub{}
	token := strings.Repeat("r", 40)
	RegisterRoutes(router.Group("/api"), testJWTAuth(), RoutesConfig{
		Enabled: true, PortalEnabled: true, InternalToken: "internal-secret", NodeRewardCreditToken: token,
	}, nil, nil, accounts)

	call := func(method, internal, rewardToken, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/veyra/internal/users/42/node-rewards", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Veyra-Internal-Token", internal)
		req.Header.Set("X-Veyra-Node-Reward-Token", rewardToken)
		router.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, http.StatusForbidden, call("POST", "internal-secret", "", `{"eligible_images":7}`).Code)
	require.Equal(t, http.StatusForbidden, call("POST", "", token, `{"eligible_images":7}`).Code)
	for _, body := range []string{`{"eligible_images":1.5}`, `{"eligible_images":-1}`, `{"eligible_images":1000000001}`} {
		require.Equal(t, http.StatusBadRequest, call("POST", "internal-secret", token, body).Code)
	}
	current := call("GET", "internal-secret", token, "")
	require.Equal(t, http.StatusOK, current.Code)
	require.Contains(t, current.Body.String(), `"redeemed_images":6`)
	require.Zero(t, accounts.creditCalls)
	success := call("POST", "internal-secret", token, `{"eligible_images":7}`)
	require.Equal(t, http.StatusOK, success.Code)
	require.Contains(t, success.Body.String(), `"credited_images":7`)
	require.Equal(t, 1, accounts.creditCalls)
}
