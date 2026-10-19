//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGrokImagesWokeyJSONAuditToMultipartBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	encoded := base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	dataURL := "data:image/png;base64," + encoded
	for _, scenario := range []string{"valid", "duplicate_prompt", "escaped_prompt", "duplicate_image_url", "duplicate_model", "body_limit"} {
		t.Run(scenario, func(t *testing.T) {
			fields := `"prompt":"AUDITED-FIRST"`
			imageField := fmt.Sprintf(`"image":{"url":%q}`, dataURL)
			modelField := `"model":"grok-imagine-image-quality"`
			switch scenario {
			case "duplicate_prompt":
				fields += `,"prompt":"UNREVIEWED-LAST"`
			case "escaped_prompt":
				fields += `,"pr\u006fmpt":"UNREVIEWED-LAST"`
			case "duplicate_image_url":
				imageField = fmt.Sprintf(`"image":{"url":%q,"url":%q}`, dataURL, dataURL)
			case "duplicate_model":
				modelField += `,"model":"grok-imagine-image-quality"`
			}
			body := []byte(`{` + modelField + `,` + fields + `,` + imageField + `,"vendor_scalar":17}`)
			account := service.Account{ID: 1, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2,
				Credentials: map[string]any{"api_key": "mock-only-key", "base_url": "https://api.wokey.ai/v1", "model_mapping": map[string]any{"grok-imagine-image-quality": "mapped-grok-edit"}}}
			accounts := openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}
			slots := &grokMediaSlotsCache{accounts: map[string]int64{}, users: map[string]int64{}}
			concurrency := service.NewConcurrencyService(slots)
			logs, charges := &grokRoutePricingCountingUsageLogRepo{}, &grokRoutePricingCountingUsageBillingRepo{}
			engine := &handlerPromptEngine{mode: securityaudit.ModeBlocking, decision: &securityaudit.PromptDecision{Kind: securityaudit.DecisionAllow, AllowNextStage: true}}
			forwarded := map[string]string{}
			upstream := &grokMediaSlotUpstream{call: func(req *http.Request, _ int64) (*http.Response, error) {
				require.Equal(t, "https://api.wokey.ai/v1/images/edits", req.URL.String())
				mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
				require.NoError(t, err)
				require.Equal(t, "multipart/form-data", mediaType)
				reader := multipart.NewReader(req.Body, params["boundary"])
				imageParts := 0
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					require.NoError(t, err)
					data, err := io.ReadAll(part)
					require.NoError(t, err)
					if part.FileName() != "" {
						imageParts++
						require.Equal(t, "image", part.FormName())
						require.Equal(t, pngBytes.Bytes(), data)
					} else {
						forwarded[part.FormName()] = string(data)
					}
				}
				require.Equal(t, 1, imageParts)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"` + encoded + `"}]}`))}, nil
			}}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			cfg.Gateway.ImageConcurrency.Enabled = true
			cfg.Gateway.ImageConcurrency.MaxConcurrentRequests = 1
			billing := service.NewBillingService(cfg, nil)
			gateway := service.NewOpenAIGatewayService(accounts, logs, charges, nil, nil, nil, nil, cfg, nil, concurrency, billing, nil, nil, upstream, &service.DeferredService{}, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil)
			billingCache := service.NewBillingCacheService(grokRoutePricingBalanceOnlyCache{}, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billingCache.Stop)
			pool := newUsageRecordTestPool(t)
			h := NewOpenAIGatewayHandler(gateway, concurrency, billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), pool, nil, nil, nil, cfg)
			h.securityAuditCoordinator = securityaudit.NewCoordinator(nil, engine)
			groupID := int64(24)
			price := 0.25
			key := &service.APIKey{ID: 20, UserID: 10, GroupID: &groupID, User: &service.User{ID: 10, Balance: 100}, Group: &service.Group{ID: groupID, Platform: service.PlatformGrok, AllowImageGeneration: true, RateMultiplier: 1, ModelPricing: []service.ChannelModelPricing{{Models: []string{"grok-imagine-image-quality"}, BillingMode: service.BillingModePerRequest, PerRequestPrice: &price}}}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body)).WithContext(context.Background())
			c.Request.Header.Set("Content-Type", "application/json")
			if scenario == "body_limit" {
				c.Request.Body = http.MaxBytesReader(recorder, c.Request.Body, int64(len(body)-1))
			}
			c.Set(string(middleware.ContextKeyAPIKey), key)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10, Concurrency: 2})
			h.GrokImages(c)
			pool.Stop()
			slots.assertReleased(t)
			h.imageLimiter.mu.Lock()
			imageActive, imageWaiting := h.imageLimiter.active, h.imageLimiter.waiting
			h.imageLimiter.mu.Unlock()
			require.Zero(t, imageActive, "global image slot is released")
			require.Zero(t, imageWaiting)
			logCount, _ := logs.snapshot()
			chargeCount, command := charges.snapshot()
			evaluated, _, requests := engine.snapshot()
			if scenario == "body_limit" {
				require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
				require.Zero(t, evaluated)
			} else {
				require.Equal(t, 1, evaluated)
				require.Equal(t, "AUDITED-FIRST", gjson.GetBytes(requests[0].Body, "prompt").String())
				require.Equal(t, int64(1), gjson.GetBytes(requests[0].Body, "images.#").Int())
				require.Positive(t, slots.acquired)
				require.Positive(t, slots.userAcquired)
			}
			if scenario != "valid" {
				require.Zero(t, upstream.calls, "ambiguous JSON must not reach a provider even after audit")
				require.Zero(t, logCount)
				require.Zero(t, chargeCount)
				require.Nil(t, command)
				if scenario != "body_limit" {
					require.Equal(t, http.StatusBadRequest, recorder.Code)
					require.Contains(t, recorder.Body.String(), "invalid_request_error")
				}
				require.NotContains(t, recorder.Body.String(), "UNREVIEWED-LAST")
				return
			}
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, 1, upstream.calls)
			require.Equal(t, "AUDITED-FIRST", forwarded["prompt"])
			require.Equal(t, "mapped-grok-edit", forwarded["model"])
			require.Equal(t, "17", forwarded["vendor_scalar"])
			require.Equal(t, 1, logCount)
			require.Equal(t, 1, chargeCount)
			require.InDelta(t, price, command.BalanceCost, 1e-12)
		})
	}
}
