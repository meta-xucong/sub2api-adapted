package service

import (
	"net/url"
	"strings"
)

const featureKeyCodexImageGenerationBridge = "codex_image_generation_bridge"

const featureKeyResponsesImageMode = "responses_image_mode"

const (
	ResponsesImageModeNative    = "native"
	ResponsesImageModeImagesAPI = "images_api"
)

const (
	featureKeyCodexImageGenerationExplicitToolPolicy = "codex_image_generation_explicit_tool_policy"

	codexImageGenerationExplicitToolPolicyAllow = "allow"
	codexImageGenerationExplicitToolPolicyStrip = "strip"
)

func boolOverridePtr(v bool) *bool {
	return &v
}

func boolOverrideFromMap(values map[string]any, keys ...string) *bool {
	if values == nil {
		return nil
	}
	for _, key := range keys {
		if v, ok := values[key].(bool); ok {
			return boolOverridePtr(v)
		}
	}
	return nil
}

func stringOverrideFromMap(values map[string]any, keys ...string) (string, bool) {
	if values == nil {
		return "", false
	}
	for _, key := range keys {
		if v, ok := values[key].(string); ok {
			return v, true
		}
	}
	return "", false
}

func normalizeCodexImageGenerationExplicitToolPolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case codexImageGenerationExplicitToolPolicyStrip, "remove", "drop":
		return codexImageGenerationExplicitToolPolicyStrip
	default:
		return codexImageGenerationExplicitToolPolicyAllow
	}
}

func platformBoolOverride(values map[string]any, key string, platform string) *bool {
	if values == nil {
		return nil
	}
	if v, ok := values[key].(bool); ok {
		return boolOverridePtr(v)
	}
	raw, ok := values[key].(map[string]any)
	if !ok {
		return nil
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return nil
	}
	if v, ok := raw[platform].(bool); ok {
		return boolOverridePtr(v)
	}
	return nil
}

// CodexImageGenerationBridgeOverride returns the channel-level override for Codex
// image_generation bridge injection. Nil means follow the global/account policy.
func (c *Channel) CodexImageGenerationBridgeOverride(platform string) *bool {
	if c == nil {
		return nil
	}
	return platformBoolOverride(c.FeaturesConfig, featureKeyCodexImageGenerationBridge, platform)
}

// CodexImageGenerationBridgeOverride returns the account-level override for Codex
// image_generation bridge injection. Nil means follow the channel/global policy.
func (a *Account) CodexImageGenerationBridgeOverride() *bool {
	if a == nil || a.Platform != PlatformOpenAI || a.Extra == nil {
		return nil
	}
	if override := boolOverrideFromMap(a.Extra, featureKeyCodexImageGenerationBridge, "codex_image_generation_bridge_enabled"); override != nil {
		return override
	}
	openaiConfig, _ := a.Extra[PlatformOpenAI].(map[string]any)
	return boolOverrideFromMap(openaiConfig, featureKeyCodexImageGenerationBridge, "codex_image_generation_bridge_enabled")
}

// CodexImageGenerationExplicitToolPolicy returns the account-level policy for
// client-provided Codex /responses image_generation tools. Unknown or unset
// values default to allow to preserve existing behavior.
func (a *Account) CodexImageGenerationExplicitToolPolicy() string {
	if a == nil || a.Platform != PlatformOpenAI || a.Extra == nil {
		return codexImageGenerationExplicitToolPolicyAllow
	}
	if policy, ok := stringOverrideFromMap(a.Extra, featureKeyCodexImageGenerationExplicitToolPolicy); ok {
		return normalizeCodexImageGenerationExplicitToolPolicy(policy)
	}
	openaiConfig, _ := a.Extra[PlatformOpenAI].(map[string]any)
	if policy, ok := stringOverrideFromMap(openaiConfig, featureKeyCodexImageGenerationExplicitToolPolicy); ok {
		return normalizeCodexImageGenerationExplicitToolPolicy(policy)
	}
	return codexImageGenerationExplicitToolPolicyAllow
}

// ResponsesImageMode declares which request protocol an OpenAI image lane
// accepts. Unknown values deliberately mean native/legacy behavior so that
// enabling the global bridge cannot rewrite an unclassified account.
func (a *Account) ResponsesImageMode() string {
	if mode, ok := a.responsesImageModeOverride(); ok {
		return normalizeResponsesImageMode(mode)
	}
	if a.isAutomaticallyImagesAPIOnly() {
		return ResponsesImageModeImagesAPI
	}
	return ResponsesImageModeNative
}

func (a *Account) responsesImageModeOverride() (string, bool) {
	if a == nil || a.Platform != PlatformOpenAI || a.Extra == nil {
		return "", false
	}
	if mode, ok := stringOverrideFromMap(a.Extra, featureKeyResponsesImageMode); ok {
		return mode, true
	}
	openaiConfig, _ := a.Extra[PlatformOpenAI].(map[string]any)
	if mode, ok := stringOverrideFromMap(openaiConfig, featureKeyResponsesImageMode); ok {
		return mode, true
	}
	return "", false
}

// isAutomaticallyImagesAPIOnly identifies the account shape produced by the
// normal admin form for third-party image-only lines. The explicit metadata
// remains available for exceptional providers, but new image lines do not
// need an Extra JSON edit merely to use the Responses bridge.
func (a *Account) isAutomaticallyImagesAPIOnly() bool {
	if a == nil || a.Platform != PlatformOpenAI {
		return false
	}
	if a.Type != AccountTypeAPIKey && a.Type != AccountTypeUpstream {
		return false
	}
	baseURL := strings.TrimSpace(a.GetCredential("base_url"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(a.GetOpenAIBaseURL())
	}
	if baseURL == "" || isOfficialOpenAIBaseURL(baseURL) {
		return false
	}
	mapping := a.GetModelMapping()
	if len(mapping) == 0 {
		return false
	}
	for requestedModel := range mapping {
		if !isOpenAIImageGenerationModel(requestedModel) {
			return false
		}
	}
	return true
}

func isOfficialOpenAIBaseURL(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	return host == "api.openai.com"
}

func normalizeResponsesImageMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), ResponsesImageModeImagesAPI) {
		return ResponsesImageModeImagesAPI
	}
	return ResponsesImageModeNative
}

func (a *Account) UsesResponsesImageBridge() bool {
	return a != nil && a.ResponsesImageMode() == ResponsesImageModeImagesAPI
}
