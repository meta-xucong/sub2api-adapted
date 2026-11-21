package veyra

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type nodeRewardAccountStub struct {
	balanceAccountStub
	creditCalls   int
	receiptCalls  int
	receiptUser   int64
	receiptBefore int64
	receiptLimit  int
	receiptError  error
}

func (*nodeRewardAccountStub) GetNodeRewardSettlement(_ context.Context, userID int64) (*service.VeyraNodeRewardResult, error) {
	return &service.VeyraNodeRewardResult{UserID: userID, RedeemedImages: 6}, nil
}

func (s *nodeRewardAccountStub) SettleNodeRewards(_ context.Context, userID, images int64) (*service.VeyraNodeRewardResult, error) {
	s.creditCalls++
	return &service.VeyraNodeRewardResult{UserID: userID, RedeemedImages: images, CreditedImages: images}, nil
}

func (s *nodeRewardAccountStub) ListNodeRewardReceipts(_ context.Context, userID, before int64, limit int) (*service.VeyraNodeRewardReceiptPage, error) {
	s.receiptCalls++
	s.receiptUser, s.receiptBefore, s.receiptLimit = userID, before, limit
	if s.receiptError != nil {
		return nil, s.receiptError
	}
	next := int64(44)
	return &service.VeyraNodeRewardReceiptPage{
		Items: []service.VeyraNodeRewardReceipt{{
			RedeemedImages: 44, CreditedImages: 44, Amount: "0.88", BalanceAfter: "900719925474.99000001",
			CreatedAt: time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC),
		}},
		NextBeforeRedeemedImages: &next,
	}, nil
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

func TestNodeRewardReceiptHistoryRequiresCredentialsAndReadsRequestedAccountPage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	accounts := &nodeRewardAccountStub{}
	token := strings.Repeat("r", 40)
	RegisterRoutes(router.Group("/api"), testJWTAuth(), RoutesConfig{
		Enabled: true, PortalEnabled: true, InternalToken: "internal-secret", NodeRewardCreditToken: token,
	}, nil, nil, accounts)

	read := func(path, internal, reward string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Veyra-Internal-Token", internal)
		req.Header.Set("X-Veyra-Node-Reward-Token", reward)
		router.ServeHTTP(rec, req)
		return rec
	}
	path := "/api/veyra/internal/users/42/node-rewards/receipts"
	require.Equal(t, http.StatusForbidden, read(path, "internal-secret", "").Code)
	require.Equal(t, http.StatusForbidden, read(path, "", token).Code)
	for _, query := range []string{"?before_redeemed_images=0", "?before_redeemed_images=-1", "?limit=51", "?limit=1.5", "?limit=1&limit=2", "?limit="} {
		require.Equal(t, http.StatusBadRequest, read(path+query, "internal-secret", token).Code, query)
	}
	require.Zero(t, accounts.receiptCalls)

	defaultPage := read(path, "internal-secret", token)
	require.Equal(t, http.StatusOK, defaultPage.Code)
	require.Equal(t, int64(42), accounts.receiptUser)
	require.Zero(t, accounts.receiptBefore)
	require.Equal(t, service.DefaultVeyraNodeRewardReceiptLimit, accounts.receiptLimit)
	require.Equal(t, "no-store", defaultPage.Header().Get("Cache-Control"))
	require.Contains(t, defaultPage.Body.String(), `"balance_after":"900719925474.99000001"`)
	require.Contains(t, defaultPage.Body.String(), `"amount":"0.88"`)
	require.Contains(t, defaultPage.Body.String(), `"created_at":"2026-11-21T09:00:00Z"`)
	require.Contains(t, defaultPage.Body.String(), `"next_before_redeemed_images":44`)

	require.Equal(t, http.StatusOK, read(path+"?before_redeemed_images=48&limit=1", "internal-secret", token).Code)
	require.Equal(t, int64(48), accounts.receiptBefore)
	require.Equal(t, 1, accounts.receiptLimit)
	accounts.receiptError = service.ErrVeyraNodeRewardInactive
	require.Equal(t, http.StatusForbidden, read(path, "internal-secret", token).Code)
	accounts.receiptError = service.ErrUserNotFound
	require.Equal(t, http.StatusNotFound, read(path, "internal-secret", token).Code)
	require.Zero(t, accounts.creditCalls)
}
