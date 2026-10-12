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
	AccountID                int64
	Source                   Source
	SourceIdentityAddPolicy  string
	IdentityCatalogPolicy    string
	AllowIdentityCatalogSync bool
	CurrentMapping           map[string]string
	ManagedMappings          map[string]state.ManagedMapping
	LiveModels               []string
	LiveListAvailable        bool
	Completeness             Completeness
	CompletenessProfileID    string
	DirectoryHash            string
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
	IdentityCatalogPolicy   string            `json:"identity_catalog_policy,omitempty"`
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
		IdentityCatalogPolicy: func() string {
			if input.AllowIdentityCatalogSync {
				return input.IdentityCatalogPolicy
			}
			return ""
		}(),
		CatalogModels:       append([]string{}, input.LiveModels...),
		CatalogWarningCodes: []string{},
		ApplyEligible:       true,
		Completeness:        input.Completeness,
		LiveListAvailable:   input.LiveListAvailable,
		Before:              cloneMapping(input.CurrentMapping),
		After:               cloneMapping(input.CurrentMapping),
		AbsenceCountsBefore: cloneAbsenceCounts(prior.AbsenceCounts),
		AbsenceCountsAfter:  cloneAbsenceCounts(prior.AbsenceCounts),
		Changes:             []Change{}, Conflicts: []string{}, UnreviewedModels: []string{},
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
	// Version families are account-scoped because a preferred upstream ID and
	// its hide actions are evidence about one exact credential/source binding.
	// SourceMatches permits account_id=0 as a wildcard for other rule types;
	// do not let that broader matching semantics apply to version families.
	for _, family := range rules.VersionFamilies {
		if !sameSourceEndpoint(family.Source, input.Source) {
			continue
		}
		if input.AccountID <= 0 || family.Source.AccountID <= 0 {
			plan.ApplyEligible = false
			plan.DiscoveryReason = "version family does not name the exact positive account ID"
			plan.Conflicts = append(plan.Conflicts, "version family requires an exact positive account ID matching the normalization account: "+family.PublicID)
		}
	}
	if len(plan.Conflicts) > 0 {
		sort.Strings(plan.Conflicts)
		return plan, nil
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
		if !ok || owner.SourceFingerprint != plan.SourceFingerprint {
			return false
		}
		current, exists := input.CurrentMapping[key]
		return exists && current == owner.LastAppliedValue
	}
	if input.AllowIdentityCatalogSync {
		if input.IdentityCatalogPolicy != "replace_identity_from_live_list" {
			plan.ApplyEligible = false
			plan.DiscoveryReason = "source profile does not approve live identity mapping synchronization"
			plan.Conflicts = append(plan.Conflicts, "identity catalog sync is not approved for this exact source profile")
			return plan, nil
		}
		// This mode only imports exact upstream model IDs as identity mappings.
		// A wildcard from a provider catalog is not an exact ID and must not be
		// written as a broad routing rule. Preserve the current mapping and stop
		// the entire account plan before any removals or additions are computed.
		for modelID := range live {
			if IsWildcard(modelID) {
				plan.ApplyEligible = false
				plan.DiscoveryReason = "live identity catalog contains a wildcard model ID"
				plan.Conflicts = append(plan.Conflicts, "live identity sync requires exact model IDs; wildcard model ID is not safe to import")
				return plan, nil
			}
		}
		for _, rule := range rules.Aliases {
			if SourceMatches(rule.Source, input.Source) {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "live identity sync cannot overlap source-specific alias rules")
			}
		}
		// Reviewed exact version families are the only approved exception to
		// identity-only reconciliation. Preflight every family before changing
		// the plan so a conflict cannot leave a partial mapping diff.
		type familyOverlay struct {
			upstreamID string
			evidence   string
		}
		familyTargets := map[string]familyOverlay{}
		hiddenMembers := map[string]string{}
		keptMembers := map[string]string{}
		familyCovered := map[string]bool{}
		familyIDOwners := map[string]string{}
		for _, family := range rules.VersionFamilies {
			if !SourceMatches(family.Source, input.Source) {
				continue
			}
			if family.Source.AccountID <= 0 || family.Source.AccountID != input.AccountID {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "live identity sync requires a version family pinned to this exact account: "+family.PublicID)
				continue
			}
			// A concrete upstream ID may belong to only one canonical family
			// for this account/source. Even two "keep" rules that reuse an ID
			// can publish conflicting canonical names, so stop before creating
			// any partial plan.
			familyIDs := map[string]bool{family.PublicID: true}
			for _, member := range family.Members {
				familyIDs[member.UpstreamID] = true
			}
			familyOverlap := false
			for id := range familyIDs {
				if owner, exists := familyIDOwners[id]; exists && owner != family.PublicID {
					plan.ApplyEligible = false
					plan.Conflicts = append(plan.Conflicts, "version families overlap on exact model ID: "+id)
					familyOverlap = true
				}
			}
			if familyOverlap {
				continue
			}
			for id := range familyIDs {
				familyIDOwners[id] = family.PublicID
			}
			if !live[family.PreferredUpstreamID] {
				plan.ApplyEligible = false
				plan.DiscoveryReason = "live identity sync conflicts with an unavailable family target"
				plan.Conflicts = append(plan.Conflicts, "version family preferred upstream ID is absent: "+family.PublicID)
				continue
			}
			if previous, exists := familyTargets[family.PublicID]; exists && previous.upstreamID != family.PreferredUpstreamID {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "multiple version families assign different upstream IDs to public key "+family.PublicID)
				continue
			}
			familyTargets[family.PublicID] = familyOverlay{upstreamID: family.PreferredUpstreamID, evidence: family.Evidence}
			familyCovered[family.PreferredUpstreamID] = true
			familyCovered[family.PublicID] = true
			for _, member := range family.Members {
				familyCovered[member.UpstreamID] = true
				if member.ExactIDAction == "hide" {
					if keptFamily, kept := keptMembers[member.UpstreamID]; kept {
						plan.ApplyEligible = false
						plan.Conflicts = append(plan.Conflicts, "version families assign conflicting keep and hide actions to member: "+member.UpstreamID+" (family "+keptFamily+")")
						continue
					}
					if oldFamily, exists := hiddenMembers[member.UpstreamID]; exists && oldFamily != family.PublicID {
						plan.ApplyEligible = false
						plan.Conflicts = append(plan.Conflicts, "version families assign conflicting hide actions to member: "+member.UpstreamID)
						continue
					}
					hiddenMembers[member.UpstreamID] = family.PublicID
				} else {
					if hiddenFamily, hidden := hiddenMembers[member.UpstreamID]; hidden {
						plan.ApplyEligible = false
						plan.Conflicts = append(plan.Conflicts, "version families assign conflicting keep and hide actions to member: "+member.UpstreamID+" (family "+hiddenFamily+")")
						continue
					}
					keptMembers[member.UpstreamID] = family.PublicID
				}
			}
		}
		for publicID, target := range familyTargets {
			if hiddenFamily, hidden := hiddenMembers[publicID]; hidden {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "version family canonical key is also hidden: "+publicID+" (family "+hiddenFamily+")")
			}
			if current, exists := input.CurrentMapping[publicID]; exists && current != target.upstreamID && !allowChanges(publicID) {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "version family canonical key would overwrite an unmanaged or drifted mapping: "+publicID)
			}
		}
		for hiddenID := range hiddenMembers {
			if current, exists := input.CurrentMapping[hiddenID]; !exists {
				continue // A newly observed hidden member is skipped before import.
			} else if !isExactIdentityMapping(hiddenID, current) {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "version-family hide only supports an exact identity mapping: "+hiddenID)
			} else if !allowChanges(hiddenID) {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "version-family hide cannot change an unmanaged or drifted identity mapping: "+hiddenID)
			}
		}
		retired := make(map[string]RetiredRule)
		for _, rule := range rules.RetiredModels {
			if !SourceMatches(rule.Source, input.Source) {
				continue
			}
			// This branch only reconciles exact identity mappings. A retirement
			// rule that renames an ID is ambiguous here and must remain manual.
			if !isExactIdentityMapping(rule.PublicID, rule.UpstreamID) {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "live identity sync cannot apply a non-identity retirement rule")
				continue
			}
			if _, duplicate := retired[rule.UpstreamID]; duplicate {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "live identity sync has duplicate source retirement rules")
				continue
			}
			retired[rule.UpstreamID] = rule
		}
		for retiredID := range retired {
			if _, hidden := hiddenMembers[retiredID]; hidden {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "retirement and version-family rules overlap on model ID: "+retiredID)
			}
			if _, kept := keptMembers[retiredID]; kept {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "retirement and version-family rules overlap on model ID: "+retiredID)
			}
		}
		for publicID := range familyTargets {
			if _, retiredPublicID := retired[publicID]; retiredPublicID {
				plan.ApplyEligible = false
				plan.Conflicts = append(plan.Conflicts, "retirement and version-family rules overlap on public key: "+publicID)
			}
		}
		if len(plan.Conflicts) > 0 {
			plan.DiscoveryReason = "live identity sync conflicts with explicit source rules"
			return plan, nil
		}
		for publicID, upstreamID := range input.CurrentMapping {
			if !isExactIdentityMapping(publicID, upstreamID) {
				continue
			}
			if _, explicitlyHidden := hiddenMembers[publicID]; explicitlyHidden {
				continue // Explicit hides are handled only through the ownership gate below.
			}
			if _, familyPublicID := familyTargets[publicID]; familyPublicID {
				continue // Keep the canonical key for the reviewed overlay below.
			}
			if live[publicID] {
				if rule, explicitlyRetired := retired[publicID]; explicitlyRetired {
					delete(plan.After, publicID)
					plan.Changes = append(plan.Changes, Change{Action: "remove", PublicID: publicID, Before: upstreamID, RuleKind: "retired_model", Evidence: rule.Evidence, Reason: "exact model ID is covered by an approved source-specific retirement rule"})
				}
				continue
			}
			delete(plan.After, publicID)
			plan.Changes = append(plan.Changes, Change{Action: "remove", PublicID: publicID, Before: upstreamID, RuleKind: "live_identity_sync", Evidence: "approved exact-source live catalog policy", Reason: "identity mapping is absent from the successful live upstream model list"})
		}
		for modelID := range live {
			if _, explicitlyHidden := hiddenMembers[modelID]; explicitlyHidden {
				continue // Never identity-import an exact member reviewed as hidden.
			}
			if _, familyPublicID := familyTargets[modelID]; familyPublicID {
				continue // The explicit family target owns this public key.
			}
			if _, explicitlyRetired := retired[modelID]; explicitlyRetired {
				if current, exists := input.CurrentMapping[modelID]; exists && current != modelID {
					plan.ApplyEligible = false
					plan.Conflicts = append(plan.Conflicts, "retired live model ID collides with a preserved non-identity mapping: "+modelID)
				}
				continue
			}
			if current, exists := input.CurrentMapping[modelID]; exists {
				if current != modelID {
					plan.ApplyEligible = false
					plan.Conflicts = append(plan.Conflicts, "live model ID collides with a preserved non-identity mapping: "+modelID)
				}
				continue
			}
			plan.After[modelID] = modelID
			plan.Changes = append(plan.Changes, Change{Action: "add", PublicID: modelID, After: modelID, RuleKind: "live_identity_sync", Evidence: "approved exact-source live catalog policy", Reason: "new exact upstream ID added without renaming or semantic inference"})
		}
		for hiddenID, familyPublicID := range hiddenMembers {
			if current, exists := plan.After[hiddenID]; exists {
				delete(plan.After, hiddenID)
				plan.Changes = append(plan.Changes, Change{Action: "remove", PublicID: hiddenID, Before: current, RuleKind: "version_family", Evidence: familyTargets[familyPublicID].evidence, Reason: "reviewed exact family member marked hide and ownership is unchanged"})
			}
		}
		for publicID, target := range familyTargets {
			if current, exists := plan.After[publicID]; exists {
				if current == target.upstreamID {
					continue
				}
				// The preflight above proves this mapping is manager-owned and
				// unchanged. Do not overwrite any other current value.
				plan.After[publicID] = target.upstreamID
				plan.Changes = append(plan.Changes, Change{Action: "update", PublicID: publicID, Before: current, After: target.upstreamID, RuleKind: "version_family", Evidence: target.evidence, Reason: "exact source-scoped reviewed family overlay"})
			} else {
				plan.After[publicID] = target.upstreamID
				plan.Changes = append(plan.Changes, Change{Action: "add", PublicID: publicID, After: target.upstreamID, RuleKind: "version_family", Evidence: target.evidence, Reason: "exact source-scoped reviewed family overlay"})
			}
		}
		if len(plan.After) == 0 {
			plan.ApplyEligible = false
			plan.DiscoveryReason = "live identity sync would produce an empty model mapping"
			plan.Conflicts = append(plan.Conflicts, "live identity sync would clear all model mappings; empty mappings may restore unrestricted or default routing")
		}
		for id := range live {
			if _, explicitlyRetired := retired[id]; explicitlyRetired {
				continue
			}
			if familyCovered[id] {
				continue
			}
			plan.UnreviewedModels = append(plan.UnreviewedModels, id)
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

// isExactIdentityMapping excludes Sub2API wildcard patterns even when their
// public and upstream strings are identical. A live catalog contains concrete
// IDs, so absence from that catalog cannot establish that a wildcard mapping
// is stale.
func isExactIdentityMapping(publicID, upstreamID string) bool {
	return publicID != "" && publicID == upstreamID && !IsWildcard(publicID)
}

// sameSourceEndpoint compares every source dimension except the account ID.
// It is used only to detect a family rule that would otherwise be dangerously
// broadened by SourceMatches' account_id=0 wildcard behavior.
func sameSourceEndpoint(rule, actual Source) bool {
	rule.AccountID = 0
	actual.AccountID = 0
	return SourceMatches(rule, actual)
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
