package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

const ApprovalPlanSchemaVersion = 3
const ApprovalPlanTTL = 15 * time.Minute

type TargetBinding struct {
	Origin        string `json:"origin"`
	BuildID       string `json:"build_id"`
	APIContractID string `json:"api_contract_id"`
}

// ApprovalPlan is the immutable, human-confirmed envelope applied later. The
// file's raw bytes are hashed separately by the CLI and are never reserialized
// before confirmation.
type ApprovalPlan struct {
	SchemaVersion          int           `json:"schema_version"`
	CreatedAt              time.Time     `json:"created_at"`
	ExpiresAt              time.Time     `json:"expires_at"`
	Target                 TargetBinding `json:"target"`
	AccountScope           string        `json:"account_scope"`
	AccountInventoryCount  int           `json:"account_inventory_count,omitempty"`
	AccountInventorySHA256 string        `json:"account_inventory_sha256,omitempty"`
	RulesSHA256            string        `json:"rules_sha256"`
	StateSHA256            string        `json:"state_sha256"`
	Accounts               []Plan        `json:"accounts"`
}

func (p ApprovalPlan) ValidateShape(now time.Time) error {
	if p.SchemaVersion != ApprovalPlanSchemaVersion || p.CreatedAt.IsZero() || p.ExpiresAt.IsZero() || len(p.Accounts) == 0 {
		return errors.New("approval plan is missing required fields")
	}
	if p.CreatedAt.After(now) || !p.ExpiresAt.After(now) || p.ExpiresAt.Sub(p.CreatedAt) > ApprovalPlanTTL || p.ExpiresAt.Before(p.CreatedAt) {
		return errors.New("approval plan is outside its 15-minute validity window")
	}
	if strings.TrimSpace(p.Target.Origin) == "" || strings.TrimSpace(p.Target.BuildID) == "" || (p.Target.APIContractID != "admin-catalog-v1" && p.Target.APIContractID != "admin-catalog-all-v1") || !validSHA256(p.RulesSHA256) || !validSHA256(p.StateSHA256) {
		return errors.New("approval plan target or hash binding is invalid")
	}
	if p.AccountScope != "allowlist" && p.AccountScope != "all" {
		return errors.New("approval plan account scope is invalid")
	}
	if p.AccountScope == "all" {
		if p.AccountInventoryCount <= 0 || !validSHA256(p.AccountInventorySHA256) {
			return errors.New("all-account plan is missing its inventory snapshot")
		}
	} else if p.AccountInventoryCount != 0 || p.AccountInventorySHA256 != "" {
		return errors.New("allowlist plan unexpectedly contains an all-account inventory")
	}
	seen := map[int64]bool{}
	for _, account := range p.Accounts {
		if account.AccountID <= 0 || seen[account.AccountID] || account.Before == nil || account.After == nil || account.AbsenceCountsBefore == nil || account.AbsenceCountsAfter == nil || !validAbsenceCounts(account.AbsenceCountsBefore) || !validAbsenceCounts(account.AbsenceCountsAfter) || account.Conflicts == nil || account.CatalogObservedAt.IsZero() || !validSHA256(account.DirectoryHash) || !validSHA256(account.SourceFingerprint) || (account.IdentityCatalogPolicy != "" && account.IdentityCatalogPolicy != "replace_identity_from_live_list") {
			return errors.New("approval plan contains an invalid account snapshot")
		}
		seen[account.AccountID] = true
		if account.Source.AccountID != 0 && account.Source.AccountID != account.AccountID || SourceFingerprint(account.Source) != account.SourceFingerprint {
			return errors.New("approval plan source identity does not match its fingerprint")
		}
		directoryHash, err := DirectoryDigest(account.CatalogModels, account.LiveListAvailable, account.CatalogWarningCodes)
		if err != nil || directoryHash != account.DirectoryHash {
			return errors.New("approval plan catalog snapshot hash does not match its contents")
		}
		if _, err := MappingFingerprint(account.Before); err != nil {
			return errors.New("approval plan before mapping is invalid")
		}
		if _, err := MappingFingerprint(account.After); err != nil {
			return errors.New("approval plan after mapping is invalid")
		}
	}
	return nil
}

func AccountIDsDigest(ids []int64) (string, error) {
	ordered := append([]int64(nil), ids...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for i, id := range ordered {
		if id <= 0 || (i > 0 && ordered[i-1] == id) {
			return "", errors.New("account inventory contains an invalid or duplicate ID")
		}
	}
	body, err := json.Marshal(ordered)
	if err != nil {
		return "", errors.New("account inventory digest could not be computed")
	}
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:]), nil
}

func validAbsenceCounts(counts map[string]int) bool {
	for key, count := range counts {
		if strings.TrimSpace(key) == "" || count < 0 {
			return false
		}
	}
	return true
}

func DirectoryDigest(models []string, live bool, warningCodes []string) (string, error) {
	modelCopy := append([]string(nil), models...)
	warningCopy := append([]string(nil), warningCodes...)
	sort.Strings(modelCopy)
	sort.Strings(warningCopy)
	for i, model := range modelCopy {
		if !validID(model) || (i > 0 && modelCopy[i-1] == model) {
			return "", errors.New("catalog contains invalid or duplicate model IDs")
		}
	}
	for i, warning := range warningCopy {
		if strings.TrimSpace(warning) == "" || (i > 0 && warningCopy[i-1] == warning) {
			return "", errors.New("catalog warning codes are invalid or duplicated")
		}
	}
	body, err := json.Marshal(struct {
		Models   []string `json:"models"`
		Live     bool     `json:"live_list_available"`
		Warnings []string `json:"warning_codes"`
	}{Models: modelCopy, Live: live, Warnings: warningCopy})
	if err != nil {
		return "", errors.New("catalog snapshot hash could not be computed")
	}
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:]), nil
}

func validSHA256(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, c := range v {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
