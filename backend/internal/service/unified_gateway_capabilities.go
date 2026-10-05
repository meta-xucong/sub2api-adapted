package service

import (
	"fmt"
	"sort"
	"strings"
)

// Provider identities are route-level adapter identities.  They are kept
// separate from Account.Platform because one platform can expose more than
// one protocol (for example OpenAI API-key and OAuth accounts, or a CN
// provider's native Anthropic endpoint).
const (
	UnifiedGatewayProviderOpenAI            = "openai"
	UnifiedGatewayProviderOpenAIAPIKey      = "openai-api-key"
	UnifiedGatewayProviderOpenAIOAuth       = "openai-oauth"
	UnifiedGatewayProviderOpenAICompatible  = "openai-compatible"
	UnifiedGatewayProviderNativeAnthropicCN = "native-anthropic-cn"
	// UnifiedGatewayProviderAnthropicAPIKey is the OpenAI-shaped unified
	// adapter backed by a generic Anthropic API-key account.  The public
	// unified endpoint stays OpenAI-compatible; the existing GatewayService
	// converts it to Anthropic Messages before the upstream call.
	UnifiedGatewayProviderAnthropicAPIKey = "anthropic-api-key"
	UnifiedGatewayProviderVolcengineArk   = "volcengine-ark"
	UnifiedGatewayProviderGrok            = "grok"
	UnifiedGatewayProviderGrokMedia       = "grok-media"
	UnifiedGatewayProviderGemini          = "gemini"
	UnifiedGatewayProviderAntigravity     = "antigravity"
)

// UnifiedGatewayProviderCapability is the serializable part of the
// capability matrix.  Account metadata checks that cannot be expressed as a
// set (such as CN api_protocol or the Ark provider marker) are applied by
// CheckUnifiedGatewayProviderCapability.
type UnifiedGatewayProviderCapability struct {
	ProviderIdentity   string   `json:"provider_identity"`
	AccountPlatforms   []string `json:"account_platforms"`
	AccountTypes       []string `json:"account_types"`
	SupportedEndpoints []string `json:"supported_endpoints"`
}

// UnifiedGatewayCapabilityDecision is the fail-closed result used by admin
// validation.  Message is safe to return to the management plane: it contains
// no credentials or upstream response data.
type UnifiedGatewayCapabilityDecision struct {
	Allowed           bool
	ProviderIdentity  string
	CanonicalProvider string
	AccountPlatform   string
	AccountType       string
	Endpoint          string
	Code              string
	Message           string
}

const (
	unifiedGatewayCapabilityUnknown             = "provider_capability_unknown"
	unifiedGatewayCapabilityAccountRequired     = "provider_capability_account_required"
	unifiedGatewayCapabilityPlatformMismatch    = "provider_capability_platform_mismatch"
	unifiedGatewayCapabilityTypeMismatch        = "provider_capability_account_type_mismatch"
	unifiedGatewayCapabilityIdentityMismatch    = "provider_capability_identity_mismatch"
	unifiedGatewayCapabilityEndpointUnsupported = "provider_endpoint_unsupported"
)

type unifiedGatewayCapabilityRule struct {
	capability   UnifiedGatewayProviderCapability
	platforms    map[string]struct{}
	accountTypes map[string]struct{}
	endpoints    map[string]struct{}
	validate     func(account *Account, endpoint string) (code, message string)
}

func unifiedGatewayCapabilityRuleFor(identity string, platforms, accountTypes, endpoints []string, validate func(*Account, string) (string, string)) unifiedGatewayCapabilityRule {
	return unifiedGatewayCapabilityRule{
		capability: UnifiedGatewayProviderCapability{
			ProviderIdentity:   identity,
			AccountPlatforms:   append([]string(nil), platforms...),
			AccountTypes:       append([]string(nil), accountTypes...),
			SupportedEndpoints: append([]string(nil), endpoints...),
		},
		platforms:    unifiedGatewayCapabilitySet(platforms...),
		accountTypes: unifiedGatewayCapabilitySet(accountTypes...),
		endpoints:    unifiedGatewayCapabilitySet(endpoints...),
		validate:     validate,
	}
}

func unifiedGatewayCapabilitySet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	return set
}

var unifiedGatewayCapabilityRuleOrder = []string{
	UnifiedGatewayProviderOpenAI,
	UnifiedGatewayProviderOpenAIAPIKey,
	UnifiedGatewayProviderOpenAIOAuth,
	UnifiedGatewayProviderOpenAICompatible,
	UnifiedGatewayProviderNativeAnthropicCN,
	UnifiedGatewayProviderAnthropicAPIKey,
	UnifiedGatewayProviderVolcengineArk,
	UnifiedGatewayProviderGrok,
	UnifiedGatewayProviderGrokMedia,
	UnifiedGatewayProviderGemini,
	UnifiedGatewayProviderAntigravity,
}

var unifiedGatewayCapabilityRules = map[string]unifiedGatewayCapabilityRule{
	UnifiedGatewayProviderOpenAI: unifiedGatewayCapabilityRuleFor(
		UnifiedGatewayProviderOpenAI,
		[]string{PlatformOpenAI},
		[]string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken},
		[]string{UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointResponses, UnifiedGatewayEndpointImages, UnifiedGatewayEndpointImageEdits},
		unifiedGatewayRejectArkAccount,
	),
	UnifiedGatewayProviderOpenAIAPIKey: unifiedGatewayCapabilityRuleFor(
		UnifiedGatewayProviderOpenAIAPIKey,
		[]string{PlatformOpenAI},
		[]string{AccountTypeAPIKey},
		[]string{UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointResponses, UnifiedGatewayEndpointImages, UnifiedGatewayEndpointImageEdits},
		unifiedGatewayRejectArkAccount,
	),
	UnifiedGatewayProviderOpenAIOAuth: unifiedGatewayCapabilityRuleFor(
		UnifiedGatewayProviderOpenAIOAuth,
		[]string{PlatformOpenAI},
		[]string{AccountTypeOAuth, AccountTypeSetupToken},
		[]string{UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointResponses, UnifiedGatewayEndpointImages, UnifiedGatewayEndpointImageEdits},
		nil,
	),
	UnifiedGatewayProviderOpenAICompatible: unifiedGatewayCapabilityRuleFor(
		UnifiedGatewayProviderOpenAICompatible,
		[]string{PlatformOpenAI, PlatformKimi, PlatformZhipu, PlatformDeepseek},
		[]string{AccountTypeAPIKey},
		[]string{UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointResponses, UnifiedGatewayEndpointImages, UnifiedGatewayEndpointImageEdits},
		unifiedGatewayValidateOpenAICompatibleAccount,
	),
	UnifiedGatewayProviderNativeAnthropicCN: unifiedGatewayCapabilityRuleFor(
		UnifiedGatewayProviderNativeAnthropicCN,
		[]string{PlatformKimi, PlatformZhipu, PlatformDeepseek},
		[]string{AccountTypeAPIKey, AccountTypeUpstream},
		[]string{UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointResponses},
		unifiedGatewayValidateNativeAnthropicCNAccount,
	),
	UnifiedGatewayProviderAnthropicAPIKey: unifiedGatewayCapabilityRuleFor(
		UnifiedGatewayProviderAnthropicAPIKey,
		[]string{PlatformAnthropic},
		[]string{AccountTypeAPIKey},
		[]string{UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointResponses},
		unifiedGatewayValidateAnthropicAPIKeyAccount,
	),
	UnifiedGatewayProviderVolcengineArk: unifiedGatewayCapabilityRuleFor(
		UnifiedGatewayProviderVolcengineArk,
		[]string{PlatformOpenAI},
		[]string{AccountTypeAPIKey},
		[]string{UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointResponses, UnifiedGatewayEndpointImages, UnifiedGatewayEndpointImageEdits},
		unifiedGatewayValidateVolcengineArkAccount,
	),
	UnifiedGatewayProviderGrok: unifiedGatewayCapabilityRuleFor(
		UnifiedGatewayProviderGrok,
		[]string{PlatformGrok},
		[]string{AccountTypeAPIKey, AccountTypeOAuth},
		[]string{UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointImages, UnifiedGatewayEndpointImageEdits, UnifiedGatewayEndpointVideos},
		unifiedGatewayValidateGrokAccount,
	),
	UnifiedGatewayProviderGrokMedia: unifiedGatewayCapabilityRuleFor(
		UnifiedGatewayProviderGrokMedia,
		[]string{PlatformGrok},
		[]string{AccountTypeAPIKey, AccountTypeOAuth},
		[]string{UnifiedGatewayEndpointImages, UnifiedGatewayEndpointImageEdits, UnifiedGatewayEndpointVideos},
		nil,
	),
	UnifiedGatewayProviderGemini: unifiedGatewayCapabilityRuleFor(
		UnifiedGatewayProviderGemini,
		[]string{PlatformGemini},
		[]string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeServiceAccount},
		[]string{UnifiedGatewayEndpointChatCompletions},
		nil,
	),
	UnifiedGatewayProviderAntigravity: unifiedGatewayCapabilityRuleFor(
		UnifiedGatewayProviderAntigravity,
		[]string{PlatformAntigravity},
		[]string{AccountTypeAPIKey, AccountTypeOAuth},
		[]string{UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointResponses},
		unifiedGatewayValidateAntigravityAccount,
	),
}

// Aliases preserve the provider identities already used by unified route
// fixtures while keeping the matrix itself canonical and auditable.
var unifiedGatewayProviderIdentityAliases = map[string]string{
	UnifiedGatewayProviderOpenAI:            UnifiedGatewayProviderOpenAI,
	UnifiedGatewayProviderOpenAIAPIKey:      UnifiedGatewayProviderOpenAIAPIKey,
	UnifiedGatewayProviderOpenAIOAuth:       UnifiedGatewayProviderOpenAIOAuth,
	UnifiedGatewayProviderOpenAICompatible:  UnifiedGatewayProviderOpenAICompatible,
	UnifiedGatewayProviderNativeAnthropicCN: UnifiedGatewayProviderNativeAnthropicCN,
	UnifiedGatewayProviderAnthropicAPIKey:   UnifiedGatewayProviderAnthropicAPIKey,
	UnifiedGatewayProviderVolcengineArk:     UnifiedGatewayProviderVolcengineArk,
	UnifiedGatewayProviderGrok:              UnifiedGatewayProviderGrok,
	UnifiedGatewayProviderGrokMedia:         UnifiedGatewayProviderGrokMedia,
	UnifiedGatewayProviderGemini:            UnifiedGatewayProviderGemini,
	UnifiedGatewayProviderAntigravity:       UnifiedGatewayProviderAntigravity,
	"openai_api_key":                        UnifiedGatewayProviderOpenAIAPIKey,
	"openai-apikey":                         UnifiedGatewayProviderOpenAIAPIKey,
	"openai_oauth":                          UnifiedGatewayProviderOpenAIOAuth,
	"openai-chatgpt":                        UnifiedGatewayProviderOpenAIOAuth,
	"openai-plus":                           UnifiedGatewayProviderOpenAIOAuth,
	"openai-pro":                            UnifiedGatewayProviderOpenAIOAuth,
	"openai_compatible":                     UnifiedGatewayProviderOpenAICompatible,
	"openai-compat":                         UnifiedGatewayProviderOpenAICompatible,
	"generic-openai-compatible":             UnifiedGatewayProviderOpenAICompatible,
	"native_anthropic_cn":                   UnifiedGatewayProviderNativeAnthropicCN,
	"anthropic-cn":                          UnifiedGatewayProviderNativeAnthropicCN,
	"anthropic_cn":                          UnifiedGatewayProviderNativeAnthropicCN,
	"cn-anthropic":                          UnifiedGatewayProviderNativeAnthropicCN,
	"cn_anthropic":                          UnifiedGatewayProviderNativeAnthropicCN,
	"anthropic-native":                      UnifiedGatewayProviderNativeAnthropicCN,
	"anthropic_native":                      UnifiedGatewayProviderNativeAnthropicCN,
	"anthropic_apikey":                      UnifiedGatewayProviderAnthropicAPIKey,
	"claude-api-key":                        UnifiedGatewayProviderAnthropicAPIKey,
	"generic-anthropic":                     UnifiedGatewayProviderAnthropicAPIKey,
	"anthropic":                             UnifiedGatewayProviderNativeAnthropicCN,
	"kimi":                                  UnifiedGatewayProviderOpenAICompatible,
	"zhipu":                                 UnifiedGatewayProviderOpenAICompatible,
	"deepseek":                              UnifiedGatewayProviderOpenAICompatible,
	"volcengine_ark":                        UnifiedGatewayProviderVolcengineArk,
	"volcengine":                            UnifiedGatewayProviderVolcengineArk,
	"ark":                                   UnifiedGatewayProviderVolcengineArk,
	"doubao-ark":                            UnifiedGatewayProviderVolcengineArk,
	"grok_media":                            UnifiedGatewayProviderGrokMedia,
	"grok-media-generation":                 UnifiedGatewayProviderGrokMedia,
	"xai":                                   UnifiedGatewayProviderGrok,
	"google-gemini":                         UnifiedGatewayProviderGemini,
	"google_gemini":                         UnifiedGatewayProviderGemini,
}

// UnifiedGatewayProviderCapabilityMatrix returns a defensive copy of the
// canonical matrix.  Aliases are intentionally omitted so callers can see the
// small set of adapter contracts that must be maintained.
func UnifiedGatewayProviderCapabilityMatrix() []UnifiedGatewayProviderCapability {
	result := make([]UnifiedGatewayProviderCapability, 0, len(unifiedGatewayCapabilityRuleOrder))
	for _, identity := range unifiedGatewayCapabilityRuleOrder {
		rule := unifiedGatewayCapabilityRules[identity]
		capability := rule.capability
		capability.AccountPlatforms = append([]string(nil), capability.AccountPlatforms...)
		capability.AccountTypes = append([]string(nil), capability.AccountTypes...)
		capability.SupportedEndpoints = append([]string(nil), capability.SupportedEndpoints...)
		result = append(result, capability)
	}
	return result
}

// CheckUnifiedGatewayProviderCapability checks the exact account/adapter/
// endpoint combination that the unified runtime will use.  It deliberately
// does not infer support from a platform alone: an unknown provider identity,
// protocol, account type, or endpoint is a blocker.
func CheckUnifiedGatewayProviderCapability(providerIdentity string, account *Account, endpoint string) UnifiedGatewayCapabilityDecision {
	identity := strings.TrimSpace(providerIdentity)
	canonical, known := unifiedGatewayProviderIdentityAliases[strings.ToLower(identity)]
	decision := UnifiedGatewayCapabilityDecision{
		ProviderIdentity:  identity,
		CanonicalProvider: canonical,
		Endpoint:          strings.TrimSpace(endpoint),
	}
	if !known {
		decision.Code = unifiedGatewayCapabilityUnknown
		decision.Message = fmt.Sprintf("provider identity %q is not in the unified gateway capability matrix", identity)
		return decision
	}
	rule := unifiedGatewayCapabilityRules[canonical]
	if account == nil {
		decision.Code = unifiedGatewayCapabilityAccountRequired
		decision.Message = fmt.Sprintf("provider identity %q cannot be validated because its bound account is unavailable", identity)
		return decision
	}
	decision.AccountPlatform = strings.TrimSpace(account.Platform)
	decision.AccountType = strings.TrimSpace(account.Type)
	if _, ok := rule.platforms[strings.ToLower(decision.AccountPlatform)]; !ok {
		decision.Code = unifiedGatewayCapabilityPlatformMismatch
		decision.Message = fmt.Sprintf("provider identity %q requires account platform %s, got %q", identity, formatUnifiedGatewayValues(rule.capability.AccountPlatforms), decision.AccountPlatform)
		return decision
	}
	if _, ok := rule.accountTypes[strings.ToLower(decision.AccountType)]; !ok {
		decision.Code = unifiedGatewayCapabilityTypeMismatch
		decision.Message = fmt.Sprintf("provider identity %q requires account type %s, got %q", identity, formatUnifiedGatewayValues(rule.capability.AccountTypes), decision.AccountType)
		return decision
	}
	if rule.validate != nil {
		if code, message := rule.validate(account, strings.TrimSpace(endpoint)); code != "" {
			decision.Code = code
			decision.Message = message
			return decision
		}
	}
	decision.Endpoint = strings.ToLower(strings.TrimSpace(endpoint))
	if _, ok := rule.endpoints[decision.Endpoint]; !ok {
		decision.Code = unifiedGatewayCapabilityEndpointUnsupported
		decision.Message = fmt.Sprintf("provider identity %q has no unified adapter for endpoint %q; supported endpoints are %s", identity, strings.TrimSpace(endpoint), formatUnifiedGatewayValues(rule.capability.SupportedEndpoints))
		return decision
	}
	decision.Allowed = true
	return decision
}

func unifiedGatewayRejectArkAccount(account *Account, _ string) (string, string) {
	if isVolcengineArkOpenAIAccount(account) {
		return unifiedGatewayCapabilityIdentityMismatch, fmt.Sprintf("account provider metadata %q requires provider identity %q", openAIProviderVolcengineArk, UnifiedGatewayProviderVolcengineArk)
	}
	return "", ""
}

func unifiedGatewayValidateOpenAICompatibleAccount(account *Account, endpoint string) (string, string) {
	if isVolcengineArkOpenAIAccount(account) {
		return unifiedGatewayCapabilityIdentityMismatch, fmt.Sprintf("account provider metadata %q requires provider identity %q", openAIProviderVolcengineArk, UnifiedGatewayProviderVolcengineArk)
	}
	if account.IsCNProvider() && account.GetAPIProtocol() == APIProtocolAnthropic {
		switch strings.TrimSpace(endpoint) {
		case UnifiedGatewayEndpointChatCompletions, UnifiedGatewayEndpointResponses:
			return "", ""
		default:
			return unifiedGatewayCapabilityEndpointUnsupported, "native CN Anthropic accounts support unified text endpoints only"
		}
	}
	return "", ""
}

func unifiedGatewayValidateNativeAnthropicCNAccount(account *Account, _ string) (string, string) {
	if account.GetAPIProtocol() != APIProtocolAnthropic {
		return unifiedGatewayCapabilityIdentityMismatch, fmt.Sprintf("provider identity %q requires account api_protocol=%q", UnifiedGatewayProviderNativeAnthropicCN, APIProtocolAnthropic)
	}
	return "", ""
}

func unifiedGatewayValidateAnthropicAPIKeyAccount(account *Account, _ string) (string, string) {
	if account.Platform != PlatformAnthropic || account.Type != AccountTypeAPIKey {
		return unifiedGatewayCapabilityIdentityMismatch, fmt.Sprintf("provider identity %q requires an Anthropic API-key account", UnifiedGatewayProviderAnthropicAPIKey)
	}
	return "", ""
}

func unifiedGatewayValidateVolcengineArkAccount(account *Account, _ string) (string, string) {
	if !isVolcengineArkOpenAIAccount(account) {
		return unifiedGatewayCapabilityIdentityMismatch, fmt.Sprintf("provider identity %q requires an OpenAI API-key account with extra.provider=%q", UnifiedGatewayProviderVolcengineArk, openAIProviderVolcengineArk)
	}
	return "", ""
}

func unifiedGatewayValidateGrokAccount(account *Account, endpoint string) (string, string) {
	if account.Type == AccountTypeOAuth && strings.EqualFold(strings.TrimSpace(endpoint), UnifiedGatewayEndpointChatCompletions) {
		return unifiedGatewayCapabilityEndpointUnsupported, "Grok OAuth accounts have a unified media adapter but no unified text adapter"
	}
	return "", ""
}

func unifiedGatewayValidateAntigravityAccount(account *Account, endpoint string) (string, string) {
	if account.Type == AccountTypeAPIKey && strings.EqualFold(strings.TrimSpace(endpoint), UnifiedGatewayEndpointResponses) {
		return unifiedGatewayCapabilityEndpointUnsupported, "Antigravity API-key accounts use the Gemini compatibility adapter, which supports chat_completions only"
	}
	return "", ""
}

func formatUnifiedGatewayValues(values []string) string {
	copyValues := append([]string(nil), values...)
	sort.Strings(copyValues)
	quoted := make([]string, 0, len(copyValues))
	for _, value := range copyValues {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return strings.Join(quoted, ", ")
}

func unifiedGatewayProviderIdentityKnown(providerIdentity string) bool {
	_, ok := unifiedGatewayProviderIdentityAliases[strings.ToLower(strings.TrimSpace(providerIdentity))]
	return ok
}

// unifiedGatewayCapabilityIssues translates the provider decision into admin
// field blockers.  accountsKnown is false when the admin service has no
// account reader; that state is intentionally not treated as a permissive
// validation mode.
func unifiedGatewayCapabilityIssues(document UnifiedGatewayConfig, accounts map[int64]*Account, accountsKnown bool) []UnifiedGatewayIssue {
	var issues []UnifiedGatewayIssue
	for laneIndex := range document.Lanes {
		lane := document.Lanes[laneIndex]
		for targetIndex := range lane.Targets {
			target := lane.Targets[targetIndex]
			targetPath := fmt.Sprintf("lanes[%d].targets[%d]", laneIndex, targetIndex)
			providerIdentity := strings.TrimSpace(target.ProviderIdentity)
			if providerIdentity == "" {
				continue
			}
			for bindingIndex := range target.Bindings {
				binding := target.Bindings[bindingIndex]
				bindingPath := fmt.Sprintf("%s.bindings[%d]", targetPath, bindingIndex)
				effectiveEndpoint := strings.TrimSpace(binding.Endpoint)
				endpointPath := bindingPath + ".endpoint"
				if effectiveEndpoint == "" {
					effectiveEndpoint = strings.TrimSpace(target.Endpoint)
					endpointPath = targetPath + ".endpoint"
				}
				if !validUnifiedEndpoint(effectiveEndpoint) {
					continue
				}

				if !unifiedGatewayProviderIdentityKnown(providerIdentity) {
					decision := CheckUnifiedGatewayProviderCapability(providerIdentity, nil, effectiveEndpoint)
					issues = append(issues, UnifiedGatewayIssue{Code: decision.Code, Message: decision.Message, Path: targetPath + ".provider_identity"})
					continue
				}
				accountID, err := parseOpaqueNumericID(binding.AccountID, "acct")
				if err != nil || accountID <= 0 {
					continue
				}
				account := accounts[accountID]
				decision := CheckUnifiedGatewayProviderCapability(providerIdentity, account, effectiveEndpoint)
				if decision.Allowed {
					continue
				}
				if account == nil && accountsKnown && decision.Code == unifiedGatewayCapabilityAccountRequired {
					continue
				}
				path := targetPath + ".provider_identity"
				switch decision.Code {
				case unifiedGatewayCapabilityEndpointUnsupported:
					path = endpointPath
				case unifiedGatewayCapabilityAccountRequired, unifiedGatewayCapabilityTypeMismatch:
					path = bindingPath + ".account_id"
				}
				issues = append(issues, UnifiedGatewayIssue{Code: decision.Code, Message: decision.Message, Path: path})
			}
		}
	}
	return issues
}
