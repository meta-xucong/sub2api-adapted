package mergereview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/state"
)

func TestBuildReportsSameSourceDateFamilyWithoutApprovingIt(t *testing.T) {
	inspect, discover, source := reviewFixture(t)
	rules := catalog.Rules{
		SchemaVersion: 1,
		VersionFamilies: []catalog.VersionFamily{{
			Source:              source,
			PublicID:            "deepseek-v4-flash",
			PreferredUpstreamID: "deepseek-v4-flash-20261002",
			Members: []catalog.FamilyMember{
				{UpstreamID: "deepseek-v4-flash", ExactIDAction: "keep"},
				{UpstreamID: "deepseek-v4-flash-20261001", ExactIDAction: "hide"},
				{UpstreamID: "deepseek-v4-flash-20261002", ExactIDAction: "keep"},
			},
			Decision: "approved", Evidence: "operator-reviewed source evidence", ReviewedAt: "2026-10-12",
		}},
	}
	if err := rules.Validate(); err != nil {
		t.Fatal(err)
	}
	local := state.Empty()
	out, err := Build(inspect, discover, rules, strings.Repeat("a", 64), local, time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if out.RuleCounts.VersionFamilies != 1 || out.Summary.DateFamilyCandidates != 1 || out.Summary.CandidatesWithCompleteRule != 1 {
		t.Fatalf("unexpected review counts: %#v %#v", out.RuleCounts, out.Summary)
	}
	candidate := out.Accounts[0].DateFamilies[0]
	if candidate.PublicIDCandidate != "deepseek-v4-flash" || candidate.Equivalence != "unproven" || candidate.Action != "none" || !candidate.DecisionRequired {
		t.Fatalf("candidate was approved or changed: %#v", candidate)
	}
	if candidate.DiscoverFamilyChanges != 1 || candidate.UnmanagedHideTargets != 1 || candidate.NoMergeReason != "hide_or_rewrite_requires_explicit_adoption_of_unmanaged_mapping_keys" {
		t.Fatalf("missing apply/readiness blocker: %#v", candidate)
	}
	if len(candidate.Members) != 3 || candidate.Members[0].UpstreamID != "deepseek-v4-flash" {
		t.Fatalf("candidate member list is not exact and sorted: %#v", candidate.Members)
	}
}

func TestBuildDoesNotGroupWithoutExactPrefixOrValidDate(t *testing.T) {
	inspect, discover, _ := reviewFixture(t)
	discover.Discovery[0].Models = []string{"gpt-5.2-2026-02-31", "other-model-20261001", "deepseek-v4-pro-20261001"}
	rules := catalog.Rules{SchemaVersion: 1}
	out, err := Build(inspect, discover, rules, strings.Repeat("b", 64), state.Empty(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary.DateFamilyCandidates != 0 {
		t.Fatalf("unproven or invalid names were grouped: %#v", out.Accounts[0].DateFamilies)
	}
}

func TestBuildRejectsMismatchedOrPartialReports(t *testing.T) {
	inspect, discover, _ := reviewFixture(t)
	discover.AccountInventorySHA256 = strings.Repeat("f", 64)
	if _, err := Build(inspect, discover, catalog.Rules{SchemaVersion: 1}, strings.Repeat("c", 64), state.Empty(), time.Now()); err == nil {
		t.Fatal("mismatched report pair was accepted")
	}
	inspect, discover, _ = reviewFixture(t)
	inspect.AccountScanStatus = "partial"
	if _, err := Build(inspect, discover, catalog.Rules{SchemaVersion: 1}, strings.Repeat("c", 64), state.Empty(), time.Now()); err == nil {
		t.Fatal("partial report was accepted")
	}
}

func TestBuildRequiresSourceFingerprintForCandidateDiscovery(t *testing.T) {
	inspect, discover, _ := reviewFixture(t)
	discover.Discovery[0].SourceFingerprint = ""
	if _, err := Build(inspect, discover, catalog.Rules{SchemaVersion: 1}, strings.Repeat("e", 64), state.Empty(), time.Now()); err == nil {
		t.Fatal("candidate discovery without a source fingerprint was accepted")
	}

	inspect, discover, _ = reviewFixture(t)
	discover.Discovery[0].SourceFingerprint = strings.Repeat("f", 64)
	if _, err := Build(inspect, discover, catalog.Rules{SchemaVersion: 1}, strings.Repeat("e", 64), state.Empty(), time.Now()); err == nil {
		t.Fatal("candidate discovery with a mismatched source fingerprint was accepted")
	}
}

func TestBuildAllowsSkippedUnrestrictedAccountWithoutDiscoveryFingerprint(t *testing.T) {
	inspect, discover, _ := reviewFixture(t)
	inspect.Accounts[0].MappingStatus = "no_explicit_mapping"
	inspect.Accounts[0].MappingSHA256 = ""
	inspect.Accounts[0].Mappings = nil
	discover.Discovery[0].SourceFingerprint = ""
	discover.Discovery[0].Status = "skipped_unrestricted_mapping"
	discover.Discovery[0].LiveListAvailable = false
	discover.Discovery[0].Models = nil
	out, err := Build(inspect, discover, catalog.Rules{SchemaVersion: 1}, strings.Repeat("e", 64), state.Empty(), time.Now())
	if err != nil {
		t.Fatalf("skipped unrestricted account without a candidate should be accepted: %v", err)
	}
	if out.Summary.DateFamilyCandidates != 0 || len(out.Accounts[0].DateFamilies) != 0 || out.Accounts[0].ExcludedReason != "no_eligible_live_catalog" {
		t.Fatalf("skipped unrestricted account was treated as candidate evidence: %#v", out.Accounts[0])
	}
}

func TestBuildFailsClosedOnTamperedMappingHashAndCorruptState(t *testing.T) {
	inspect, discover, _ := reviewFixture(t)
	inspect.Accounts[0].MappingSHA256 = strings.Repeat("f", 64)
	if _, err := Build(inspect, discover, catalog.Rules{SchemaVersion: 1}, strings.Repeat("e", 64), state.Empty(), time.Now()); err == nil {
		t.Fatal("mapping snapshot with a mismatched hash was accepted")
	}

	inspect, discover, _ = reviewFixture(t)
	corrupt := state.State{Version: state.CurrentVersion + 1, Accounts: map[string]state.Account{}}
	if _, err := Build(inspect, discover, catalog.Rules{SchemaVersion: 1}, strings.Repeat("e", 64), corrupt, time.Now()); err == nil {
		t.Fatal("corrupt state was accepted")
	}
}

func TestNoExplicitMappingIsValidButCannotAuthorizeCandidateChanges(t *testing.T) {
	if err := validateMappingSnapshot(InspectAccount{ID: 12, MappingStatus: "no_explicit_mapping"}); err != nil {
		t.Fatalf("empty unrestricted mapping should remain a valid read-only observation: %v", err)
	}
	if err := validateMappingSnapshot(InspectAccount{ID: 12, MappingStatus: "no_explicit_mapping", Mappings: []Mapping{{PublicID: "model", UpstreamID: "model"}}}); err == nil {
		t.Fatal("no-explicit-mapping status with mapping rows was accepted")
	}
}

func TestReadReportAndWriteDropUnknownSecretsAndSourceURL(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "inspect.json")
	const sentinel = "MERGE-REVIEW-SECRET-SENTINEL"
	inspect, discover, _ := reviewFixture(t)
	body, err := json.Marshal(inspect)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.TrimSuffix(string(body), "}") + `,"admin_key":"` + sentinel + `","headers":{"authorization":"` + sentinel + `"}}`
	if err := os.WriteFile(input, []byte(encoded), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadReport(input)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Build(loaded, discover, catalog.Rules{SchemaVersion: 1}, strings.Repeat("d", 64), state.Empty(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(dir, "reports")
	path, err := Write(outputDir, out)
	if err != nil {
		t.Fatal(err)
	}
	result, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), sentinel) || strings.Contains(string(result), "base_url") || strings.Contains(string(result), "provider.example") {
		t.Fatal("merge review output exposed an unapproved input field")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.PathSeparator == '/' && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("report permissions are broader than owner-only: %v", info.Mode().Perm())
	}
}

func reviewFixture(t *testing.T) (InputReport, InputReport, catalog.Source) {
	t.Helper()
	source, err := catalog.SourceForAccount("openai", "apikey", "openai", "https://provider.example/v1", 9001)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := catalog.SourceFingerprint(source)
	idsHash, err := catalog.AccountIDsDigest([]int64{9001})
	if err != nil {
		t.Fatal(err)
	}
	mapping := map[string]string{
		"deepseek-v4-flash":          "deepseek-v4-flash",
		"deepseek-v4-flash-20261001": "deepseek-v4-flash-20261001",
	}
	mappingHash, err := catalog.MappingHash(mapping)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	inspect := InputReport{
		RunID: "inspect-test", CreatedAt: created, Mode: "inspect", AccountScope: "all", AccountScanStatus: "complete",
		AccountInventoryCount: 1, AccountInventorySHA256: idsHash, TargetFingerprint: "build-test",
		Accounts: []InspectAccount{{
			ID: 9001, Platform: source.Platform, AccountType: source.AccountType, APIProtocol: source.APIProtocol,
			BaseURL: source.BaseURL, SourceFingerprint: fingerprint, MappingSHA256: mappingHash, MappingStatus: "explicit_nonempty",
			Mappings: []Mapping{
				{PublicID: "deepseek-v4-flash", UpstreamID: "deepseek-v4-flash"},
				{PublicID: "deepseek-v4-flash-20261001", UpstreamID: "deepseek-v4-flash-20261001"},
			},
		}},
	}
	discover := InputReport{
		RunID: "discover-test", CreatedAt: created.Add(time.Minute), Mode: "discover", AccountScope: "all", AccountScanStatus: "complete",
		AccountInventoryCount: 1, AccountInventorySHA256: idsHash, TargetFingerprint: "build-test",
		Discovery: []DiscoveryAccount{{
			ID: 9001, SourceFingerprint: fingerprint, Status: "candidate_reported", LiveListAvailable: true,
			Models:  []string{"deepseek-v4-flash", "deepseek-v4-flash-20261001", "deepseek-v4-flash-20261002"},
			Changes: []MappingChange{{Action: "remove", PublicID: "deepseek-v4-flash-20261001", RuleKind: "version_family"}},
		}},
	}
	return inspect, discover, source
}
