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
	rules.Aliases = []AliasRule{approvedAlias(sourceFixture(), "deepseek-flash", "deepseek-v4-flash")}
	in := Input{AccountID: 9001, Source: sourceFixture(), CurrentMapping: map[string]string{"manual": "raw-manual", "wildcard-*": "*"}, LiveModels: []string{"deepseek-flash", "deepseek-flash-2026-01-01", "GLM-5.2"}, LiveListAvailable: true, Completeness: CompletenessUnknown}
	plan, err := Normalize(rules, in, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.After["deepseek-v4-flash"] != "deepseek-flash" || plan.After["manual"] != "raw-manual" || plan.After["wildcard-*"] != "*" {
		t.Fatalf("unexpected mapping result: %#v", plan.After)
	}
	if len(plan.UnreviewedModels) != 2 || plan.UnreviewedModels[0] != "GLM-5.2" || plan.UnreviewedModels[1] != "deepseek-flash-2026-01-01" {
		t.Fatalf("unknown/date IDs must remain review candidates: %#v", plan.UnreviewedModels)
	}
	other := in
	other.Source = Source{Platform: "anthropic", AccountType: "apikey", APIProtocol: "anthropic", BaseURL: sourceFixture().BaseURL, AccountID: 9001}
	otherPlan, err := Normalize(rules, other, state.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := otherPlan.After["deepseek-v4-flash"]; exists {
		t.Fatal("alias leaked across source identity")
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
