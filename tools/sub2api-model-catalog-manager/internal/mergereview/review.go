package mergereview

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/state"
)

const (
	SchemaVersion   = 1
	MaxInputBytes   = 8 << 20
	MaxAccounts     = 5000
	MaxModels       = 20000
	MaxMappings     = 20000
	MaxOutputBytes  = 8 << 20
	dateShape8      = "20060102"
	dateShapeHyphen = "2006-01-02"
)

var (
	sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	dateSuffix    = regexp.MustCompile(`^(.+)-([0-9]{8}|[0-9]{4}-[0-9]{2}-[0-9]{2})$`)
)

type InputReport struct {
	RunID                  string             `json:"run_id"`
	CreatedAt              time.Time          `json:"created_at"`
	Mode                   string             `json:"mode"`
	AccountScope           string             `json:"account_scope"`
	AccountScanStatus      string             `json:"account_scan_status"`
	AccountInventoryCount  int                `json:"account_inventory_count"`
	AccountInventorySHA256 string             `json:"account_inventory_sha256"`
	TargetFingerprint      string             `json:"target_build_fingerprint"`
	Accounts               []InspectAccount   `json:"accounts"`
	Discovery              []DiscoveryAccount `json:"discovery"`
}

type InspectAccount struct {
	ID                int64     `json:"account_id"`
	Platform          string    `json:"platform"`
	AccountType       string    `json:"account_type"`
	APIProtocol       string    `json:"api_protocol"`
	BaseURL           string    `json:"base_url"`
	SourceFingerprint string    `json:"source_fingerprint"`
	MappingSHA256     string    `json:"mapping_sha256"`
	MappingStatus     string    `json:"mapping_status"`
	CredentialShadow  bool      `json:"credential_shadow"`
	Mappings          []Mapping `json:"mappings"`
}

type Mapping struct {
	PublicID   string `json:"public_id"`
	UpstreamID string `json:"upstream_id"`
}

type DiscoveryAccount struct {
	ID                int64           `json:"account_id"`
	SourceFingerprint string          `json:"source_fingerprint"`
	Status            string          `json:"status"`
	LiveListAvailable bool            `json:"live_list_available"`
	Models            []string        `json:"models"`
	Changes           []MappingChange `json:"proposed_changes"`
	Conflicts         []string        `json:"conflicts"`
}

type MappingChange struct {
	Action   string `json:"action"`
	PublicID string `json:"public_id"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after,omitempty"`
	RuleKind string `json:"rule_kind"`
}

type Report struct {
	SchemaVersion          int             `json:"schema_version"`
	ReviewID               string          `json:"review_id"`
	CreatedAt              time.Time       `json:"created_at"`
	InspectRunID           string          `json:"inspect_run_id"`
	DiscoverRunID          string          `json:"discover_run_id"`
	InspectCreatedAt       time.Time       `json:"inspect_created_at"`
	DiscoverCreatedAt      time.Time       `json:"discover_created_at"`
	TargetFingerprint      string          `json:"target_build_fingerprint"`
	AccountCount           int             `json:"account_count"`
	AccountInventorySHA256 string          `json:"account_inventory_sha256"`
	RulesSHA256            string          `json:"rules_sha256"`
	RuleCounts             RuleCounts      `json:"approved_rule_counts"`
	Summary                Summary         `json:"summary"`
	Accounts               []AccountReview `json:"accounts"`
	Warnings               []string        `json:"warnings"`
}

type RuleCounts struct {
	Aliases         int `json:"aliases"`
	VersionFamilies int `json:"version_families"`
	RetiredModels   int `json:"retired_models"`
}

type Summary struct {
	AccountsWithLiveCatalog         int `json:"accounts_with_live_catalog"`
	AccountsWithoutMergeRule        int `json:"accounts_with_date_candidates_but_no_matching_family_rule"`
	DateFamilyCandidates            int `json:"date_family_candidates"`
	CandidatesWithNoRule            int `json:"candidates_with_no_family_rule"`
	CandidatesWithPartialRule       int `json:"candidates_with_partial_family_rule"`
	CandidatesWithCompleteRule      int `json:"candidates_with_complete_family_rule"`
	CandidatesWithUnmanagedMappings int `json:"candidates_with_unmanaged_or_drifted_hide_targets"`
}

type AccountReview struct {
	AccountID           int64             `json:"account_id"`
	SourceFingerprint   string            `json:"source_fingerprint"`
	DiscoveryStatus     string            `json:"discovery_status"`
	LiveModelCount      int               `json:"live_model_count"`
	MatchingAliasRules  int               `json:"source_scoped_alias_rules"`
	MatchingFamilyRules int               `json:"source_scoped_version_family_rules"`
	DateFamilies        []FamilyCandidate `json:"date_family_candidates"`
	ExcludedReason      string            `json:"excluded_reason,omitempty"`
}

type FamilyCandidate struct {
	CandidateID            string             `json:"candidate_id"`
	PublicIDCandidate      string             `json:"public_id_candidate"`
	Members                []Member           `json:"members"`
	Equivalence            string             `json:"equivalence"`
	Action                 string             `json:"action"`
	DecisionRequired       bool               `json:"decision_required"`
	FamilyRuleCoverage     string             `json:"family_rule_coverage"`
	MatchingFamilyRules    int                `json:"matching_family_rules"`
	DiscoverFamilyChanges  int                `json:"discover_family_changes"`
	DiscoverHasConflicts   bool               `json:"discover_has_conflicts"`
	UnmanagedHideTargets   int                `json:"unmanaged_or_drifted_hide_targets"`
	NonIdentityHideTargets int                `json:"non_identity_hide_targets"`
	NoMergeReason          string             `json:"no_merge_reason"`
	RelatedMappings        []MappingOwnership `json:"related_mappings"`
}

type Member struct {
	UpstreamID string `json:"upstream_id"`
	DateShaped bool   `json:"date_shaped"`
}

type MappingOwnership struct {
	PublicID   string `json:"public_id"`
	UpstreamID string `json:"upstream_id"`
	Ownership  string `json:"ownership"`
	Identity   bool   `json:"identity"`
}

// Build creates a report-only analysis. It never alters rules/state or emits
// an executable mapping plan.
func Build(inspect, discover InputReport, rules catalog.Rules, rulesHash string, local state.State, now time.Time) (Report, error) {
	var out Report
	if err := rules.Validate(); err != nil {
		return out, errors.New("merge review rules are invalid")
	}
	if local.Version != state.CurrentVersion || local.Accounts == nil {
		return out, errors.New("merge review state is invalid")
	}
	if !sha256Pattern.MatchString(rulesHash) {
		return out, errors.New("merge review rules hash is invalid")
	}
	inspectIDs, err := validateReport(inspect, "inspect")
	if err != nil {
		return out, err
	}
	discoverIDs, err := validateReport(discover, "discover")
	if err != nil {
		return out, err
	}
	if inspect.AccountInventoryCount != discover.AccountInventoryCount || inspect.AccountInventorySHA256 != discover.AccountInventorySHA256 || inspect.TargetFingerprint != discover.TargetFingerprint || len(inspectIDs) != len(discoverIDs) {
		return out, errors.New("inspect and discover reports do not describe the same complete account inventory and target")
	}
	for id := range inspectIDs {
		if !discoverIDs[id] {
			return out, errors.New("inspect and discover account ID sets do not match")
		}
	}
	inspectByID := make(map[int64]InspectAccount, len(inspect.Accounts))
	for _, account := range inspect.Accounts {
		inspectByID[account.ID] = account
	}
	discoverByID := make(map[int64]DiscoveryAccount, len(discover.Discovery))
	for _, account := range discover.Discovery {
		discoverByID[account.ID] = account
	}

	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return out, errors.New("merge review ID could not be created")
	}
	out = Report{
		SchemaVersion:          SchemaVersion,
		ReviewID:               hex.EncodeToString(id[:]),
		CreatedAt:              now.UTC(),
		InspectRunID:           inspect.RunID,
		DiscoverRunID:          discover.RunID,
		InspectCreatedAt:       inspect.CreatedAt,
		DiscoverCreatedAt:      discover.CreatedAt,
		TargetFingerprint:      inspect.TargetFingerprint,
		AccountCount:           inspect.AccountInventoryCount,
		AccountInventorySHA256: inspect.AccountInventorySHA256,
		RulesSHA256:            rulesHash,
		RuleCounts:             RuleCounts{Aliases: len(rules.Aliases), VersionFamilies: len(rules.VersionFamilies), RetiredModels: len(rules.RetiredModels)},
		Accounts:               make([]AccountReview, 0, len(inspectByID)),
		Warnings:               []string{"date-shaped names are candidates only; this report does not prove model equivalence or choose a preferred upstream ID", "candidate actions are none; output is not an apply plan and does not change Sub2API, rules, or local ownership state", "inspect and discover are separate non-atomic snapshots; review mapping drift before any later approved apply"},
	}
	ids := sortedAccountIDs(inspectByID)
	for _, accountID := range ids {
		account := inspectByID[accountID]
		discovery := discoverByID[accountID]
		source, err := catalog.SourceForAccount(account.Platform, account.AccountType, account.APIProtocol, account.BaseURL, account.ID)
		if err != nil || catalog.SourceFingerprint(source) != account.SourceFingerprint {
			return Report{}, fmt.Errorf("account %d has an invalid or inconsistent source fingerprint", accountID)
		}
		if discovery.SourceFingerprint != "" && discovery.SourceFingerprint != account.SourceFingerprint {
			return Report{}, fmt.Errorf("account %d source changed between reports", accountID)
		}
		if discovery.Status == "candidate_reported" && discovery.SourceFingerprint != account.SourceFingerprint {
			return Report{}, fmt.Errorf("account %d candidate discovery is missing its source fingerprint", accountID)
		}
		if err := validateMappingSnapshot(account); err != nil {
			return Report{}, fmt.Errorf("account %d mapping snapshot is invalid", accountID)
		}
		row := AccountReview{
			AccountID:         accountID,
			SourceFingerprint: account.SourceFingerprint,
			DiscoveryStatus:   safeStatus(discovery.Status),
			DateFamilies:      []FamilyCandidate{},
		}
		row.MatchingAliasRules = matchingAliasRules(rules, source)
		row.MatchingFamilyRules = matchingFamilyRules(rules, source)
		if discovery.Status != "candidate_reported" || !discovery.LiveListAvailable || len(discovery.Models) == 0 || account.CredentialShadow {
			row.ExcludedReason = "no_eligible_live_catalog"
			out.Accounts = append(out.Accounts, row)
			continue
		}
		if err := validateModelList(discovery.Models); err != nil {
			return Report{}, fmt.Errorf("account %d live model list is invalid", accountID)
		}
		row.LiveModelCount = len(uniqueSorted(discovery.Models))
		out.Summary.AccountsWithLiveCatalog++
		candidates := findDateFamilies(accountID, account.SourceFingerprint, source, discovery.Models, account.Mappings, discovery.Changes, discovery.Conflicts, rules, local)
		for i := range candidates {
			candidate := &candidates[i]
			switch candidate.FamilyRuleCoverage {
			case "none":
				out.Summary.CandidatesWithNoRule++
			case "partial":
				out.Summary.CandidatesWithPartialRule++
			case "complete":
				out.Summary.CandidatesWithCompleteRule++
			}
			if candidate.UnmanagedHideTargets > 0 {
				out.Summary.CandidatesWithUnmanagedMappings++
			}
		}
		if len(candidates) > 0 && row.MatchingFamilyRules == 0 {
			out.Summary.AccountsWithoutMergeRule++
		}
		row.DateFamilies = candidates
		out.Summary.DateFamilyCandidates += len(candidates)
		out.Accounts = append(out.Accounts, row)
	}
	return out, nil
}

func ReadReport(path string) (InputReport, error) {
	var input InputReport
	f, err := os.Open(path)
	if err != nil {
		return input, errors.New("input report is unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxInputBytes {
		return input, errors.New("input report must be a small regular JSON file")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxInputBytes+1))
	if err != nil || len(data) == 0 || len(data) > MaxInputBytes {
		zero(data)
		return input, errors.New("input report exceeds size limit or could not be read")
	}
	defer zero(data)
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&input); err != nil {
		return input, errors.New("input report is invalid")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return input, errors.New("input report contains trailing data")
	}
	return input, nil
}

func Write(outDir string, value Report) (string, error) {
	if outDir == "" || value.SchemaVersion != SchemaVersion || value.ReviewID == "" || !sha256Pattern.MatchString(value.RulesSHA256) {
		return "", errors.New("merge review report is invalid")
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil || len(data) == 0 || len(data) > MaxOutputBytes {
		return "", errors.New("merge review report serialization failed")
	}
	defer zero(data)
	if err := os.MkdirAll(outDir, 0700); err != nil {
		return "", errors.New("merge review output directory could not be created")
	}
	name := "merge-review-" + value.CreatedAt.UTC().Format("20060102T150405Z") + "-" + value.ReviewID + ".json"
	path := filepath.Join(outDir, name)
	tmp, err := os.CreateTemp(outDir, ".merge-review-*.tmp")
	if err != nil {
		return "", errors.New("temporary merge review file could not be created")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return "", errors.New("merge review permissions could not be restricted")
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", errors.New("merge review write failed")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", errors.New("merge review flush failed")
	}
	if err := tmp.Close(); err != nil {
		return "", errors.New("merge review close failed")
	}
	if _, err := os.Stat(path); err == nil {
		return "", errors.New("merge review output name collision")
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", errors.New("merge review atomic publish failed")
	}
	return path, nil
}

func validateReport(value InputReport, mode string) (map[int64]bool, error) {
	if value.Mode != mode || value.AccountScope != "all" || value.AccountScanStatus != "complete" || value.AccountInventoryCount <= 0 || value.AccountInventoryCount > MaxAccounts || !sha256Pattern.MatchString(value.AccountInventorySHA256) || strings.TrimSpace(value.TargetFingerprint) == "" || value.RunID == "" || value.CreatedAt.IsZero() {
		return nil, fmt.Errorf("%s report must be a complete all-account snapshot", mode)
	}
	rows := make(map[int64]bool, len(value.Accounts))
	ids := make([]int64, 0, len(value.Accounts))
	if mode == "inspect" {
		for _, row := range value.Accounts {
			if row.ID <= 0 || rows[row.ID] {
				return nil, errors.New("inspect report has invalid or duplicate account IDs")
			}
			rows[row.ID] = true
			ids = append(ids, row.ID)
		}
	} else {
		// Discover rows are validated against the corresponding inspect rows in Build.
		for _, row := range value.Discovery {
			if row.ID <= 0 {
				return nil, errors.New("discover report has an invalid account ID")
			}
			if rows[row.ID] {
				return nil, errors.New("discover report has duplicate account IDs")
			}
			rows[row.ID] = true
			ids = append(ids, row.ID)
		}
	}
	if len(rows) != value.AccountInventoryCount {
		return nil, fmt.Errorf("%s report account rows do not match its inventory count", mode)
	}
	digest, err := catalog.AccountIDsDigest(ids)
	if err != nil || digest != value.AccountInventorySHA256 {
		return nil, fmt.Errorf("%s report account inventory digest is inconsistent", mode)
	}
	return rows, nil
}

func findDateFamilies(accountID int64, fingerprint string, source catalog.Source, models []string, mappings []Mapping, changes []MappingChange, conflicts []string, rules catalog.Rules, local state.State) []FamilyCandidate {
	modelSet := make(map[string]bool, len(models))
	for _, model := range models {
		modelSet[model] = true
	}
	byPrefix := map[string]map[string]bool{}
	for _, model := range models {
		match := dateSuffix.FindStringSubmatch(model)
		if len(match) != 3 || !validDate(match[2]) || !modelSet[match[1]] {
			continue
		}
		if byPrefix[match[1]] == nil {
			byPrefix[match[1]] = map[string]bool{match[1]: false}
		}
		byPrefix[match[1]][model] = true
	}
	actualFamilies := matchingFamiliesForSource(rules, source)
	result := make([]FamilyCandidate, 0, len(byPrefix))
	for prefix, members := range byPrefix {
		memberIDs := make([]string, 0, len(members))
		for id := range members {
			memberIDs = append(memberIDs, id)
		}
		sort.Strings(memberIDs)
		candidate := FamilyCandidate{
			PublicIDCandidate:  prefix,
			Equivalence:        "unproven",
			Action:             "none",
			DecisionRequired:   true,
			FamilyRuleCoverage: "none",
			NoMergeReason:      "no_reviewed_family_rule",
			Members:            make([]Member, 0, len(memberIDs)),
			RelatedMappings:    []MappingOwnership{},
		}
		for _, modelID := range memberIDs {
			candidate.Members = append(candidate.Members, Member{UpstreamID: modelID, DateShaped: modelID != prefix})
		}
		matchingRuleCount := 0
		covered := map[string]bool{}
		preferredPresent := false
		matchedFamilies := make([]catalog.VersionFamily, 0)
		for _, family := range actualFamilies {
			if family.PublicID != prefix {
				continue
			}
			matchingRuleCount++
			matchedFamilies = append(matchedFamilies, family)
			for _, member := range family.Members {
				covered[member.UpstreamID] = true
				if member.UpstreamID == family.PreferredUpstreamID && modelSet[member.UpstreamID] {
					preferredPresent = true
				}
			}
		}
		candidate.MatchingFamilyRules = matchingRuleCount
		if matchingRuleCount > 1 {
			candidate.FamilyRuleCoverage = "ambiguous"
			candidate.NoMergeReason = "multiple_matching_family_rules"
		} else if matchingRuleCount == 1 {
			allCovered := true
			for _, modelID := range memberIDs {
				if !covered[modelID] {
					allCovered = false
					break
				}
			}
			if allCovered {
				candidate.FamilyRuleCoverage = "complete"
				candidate.NoMergeReason = "preferred_upstream_id_not_in_current_live_list"
				if preferredPresent {
					candidate.NoMergeReason = "family_rule_matches; inspect discover proposal and apply result"
				}
			} else {
				candidate.FamilyRuleCoverage = "partial"
				candidate.NoMergeReason = "family_rule_does_not_cover_all_observed_members"
			}
		}
		candidate.DiscoverFamilyChanges = len(matchingChangesForCandidate(changes, prefix, memberIDs))
		candidate.DiscoverHasConflicts = len(conflicts) > 0
		if candidate.FamilyRuleCoverage == "complete" && candidate.DiscoverHasConflicts {
			candidate.NoMergeReason = "discover_report_contains_conflicts; inspect exact account conflict before apply"
		} else if candidate.FamilyRuleCoverage == "complete" && candidate.DiscoverFamilyChanges > 0 {
			candidate.NoMergeReason = "family_change_was_proposed_in_discover; a reviewed apply is still required"
		} else if candidate.FamilyRuleCoverage == "complete" && preferredPresent {
			candidate.NoMergeReason = "rule_matches_but_discover_report_contains_no_family_delta"
		}
		candidateID := makeCandidateID(accountID, fingerprint, prefix, memberIDs)
		candidate.CandidateID = candidateID
		relatedIDs := make(map[string]bool, len(memberIDs)+1)
		relatedIDs[prefix] = true
		for _, modelID := range memberIDs {
			relatedIDs[modelID] = true
		}
		for _, family := range matchedFamilies {
			for _, member := range family.Members {
				relatedIDs[member.UpstreamID] = true
			}
		}
		for _, mapping := range mappings {
			if !relatedIDs[mapping.PublicID] && !relatedIDs[mapping.UpstreamID] {
				continue
			}
			ownership := "unmanaged"
			if account, ok := local.Accounts[strconv.FormatInt(accountID, 10)]; ok {
				if owner, found := account.ManagedMappings[mapping.PublicID]; found {
					if owner.SourceFingerprint == fingerprint && owner.LastAppliedValue == mapping.UpstreamID {
						ownership = "managed"
					} else {
						ownership = "drifted"
					}
				}
			}
			candidate.RelatedMappings = append(candidate.RelatedMappings, MappingOwnership{PublicID: mapping.PublicID, UpstreamID: mapping.UpstreamID, Ownership: ownership, Identity: mapping.PublicID == mapping.UpstreamID})
		}
		sort.Slice(candidate.RelatedMappings, func(i, j int) bool {
			if candidate.RelatedMappings[i].PublicID != candidate.RelatedMappings[j].PublicID {
				return candidate.RelatedMappings[i].PublicID < candidate.RelatedMappings[j].PublicID
			}
			return candidate.RelatedMappings[i].UpstreamID < candidate.RelatedMappings[j].UpstreamID
		})
		if candidate.FamilyRuleCoverage == "complete" {
			for _, family := range matchedFamilies {
				for _, familyMember := range family.Members {
					if familyMember.ExactIDAction != "hide" {
						continue
					}
					for _, related := range candidate.RelatedMappings {
						if related.PublicID != familyMember.UpstreamID {
							continue
						}
						if !related.Identity {
							candidate.NonIdentityHideTargets++
						} else if related.Ownership == "unmanaged" || related.Ownership == "drifted" {
							candidate.UnmanagedHideTargets++
						}
					}
				}
			}
			if candidate.NonIdentityHideTargets > 0 {
				candidate.NoMergeReason = "hide_target_is_not_an_exact_identity_mapping"
			} else if candidate.UnmanagedHideTargets > 0 {
				candidate.NoMergeReason = "hide_or_rewrite_requires_explicit_adoption_of_unmanaged_mapping_keys"
			}
		}
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PublicIDCandidate < result[j].PublicIDCandidate })
	return result
}

func validateMappingSnapshot(account InspectAccount) error {
	if account.ID <= 0 || account.MappingStatus == "" || len(account.Mappings) > MaxMappings {
		return errors.New("mapping snapshot fields are invalid")
	}
	if account.MappingStatus == "no_explicit_mapping" {
		if len(account.Mappings) == 0 && account.MappingSHA256 == "" {
			return nil
		}
		return errors.New("no-explicit-mapping status conflicts with mapping rows")
	}
	if !sha256Pattern.MatchString(account.MappingSHA256) {
		return errors.New("mapping snapshot hash is invalid")
	}
	mapping := make(map[string]string, len(account.Mappings))
	for _, edge := range account.Mappings {
		if edge.PublicID == "" || edge.UpstreamID == "" || mapping[edge.PublicID] != "" {
			return errors.New("mapping snapshot has an invalid or duplicate key")
		}
		mapping[edge.PublicID] = edge.UpstreamID
	}
	hash, err := catalog.MappingHash(mapping)
	if err != nil || hash != account.MappingSHA256 {
		return errors.New("mapping snapshot hash does not match its rows")
	}
	return nil
}

func validateModelList(models []string) error {
	if len(models) > MaxModels {
		return errors.New("live model list exceeds size limit")
	}
	for _, model := range models {
		if model == "" || len(model) > 512 || strings.TrimSpace(model) != model || strings.ContainsAny(model, "\x00\r\n") {
			return errors.New("live model list contains an invalid model ID")
		}
	}
	return nil
}

func matchingAliasRules(rules catalog.Rules, source catalog.Source) int {
	count := 0
	for _, rule := range rules.Aliases {
		if catalog.SourceMatches(rule.Source, source) {
			count++
		}
	}
	return count
}

func matchingFamilyRules(rules catalog.Rules, source catalog.Source) int {
	return len(matchingFamiliesForSource(rules, source))
}

func matchingFamiliesForSource(rules catalog.Rules, source catalog.Source) []catalog.VersionFamily {
	result := make([]catalog.VersionFamily, 0)
	for _, rule := range rules.VersionFamilies {
		if catalog.SourceMatches(rule.Source, source) {
			result = append(result, rule)
		}
	}
	return result
}

func validDate(value string) bool {
	layout := dateShape8
	if strings.Contains(value, "-") {
		layout = dateShapeHyphen
	}
	parsed, err := time.Parse(layout, value)
	return err == nil && parsed.Format(layout) == value
}

func makeCandidateID(accountID int64, fingerprint, prefix string, members []string) string {
	body, _ := json.Marshal(struct {
		AccountID int64    `json:"account_id"`
		Source    string   `json:"source_fingerprint"`
		Prefix    string   `json:"prefix"`
		Members   []string `json:"members"`
	}{accountID, fingerprint, prefix, members})
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:16])
}

func matchingChangesForCandidate(changes []MappingChange, prefix string, members []string) []MappingChange {
	result := make([]MappingChange, 0)
	for _, change := range changes {
		if change.RuleKind == "version_family" && (change.PublicID == prefix || contains(members, change.PublicID)) {
			result = append(result, change)
		}
	}
	return result
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedAccountIDs(accounts map[int64]InspectAccount) []int64 {
	ids := make([]int64, 0, len(accounts))
	for id := range accounts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func safeStatus(value string) string {
	if len(value) > 80 || strings.ContainsAny(value, "\x00\r\n") {
		return "unknown"
	}
	return value
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
