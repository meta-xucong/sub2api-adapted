package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func unifiedCapabilityTestAccount(platform, accountType string, credentials, extra map[string]any) *Account {
	return &Account{
		ID:          1,
		Platform:    platform,
		Type:        accountType,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: credentials,
		Extra:       extra,
	}
}

func TestUnifiedGatewayProviderCapabilityMatrix(t *testing.T) {
	nativeAnthropicCredentials := map[string]any{"api_protocol": APIProtocolAnthropic}
	tests := []struct {
		name        string
		provider    string
		account     *Account
		endpoint    string
		wantAllowed bool
		wantCode    string
	}{
		{
			name:        "OpenAI API key chat",
			provider:    UnifiedGatewayProviderOpenAIAPIKey,
			account:     unifiedCapabilityTestAccount(PlatformOpenAI, AccountTypeAPIKey, nil, nil),
			endpoint:    UnifiedGatewayEndpointChatCompletions,
			wantAllowed: true,
		},
		{
			name:     "OpenAI API key rejects videos",
			provider: UnifiedGatewayProviderOpenAIAPIKey,
			account:  unifiedCapabilityTestAccount(PlatformOpenAI, AccountTypeAPIKey, nil, nil),
			endpoint: UnifiedGatewayEndpointVideos,
			wantCode: unifiedGatewayCapabilityEndpointUnsupported,
		},
		{
			name:        "OpenAI OAuth responses",
			provider:    "openai-chatgpt",
			account:     unifiedCapabilityTestAccount(PlatformOpenAI, AccountTypeOAuth, nil, nil),
			endpoint:    UnifiedGatewayEndpointResponses,
			wantAllowed: true,
		},
		{
			name:     "OpenAI OAuth rejects videos",
			provider: UnifiedGatewayProviderOpenAIOAuth,
			account:  unifiedCapabilityTestAccount(PlatformOpenAI, AccountTypeOAuth, nil, nil),
			endpoint: UnifiedGatewayEndpointVideos,
			wantCode: unifiedGatewayCapabilityEndpointUnsupported,
		},
		{
			name:        "generic OpenAI compatible images",
			provider:    UnifiedGatewayProviderOpenAICompatible,
			account:     unifiedCapabilityTestAccount(PlatformOpenAI, AccountTypeAPIKey, nil, nil),
			endpoint:    UnifiedGatewayEndpointImages,
			wantAllowed: true,
		},
		{
			name:        "generic CN compatible chat",
			provider:    UnifiedGatewayProviderOpenAICompatible,
			account:     unifiedCapabilityTestAccount(PlatformKimi, AccountTypeAPIKey, nil, nil),
			endpoint:    UnifiedGatewayEndpointChatCompletions,
			wantAllowed: true,
		},
		{
			name:        "native Anthropic CN chat",
			provider:    "anthropic-cn",
			account:     unifiedCapabilityTestAccount(PlatformKimi, AccountTypeAPIKey, nativeAnthropicCredentials, nil),
			endpoint:    UnifiedGatewayEndpointChatCompletions,
			wantAllowed: true,
		},
		{
			name:        "generic Anthropic API key chat",
			provider:    UnifiedGatewayProviderAnthropicAPIKey,
			account:     unifiedCapabilityTestAccount(PlatformAnthropic, AccountTypeAPIKey, nil, nil),
			endpoint:    UnifiedGatewayEndpointChatCompletions,
			wantAllowed: true,
		},
		{
			name:        "generic Anthropic API key responses",
			provider:    "claude-api-key",
			account:     unifiedCapabilityTestAccount(PlatformAnthropic, AccountTypeAPIKey, nil, nil),
			endpoint:    UnifiedGatewayEndpointResponses,
			wantAllowed: true,
		},
		{
			name:     "generic Anthropic API key rejects media",
			provider: UnifiedGatewayProviderAnthropicAPIKey,
			account:  unifiedCapabilityTestAccount(PlatformAnthropic, AccountTypeAPIKey, nil, nil),
			endpoint: UnifiedGatewayEndpointImages,
			wantCode: unifiedGatewayCapabilityEndpointUnsupported,
		},
		{
			name:     "native Anthropic CN rejects media",
			provider: UnifiedGatewayProviderNativeAnthropicCN,
			account:  unifiedCapabilityTestAccount(PlatformDeepseek, AccountTypeUpstream, nativeAnthropicCredentials, nil),
			endpoint: UnifiedGatewayEndpointImages,
			wantCode: unifiedGatewayCapabilityEndpointUnsupported,
		},
		{
			name:     "native Anthropic CN cannot use generic media alias",
			provider: "kimi",
			account:  unifiedCapabilityTestAccount(PlatformKimi, AccountTypeAPIKey, nativeAnthropicCredentials, nil),
			endpoint: UnifiedGatewayEndpointImages,
			wantCode: unifiedGatewayCapabilityEndpointUnsupported,
		},
		{
			name:     "native Anthropic identity requires native protocol",
			provider: UnifiedGatewayProviderNativeAnthropicCN,
			account:  unifiedCapabilityTestAccount(PlatformZhipu, AccountTypeAPIKey, nil, nil),
			endpoint: UnifiedGatewayEndpointChatCompletions,
			wantCode: unifiedGatewayCapabilityIdentityMismatch,
		},
		{
			name:        "Volcengine Ark image generation",
			provider:    "volcengine_ark",
			account:     unifiedCapabilityTestAccount(PlatformOpenAI, AccountTypeAPIKey, nil, map[string]any{"provider": openAIProviderVolcengineArk}),
			endpoint:    UnifiedGatewayEndpointImages,
			wantAllowed: true,
		},
		{
			name:     "Volcengine Ark rejects videos",
			provider: UnifiedGatewayProviderVolcengineArk,
			account:  unifiedCapabilityTestAccount(PlatformOpenAI, AccountTypeAPIKey, nil, map[string]any{"provider": openAIProviderVolcengineArk}),
			endpoint: UnifiedGatewayEndpointVideos,
			wantCode: unifiedGatewayCapabilityEndpointUnsupported,
		},
		{
			name:     "Volcengine Ark identity requires Ark metadata",
			provider: UnifiedGatewayProviderVolcengineArk,
			account:  unifiedCapabilityTestAccount(PlatformOpenAI, AccountTypeAPIKey, nil, nil),
			endpoint: UnifiedGatewayEndpointChatCompletions,
			wantCode: unifiedGatewayCapabilityIdentityMismatch,
		},
		{
			name:        "Grok media video",
			provider:    UnifiedGatewayProviderGrokMedia,
			account:     unifiedCapabilityTestAccount(PlatformGrok, AccountTypeOAuth, nil, nil),
			endpoint:    UnifiedGatewayEndpointVideos,
			wantAllowed: true,
		},
		{
			name:     "Grok media rejects chat",
			provider: UnifiedGatewayProviderGrokMedia,
			account:  unifiedCapabilityTestAccount(PlatformGrok, AccountTypeAPIKey, nil, nil),
			endpoint: UnifiedGatewayEndpointChatCompletions,
			wantCode: unifiedGatewayCapabilityEndpointUnsupported,
		},
		{
			name:     "Grok OAuth text is not a unified adapter",
			provider: UnifiedGatewayProviderGrok,
			account:  unifiedCapabilityTestAccount(PlatformGrok, AccountTypeOAuth, nil, nil),
			endpoint: UnifiedGatewayEndpointChatCompletions,
			wantCode: unifiedGatewayCapabilityEndpointUnsupported,
		},
		{
			name:        "Gemini chat",
			provider:    UnifiedGatewayProviderGemini,
			account:     unifiedCapabilityTestAccount(PlatformGemini, AccountTypeServiceAccount, nil, nil),
			endpoint:    UnifiedGatewayEndpointChatCompletions,
			wantAllowed: true,
		},
		{
			name:     "Gemini rejects images",
			provider: UnifiedGatewayProviderGemini,
			account:  unifiedCapabilityTestAccount(PlatformGemini, AccountTypeAPIKey, nil, nil),
			endpoint: UnifiedGatewayEndpointImages,
			wantCode: unifiedGatewayCapabilityEndpointUnsupported,
		},
		{
			name:        "Antigravity OAuth responses",
			provider:    UnifiedGatewayProviderAntigravity,
			account:     unifiedCapabilityTestAccount(PlatformAntigravity, AccountTypeOAuth, nil, nil),
			endpoint:    UnifiedGatewayEndpointResponses,
			wantAllowed: true,
		},
		{
			name:     "Antigravity API key rejects responses",
			provider: UnifiedGatewayProviderAntigravity,
			account:  unifiedCapabilityTestAccount(PlatformAntigravity, AccountTypeAPIKey, nil, nil),
			endpoint: UnifiedGatewayEndpointResponses,
			wantCode: unifiedGatewayCapabilityEndpointUnsupported,
		},
		{
			name:     "Antigravity rejects videos",
			provider: UnifiedGatewayProviderAntigravity,
			account:  unifiedCapabilityTestAccount(PlatformAntigravity, AccountTypeOAuth, nil, nil),
			endpoint: UnifiedGatewayEndpointVideos,
			wantCode: unifiedGatewayCapabilityEndpointUnsupported,
		},
		{
			name:     "unknown provider fails closed",
			provider: "provider-that-has-no-adapter",
			account:  unifiedCapabilityTestAccount(PlatformOpenAI, AccountTypeAPIKey, nil, nil),
			endpoint: UnifiedGatewayEndpointChatCompletions,
			wantCode: unifiedGatewayCapabilityUnknown,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := CheckUnifiedGatewayProviderCapability(test.provider, test.account, test.endpoint)
			require.Equal(t, test.wantAllowed, decision.Allowed, decision.Message)
			if test.wantCode != "" {
				require.Equal(t, test.wantCode, decision.Code, decision.Message)
			}
		})
	}
}

func TestUnifiedGatewayProviderCapabilityMatrixIsCanonicalAndDefensive(t *testing.T) {
	matrix := UnifiedGatewayProviderCapabilityMatrix()
	require.Len(t, matrix, 11)
	require.Equal(t, UnifiedGatewayProviderOpenAI, matrix[0].ProviderIdentity)
	require.Equal(t, UnifiedGatewayProviderAntigravity, matrix[len(matrix)-1].ProviderIdentity)
	matrix[0].SupportedEndpoints[0] = "mutated"
	matrixAgain := UnifiedGatewayProviderCapabilityMatrix()
	require.NotEqual(t, "mutated", matrixAgain[0].SupportedEndpoints[0])
}

func TestUnifiedGatewayCandidateProviderRecognizesGenericAnthropicAPIKey(t *testing.T) {
	account := unifiedCapabilityTestAccount(PlatformAnthropic, AccountTypeAPIKey, nil, nil)
	require.Equal(t, UnifiedGatewayProviderAnthropicAPIKey, unifiedGatewayCandidateProvider(account))
	require.Equal(t, UnifiedGatewayEndpointChatCompletions, unifiedGatewayCandidateEndpoint(account, "claude-opus-4-6"))
}

type unifiedCapabilityAccountReader struct {
	accounts []*Account
}

func requireUnifiedGatewayBlocker(t *testing.T, validation *UnifiedGatewayValidationResult, code string) {
	t.Helper()
	for _, blocker := range validation.Blockers {
		if blocker.Code == code {
			return
		}
	}
	require.Failf(t, "missing blocker", "expected blocker %q, got %#v", code, validation.Blockers)
}

func (r unifiedCapabilityAccountReader) GetByIDs(context.Context, []int64) ([]*Account, error) {
	return r.accounts, nil
}

func (unifiedCapabilityAccountReader) List(context.Context, pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	return nil, &pagination.PaginationResult{}, nil
}

func newUnifiedCapabilityAdminService(repo *unifiedAdminMemoryRepo, accounts ...*Account) *UnifiedGatewayAdminService {
	cfg := &config.Config{}
	cfg.Gateway.UnifiedGatewayAdminUIEnabled = true
	cfg.Gateway.UnifiedGatewayAccessGroupID = 7
	return NewUnifiedGatewayAdminService(repo, unifiedAdminGroupReader{}, unifiedCapabilityAccountReader{accounts: accounts}, cfg)
}

func TestUnifiedGatewayAdminCapabilityValidationUsesBindingEndpointOverride(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedCapabilityAdminService(repo, unifiedCapabilityTestAccount(PlatformGemini, AccountTypeAPIKey, nil, nil))
	document := validUnifiedAdminDocument()
	document.Lanes[0].Targets[0].ProviderIdentity = UnifiedGatewayProviderGemini
	document.Lanes[0].Targets[0].Endpoint = UnifiedGatewayEndpointChatCompletions
	document.Lanes[0].Targets[0].Bindings[0].Endpoint = UnifiedGatewayEndpointResponses

	validation, err := service.validateDocument(context.Background(), document)
	require.NoError(t, err)
	require.False(t, validation.Valid)
	requireUnifiedGatewayBlocker(t, validation, unifiedGatewayCapabilityEndpointUnsupported)
	require.Contains(t, validation.FieldErrors, "lanes[0].targets[0].bindings[0].endpoint")

	document.Lanes[0].Targets[0].Bindings[0].Endpoint = UnifiedGatewayEndpointChatCompletions
	validation, err = service.validateDocument(context.Background(), document)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Blockers)
}

func TestUnifiedGatewayAdminPublishRejectsUnsupportedProviderCombination(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedCapabilityAdminService(repo, unifiedCapabilityTestAccount(PlatformGemini, AccountTypeAPIKey, nil, nil))
	document := validUnifiedAdminDocument()
	document.Lanes[0].Targets[0].ProviderIdentity = UnifiedGatewayProviderGemini
	document.Lanes[0].Targets[0].Endpoint = UnifiedGatewayEndpointImages
	document.Lanes[0].Targets[0].Bindings[0].Endpoint = UnifiedGatewayEndpointImages
	draft, err := service.CreateDraft(context.Background(), "7", "unsupported-provider", document)
	require.NoError(t, err)

	_, err = service.PublishDraft(context.Background(), "7", "publish-unsupported-provider", draft.ID, "0", "test", validationToken(draft.Document))
	require.ErrorIs(t, err, ErrUnifiedGatewayAdminValidation)
}

func TestUnifiedGatewayAdminValidationRejectsUnknownProvider(t *testing.T) {
	repo := newUnifiedAdminMemoryRepo()
	service := newUnifiedCapabilityAdminService(repo, unifiedCapabilityTestAccount(PlatformOpenAI, AccountTypeAPIKey, nil, nil))
	document := validUnifiedAdminDocument()
	document.Lanes[0].Targets[0].ProviderIdentity = "provider-without-an-adapter"

	validation, err := service.validateDocument(context.Background(), document)
	require.NoError(t, err)
	require.False(t, validation.Valid)
	requireUnifiedGatewayBlocker(t, validation, unifiedGatewayCapabilityUnknown)
	require.Contains(t, validation.FieldErrors["lanes[0].targets[0].provider_identity"], "not in the unified gateway capability matrix")
}
