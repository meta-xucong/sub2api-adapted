package nativeharness

import "sort"

const StandardChatPath = "/v1/chat/completions"
const StandardModelsPath = "/v1/models"

const ModelsPath = "/v1/native-harness/models"

// ModelList is a configuration-only view. It never proves native readiness.
type ModelList struct {
	Object        string         `json:"object"`
	Data          []ModelInfo    `json:"data"`
	NativeHarness ModelPreflight `json:"native_harness"`
}

type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

type ModelPreflight struct {
	AcceptingNew         bool `json:"accepting_new"`
	LiveReadinessChecked bool `json:"live_readiness_checked"`
}

// ConfiguredModels does no store, credential resolver or network IO. The caller
// must authenticate the key and apply its group model allowlist before returning.
func (s *Service) ConfiguredModels(userID, keyID int64, canExecute bool) (*ModelList, error) {
	cfg := s.Config
	if !cfg.Enabled {
		return nil, fail("NATIVE_DISABLED", 503)
	}
	if userID <= 0 || keyID <= 0 || userID != cfg.AllowedUserID || keyID != cfg.AllowedAPIKeyID {
		return nil, fail("NATIVE_FORBIDDEN", 403)
	}
	result := &ModelList{Object: "list", Data: []ModelInfo{}, NativeHarness: ModelPreflight{AcceptingNew: cfg.AcceptingNew && canExecute, LiveReadinessChecked: false}}
	if !result.NativeHarness.AcceptingNew {
		return result, nil
	}
	aliases := make([]string, 0, len(cfg.Routes))
	for alias, route := range cfg.Routes {
		if ValidRoute(alias, route) {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		result.Data = append(result.Data, ModelInfo{ID: alias, Object: "model", OwnedBy: "native-harness"})
	}
	return result, nil
}
