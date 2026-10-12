package catalog

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sub2api-model-catalog-manager/internal/state"
)

func sourceFixture() Source {
	return Source{Platform: "openai", AccountType: "apikey", APIProtocol: "openai", BaseURL: "https://provider.example/v1", AccountID: 9001}
}

func approvedAlias(source Source, upstream, public string) AliasRule {
	return AliasRule{Source: source, UpstreamID: upstream, PublicID: public, Decision: "approved", Evidence: "https://provider.example/models", ReviewedAt: "2026-10-11"}
}

func baseRules() Rules {
	return Rules{SchemaVersion: 1, Aliases: []AliasRule{}, VersionFamilies: []VersionFamily{}, RetiredModels: []RetiredRule{}}
}

func TestAliasIsExactAndSourceScopedUnknownModelsRemainCandidates(t *testing.T) {
	rules := baseRules()
	// These fictional IDs exercise normalization only; they do not assert a
	// mapping for any real provider or model.
	rules.Aliases = []AliasRule{approvedAlias(sourceFixture(), "raw-lantern", "public-lantern")}
	in := Input{AccountID: 9001, Source: sourceFixture(), CurrentMapping: map[string]string{"manual": "raw-manual", "wildcard-*": "*"}, LiveModels: []string{"raw-lantern", "raw-lantern-2026-01-01", "UNKNOWN-5.2"}, LiveListAvailable: true, Completeness: CompletenessUnknown}
	plan, err := Normalize(rules, in, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.After["public-lantern"] != "raw-lantern" || plan.After["manual"] != "raw-manual" || plan.After["wildcard-*"] != "*" {
		t.Fatalf("unexpected mapping result: %#v", plan.After)
	}
	if len(plan.UnreviewedModels) != 2 || plan.UnreviewedModels[0] != "UNKNOWN-5.2" || plan.UnreviewedModels[1] != "raw-lantern-2026-01-01" {
		t.Fatalf("unknown/date IDs must remain review candidates: %#v", plan.UnreviewedModels)
	}
	other := in
	other.Source = Source{Platform: "anthropic", AccountType: "apikey", APIProtocol: "anthropic", BaseURL: sourceFixture().BaseURL, AccountID: 9001}
	otherPlan, err := Normalize(rules, other, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := otherPlan.After["public-lantern"]; exists {
		t.Fatal("alias leaked across source identity")
	}
}

func TestOrdinaryNormalizationRejectsWildcardVersionFamilyAccountScope(t *testing.T) {
	s := sourceFixture()
	wildcardAccountSource := s
	wildcardAccountSource.AccountID = 0
	rules := baseRules()
	rules.VersionFamilies = []VersionFamily{exactFamily(wildcardAccountSource, "canonical", "preferred", FamilyMember{UpstreamID: "preferred", ExactIDAction: "keep"})}
	current := map[string]string{"manual": "manual-target"}
	plan, err := Normalize(rules, Input{
		AccountID: 9001, Source: s, CurrentMapping: current,
		LiveModels: []string{"preferred"}, LiveListAvailable: true, Completeness: CompletenessUnknown,
	}, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ApplyEligible || len(plan.Conflicts) == 0 || !reflect.DeepEqual(plan.After, current) || len(plan.Changes) != 0 {
		t.Fatalf("wildcard-account family must fail closed before changing ordinary mappings: %#v", plan)
	}
}

func TestVersionFamilyUsesOnlyPreferredLiveIDAndManagedExactHide(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.VersionFamilies = []VersionFamily{{
		Source: s, PublicID: "model-stable", PreferredUpstreamID: "model-2026-10-01",
		Members:  []FamilyMember{{UpstreamID: "model-2026-10-01", ExactIDAction: "hide"}, {UpstreamID: "model-2026-09-01", ExactIDAction: "keep"}},
		Decision: "approved", Evidence: "https://provider.example/family", ReviewedAt: "2026-10-11",
	}}
	fingerprint := SourceFingerprint(s)
	in := Input{AccountID: 9001, Source: s, CurrentMapping: map[string]string{"model-2026-10-01": "model-2026-10-01", "model-2026-09-01": "model-2026-09-01"}, LiveModels: []string{"model-2026-10-01", "model-2026-09-01"}, LiveListAvailable: true, Completeness: CompletenessUnknown}
	prior := state.Account{ManagedMappings: map[string]state.ManagedMapping{
		"model-2026-10-01": {OriginalValue: "model-2026-10-01", LastAppliedValue: "model-2026-10-01", SourceFingerprint: fingerprint},
	}}
	plan, err := Normalize(rules, in, prior)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 0 || plan.After["model-stable"] != "model-2026-10-01" {
		t.Fatalf("preferred mapping failed: conflicts=%v after=%v", plan.Conflicts, plan.After)
	}
	if _, exists := plan.After["model-2026-10-01"]; exists {
		t.Fatal("managed exact hide identity was retained")
	}
	if plan.After["model-2026-09-01"] != "model-2026-09-01" {
		t.Fatal("explicit keep member was not preserved")
	}

	delete(in.CurrentMapping, "model-2026-10-01")
	in.LiveModels = []string{"model-2026-09-01"}
	plan, err = Normalize(rules, in, prior)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) == 0 || plan.After["model-stable"] != "" {
		t.Fatalf("missing preferred ID must block family without fallback: conflicts=%v after=%v", plan.Conflicts, plan.After)
	}
}

func TestUnmanagedHideAndRetirementAreBlocked(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.VersionFamilies = []VersionFamily{{
		Source: s, PublicID: "stable", PreferredUpstreamID: "model-v2",
		Members:  []FamilyMember{{UpstreamID: "model-v2", ExactIDAction: "hide"}},
		Decision: "approved", Evidence: "https://provider.example/family", ReviewedAt: "2026-10-11",
	}}
	rules.RetiredModels = []RetiredRule{{Source: s, UpstreamID: "old", PublicID: "legacy", Decision: "approved", Evidence: "https://provider.example/retired", ReviewedAt: "2026-10-11"}}
	in := Input{AccountID: 9001, Source: s, CurrentMapping: map[string]string{"model-v2": "model-v2", "legacy": "old"}, LiveModels: []string{"model-v2"}, LiveListAvailable: true, Completeness: CompletenessUnknown}
	plan, err := Normalize(rules, in, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) < 2 || plan.After["model-v2"] != "model-v2" || plan.After["legacy"] != "old" {
		t.Fatalf("unmanaged mappings were changed: conflicts=%v after=%v", plan.Conflicts, plan.After)
	}
}

func TestRetirementRuleIsIdempotentAfterExactMappingWasRemoved(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.RetiredModels = []RetiredRule{{Source: s, UpstreamID: "old-model", PublicID: "legacy", Decision: "approved", Evidence: "verified retired ID", ReviewedAt: "2026-10-11"}}
	plan, err := Normalize(rules, Input{AccountID: 9001, Source: s, CurrentMapping: map[string]string{"manual": "keep"}, LiveModels: []string{"current"}, LiveListAvailable: true, Completeness: CompletenessUnknown}, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 0 || plan.After["manual"] != "keep" {
		t.Fatalf("already-removed retired model blocked unrelated mapping: conflicts=%v after=%v", plan.Conflicts, plan.After)
	}
}

func TestRuleConflictsBlockPlanAndUnknownOrIncompleteCatalogNeverRetires(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.Aliases = []AliasRule{approvedAlias(s, "raw-a", "public"), approvedAlias(s, "raw-b", "public")}
	managed := map[string]state.ManagedMapping{
		"stale": {OriginalValue: "stale-model", LastAppliedValue: "stale-model", SourceFingerprint: SourceFingerprint(s)},
	}
	for _, completeness := range []Completeness{CompletenessUnknown} {
		in := Input{AccountID: 9001, Source: s, CurrentMapping: map[string]string{"stale": "stale-model"}, ManagedMappings: managed, LiveModels: []string{"raw-a", "raw-b"}, LiveListAvailable: true, Completeness: completeness}
		plan, err := Normalize(rules, in, state.Account{})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Conflicts) == 0 {
			t.Fatal("alias collision should block apply")
		}
		if plan.After["stale"] != "stale-model" {
			t.Fatal("unknown/incomplete source removed a missing model")
		}
	}
}

func TestNoUnimplementedCompleteCatalogProfilesCanBeSelected(t *testing.T) {
	rules := baseRules()
	rules.CompleteCatalogProfiles = []CompleteCatalogProfile{{Source: sourceFixture(), ProfileID: "not-implemented", Evidence: "https://provider.example", ReviewedAt: "2026-10-11"}}
	if err := rules.Validate(); err == nil {
		t.Fatal("unregistered complete-catalog profile was accepted")
	}
}

func TestSourceProfileIdentityPolicyIsExplicitAndBuildScoped(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.SourceProfiles = []SourceProfile{{Source: s, BuildID: "build-1", APIContractID: "admin-catalog-v1", Decision: "approved", Evidence: "target test", ReviewedAt: "2026-10-11"}}
	if err := rules.Validate(); err != nil {
		t.Fatal(err)
	}
	profile, ok := rules.MatchSourceProfile(s, "build-1", "admin-catalog-v1")
	if !ok || profile.IdentityAddPolicy != "report_only" {
		t.Fatalf("default policy was not fail closed: %#v %v", profile, ok)
	}
	if _, ok := rules.MatchSourceProfile(s, "build-2", "admin-catalog-v1"); ok {
		t.Fatal("source profile matched an unreviewed target build")
	}
	rules.SourceProfiles[0].IdentityAddPolicy = "guess"
	if err := rules.Validate(); err == nil {
		t.Fatal("invalid identity_add_policy was accepted")
	}
}

func TestLiveIdentityCatalogSyncReplacesOnlyIdentityMappings(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.SourceProfiles = []SourceProfile{{Source: s, BuildID: "build-1", APIContractID: "admin-catalog-all-v1", Decision: "approved", IdentityCatalogPolicy: "replace_identity_from_live_list", Evidence: "owner-approved live upstream catalog for the exact source", ReviewedAt: "2026-10-12"}}
	in := Input{
		AccountID: 9001, Source: s,
		IdentityCatalogPolicy: "replace_identity_from_live_list", AllowIdentityCatalogSync: true,
		CurrentMapping: map[string]string{"stale-model": "stale-model", "keep-alias": "provider-alias", "wildcard-*": "model-*", "gpt-*": "gpt-*", "model?x": "model?x", "*": "*", "still-live": "still-live"},
		LiveModels:     []string{"new-model", "still-live", "Model-X", "model-x"}, LiveListAvailable: true,
		Completeness: CompletenessUnknown,
	}
	plan, err := Normalize(rules, in, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ApplyEligible || len(plan.Conflicts) != 0 || plan.IdentityCatalogPolicy != "replace_identity_from_live_list" {
		t.Fatalf("approved live catalog policy did not produce an applicable plan: %#v", plan)
	}
	if _, ok := plan.After["stale-model"]; ok {
		t.Fatal("stale identity mapping was not removed")
	}
	if plan.After["new-model"] != "new-model" || plan.After["still-live"] != "still-live" || plan.After["Model-X"] != "Model-X" || plan.After["model-x"] != "model-x" {
		t.Fatalf("exact live IDs were not added with case preserved: %#v", plan.After)
	}
	if plan.After["keep-alias"] != "provider-alias" || plan.After["wildcard-*"] != "model-*" || plan.After["gpt-*"] != "gpt-*" || plan.After["model?x"] != "model?x" || plan.After["*"] != "*" {
		t.Fatalf("manual alias or wildcard was changed: %#v", plan.After)
	}
	if len(plan.Changes) != 4 || len(plan.UnreviewedModels) != 4 {
		t.Fatalf("unexpected exact mapping diff: changes=%#v unreviewed=%#v", plan.Changes, plan.UnreviewedModels)
	}
}

func TestLiveIdentityCatalogSyncDoesNotTreatWildcardAsRetirement(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.SourceProfiles = []SourceProfile{{Source: s, BuildID: "build-1", APIContractID: "admin-catalog-all-v1", Decision: "approved", IdentityCatalogPolicy: "replace_identity_from_live_list", Evidence: "owner-approved exact source", ReviewedAt: "2026-10-12"}}
	rules.RetiredModels = []RetiredRule{{
		Source: s, UpstreamID: "gpt-*", PublicID: "gpt-*",
		Decision: "approved", Evidence: "invalid wildcard retirement fixture", ReviewedAt: "2026-10-12",
	}, {
		Source: s, UpstreamID: "model?x", PublicID: "model?x",
		Decision: "approved", Evidence: "invalid wildcard retirement fixture", ReviewedAt: "2026-10-12",
	}}
	plan, err := Normalize(rules, Input{
		AccountID: 9001, Source: s, IdentityCatalogPolicy: "replace_identity_from_live_list", AllowIdentityCatalogSync: true,
		CurrentMapping: map[string]string{"gpt-*": "gpt-*", "model?x": "model?x", "*": "*", "stale-model": "stale-model"},
		LiveModels:     []string{"new-model"}, LiveListAvailable: true, Completeness: CompletenessUnknown,
	}, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ApplyEligible || len(plan.Conflicts) == 0 {
		t.Fatalf("wildcard retirement rule must fail closed: %#v", plan)
	}
	if plan.After["gpt-*"] != "gpt-*" || plan.After["model?x"] != "model?x" || plan.After["*"] != "*" {
		t.Fatalf("wildcard mappings were removed by live catalog reconciliation: %#v", plan.After)
	}
}

func TestLiveIdentityCatalogSyncRejectsWildcardLiveIDs(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.SourceProfiles = []SourceProfile{{Source: s, BuildID: "build-1", APIContractID: "admin-catalog-all-v1", Decision: "approved", IdentityCatalogPolicy: "replace_identity_from_live_list", Evidence: "owner-approved exact source", ReviewedAt: "2026-10-12"}}
	current := map[string]string{"known-model": "known-model", "gpt-*": "gpt-*"}
	for _, wildcardID := range []string{"*", "gpt-*", "model?x"} {
		plan, err := Normalize(rules, Input{
			AccountID: 9001, Source: s, IdentityCatalogPolicy: "replace_identity_from_live_list", AllowIdentityCatalogSync: true,
			CurrentMapping: current,
			LiveModels:     []string{"known-model", wildcardID}, LiveListAvailable: true, Completeness: CompletenessUnknown,
		}, state.Account{})
		if err != nil {
			t.Fatalf("wildcard ID %q: %v", wildcardID, err)
		}
		if plan.ApplyEligible || len(plan.Conflicts) == 0 || len(plan.Changes) != 0 {
			t.Fatalf("wildcard live ID %q must block the whole account plan without mapping changes: %#v", wildcardID, plan)
		}
		if plan.After["known-model"] != "known-model" || plan.After["gpt-*"] != "gpt-*" || len(plan.After) != len(current) {
			t.Fatalf("wildcard live ID %q changed the existing mapping: %#v", wildcardID, plan.After)
		}
		if !strings.Contains(plan.DiscoveryReason, "wildcard") {
			t.Fatalf("wildcard live ID %q lacks explicit discovery reason: %#v", wildcardID, plan)
		}
	}
}

func TestLiveIdentityCatalogSyncBlocksEmptyResultMapping(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.SourceProfiles = []SourceProfile{{Source: s, BuildID: "build-1", APIContractID: "admin-catalog-all-v1", Decision: "approved", IdentityCatalogPolicy: "replace_identity_from_live_list", Evidence: "owner-approved exact source", ReviewedAt: "2026-10-12"}}
	rules.RetiredModels = []RetiredRule{{
		Source: s, UpstreamID: "retired-id", PublicID: "retired-id",
		Decision: "approved", Evidence: "owner-approved exact retirement", ReviewedAt: "2026-10-12",
	}}
	plan, err := Normalize(rules, Input{
		AccountID: 9001, Source: s, IdentityCatalogPolicy: "replace_identity_from_live_list", AllowIdentityCatalogSync: true,
		CurrentMapping: map[string]string{"stale-id": "stale-id", "retired-id": "retired-id"},
		LiveModels:     []string{"retired-id"}, LiveListAvailable: true, Completeness: CompletenessUnknown,
	}, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ApplyEligible || len(plan.Conflicts) == 0 || len(plan.After) != 0 {
		t.Fatalf("empty resulting mapping must be blocked: %#v", plan)
	}
	if !strings.Contains(strings.Join(plan.Conflicts, " "), "empty mappings") {
		t.Fatalf("expected an explicit empty-mapping guard finding: %#v", plan.Conflicts)
	}
}

func TestLiveIdentityCatalogSyncFailsClosedOnMissingApprovalOrAliasCollision(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.SourceProfiles = []SourceProfile{{Source: s, BuildID: "build-1", APIContractID: "admin-catalog-all-v1", Decision: "approved", Evidence: "sync only", ReviewedAt: "2026-10-12"}}
	plan, err := Normalize(rules, Input{AccountID: 9001, Source: s, AllowIdentityCatalogSync: true, CurrentMapping: map[string]string{"old": "old"}, LiveModels: []string{"new"}, LiveListAvailable: true, Completeness: CompletenessUnknown}, state.Account{})
	if err != nil || plan.ApplyEligible || len(plan.Conflicts) == 0 || plan.After["old"] != "old" {
		t.Fatalf("missing source policy did not fail closed: err=%v plan=%#v", err, plan)
	}

	rules.SourceProfiles[0].IdentityCatalogPolicy = "replace_identity_from_live_list"
	plan, err = Normalize(rules, Input{AccountID: 9001, Source: s, IdentityCatalogPolicy: "replace_identity_from_live_list", AllowIdentityCatalogSync: true, CurrentMapping: map[string]string{"new": "manual-target", "keep": "keep"}, LiveModels: []string{"new"}, LiveListAvailable: true, Completeness: CompletenessUnknown}, state.Account{})
	if err != nil || plan.ApplyEligible || len(plan.Conflicts) == 0 || plan.After["new"] != "manual-target" {
		t.Fatalf("live model colliding with a manual alias was overwritten: err=%v plan=%#v", err, plan)
	}
}

func TestLiveIdentityCatalogSyncStillRejectsFallbackAndEmptyCatalog(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.SourceProfiles = []SourceProfile{{Source: s, BuildID: "build-1", APIContractID: "admin-catalog-all-v1", Decision: "approved", IdentityCatalogPolicy: "replace_identity_from_live_list", Evidence: "owner-approved exact source", ReviewedAt: "2026-10-12"}}
	for _, tc := range []struct {
		live bool
		ids  []string
	}{{false, []string{"new"}}, {true, nil}} {
		plan, err := Normalize(rules, Input{AccountID: 9001, Source: s, IdentityCatalogPolicy: "replace_identity_from_live_list", AllowIdentityCatalogSync: true, CurrentMapping: map[string]string{"old": "old"}, LiveModels: tc.ids, LiveListAvailable: tc.live, Completeness: CompletenessUnknown}, state.Account{})
		if err != nil || plan.ApplyEligible || len(plan.Conflicts) == 0 || plan.After["old"] != "old" {
			t.Fatalf("fallback/empty live catalog must preserve mapping: err=%v plan=%#v", err, plan)
		}
	}
}

func TestLiveIdentityCatalogSyncHonorsExactRetirementRules(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.SourceProfiles = []SourceProfile{{Source: s, BuildID: "build-1", APIContractID: "admin-catalog-all-v1", Decision: "approved", IdentityCatalogPolicy: "replace_identity_from_live_list", Evidence: "owner-approved live catalog for the exact source", ReviewedAt: "2026-10-12"}}
	rules.SourceProfiles[0].IdentityCatalogPolicy = "replace_identity_from_live_list"
	rules.RetiredModels = []RetiredRule{{
		Source: s, UpstreamID: "retired-id", PublicID: "retired-id",
		Decision: "approved", Evidence: "owner-approved exact retirement", ReviewedAt: "2026-10-12",
	}}
	plan, err := Normalize(rules, Input{
		AccountID: 9001, Source: s, IdentityCatalogPolicy: "replace_identity_from_live_list", AllowIdentityCatalogSync: true,
		CurrentMapping: map[string]string{"retired-id": "retired-id", "old-live-id": "old-live-id", "keep-alias": "provider-name"},
		LiveModels:     []string{"retired-id", "new-live-id"}, LiveListAvailable: true, Completeness: CompletenessUnknown,
	}, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ApplyEligible || len(plan.Conflicts) != 0 {
		t.Fatalf("an exact retirement rule should remain compatible with live identity sync: %#v", plan)
	}
	if _, exists := plan.After["retired-id"]; exists {
		t.Fatal("explicitly retired live ID was re-added")
	}
	if _, exists := plan.After["old-live-id"]; exists {
		t.Fatal("stale identity mapping absent from the live list was retained")
	}
	if plan.After["new-live-id"] != "new-live-id" || plan.After["keep-alias"] != "provider-name" {
		t.Fatalf("new exact ID or non-identity mapping was not handled correctly: %#v", plan.After)
	}
	if len(plan.UnreviewedModels) != 1 || plan.UnreviewedModels[0] != "new-live-id" {
		t.Fatalf("retired ID should not remain an unreviewed add candidate: %v", plan.UnreviewedModels)
	}
	foundRetirement := false
	for _, change := range plan.Changes {
		if change.PublicID == "retired-id" && change.RuleKind == "retired_model" && change.Action == "remove" {
			foundRetirement = true
		}
	}
	if !foundRetirement {
		t.Fatalf("retirement was not represented as an exact removal: %#v", plan.Changes)
	}
}

func identitySyncRules(source Source) Rules {
	rules := baseRules()
	rules.SourceProfiles = []SourceProfile{{Source: source, BuildID: "build-1", APIContractID: "admin-catalog-all-v1", Decision: "approved", IdentityCatalogPolicy: "replace_identity_from_live_list", Evidence: "owner-approved exact source", ReviewedAt: "2026-10-12"}}
	return rules
}

func identitySyncInput(source Source, mapping map[string]string, live []string) Input {
	return Input{AccountID: source.AccountID, Source: source, IdentityCatalogPolicy: "replace_identity_from_live_list", AllowIdentityCatalogSync: true, CurrentMapping: mapping, LiveModels: live, LiveListAvailable: true, Completeness: CompletenessUnknown}
}

func exactFamily(source Source, publicID, preferredID string, members ...FamilyMember) VersionFamily {
	return VersionFamily{Source: source, PublicID: publicID, PreferredUpstreamID: preferredID, Members: members, Decision: "approved", Evidence: "https://provider.example/exact-alias-snapshot", ReviewedAt: "2026-10-13"}
}

func TestLiveIdentityCatalogSyncOverlaysApprovedExactVersionFamily(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	rules.VersionFamilies = []VersionFamily{exactFamily(s, "model", "model", FamilyMember{UpstreamID: "model", ExactIDAction: "keep"}, FamilyMember{UpstreamID: "model-2026-10-01", ExactIDAction: "hide"})}
	fingerprint := SourceFingerprint(s)
	input := identitySyncInput(s, map[string]string{"model-2026-10-01": "model-2026-10-01", "stale": "stale"}, []string{"model", "model-2026-10-01", "unknown"})
	prior := state.Account{ManagedMappings: map[string]state.ManagedMapping{"model-2026-10-01": {LastAppliedValue: "model-2026-10-01", SourceFingerprint: fingerprint}}}
	plan, err := Normalize(rules, input, prior)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ApplyEligible || len(plan.Conflicts) != 0 {
		t.Fatalf("exact approved family was not applicable: conflicts=%v", plan.Conflicts)
	}
	if plan.After["model"] != "model" {
		t.Fatalf("canonical identity did not map to the preferred live target: %#v", plan.After)
	}
	if _, exists := plan.After["model-2026-10-01"]; exists {
		t.Fatalf("managed exact snapshot hide was not applied: %#v", plan.After)
	}
	if plan.After["unknown"] != "unknown" || len(plan.UnreviewedModels) != 1 || plan.UnreviewedModels[0] != "unknown" {
		t.Fatalf("unknown live model was not preserved and reported: after=%#v unreviewed=%v", plan.After, plan.UnreviewedModels)
	}
}

func TestLiveIdentityCatalogSyncAddsCanonicalPublicIDForDatedOnlyUpstream(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	rules.VersionFamilies = []VersionFamily{exactFamily(s, "claude-haiku-4-5", "claude-haiku-4-5-20251001", FamilyMember{UpstreamID: "claude-haiku-4-5-20251001", ExactIDAction: "hide"})}
	fingerprint := SourceFingerprint(s)
	input := identitySyncInput(s, map[string]string{"claude-haiku-4-5-20251001": "claude-haiku-4-5-20251001"}, []string{"claude-haiku-4-5-20251001"})
	prior := state.Account{ManagedMappings: map[string]state.ManagedMapping{"claude-haiku-4-5-20251001": {OriginalValue: "claude-haiku-4-5-20251001", LastAppliedValue: "claude-haiku-4-5-20251001", SourceFingerprint: fingerprint}}}
	plan, err := Normalize(rules, input, prior)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ApplyEligible || len(plan.Conflicts) != 0 {
		t.Fatalf("exact dated-only upstream target should allow canonical public name: conflicts=%v", plan.Conflicts)
	}
	if plan.After["claude-haiku-4-5"] != "claude-haiku-4-5-20251001" {
		t.Fatalf("canonical public key did not preserve exact live upstream target: %#v", plan.After)
	}
	if _, exists := plan.After["claude-haiku-4-5-20251001"]; exists {
		t.Fatalf("dated public key was not hidden: %#v", plan.After)
	}
}

func TestLiveIdentityCatalogSyncCollapsesReviewedExactAliasesToTargets(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	rules.VersionFamilies = []VersionFamily{
		// Fictional IDs keep this test focused on exact-family mechanics; they
		// do not approve a mapping for a real provider or model.
		exactFamily(s, "public-sunbird", "raw-sunbird-v1",
			FamilyMember{UpstreamID: "raw-sunbird-v1", ExactIDAction: "keep"},
			FamilyMember{UpstreamID: "raw-sunbird-legacy", ExactIDAction: "hide"}),
		exactFamily(s, "public-lantern", "raw-lantern-v2",
			FamilyMember{UpstreamID: "raw-lantern-v2", ExactIDAction: "hide"}),
	}
	fingerprint := SourceFingerprint(s)
	current := map[string]string{
		"raw-sunbird-v1":     "raw-sunbird-v1",
		"raw-sunbird-legacy": "raw-sunbird-legacy",
		"raw-lantern-v2":     "raw-lantern-v2",
	}
	managed := make(map[string]state.ManagedMapping, len(current))
	for id, value := range current {
		managed[id] = state.ManagedMapping{OriginalValue: value, LastAppliedValue: value, SourceFingerprint: fingerprint}
	}
	plan, err := Normalize(rules, identitySyncInput(s, current, []string{
		"raw-sunbird-v1", "raw-sunbird-legacy", "raw-lantern-v2",
	}), state.Account{ManagedMappings: managed})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ApplyEligible || len(plan.Conflicts) != 0 {
		t.Fatalf("exact reviewed aliases were not applicable: conflicts=%v", plan.Conflicts)
	}
	want := map[string]string{
		"public-sunbird": "raw-sunbird-v1",
		"public-lantern": "raw-lantern-v2",
		"raw-sunbird-v1": "raw-sunbird-v1",
	}
	if !reflect.DeepEqual(plan.After, want) {
		t.Fatalf("aliases did not collapse onto their exact upstream targets: got=%#v want=%#v", plan.After, want)
	}
}

func TestLiveIdentityCatalogSyncFamilyRequiresPreferredIDLive(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	rules.VersionFamilies = []VersionFamily{exactFamily(s, "canonical", "preferred", FamilyMember{UpstreamID: "preferred", ExactIDAction: "keep"}, FamilyMember{UpstreamID: "snapshot", ExactIDAction: "hide"})}
	current := map[string]string{"existing": "existing"}
	plan, err := Normalize(rules, identitySyncInput(s, current, []string{"snapshot", "other"}), state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ApplyEligible || len(plan.Conflicts) == 0 || !reflect.DeepEqual(plan.After, current) || len(plan.Changes) != 0 {
		t.Fatalf("missing preferred target must block without a partial diff: %#v", plan)
	}
}

func TestLiveIdentityCatalogSyncFamilyHideRequiresManagedUnchangedExactMapping(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	rules.VersionFamilies = []VersionFamily{exactFamily(s, "canonical", "preferred", FamilyMember{UpstreamID: "preferred", ExactIDAction: "keep"}, FamilyMember{UpstreamID: "snapshot", ExactIDAction: "hide"})}
	for _, tc := range []struct {
		name    string
		mapping map[string]string
		managed map[string]state.ManagedMapping
	}{
		{name: "unmanaged", mapping: map[string]string{"snapshot": "snapshot"}},
		{name: "drifted", mapping: map[string]string{"snapshot": "changed"}, managed: map[string]state.ManagedMapping{"snapshot": {LastAppliedValue: "snapshot", SourceFingerprint: SourceFingerprint(s)}}},
		{name: "wrong source fingerprint", mapping: map[string]string{"snapshot": "snapshot"}, managed: map[string]state.ManagedMapping{"snapshot": {LastAppliedValue: "snapshot", SourceFingerprint: "another-source"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := identitySyncInput(s, tc.mapping, []string{"preferred", "snapshot"})
			input.ManagedMappings = tc.managed
			plan, err := Normalize(rules, input, state.Account{})
			if err != nil {
				t.Fatal(err)
			}
			if plan.ApplyEligible || len(plan.Conflicts) == 0 || !reflect.DeepEqual(plan.After, tc.mapping) || len(plan.Changes) != 0 {
				t.Fatalf("unsafe hide did not fail closed: %#v", plan)
			}
		})
	}
}

func TestLiveIdentityCatalogSyncFamilyCannotOverwriteUnmanagedCanonicalKey(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	rules.VersionFamilies = []VersionFamily{exactFamily(s, "canonical", "preferred", FamilyMember{UpstreamID: "preferred", ExactIDAction: "keep"})}
	current := map[string]string{"canonical": "manual-target"}
	plan, err := Normalize(rules, identitySyncInput(s, current, []string{"preferred", "canonical"}), state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ApplyEligible || len(plan.Conflicts) == 0 || !reflect.DeepEqual(plan.After, current) || len(plan.Changes) != 0 {
		t.Fatalf("unmanaged canonical key was overwritten by family overlay: %#v", plan)
	}
}

func TestLiveIdentityCatalogSyncSkipsNewHiddenMemberBeforeImport(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	rules.VersionFamilies = []VersionFamily{exactFamily(s, "canonical", "preferred", FamilyMember{UpstreamID: "preferred", ExactIDAction: "keep"}, FamilyMember{UpstreamID: "new-snapshot", ExactIDAction: "hide"})}
	plan, err := Normalize(rules, identitySyncInput(s, map[string]string{}, []string{"preferred", "new-snapshot", "unknown"}), state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ApplyEligible || len(plan.Conflicts) != 0 {
		t.Fatalf("new hidden member should not create a hide conflict: %#v", plan)
	}
	if _, exists := plan.After["new-snapshot"]; exists {
		t.Fatalf("new hidden member was implicitly imported: %#v", plan.After)
	}
	if plan.After["canonical"] != "preferred" || plan.After["unknown"] != "unknown" {
		t.Fatalf("family overlay or unknown preservation failed: %#v", plan.After)
	}
}

func TestLiveIdentityCatalogSyncFamilyIsSourceScopedAndPreservesVariants(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	rules.VersionFamilies = []VersionFamily{exactFamily(s, "gpt-5.2", "gpt-5.2", FamilyMember{UpstreamID: "gpt-5.2", ExactIDAction: "keep"}, FamilyMember{UpstreamID: "gpt-5.2-2025-12-11", ExactIDAction: "hide"})}
	live := []string{"gpt-5.2", "gpt-5.2-2025-12-11", "gpt-5.2-pro", "gpt-5.2-codex", "gpt-5.2-compact"}
	input := identitySyncInput(s, map[string]string{}, live)
	input.Source.APIProtocol = "anthropic"
	plan, err := Normalize(rules, input, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range live {
		if plan.After[id] != id {
			t.Fatalf("source-mismatched rule or variant altered %q: %#v", id, plan.After)
		}
	}

	input = identitySyncInput(s, map[string]string{}, live)
	plan, err = Normalize(rules, input, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := plan.After["gpt-5.2-2025-12-11"]; exists {
		t.Fatalf("exact approved snapshot was not hidden: %#v", plan.After)
	}
	for _, id := range []string{"gpt-5.2", "gpt-5.2-pro", "gpt-5.2-codex", "gpt-5.2-compact"} {
		if plan.After[id] != id {
			t.Fatalf("distinct protocol/capability variant %q was incorrectly merged: %#v", id, plan.After)
		}
	}
}

func TestLiveIdentityCatalogSyncFamilyRejectsCrossFamilyIDOverlap(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	rules.VersionFamilies = []VersionFamily{
		exactFamily(s, "model-a", "model-a", FamilyMember{UpstreamID: "model-a", ExactIDAction: "keep"}, FamilyMember{UpstreamID: "shared-snapshot", ExactIDAction: "hide"}),
		exactFamily(s, "model-b", "model-b", FamilyMember{UpstreamID: "model-b", ExactIDAction: "keep"}, FamilyMember{UpstreamID: "shared-snapshot", ExactIDAction: "keep"}),
	}
	current := map[string]string{"model-a": "model-a", "model-b": "model-b", "shared-snapshot": "shared-snapshot", "unrelated": "unrelated"}
	plan, err := Normalize(rules, identitySyncInput(s, current, []string{"model-a", "model-b", "shared-snapshot", "unrelated"}), state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ApplyEligible || len(plan.Conflicts) == 0 || len(plan.Changes) != 0 || !reflect.DeepEqual(plan.After, current) {
		t.Fatalf("cross-family exact-ID overlap must block the whole plan: %#v", plan)
	}
}

func TestLiveIdentityCatalogSyncFamilyCanBePinnedToExactAccount(t *testing.T) {
	ruleSource := sourceFixture()
	rules := identitySyncRules(ruleSource)
	rules.VersionFamilies = []VersionFamily{exactFamily(ruleSource, "canonical", "preferred", FamilyMember{UpstreamID: "preferred", ExactIDAction: "keep"}, FamilyMember{UpstreamID: "snapshot", ExactIDAction: "hide"})}
	otherSource := ruleSource
	otherSource.AccountID++
	current := map[string]string{"canonical": "canonical", "preferred": "preferred", "snapshot": "snapshot"}
	plan, err := Normalize(rules, identitySyncInput(otherSource, current, []string{"canonical", "preferred", "snapshot"}), state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.ApplyEligible || len(plan.Conflicts) != 0 || plan.After["snapshot"] != "snapshot" || plan.After["canonical"] != "canonical" {
		t.Fatalf("account-pinned family leaked to another account sharing the same endpoint: %#v", plan)
	}
}

func TestLiveIdentityCatalogSyncFamilyRequiresExactAccountRule(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	familySource := s
	familySource.AccountID = 0
	rules.VersionFamilies = []VersionFamily{exactFamily(familySource, "canonical", "preferred", FamilyMember{UpstreamID: "preferred", ExactIDAction: "keep"}, FamilyMember{UpstreamID: "snapshot", ExactIDAction: "hide"})}
	current := map[string]string{"canonical": "canonical", "preferred": "preferred", "snapshot": "snapshot"}
	plan, err := Normalize(rules, identitySyncInput(s, current, []string{"canonical", "preferred", "snapshot"}), state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ApplyEligible || len(plan.Conflicts) == 0 || len(plan.Changes) != 0 || !reflect.DeepEqual(plan.After, current) {
		t.Fatalf("wildcard-account family must fail closed without producing changes: %#v", plan)
	}
}

func TestLiveIdentityCatalogSyncFamilyIsIdempotentAfterApply(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	rules.VersionFamilies = []VersionFamily{exactFamily(s, "canonical", "preferred", FamilyMember{UpstreamID: "preferred", ExactIDAction: "keep"}, FamilyMember{UpstreamID: "snapshot", ExactIDAction: "hide"})}
	fingerprint := SourceFingerprint(s)
	prior := state.Account{ManagedMappings: map[string]state.ManagedMapping{"snapshot": {LastAppliedValue: "snapshot", SourceFingerprint: fingerprint}}}
	first, err := Normalize(rules, identitySyncInput(s, map[string]string{"snapshot": "snapshot"}, []string{"preferred", "snapshot"}), prior)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Conflicts) != 0 || first.After["canonical"] != "preferred" {
		t.Fatalf("first family overlay failed: %#v", first)
	}
	second, err := Normalize(rules, identitySyncInput(s, first.After, []string{"preferred", "snapshot"}), prior)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Conflicts) != 0 || len(second.Changes) != 0 || !reflect.DeepEqual(second.After, first.After) {
		t.Fatalf("second run was not idempotent: first=%#v second=%#v", first.After, second)
	}
}

func TestLiveIdentityCatalogSyncAliasRulesRemainFailClosedWithFamilies(t *testing.T) {
	s := sourceFixture()
	rules := identitySyncRules(s)
	rules.Aliases = []AliasRule{approvedAlias(s, "alias", "canonical")}
	rules.VersionFamilies = []VersionFamily{exactFamily(s, "family", "preferred", FamilyMember{UpstreamID: "preferred", ExactIDAction: "keep"})}
	current := map[string]string{"existing": "existing"}
	plan, err := Normalize(rules, identitySyncInput(s, current, []string{"preferred", "alias"}), state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ApplyEligible || len(plan.Conflicts) == 0 || !reflect.DeepEqual(plan.After, current) || len(plan.Changes) != 0 {
		t.Fatalf("alias rules must still fail closed in identity sync: %#v", plan)
	}
}

func TestSourceProtocolIsOptionalButMatchedExactly(t *testing.T) {
	base := Source{Platform: "openai", AccountType: "apikey", BaseURL: "https://provider.example/v1", AccountID: 9001}
	accountSource, err := SourceForAccount(base.Platform, base.AccountType, "", base.BaseURL, base.AccountID)
	if err != nil || accountSource.APIProtocol != "" {
		t.Fatalf("source construction inferred or rejected an absent protocol: %#v err=%v", accountSource, err)
	}

	blankRules := baseRules()
	blankRules.SourceProfiles = []SourceProfile{{Source: base, BuildID: "build-1", APIContractID: "admin-catalog-v1", Decision: "approved", Evidence: "synthetic fixture DTO and sync evidence", ReviewedAt: "2026-10-11"}}
	if err := blankRules.Validate(); err != nil {
		t.Fatalf("blank protocol source profile rejected: %v", err)
	}
	if _, ok := blankRules.MatchSourceProfile(accountSource, "build-1", "admin-catalog-v1"); !ok {
		t.Fatal("blank protocol source profile did not match the original blank account value")
	}

	explicitSource := base
	explicitSource.APIProtocol = "openai"
	explicitRules := baseRules()
	explicitRules.SourceProfiles = []SourceProfile{{Source: explicitSource, BuildID: "build-1", APIContractID: "admin-catalog-v1", Decision: "approved", Evidence: "explicit protocol evidence", ReviewedAt: "2026-10-11"}}
	if err := explicitRules.Validate(); err != nil {
		t.Fatalf("explicit protocol source profile rejected: %v", err)
	}
	if _, ok := explicitRules.MatchSourceProfile(accountSource, "build-1", "admin-catalog-v1"); ok {
		t.Fatal("explicit protocol source profile matched an account whose original protocol is blank")
	}
	if catalogSourceMatches := SourceMatches(base, explicitSource); catalogSourceMatches {
		t.Fatal("blank source identity matched an explicit protocol source")
	}
	if _, ok := blankRules.MatchSourceProfile(explicitSource, "build-1", "admin-catalog-v1"); ok {
		t.Fatal("blank protocol source profile matched an explicit protocol account")
	}

	otherAccount := accountSource
	otherAccount.AccountID = 9002
	if _, ok := blankRules.MatchSourceProfile(otherAccount, "build-1", "admin-catalog-v1"); ok {
		t.Fatal("account-scoped source profile matched a different account ID")
	}
	if SourceFingerprint(base) == SourceFingerprint(explicitSource) {
		t.Fatal("empty and explicit protocol source fingerprints unexpectedly matched")
	}
}

func TestAddExactCannotAddUnknownModelWithoutRegisteredCompleteProfile(t *testing.T) {
	s := sourceFixture()
	rules := baseRules()
	rules.SourceProfiles = []SourceProfile{{Source: s, BuildID: "build-1", APIContractID: "admin-catalog-v1", Decision: "approved", IdentityAddPolicy: "add_exact", Evidence: "target test", ReviewedAt: "2026-10-11"}}
	if err := rules.Validate(); err != nil {
		t.Fatal(err)
	}
	plan, err := Normalize(rules, Input{AccountID: 9001, Source: s, CurrentMapping: map[string]string{}, LiveModels: []string{"new-raw-model"}, LiveListAvailable: true, Completeness: CompletenessComplete, CompletenessProfileID: "fake-complete-profile", SourceIdentityAddPolicy: "add_exact"}, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 0 || len(plan.UnreviewedModels) != 1 || plan.After["new-raw-model"] != "" {
		t.Fatalf("unregistered completeness profile caused identity add: %#v", plan)
	}
}

func TestUnavailableFallbackAndEmptyCatalogBlockAllMappingChanges(t *testing.T) {
	rules := baseRules()
	rules.Aliases = []AliasRule{approvedAlias(sourceFixture(), "raw", "public")}
	for _, tc := range []struct {
		liveFlag bool
		models   []string
	}{
		{false, []string{"raw"}},
		{true, nil},
	} {
		plan, err := Normalize(rules, Input{AccountID: 9001, Source: sourceFixture(), CurrentMapping: map[string]string{}, LiveModels: tc.models, LiveListAvailable: tc.liveFlag, Completeness: CompletenessUnknown}, state.Account{})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Changes) != 0 || len(plan.Conflicts) == 0 {
			t.Fatalf("fallback/empty catalog should be blocked: %#v", plan)
		}
	}
}

func TestTwoSnapshotAbsenceCounterRequiresCompleteLiveSnapshots(t *testing.T) {
	count := ObserveAbsence(0, CompletenessUnknown, true, false)
	if count != 0 {
		t.Fatalf("unknown catalog changed absence count: %d", count)
	}
	count = ObserveAbsence(count, CompletenessComplete, true, false)
	if count != 1 {
		t.Fatalf("first complete absence = %d", count)
	}
	count = ObserveAbsence(count, CompletenessUnknown, true, false)
	if count != 1 {
		t.Fatalf("unknown snapshot must not advance count: %d", count)
	}
	count = ObserveAbsence(count, CompletenessComplete, true, false)
	if count != 2 {
		t.Fatalf("second complete absence = %d", count)
	}
	if got := ObserveAbsence(count, CompletenessComplete, true, true); got != 0 {
		t.Fatalf("model reappearance did not reset count: %d", got)
	}
}

func TestCompleteCatalogAbsenceCounterReturnsAndPersistsAcrossRuns(t *testing.T) {
	source := sourceFixture()
	fingerprint := SourceFingerprint(source)
	managed := map[string]state.ManagedMapping{
		"public-model": {OriginalValue: "old", LastAppliedValue: "upstream-model", SourceFingerprint: fingerprint},
	}
	before := map[string]string{"public-model": "upstream-model"}
	currentCounts := map[string]int{}
	allow := func(string) bool { return true }

	afterFirstMapping := cloneMapping(before)
	countsAfterFirst, firstChanges := updateCompleteCatalogAbsence(fingerprint, map[string]bool{}, managed, currentCounts, afterFirstMapping, nil, allow)
	if countsAfterFirst["public-model"] != 1 || len(firstChanges) != 0 {
		t.Fatalf("first complete absence should persist count 1 without removal: counts=%v changes=%v", countsAfterFirst, firstChanges)
	}
	if currentCounts["public-model"] != 0 {
		t.Fatal("counter helper mutated its input map instead of returning updated state")
	}

	// The persisted state is the source for the next discovery run.
	path := filepath.Join(t.TempDir(), "state.json")
	stateBefore := state.Empty()
	stateBefore.Accounts["9001"] = state.Account{ManagedMappings: managed, AbsenceCounts: countsAfterFirst}
	if err := state.Save(path, stateBefore); err != nil {
		t.Fatal(err)
	}
	stateAfterFirst, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	afterSecondMapping := cloneMapping(before)
	countsAfterSecond, secondChanges := updateCompleteCatalogAbsence(fingerprint, map[string]bool{}, stateAfterFirst.Accounts["9001"].ManagedMappings, stateAfterFirst.Accounts["9001"].AbsenceCounts, afterSecondMapping, nil, allow)
	if len(secondChanges) != 1 || secondChanges[0].Action != "remove" || secondChanges[0].PublicID != "public-model" {
		t.Fatalf("second consecutive complete absence should propose exact retirement: %#v", secondChanges)
	}
	if countsAfterSecond["public-model"] != 2 {
		t.Fatalf("the approval state must retain count 2 until apply consumes the retirement: %v", countsAfterSecond)
	}
	secondPlan := Plan{
		Completeness: CompletenessComplete, AbsenceCountsBefore: cloneAbsenceCounts(stateAfterFirst.Accounts["9001"].AbsenceCounts),
		AbsenceCountsAfter: cloneAbsenceCounts(countsAfterSecond), Before: cloneMapping(before), After: afterSecondMapping, Changes: secondChanges,
	}
	encodedPlan, err := json.Marshal(secondPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedPlan), `"absence_counts_before"`) || !strings.Contains(string(encodedPlan), `"absence_counts_after"`) {
		t.Fatalf("approval plan JSON did not bind both counter snapshots: %s", encodedPlan)
	}
	stateAfterSecond := stateAfterFirst
	stateAfterSecond.Accounts = map[string]state.Account{}
	for key, value := range stateAfterFirst.Accounts {
		stateAfterSecond.Accounts[key] = value
	}
	accountAfterSecond := stateAfterSecond.Accounts["9001"]
	accountAfterSecond.AbsenceCounts = cloneAbsenceCounts(secondPlan.AbsenceCountsAfter)
	stateAfterSecond.Accounts["9001"] = accountAfterSecond
	if err := state.Save(path, stateAfterSecond); err != nil {
		t.Fatal(err)
	}
	stateForApply, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stateForApply.Accounts["9001"].AbsenceCounts, secondPlan.AbsenceCountsAfter) {
		t.Fatalf("persisted after-counter snapshot differs from approved plan: state=%v plan=%v", stateForApply.Accounts["9001"].AbsenceCounts, secondPlan.AbsenceCountsAfter)
	}
	// Apply replays the same catalog snapshot from the plan's before-counter
	// snapshot, not the already-advanced state counter. The retirement delta and
	// after snapshot therefore remain byte-for-byte equivalent.
	applyMapping := cloneMapping(secondPlan.Before)
	applyCounts, applyChanges := updateCompleteCatalogAbsence(fingerprint, map[string]bool{}, managed, secondPlan.AbsenceCountsBefore, applyMapping, nil, allow)
	if !reflect.DeepEqual(applyCounts, secondPlan.AbsenceCountsAfter) || !reflect.DeepEqual(applyMapping, secondPlan.After) || !reflect.DeepEqual(applyChanges, secondPlan.Changes) {
		t.Fatalf("apply re-normalized a different retirement delta: counts=%v mapping=%v changes=%v", applyCounts, applyMapping, applyChanges)
	}
	driftedCounts := cloneAbsenceCounts(stateForApply.Accounts["9001"].AbsenceCounts)
	driftedCounts["public-model"] = 1
	if reflect.DeepEqual(driftedCounts, secondPlan.AbsenceCountsAfter) {
		t.Fatal("counter drift unexpectedly matched the frozen approval snapshot")
	}

	// Non-complete snapshots preserve the durable count, and a live reappearance
	// in a complete snapshot resets it.
	if got := ObserveAbsence(stateAfterFirst.Accounts["9001"].AbsenceCounts["public-model"], CompletenessUnknown, true, false); got != 1 {
		t.Fatalf("unknown snapshot changed persisted counter: %d", got)
	}
	if got := ObserveAbsence(1, CompletenessComplete, true, true); got != 0 {
		t.Fatalf("reappearing model did not reset absence counter: %d", got)
	}
}

func TestNormalizeKeepsCompletenessFailClosedAndReturnsPriorCounters(t *testing.T) {
	rules := baseRules()
	source := sourceFixture()
	prior := state.Account{
		ManagedMappings: map[string]state.ManagedMapping{
			"public-model": {OriginalValue: "old", LastAppliedValue: "missing-upstream", SourceFingerprint: SourceFingerprint(source)},
		},
		AbsenceCounts: map[string]int{"public-model": 1},
	}
	plan, err := Normalize(rules, Input{
		AccountID: 9001, Source: source, CurrentMapping: map[string]string{"public-model": "missing-upstream"},
		LiveModels: []string{"some-live-model"}, LiveListAvailable: true,
		Completeness: CompletenessComplete, CompletenessProfileID: "not-registered",
	}, prior)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Completeness != CompletenessUnknown || plan.AbsenceCountsBefore["public-model"] != 1 || plan.AbsenceCountsAfter["public-model"] != 1 {
		t.Fatalf("unregistered completeness must be downgraded without changing counters: completeness=%s before=%v after=%v", plan.Completeness, plan.AbsenceCountsBefore, plan.AbsenceCountsAfter)
	}
	if prior.AbsenceCounts["public-model"] != 1 {
		t.Fatal("Normalize mutated caller-owned prior state")
	}
	if len(plan.Changes) != 0 || plan.After["public-model"] != "missing-upstream" {
		t.Fatalf("unregistered completeness changed mapping: changes=%v after=%v", plan.Changes, plan.After)
	}
}
