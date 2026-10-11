package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnifiedGatewayUpstreamIdentityUsesSafeNormalizedOrigin(t *testing.T) {
	first := &Account{Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://operator:secret@API.YETOKEN.VIP:443/v1/tenant-a?token=secret#fragment"}}
	second := &Account{Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://api.yetoken.vip/v1/tenant-b"}}

	firstKey, firstLabel := unifiedGatewayUpstreamIdentity(first)
	secondKey, secondLabel := unifiedGatewayUpstreamIdentity(second)
	require.Equal(t, "url:https://api.yetoken.vip", firstKey)
	require.Equal(t, firstKey, secondKey, "accounts at the same upstream host group together")
	require.Equal(t, "https://api.yetoken.vip", firstLabel)
	require.Equal(t, firstLabel, secondLabel)
	require.NotContains(t, firstLabel, "operator")
	require.NotContains(t, firstLabel, "secret")
	require.NotContains(t, firstLabel, "tenant-a")
}

func TestUnifiedGatewayUpstreamIdentityUsesDedicatedWokeyKey(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://api.wokey.ai/v1"}}
	key, label := unifiedGatewayUpstreamIdentity(account)
	require.Equal(t, "provider:wokey", key)
	require.Equal(t, "Wokey", label)
}

func TestUnifiedGatewayUpstreamIdentityFallsBackToPlatform(t *testing.T) {
	account := &Account{Platform: PlatformDeepseek}
	key, label := unifiedGatewayUpstreamIdentity(account)
	require.Equal(t, "platform:deepseek", key)
	require.Equal(t, "DeepSeek", label)
}
