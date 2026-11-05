package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"sub2api-model-catalog-manager/internal/state"
)

type Completeness string

const (
	CompletenessUnknown  Completeness = "unknown"
	CompletenessComplete Completeness = "complete"
)

type Input struct {
	AccountID               int64
	Source                  Source
	SourceIdentityAddPolicy string
	CurrentMapping          map[string]string
	ManagedMappings         map[string]state.ManagedMapping
	LiveModels              []string
	LiveListAvailable       bool
	Completeness            Completeness
	CompletenessProfileID   string
	DirectoryHash           string
}

type Change struct {
	Action   string `json:"action"`
	PublicID string `json:"public_id"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after,omitempty"`
	RuleKind string `json:"rule_kind"`
	Evidence string `json:"evidence,omitempty"`
	Reason   string `json:"reason"`
}

type Plan struct {
	AccountID               int64             `json:"account_id"`
	Source                  Source            `json:"source"`
	SourceFingerprint       string            `json:"source_fingerprint"`
	BeforeHash              string            `json:"before_mapping_hash"`
	DirectoryHash           string            `json:"directory_hash,omitempty"`
	CatalogObservedAt       time.Time         `json:"catalog_observed_at"`
	CompletenessProfileID   string            `json:"completeness_profile_id,omitempty"`
	SourceIdentityAddPolicy string            `json:"source_identity_add_policy"`
	CatalogModels           []string          `json:"catalog_models"`
	CatalogWarningCodes     []string          `json:"catalog_warning_codes"`
	ApplyEligible           bool              `json:"apply_eligible"`
	DiscoveryReason         string            `json:"discovery_reason,omitempty"`
	RulesHash               string            `json:"rules_hash"`
	Completeness            Completeness      `json:"catalog_completeness"`
	LiveListAvailable       bool              `json:"live_list_available"`
	Before                  map[string]string `json:"before_mapping"`
	After                   map[string]string `json:"after_mapping"`
	Changes                 []Change          `json:"changes"`
	Conflicts               []string          `json:"conflicts"`
	UnreviewedModels        []string          `json:"unreviewed_models"`
	CreatedAt               time.Time         `json:"created_at"`
	AbsenceCountsBefore     map[string]int    `json:"absence_counts_before"`
	AbsenceCountsAfter      map[string]int    `json:"absence_counts_after"`
}

func Normalize(rules Rules, input Input, prior state.Account) (Plan, error) {
	if err := rules.Validate(); err != nil {
		return Plan{}, err
	}
	if input.AccountID <= 0 || input.CurrentMapping == nil {
		return Plan{}, errors.New("invalid normalization input")
	}
	if input.Completeness != CompletenessUnknown && input.Completeness != CompletenessComplete {
		return Plan{}, errors.New("unknown catalog completeness value")
	}
	if input.SourceIdentityAddPolicy == "" {
		input.SourceIdentityAddPolicy = "report_only"
	}
	if input.SourceIdentityAddPolicy != "report_only" && input.SourceIdentityAddPolicy != "add_exact" {
		return Plan{}, errors.New("invalid source identity add policy")
	}
	// Completeness is accepted only for an implemented parser profile. No such
	// profile is currently registered; an unrecognized ID fails closed to unknown.
	if input.Completeness != CompletenessUnknown {
		if input.CompletenessProfileID == "" {
			input.Completeness = CompletenessUnknown
		} else if _, ok := implementedCompleteProfiles[input.CompletenessProfileID]; !ok {
			input.Completeness = CompletenessUnknown
		}
	}
	plan := Plan{
		AccountID:               input.AccountID,
		Source:                  input.Source,
		SourceFingerprint:       SourceFingerprint(input.Source),
		DirectoryHash:           input.DirectoryHash,
		CatalogObservedAt:       time.Now().UTC(),
		CompletenessProfileID:   input.CompletenessProfileID,
		SourceIdentityAddPolicy: input.SourceIdentityAddPolicy,
		CatalogModels:           append([]string{}, input.LiveModels...),
		CatalogWarningCodes:     []string{},
		ApplyEligible:           true,
		Completeness:            input.Completeness,
		LiveListAvailable:       input.LiveListAvailable,
		Before:                  cloneMapping(input.CurrentMapping),
		After:                   cloneMapping(input.CurrentMapping),
		AbsenceCountsBefore:     cloneAbsenceCounts(prior.AbsenceCounts),
		AbsenceCountsAfter:      cloneAbsenceCounts(prior.AbsenceCounts),
		Changes:                 []Change{}, Conflicts: []string{}, UnreviewedModels: []string{},
		CreatedAt: time.Now().UTC(),
	}
	var err error
	plan.BeforeHash, err = MappingHash(input.CurrentMapping)
	if err != nil {
		return Plan{}, err
	}
	plan.RulesHash, err = canonicalHash(rules)
	if err != nil {
		return Plan{}, err
	}
	if input.Source.AccountID != 0 && input.Source.AccountID != input.AccountID {
		return Plan{}, errors.New("source account ID does not match plan account")
	}
	if !input.LiveListAvailable || len(input.LiveModels) == 0 {
		plan.ApplyEligible = false
		plan.DiscoveryReason = "live catalog unavailable, fallback, or empty"
		plan.Conflicts = append(plan.Conflicts, "live catalog is unavailable, fallback, or empty; mapping changes are blocked")
		plan.UnreviewedModels = append([]string{}, input.LiveModels...)
		return plan, nil
	}
	live := make(map[string]bool, len(input.LiveModels))
	for _, id := range input.LiveModels {
		if !validID(id) {
			plan.Conflicts = append(plan.Conflicts, "live catalog contains an invalid model ID")
			return plan, nil
		}
		live[id] = true
	}
	managed := input.ManagedMappings
	if managed == nil {
		managed = prior.ManagedMappings
	}
	if managed == nil {
		managed = map[string]state.ManagedMapping{}
	}
	allowChanges := func(key string) bool {
		owner, ok := managed[key]
		if !ok {
			return false
		}
		if owner.SourceFingerprint != plan.SourceFingerprint {
			return false
		}
		current, exists := input.CurrentMapping[key]
		return exists && current == owner.LastAppliedValue
	}
	desired := map[string]struct{ value, kind, evidence string }{}
	addDesired := func(public, upstream, kind, evidence string) {
		if old, ok := desired[public]; ok && old.value != upstream {
			plan.Conflicts = append(plan.Conflicts, "multiple approved rules assign different upstream IDs to public key "+public)
			return
		}
		desired[public] = struct{ value, kind, evidence string }{upstream, kind, evidence}
	}
	for _, rule := range rules.Aliases {
		if !SourceMatches(rule.Source, input.Source) {
			continue
		}
		if live[rule.UpstreamID] {
			addDesired(rule.PublicID, rule.UpstreamID, "alias", rule.Evidence)
		}
	}
	for _, family := range rules.VersionFamilies {
		if !SourceMatches(family.Source, input.Source) {
			continue
		}
		if !live[family.PreferredUpstreamID] {
			plan.Conflicts = append(plan.Conflicts, "version family preferred upstream ID is absent: "+family.PublicID)
			continue
		}
		addDesired(family.PublicID, family.PreferredUpstreamID, "version_family", family.Evidence)
		for _, member := range family.Members {
			if member.ExactIDAction == "keep" && live[member.UpstreamID] {
				addDesiredIfIdentityAbsent(plan.After, member.UpstreamID, member.UpstreamID, "version_family_keep", family.Evidence, &plan)
			}
			if member.ExactIDAction == "hide" {
				if current, exists := plan.After[member.UpstreamID]; exists {
					if current != member.UpstreamID {
						plan.Conflicts = append(plan.Conflicts, "hide rule only supports exact identity mapping: "+member.UpstreamID)
						continue
					}
					if !allowChanges(member.UpstreamID) {
						plan.Conflicts = append(plan.Conflicts, "hide rule cannot change unmanaged or drifted identity mapping: "+member.UpstreamID)
						continue
					}
					delete(plan.After, member.UpstreamID)
					plan.Changes = append(plan.Changes, Change{Action: "remove", PublicID: member.UpstreamID, Before: current, RuleKind: "version_family", Evidence: family.Evidence, Reason: "reviewed exact member action is hide"})
				}
			}
		}
	}
	for public, item := range desired {
		if current, exists := plan.After[public]; exists {
			if current == item.value {
				continue
			}
			if !allowChanges(public) {
				plan.Conflicts = append(plan.Conflicts, "approved mapping is blocked by unmanaged or drifted key: "+public)
				continue
			}
			plan.After[public] = item.value
			plan.Changes = append(plan.Changes, Change{Action: "update", PublicID: public, Before: current, After: item.value, RuleKind: item.kind, Evidence: item.evidence, Reason: "exact source-scoped reviewed rule"})
		} else {
			plan.After[public] = item.value
			plan.Changes = append(plan.Changes, Change{Action: "add", PublicID: public, After: item.value, RuleKind: item.kind, Evidence: item.evidence, Reason: "exact source-scoped reviewed rule"})
		}
	}
	for _, rule := range rules.RetiredModels {
		if !SourceMatches(rule.Source, input.Source) {
			continue
		}
		current, exists := plan.After[rule.PublicID]
		if !exists {
			// Retiring an already absent exact key is idempotent and must not
			// block unrelated reviewed changes for this source.
			continue
		}
		if current != rule.UpstreamID {
			plan.Conflicts = append(plan.Conflicts, "retirement rule has no exact public-to-upstream mapping: "+rule.PublicID)
			continue
		}
		if !allowChanges(rule.PublicID) {
			plan.Conflicts = append(plan.Conflicts, "retirement rule cannot remove unmanaged or drifted mapping: "+rule.PublicID)
			continue
		}
		delete(plan.After, rule.PublicID)
		plan.Changes = append(plan.Changes, Change{Action: "remove", PublicID: rule.PublicID, Before: current, RuleKind: "retired_model", Evidence: rule.Evidence, Reason: "exact source-scoped retirement rule"})
	}
	if input.Completeness == CompletenessComplete {
		plan.AbsenceCountsAfter, plan.Changes = updateCompleteCatalogAbsence(
			plan.SourceFingerprint, live, managed, plan.AbsenceCountsAfter, plan.After, plan.Changes, allowChanges,
		)
	}
	known := map[string]bool{}
	for _, v := range input.CurrentMapping {
		known[v] = true
	}
	for _, rule := range rules.Aliases {
		if SourceMatches(rule.Source, input.Source) {
			known[rule.UpstreamID] = true
		}
	}
	for _, fam := range rules.VersionFamilies {
		if SourceMatches(fam.Source, input.Source) {
			for _, m := range fam.Members {
				known[m.UpstreamID] = true
			}
		}
	}
	for _, retired := range rules.RetiredModels {
		if SourceMatches(retired.Source, input.Source) {
			known[retired.UpstreamID] = true
		}
	}
	if input.Completeness == CompletenessComplete && input.SourceIdentityAddPolicy == "add_exact" {
		for id := range live {
			if known[id] {
				continue
			}
			if current, exists := plan.After[id]; exists {
				if current != id {
					plan.Conflicts = append(plan.Conflicts, "identity add is blocked by an existing mapping key: "+id)
				}
				continue
			}
			plan.After[id] = id
			plan.Changes = append(plan.Changes, Change{Action: "add", PublicID: id, After: id, RuleKind: "identity_add", Reason: "approved add_exact source profile and complete catalog; raw ID kept unreviewed"})
			plan.UnreviewedModels = append(plan.UnreviewedModels, id)
		}
	}
	for id := range live {
		if !known[id] {
			plan.UnreviewedModels = append(plan.UnreviewedModels, id)
		}
	}
	sort.Strings(plan.UnreviewedModels)
	sort.Slice(plan.Changes, func(i, j int) bool {
		if plan.Changes[i].PublicID == plan.Changes[j].PublicID {
			return plan.Changes[i].Action < plan.Changes[j].Action
		}
		return plan.Changes[i].PublicID < plan.Changes[j].PublicID
	})
	sort.Strings(plan.Conflicts)
	return plan, nil
}

func addDesiredIfIdentityAbsent(after map[string]string, key, value, kind, evidence string, plan *Plan) {
	if current, exists := after[key]; exists {
		if current != value {
			plan.Conflicts = append(plan.Conflicts, "version family keep conflicts with existing mapping: "+key)
		}
		return
	}
	after[key] = value
	plan.Changes = append(plan.Changes, Change{Action: "add", PublicID: key, After: value, RuleKind: kind, Evidence: evidence, Reason: "reviewed family member explicitly marked keep"})
}

func SourceFingerprint(s Source) string {
	normalized, _ := NormalizeBaseURL(s.BaseURL)
	s.BaseURL = normalized
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func canonicalHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", errors.New("canonical hash could not be computed")
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func cloneMapping(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneAbsenceCounts(src map[string]int) map[string]int {
	dst := make(map[string]int, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// updateCompleteCatalogAbsence returns the next per-account counters and any
// exact managed removals justified by the second consecutive complete snapshot.
// The caller is responsible for proving completeness before calling it.
func updateCompleteCatalogAbsence(sourceFingerprint string, live map[string]bool, managed map[string]state.ManagedMapping, counts map[string]int, after map[string]string, changes []Change, allowChanges func(string) bool) (map[string]int, []Change) {
	next := cloneAbsenceCounts(counts)
	if next == nil {
		next = map[string]int{}
	}
	for key, owner := range managed {
		if owner.SourceFingerprint != sourceFingerprint {
			continue
		}
		next[key] = ObserveAbsence(next[key], CompletenessComplete, true, live[owner.LastAppliedValue])
		if next[key] < 2 {
			continue
		}
		current, exists := after[key]
		if !exists || current != owner.LastAppliedValue || !allowChanges(key) {
			continue
		}
		delete(after, key)
		changes = append(changes, Change{Action: "remove", PublicID: key, Before: current, RuleKind: "complete_catalog_absence", Reason: "missing from two consecutive complete snapshots"})
	}
	return next, changes
}

func ObserveAbsence(count int, completeness Completeness, live bool, modelPresent bool) int {
	if completeness != CompletenessComplete || !live {
		return count
	}
	if modelPresent {
		return 0
	}
	return count + 1
}

func (p Plan) ValidateBeforeApply(expectedSource Source, mapping map[string]string, rules Rules) error {
	if len(p.Conflicts) > 0 {
		return errors.New("plan contains conflicts")
	}
	if p.AccountID <= 0 || p.SourceFingerprint != SourceFingerprint(expectedSource) {
		return errors.New("plan source fingerprint changed")
	}
	hash, err := MappingHash(mapping)
	if err != nil || hash != p.BeforeHash {
		return errors.New("plan mapping snapshot changed")
	}
	rulesHash, err := canonicalHash(rules)
	if err != nil || rulesHash != p.RulesHash {
		return errors.New("plan rules changed")
	}
	return nil
}

func CanonicalMappingJSON(mapping map[string]string) ([]byte, error) {
	if mapping == nil {
		return nil, errors.New("mapping is nil")
	}
	keys := make([]string, 0, len(mapping))
	for key := range mapping {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	b := []byte("{")
	for i, key := range keys {
		k, _ := json.Marshal(key)
		v, _ := json.Marshal(mapping[key])
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, k...)
		b = append(b, ':')
		b = append(b, v...)
	}
	b = append(b, '}')
	return b, nil
}

func MappingFingerprint(mapping map[string]string) (string, error) {
	b, err := CanonicalMappingJSON(mapping)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func AccountKey(id int64) string { return strconv.FormatInt(id, 10) }

func (p Plan) String() string {
	return fmt.Sprintf("account=%d changes=%d conflicts=%d", p.AccountID, len(p.Changes), len(p.Conflicts))
}

func IsWildcard(s string) bool { return strings.ContainsAny(s, "*?") }
