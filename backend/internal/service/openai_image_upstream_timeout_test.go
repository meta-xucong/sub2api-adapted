//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	smartrouter "github.com/Wei-Shaw/sub2api/internal/smartrouter/core"
	"github.com/stretchr/testify/require"
)

func TestOpenAIImageUpstreamTimeout_DefaultsToThreeMinutes(t *testing.T) {
	svc := &OpenAIGatewayService{}

	require.Equal(t, 180*time.Second, svc.openAIImageUpstreamTimeout())
}

func TestOpenAIImageUpstreamTimeout_ConfiguresDeadline(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ImageUpstreamTimeoutSeconds = 2
	svc := &OpenAIGatewayService{cfg: cfg}

	ctx, cancel := svc.withOpenAIImageUpstreamTimeout(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(2*time.Second), deadline, 250*time.Millisecond)
}

func TestOpenAIImageUpstreamTimeout_CanBeDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ImageUpstreamTimeoutSeconds = 0
	svc := &OpenAIGatewayService{cfg: cfg}

	ctx, cancel := svc.withOpenAIImageUpstreamTimeout(context.Background())
	defer cancel()

	_, ok := ctx.Deadline()
	require.False(t, ok)
}

func TestOpenAIImageRequestTimeout_DefaultsToTenMinutes(t *testing.T) {
	svc := &OpenAIGatewayService{}

	require.Equal(t, 600*time.Second, svc.openAIImageRequestTimeout())
}

func TestOpenAIImageRequestTimeout_ConfiguresDeadline(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ImageRequestTimeoutSeconds = 2
	svc := &OpenAIGatewayService{cfg: cfg}

	ctx, cancel := svc.WithOpenAIImageRequestTimeout(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(2*time.Second), deadline, 250*time.Millisecond)
}

func TestOpenAIImageRequestTimeout_CanBeDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ImageRequestTimeoutSeconds = 0
	svc := &OpenAIGatewayService{cfg: cfg}

	ctx, cancel := svc.WithOpenAIImageRequestTimeout(context.Background())
	defer cancel()

	_, ok := ctx.Deadline()
	require.False(t, ok)
}

func TestOpenAIImageUpstreamTimeout_UsesLaneCapabilityProfileWhenEnabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ImageUpstreamTimeoutSeconds = 180
	cfg.Gateway.SmartRouter.Enabled = true
	cfg.Gateway.SmartRouter.AdaptiveTimeout.Enabled = true
	cfg.Gateway.SmartRouter.AdaptiveTimeout.DefaultSeconds = 180
	cfg.Gateway.SmartRouter.AdaptiveTimeout.MinSeconds = 30
	cfg.Gateway.SmartRouter.AdaptiveTimeout.MaxSeconds = 300
	cfg.Gateway.SmartRouter.AdaptiveTimeout.SafetyMarginSeconds = 10
	cfg.Gateway.SmartRouter.AdaptiveTimeout.Multiplier = 1.25
	cfg.Gateway.SmartRouter.AdaptiveTimeout.WindowSize = 4
	cfg.Gateway.SmartRouter.AdaptiveTimeout.ReserveSeconds = 30
	svc := &OpenAIGatewayService{cfg: cfg}
	account := &Account{ID: 88001}
	for i := 0; i < 4; i++ {
		svc.observeSmartRouterImageAttempt(context.Background(), account, smartrouter.CapabilityImageGeneration, 30*time.Second, 200, true, nil)
	}

	ctx, cancel := svc.withOpenAIImageUpstreamTimeoutFor(context.Background(), account, smartrouter.CapabilityImageGeneration)
	defer cancel()
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(47*time.Second), deadline, 2*time.Second)
}

func TestOpenAIImageUpstreamTimeout_ImageResilienceUsesSizeAndInputProfiles(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ImageUpstreamTimeoutSeconds = 180
	cfg.Gateway.SmartRouter.Enabled = true
	cfg.Gateway.SmartRouter.ImageResilience.Enabled = true
	cfg.Gateway.SmartRouter.ImageResilience.GenerationEnabled = true
	cfg.Gateway.SmartRouter.ImageResilience.EditEnabled = true
	cfg.Gateway.SmartRouter.ImageResilience.StandardDefaultSeconds = 150
	cfg.Gateway.SmartRouter.ImageResilience.StandardMinSeconds = 45
	cfg.Gateway.SmartRouter.ImageResilience.StandardMaxSeconds = 240
	cfg.Gateway.SmartRouter.ImageResilience.SpecialistDefaultSeconds = 210
	cfg.Gateway.SmartRouter.ImageResilience.SpecialistMinSeconds = 75
	cfg.Gateway.SmartRouter.ImageResilience.SpecialistMaxSeconds = 360
	cfg.Gateway.SmartRouter.ImageResilience.SafetyMarginSeconds = 20
	cfg.Gateway.SmartRouter.ImageResilience.P90Multiplier = 1.25
	cfg.Gateway.SmartRouter.ImageResilience.FallbackReserveSeconds = 45
	svc := &OpenAIGatewayService{cfg: cfg}
	account := &Account{ID: 88002}

	textCtx := WithOpenAIImageSmartRouterInputMode(
		WithOpenAIImageSmartRouterSizeTier(context.Background(), "1K"),
		"text_only",
	)
	textUpstream, textCancel := svc.withOpenAIImageUpstreamTimeoutFor(textCtx, account, smartrouter.CapabilityImageGeneration)
	defer textCancel()
	textDeadline, ok := textUpstream.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(150*time.Second), textDeadline, 2*time.Second)

	editCtx := WithOpenAIImageSmartRouterInputMode(
		WithOpenAIImageSmartRouterSizeTier(context.Background(), "4K"),
		"reference_image",
	)
	editUpstream, editCancel := svc.withOpenAIImageUpstreamTimeoutFor(editCtx, account, smartrouter.CapabilityImageEdit)
	defer editCancel()
	editDeadline, ok := editUpstream.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(210*time.Second), editDeadline, 2*time.Second)
}

func TestDetachOpenAIImageUpstreamContext_PreservesDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ctx, release := detachOpenAIImageUpstreamContext(parent)
	defer release()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(time.Second), deadline, 250*time.Millisecond)

	cancel()
	require.NoError(t, ctx.Err())
}
