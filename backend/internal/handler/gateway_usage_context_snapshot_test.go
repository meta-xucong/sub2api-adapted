//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type usageSnapshotContextReadCounter struct {
	context.Context
	reads atomic.Int64
}

func (c *usageSnapshotContextReadCounter) Value(key any) any {
	c.reads.Add(1)
	return c.Context.Value(key)
}

func TestAlphaSearchQueuedUsageDoesNotReadReusedGinContext(t *testing.T) {
	usageRepo := &compatPartialUsageRepo{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gateway := service.NewOpenAIGatewayService(nil, usageRepo, nil, nil, nil, nil, nil, cfg,
		nil, nil, service.NewBillingService(cfg, nil), nil, nil, nil, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	pool := newUsageRecordTestPool(t)
	releaseWorker := blockCompatUsageWorker(t, pool)
	h := &OpenAIGatewayHandler{gatewayService: gateway, usageRecordWorkerPool: pool}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	requestCtx, pricingAt := gateway.WithOpenAIRequestPricingContext(context.Background(), nil)
	require.False(t, pricingAt.IsZero())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", nil).WithContext(requestCtx)
	apiKey := &service.APIKey{ID: 9301, UserID: 9302, User: &service.User{ID: 9302}}
	account := &service.Account{ID: 9303, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}
	result := &service.OpenAIForwardResult{RequestID: "search_snapshot", Model: "web-search", WebSearchCalls: 1}
	h.recordAlphaSearchUsage(c, apiKey, account, nil, service.ChannelMappingResult{}, "web-search", nil, result, apiKey.UserID)

	reused := &usageSnapshotContextReadCounter{Context: context.Background()}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(reused)
	releaseWorker()
	pool.Stop()
	require.Zero(t, reused.reads.Load(), "queued pricing must not inspect the next request's context")
	usageRepo.mu.Lock()
	defer usageRepo.mu.Unlock()
	require.Len(t, usageRepo.logs, 1, "the original request's usage must still be recorded")
}
