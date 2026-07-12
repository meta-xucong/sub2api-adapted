//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
