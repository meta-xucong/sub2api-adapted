package inventorysummary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/officialcatalog"
)

func TestBuildGroupsOnlyExactCrossAccountIDsAndReportsCoverage(t *testing.T) {
	inspect, discover := pairedInventories(t)
	report, err := Build(inspect, discover, time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), "report-test")
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.Accounts != 3 || report.Counts.MappingPairs != 8 || report.Counts.UniquePublicIDs != 6 || report.Counts.UniqueUpstreamIDs != 7 {
		t.Fatalf("unexpected mapping counts: %#v", report.Counts)
	}
	if report.Counts.LiveDiscoveredObservations != 3 || report.Counts.AccountsWithLiveLists != 2 || report.Counts.AccountsWithoutLiveLists != 1 || report.CatalogCoverage != "partial_or_skipped" {
		t.Fatalf("unexpected live catalog coverage: %#v %s", report.Counts, report.CatalogCoverage)
	}
	if len(report.PublicIDRoutes) != 1 || report.PublicIDRoutes[0].PublicID != "GPT" || len(report.PublicIDRoutes[0].Accounts) != 2 || report.PublicIDRoutes[0].Accounts[0].UpstreamID != "gpt-latest" || report.PublicIDRoutes[0].Accounts[1].UpstreamID != "gpt-5.6" {
		t.Fatalf("exact public mapping routes were not grouped correctly: %#v", report.PublicIDRoutes)
	}
	if len(report.UpstreamIDReuse) != 1 || report.UpstreamIDReuse[0].UpstreamID != "deepseek-v4" || len(report.UpstreamIDReuse[0].Accounts) != 2 {
		t.Fatalf("exact upstream ID reuse was not grouped correctly: %#v", report.UpstreamIDReuse)
	}
	if len(report.DiscoveredIDReuse) != 1 || report.DiscoveredIDReuse[0].ModelID != "model-A" || len(report.DiscoveredIDReuse[0].Accounts) != 2 {
		t.Fatalf("live discovered duplicates were not grouped correctly: %#v", report.DiscoveredIDReuse)
	}
	if len(report.IntraAccountMappingConflicts) != 1 || report.IntraAccountMappingConflicts[0].PublicID != "conflict" || len(report.IntraAccountMappingConflicts[0].UpstreamIDs) != 2 {
		t.Fatalf("intra-account mapping conflict was not isolated: %#v", report.IntraAccountMappingConflicts)
	}
	for _, route := range report.PublicIDRoutes {
		if route.PublicID == "conflict" || route.PublicID == "minimax-m3" || route.PublicID == "MiniMax-M3" {
			t.Fatalf("conflict or case-only IDs were incorrectly grouped: %#v", route)
		}
	}
	if len(report.Warnings) != 4 || !strings.Contains(report.Warnings[1], "native scheduler selection") {
		t.Fatalf("report warnings do not state the evidence boundary: %#v", report.Warnings)
	}
}

func TestBuildRejectsPartialAndMismatchedReports(t *testing.T) {
	inspect, discover := pairedInventories(t)
	tests := []struct {
		name   string
		mutate func(*officialcatalog.Inventory, *officialcatalog.Inventory)
	}{
		{name: "partial inspect", mutate: func(i, _ *officialcatalog.Inventory) { i.Metadata.AccountScanStatus = "partial" }},
		{name: "allowlist", mutate: func(i, _ *officialcatalog.Inventory) { i.Metadata.AccountScope = "allowlist" }},
		{name: "account hash mismatch", mutate: func(i, _ *officialcatalog.Inventory) { i.Metadata.AccountInventorySHA256 = strings.Repeat("f", 64) }},
		{name: "target build mismatch", mutate: func(_, d *officialcatalog.Inventory) { d.Metadata.TargetBuildFingerprint = "other-build" }},
		{name: "source fingerprint mismatch", mutate: func(_, d *officialcatalog.Inventory) { d.Accounts[0].SourceFingerprint = strings.Repeat("f", 64) }},
		{name: "account set mismatch", mutate: func(_, d *officialcatalog.Inventory) { d.DiscoveryRowIDs[0] = 99 }},
		{name: "duplicate discovery row", mutate: func(_, d *officialcatalog.Inventory) { d.DiscoveryRowIDs[1] = d.DiscoveryRowIDs[0] }},
		{name: "invalid hash case", mutate: func(i, _ *officialcatalog.Inventory) {
			i.Metadata.AccountInventorySHA256 = strings.ToUpper(i.Metadata.AccountInventorySHA256)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i, d := cloneInventory(inspect), cloneInventory(discover)
			tt.mutate(&i, &d)
			if _, err := Build(i, d, time.Now().UTC(), "report-test"); err == nil {
				t.Fatal("invalid report pair was accepted")
			}
		})
	}
}

func TestBuildRejectsMatchingButForgedInventoryHash(t *testing.T) {
	inspect, discover := pairedInventories(t)
	forgedHash := strings.Repeat("f", 64)
	inspect.Metadata.AccountInventorySHA256 = forgedHash
	discover.Metadata.AccountInventorySHA256 = forgedHash

	if _, err := Build(inspect, discover, time.Now().UTC(), "report-test"); err == nil || !strings.Contains(err.Error(), "account inventory hash does not match its account ID set") {
		t.Fatalf("matching forged inventory hash was not rejected by the recomputed-ID check: %v", err)
	}
}

func TestBuildCountsUnpairedFingerprintsAsUnverified(t *testing.T) {
	inspect, discover := pairedInventories(t)
	discover.Accounts[0].SourceFingerprint = ""
	report, err := Build(inspect, discover, time.Now().UTC(), "report-test")
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts.AccountsWithoutPairedFingerprint != 1 {
		t.Fatalf("missing fingerprint was not reported as unverified: %#v", report.Counts)
	}
}

func TestWriteIsAtomicPrivateAndDoesNotSerializeUnapprovedFields(t *testing.T) {
	inspect, discover := pairedInventories(t)
	report, err := Build(inspect, discover, time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), "report-test")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "private")
	path, err := Write(dir, report)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "base_url") || strings.Contains(string(data), "SECRET-SENTINEL") || strings.Contains(string(data), "example.invalid") {
		t.Fatal("unapproved input fields were serialized")
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Counts != report.Counts {
		t.Fatalf("serialized report changed counts: %#v != %#v", decoded.Counts, report.Counts)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("report permissions are broader than owner-only: %v", info.Mode().Perm())
	}
}

func TestWriteRejectsInvalidReportWithoutPublishing(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, Report{}); err == nil {
		t.Fatal("invalid report was written")
	}
	files, err := filepath.Glob(filepath.Join(dir, "inventory-summary-*.json"))
	if err != nil || len(files) != 0 {
		t.Fatalf("invalid report left published files: %v %v", files, err)
	}
}

func pairedInventories(t *testing.T) (officialcatalog.Inventory, officialcatalog.Inventory) {
	t.Helper()
	ids := []int64{1, 2, 3}
	hash, err := catalog.AccountIDsDigest(ids)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint1 := strings.Repeat("a", 64)
	fingerprint2 := strings.Repeat("b", 64)
	fingerprint3 := strings.Repeat("c", 64)
	accounts := []officialcatalog.InputAccount{
		{ID: 1, SourceFingerprint: fingerprint1, Platform: "openai", AccountType: "apikey", APIProtocol: "openai"},
		{ID: 2, SourceFingerprint: fingerprint2, Platform: "openai", AccountType: "apikey", APIProtocol: "openai"},
		{ID: 3, SourceFingerprint: fingerprint3, Platform: "anthropic", AccountType: "apikey", APIProtocol: "anthropic"},
	}
	meta := officialcatalog.InputMetadata{
		CreatedAt: "2026-10-12T11:00:00Z", AccountScope: "all", AccountScanStatus: "complete",
		AccountInventoryCount: 3, AccountInventorySHA256: hash, TargetBuildFingerprint: "verified-build-v1",
	}
	inspect := officialcatalog.Inventory{
		Mode: "inspect", Digest: strings.Repeat("1", 64), Metadata: meta,
		AccountRowIDs: append([]int64{}, ids...), Accounts: append([]officialcatalog.InputAccount{}, accounts...),
		Mappings: []officialcatalog.MappingEdge{
			{AccountID: 1, SourceFingerprint: fingerprint1, Platform: "openai", AccountType: "apikey", APIProtocol: "openai", PublicID: "GPT", UpstreamID: "gpt-latest"},
			{AccountID: 2, SourceFingerprint: fingerprint2, Platform: "openai", AccountType: "apikey", APIProtocol: "openai", PublicID: "GPT", UpstreamID: "gpt-5.6"},
			{AccountID: 1, SourceFingerprint: fingerprint1, PublicID: "same", UpstreamID: "deepseek-v4"},
			{AccountID: 2, SourceFingerprint: fingerprint2, PublicID: "other", UpstreamID: "deepseek-v4"},
			{AccountID: 3, SourceFingerprint: fingerprint3, PublicID: "MiniMax-M3", UpstreamID: "MiniMax-M3"},
			{AccountID: 2, SourceFingerprint: fingerprint2, PublicID: "minimax-m3", UpstreamID: "minimax-m3"},
			{AccountID: 1, SourceFingerprint: fingerprint1, PublicID: "conflict", UpstreamID: "target-one"},
			{AccountID: 1, SourceFingerprint: fingerprint1, PublicID: "conflict", UpstreamID: "target-two"},
		},
	}
	discoverMeta := meta
	discoverMeta.CreatedAt = "2026-10-12T11:05:00Z"
	discover := officialcatalog.Inventory{
		Mode: "discover", Digest: strings.Repeat("2", 64), Metadata: discoverMeta,
		DiscoveryRowIDs: append([]int64{}, ids...), Accounts: append([]officialcatalog.InputAccount{}, accounts...),
		DiscoveryRecords: []officialcatalog.DiscoveryRecord{
			{AccountID: 1, SourceFingerprint: fingerprint1, Status: "candidate_reported", LiveListAvailable: true, Models: []string{"model-A", "only-one"}},
			{AccountID: 2, SourceFingerprint: fingerprint2, Status: "candidate_reported", LiveListAvailable: true, Models: []string{"model-A"}},
			{AccountID: 3, SourceFingerprint: fingerprint3, Status: "catalog_unknown", LiveListAvailable: false, Models: []string{"model-A"}},
		},
	}
	return inspect, discover
}

func cloneInventory(in officialcatalog.Inventory) officialcatalog.Inventory {
	out := in
	out.AccountRowIDs = append([]int64(nil), in.AccountRowIDs...)
	out.DiscoveryRowIDs = append([]int64(nil), in.DiscoveryRowIDs...)
	out.Accounts = append([]officialcatalog.InputAccount(nil), in.Accounts...)
	out.Mappings = append([]officialcatalog.MappingEdge(nil), in.Mappings...)
	out.DiscoveryRecords = append([]officialcatalog.DiscoveryRecord(nil), in.DiscoveryRecords...)
	for i := range out.DiscoveryRecords {
		out.DiscoveryRecords[i].Models = append([]string(nil), in.DiscoveryRecords[i].Models...)
	}
	return out
}
