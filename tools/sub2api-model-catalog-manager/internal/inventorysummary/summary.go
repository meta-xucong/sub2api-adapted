package inventorysummary

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/officialcatalog"
)

const SchemaVersion = 1

var (
	sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	buildPattern  = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,80}$`)
)

type Counts struct {
	Accounts                         int `json:"accounts"`
	MappingPairs                     int `json:"mapping_pairs"`
	UniquePublicIDs                  int `json:"unique_public_ids"`
	UniqueUpstreamIDs                int `json:"unique_upstream_ids"`
	LiveDiscoveredObservations       int `json:"live_discovered_observations"`
	AccountsWithLiveLists            int `json:"accounts_with_live_lists"`
	AccountsWithoutLiveLists         int `json:"accounts_without_live_lists"`
	AccountsWithoutPairedFingerprint int `json:"accounts_without_paired_source_fingerprint"`
}

type AccountLabel struct {
	ID          int64  `json:"account_id"`
	Platform    string `json:"platform,omitempty"`
	AccountType string `json:"account_type,omitempty"`
	APIProtocol string `json:"api_protocol,omitempty"`
}

type PublicRouteMember struct {
	AccountLabel
	UpstreamID string `json:"upstream_id"`
}

type PublicIDRoute struct {
	PublicID string              `json:"public_id"`
	Accounts []PublicRouteMember `json:"accounts"`
	Meaning  string              `json:"meaning"`
}

type UpstreamReuseMember struct {
	AccountLabel
	PublicIDs []string `json:"public_ids"`
}

type UpstreamIDReuse struct {
	UpstreamID string                `json:"upstream_id"`
	Accounts   []UpstreamReuseMember `json:"accounts"`
	Meaning    string                `json:"meaning"`
}

type DiscoveredReuseMember struct {
	AccountLabel
}

type DiscoveredIDReuse struct {
	ModelID  string                  `json:"model_id"`
	Accounts []DiscoveredReuseMember `json:"accounts"`
	Meaning  string                  `json:"meaning"`
}

type MappingConflict struct {
	AccountLabel
	PublicID    string   `json:"public_id"`
	UpstreamIDs []string `json:"upstream_ids"`
	Meaning     string   `json:"meaning"`
}

type Report struct {
	SchemaVersion                  int                 `json:"schema_version"`
	ReportID                       string              `json:"report_id"`
	CreatedAt                      time.Time           `json:"created_at"`
	AccountScope                   string              `json:"account_scope"`
	AccountScanStatus              string              `json:"account_scan_status"`
	ReportedTargetBuildFingerprint string              `json:"reported_target_build_fingerprint"`
	AccountInventorySHA256         string              `json:"account_inventory_sha256"`
	InspectCreatedAt               time.Time           `json:"inspect_created_at"`
	DiscoverCreatedAt              time.Time           `json:"discover_created_at"`
	InspectWhitelistSHA256         string              `json:"inspect_input_whitelist_sha256"`
	DiscoverWhitelistSHA256        string              `json:"discover_input_whitelist_sha256"`
	CatalogCoverage                string              `json:"catalog_fetch_coverage"`
	Counts                         Counts              `json:"counts"`
	PublicIDRoutes                 []PublicIDRoute     `json:"public_id_routes"`
	UpstreamIDReuse                []UpstreamIDReuse   `json:"upstream_id_reuse"`
	DiscoveredIDReuse              []DiscoveredIDReuse `json:"discovered_id_reuse"`
	IntraAccountMappingConflicts   []MappingConflict   `json:"intra_account_mapping_conflicts"`
	Warnings                       []string            `json:"warnings"`
}

const (
	publicRouteMeaning     = "the exact public ID occurs in multiple account mappings; this does not prove account eligibility, availability, equivalence, or native scheduler selection"
	upstreamReuseMeaning   = "the exact upstream ID text occurs in mappings for multiple accounts; semantic equivalence and route availability are unproven"
	discoveredReuseMeaning = "the exact model ID appeared in live-list responses from multiple accounts; completeness, semantic equivalence, and future availability are unproven"
	mappingConflictMeaning = "one account report contains multiple upstream targets for one public ID; review the input report before using it as evidence"
)

// Build validates a paired, full-account inspect/discover snapshot and groups
// only exact strings. It has no API or credential dependencies.
func Build(inspect, discover officialcatalog.Inventory, now time.Time, reportID string) (Report, error) {
	var out Report
	if err := validatePair(inspect, discover); err != nil {
		return out, err
	}
	if now.IsZero() || reportID == "" {
		return out, errors.New("summary report metadata is invalid")
	}

	accountLabels := make(map[int64]AccountLabel, len(inspect.Accounts))
	for _, account := range inspect.Accounts {
		accountLabels[account.ID] = AccountLabel{ID: account.ID, Platform: account.Platform, AccountType: account.AccountType, APIProtocol: account.APIProtocol}
	}

	publicAccounts := make(map[string]map[int64]map[string]bool)
	upstreamAccounts := make(map[string]map[int64]map[string]bool)
	publicIDs := make(map[string]bool)
	upstreamIDs := make(map[string]bool)
	tripleSeen := make(map[string]bool)
	for _, edge := range inspect.Mappings {
		if !tripleSeen[edgeKey(edge.AccountID, edge.PublicID, edge.UpstreamID)] {
			tripleSeen[edgeKey(edge.AccountID, edge.PublicID, edge.UpstreamID)] = true
		}
		publicIDs[edge.PublicID] = true
		upstreamIDs[edge.UpstreamID] = true
		if publicAccounts[edge.PublicID] == nil {
			publicAccounts[edge.PublicID] = make(map[int64]map[string]bool)
		}
		if publicAccounts[edge.PublicID][edge.AccountID] == nil {
			publicAccounts[edge.PublicID][edge.AccountID] = make(map[string]bool)
		}
		publicAccounts[edge.PublicID][edge.AccountID][edge.UpstreamID] = true
		if upstreamAccounts[edge.UpstreamID] == nil {
			upstreamAccounts[edge.UpstreamID] = make(map[int64]map[string]bool)
		}
		if upstreamAccounts[edge.UpstreamID][edge.AccountID] == nil {
			upstreamAccounts[edge.UpstreamID][edge.AccountID] = make(map[string]bool)
		}
		upstreamAccounts[edge.UpstreamID][edge.AccountID][edge.PublicID] = true
	}

	conflictPairs := make(map[string]bool)
	conflicts := make([]MappingConflict, 0)
	for publicID, perAccount := range publicAccounts {
		for accountID, targets := range perAccount {
			if len(targets) < 2 {
				continue
			}
			ids := sortedBoolKeys(targets)
			conflicts = append(conflicts, MappingConflict{AccountLabel: accountLabels[accountID], PublicID: publicID, UpstreamIDs: ids, Meaning: mappingConflictMeaning})
			conflictPairs[publicID] = true
		}
	}

	publicRoutes := make([]PublicIDRoute, 0)
	for publicID, perAccount := range publicAccounts {
		if conflictPairs[publicID] || len(perAccount) < 2 {
			continue
		}
		members := make([]PublicRouteMember, 0, len(perAccount))
		for accountID, targets := range perAccount {
			if len(targets) != 1 {
				continue
			}
			members = append(members, PublicRouteMember{AccountLabel: accountLabels[accountID], UpstreamID: sortedBoolKeys(targets)[0]})
		}
		sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
		if len(members) >= 2 {
			publicRoutes = append(publicRoutes, PublicIDRoute{PublicID: publicID, Accounts: members, Meaning: publicRouteMeaning})
		}
	}
	sort.Slice(publicRoutes, func(i, j int) bool { return publicRoutes[i].PublicID < publicRoutes[j].PublicID })

	upstreamReuse := make([]UpstreamIDReuse, 0)
	for upstreamID, perAccount := range upstreamAccounts {
		if len(perAccount) < 2 {
			continue
		}
		members := make([]UpstreamReuseMember, 0, len(perAccount))
		for accountID, pubs := range perAccount {
			members = append(members, UpstreamReuseMember{AccountLabel: accountLabels[accountID], PublicIDs: sortedBoolKeys(pubs)})
		}
		sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
		upstreamReuse = append(upstreamReuse, UpstreamIDReuse{UpstreamID: upstreamID, Accounts: members, Meaning: upstreamReuseMeaning})
	}
	sort.Slice(upstreamReuse, func(i, j int) bool { return upstreamReuse[i].UpstreamID < upstreamReuse[j].UpstreamID })

	modelAccounts := make(map[string]map[int64]bool)
	liveObservationSeen := make(map[string]bool)
	liveLists := 0
	for _, row := range discover.DiscoveryRecords {
		if row.LiveListAvailable {
			liveLists++
			for _, modelID := range row.Models {
				key := fmt.Sprintf("%d\x00%s", row.AccountID, modelID)
				if liveObservationSeen[key] {
					continue
				}
				liveObservationSeen[key] = true
				if modelAccounts[modelID] == nil {
					modelAccounts[modelID] = make(map[int64]bool)
				}
				modelAccounts[modelID][row.AccountID] = true
			}
		}
	}
	discoveredReuse := make([]DiscoveredIDReuse, 0)
	for modelID, perAccount := range modelAccounts {
		if len(perAccount) < 2 {
			continue
		}
		members := make([]DiscoveredReuseMember, 0, len(perAccount))
		for accountID := range perAccount {
			members = append(members, DiscoveredReuseMember{AccountLabel: accountLabels[accountID]})
		}
		sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
		discoveredReuse = append(discoveredReuse, DiscoveredIDReuse{ModelID: modelID, Accounts: members, Meaning: discoveredReuseMeaning})
	}
	sort.Slice(discoveredReuse, func(i, j int) bool { return discoveredReuse[i].ModelID < discoveredReuse[j].ModelID })
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].ID != conflicts[j].ID {
			return conflicts[i].ID < conflicts[j].ID
		}
		return conflicts[i].PublicID < conflicts[j].PublicID
	})

	coverage := "partial_or_skipped"
	if liveLists == inspect.Metadata.AccountInventoryCount {
		coverage = "all_accounts_returned_live_lists"
	}
	unpairedFingerprints := countUnpairedFingerprints(inspect.Accounts, discover.Accounts)
	withoutLive := inspect.Metadata.AccountInventoryCount - liveLists
	if withoutLive < 0 {
		return out, errors.New("discover report has an invalid live-list account count")
	}

	out = Report{
		SchemaVersion: SchemaVersion, ReportID: reportID, CreatedAt: now.UTC(), AccountScope: "all",
		AccountScanStatus: "complete", ReportedTargetBuildFingerprint: inspect.Metadata.TargetBuildFingerprint,
		AccountInventorySHA256: inspect.Metadata.AccountInventorySHA256,
		InspectCreatedAt:       mustTime(inspect.Metadata.CreatedAt), DiscoverCreatedAt: mustTime(discover.Metadata.CreatedAt),
		InspectWhitelistSHA256: inspect.Digest, DiscoverWhitelistSHA256: discover.Digest,
		CatalogCoverage: coverage,
		Counts: Counts{
			Accounts: inspect.Metadata.AccountInventoryCount, MappingPairs: len(tripleSeen),
			UniquePublicIDs: len(publicIDs), UniqueUpstreamIDs: len(upstreamIDs),
			LiveDiscoveredObservations: len(liveObservationSeen), AccountsWithLiveLists: liveLists,
			AccountsWithoutLiveLists: withoutLive, AccountsWithoutPairedFingerprint: unpairedFingerprints,
		},
		PublicIDRoutes: publicRoutes, UpstreamIDReuse: upstreamReuse, DiscoveredIDReuse: discoveredReuse,
		IntraAccountMappingConflicts: conflicts,
		Warnings: []string{
			"inspect and discover are separate reports, not an atomic snapshot; the shared account-ID inventory hash does not detect mapping or catalog changes between them",
			"groups contain exact case-sensitive text only; no semantic equivalence, provider route, model availability, or native scheduler selection is inferred",
			"catalog fetch coverage is not catalog completeness; missing groups in unobserved or partial catalogs do not imply model absence or retirement",
			"reported target build fingerprint is metadata consistency only, not independent live-server attestation",
		},
	}
	return out, nil
}

func validatePair(inspect, discover officialcatalog.Inventory) error {
	if inspect.Mode != "inspect" || discover.Mode != "discover" {
		return errors.New("summary requires one inspect report and one discover report")
	}
	if !validCompleteMetadata(inspect.Metadata) || !validCompleteMetadata(discover.Metadata) {
		return errors.New("summary requires complete all-account reports with valid inventory metadata")
	}
	if inspect.Metadata.TargetBuildFingerprint != discover.Metadata.TargetBuildFingerprint {
		return errors.New("summary input reports have different target build fingerprints")
	}
	if inspect.Metadata.AccountInventoryCount != discover.Metadata.AccountInventoryCount || inspect.Metadata.AccountInventorySHA256 != discover.Metadata.AccountInventorySHA256 {
		return errors.New("summary input reports have different account inventory snapshots")
	}
	inspectTime, err := time.Parse(time.RFC3339Nano, inspect.Metadata.CreatedAt)
	if err != nil || inspectTime.IsZero() {
		return errors.New("inspect report timestamp is invalid")
	}
	discoverTime, err := time.Parse(time.RFC3339Nano, discover.Metadata.CreatedAt)
	if err != nil || discoverTime.IsZero() {
		return errors.New("discover report timestamp is invalid")
	}
	inspectIDs, err := uniquePositiveIDs(inspect.AccountRowIDs)
	if err != nil || len(inspectIDs) != inspect.Metadata.AccountInventoryCount {
		return errors.New("inspect report account rows do not match the declared complete inventory")
	}
	discoverIDs, err := uniquePositiveIDs(discover.DiscoveryRowIDs)
	if err != nil || len(discoverIDs) != discover.Metadata.AccountInventoryCount {
		return errors.New("discover report account rows do not match the declared complete inventory")
	}
	if !sameIDSet(inspectIDs, discoverIDs) {
		return errors.New("summary input reports contain different account ID sets")
	}
	inspectHash, err := catalog.AccountIDsDigest(inspectIDs)
	if err != nil || inspectHash != inspect.Metadata.AccountInventorySHA256 {
		return errors.New("inspect report account inventory hash does not match its account ID set")
	}
	discoverHash, err := catalog.AccountIDsDigest(discoverIDs)
	if err != nil || discoverHash != discover.Metadata.AccountInventorySHA256 {
		return errors.New("discover report account inventory hash does not match its account ID set")
	}
	inspectAccounts := accountMap(inspect.Accounts)
	discoverAccounts := accountMap(discover.Accounts)
	for id, a := range inspectAccounts {
		b, ok := discoverAccounts[id]
		if !ok {
			return errors.New("summary input reports do not cover the same account IDs")
		}
		if a.SourceFingerprint != "" && b.SourceFingerprint != "" && a.SourceFingerprint != b.SourceFingerprint {
			return errors.New("summary input reports have conflicting account source fingerprints")
		}
	}
	if len(inspectAccounts) != len(inspectIDs) || len(discoverAccounts) != len(discoverIDs) {
		return errors.New("summary input reports contain duplicate or missing account rows")
	}
	if len(discover.DiscoveryRecords) != len(discoverIDs) || hasDuplicateDiscoveryIDs(discover.DiscoveryRecords) {
		return errors.New("discover report contains duplicate or missing discovery rows")
	}
	for _, mapping := range inspect.Mappings {
		if !containsID(inspectIDs, mapping.AccountID) {
			return errors.New("inspect report contains a mapping for an unknown account")
		}
	}
	for _, row := range discover.DiscoveryRecords {
		if !containsID(discoverIDs, row.AccountID) {
			return errors.New("discover report contains a catalog row for an unknown account")
		}
	}
	return nil
}

func validCompleteMetadata(m officialcatalog.InputMetadata) bool {
	if m.AccountScope != "all" || m.AccountScanStatus != "complete" || m.AccountInventoryCount <= 0 || !sha256Pattern.MatchString(m.AccountInventorySHA256) || !buildPattern.MatchString(m.TargetBuildFingerprint) {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, m.CreatedAt)
	return err == nil && !t.IsZero()
}

func uniquePositiveIDs(ids []int64) ([]int64, error) {
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, errors.New("invalid or duplicate account ID")
		}
		seen[id] = true
	}
	out := make([]int64, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func sameIDSet(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[int64]bool, len(a))
	for _, id := range a {
		set[id] = true
	}
	for _, id := range b {
		if !set[id] {
			return false
		}
	}
	return true
}

func accountMap(accounts []officialcatalog.InputAccount) map[int64]officialcatalog.InputAccount {
	out := make(map[int64]officialcatalog.InputAccount, len(accounts))
	for _, account := range accounts {
		out[account.ID] = account
	}
	return out
}

func countUnpairedFingerprints(inspect, discover []officialcatalog.InputAccount) int {
	inspectAccounts := accountMap(inspect)
	discoverAccounts := accountMap(discover)
	count := 0
	for id, account := range inspectAccounts {
		if account.SourceFingerprint == "" || discoverAccounts[id].SourceFingerprint == "" {
			count++
		}
	}
	return count
}

func hasDuplicateDiscoveryIDs(records []officialcatalog.DiscoveryRecord) bool {
	seen := make(map[int64]bool, len(records))
	for _, row := range records {
		if seen[row.AccountID] {
			return true
		}
		seen[row.AccountID] = true
	}
	return false
}

func containsID(ids []int64, target int64) bool {
	i := sort.Search(len(ids), func(i int) bool { return ids[i] >= target })
	return i < len(ids) && ids[i] == target
}

func edgeKey(accountID int64, publicID, upstreamID string) string {
	return fmt.Sprintf("%d\x00%s\x00%s", accountID, publicID, upstreamID)
}

func sortedBoolKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func mustTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t.UTC()
}

func Write(dir string, report Report) (string, error) {
	if dir == "" || report.SchemaVersion != SchemaVersion || report.ReportID == "" || report.CreatedAt.IsZero() {
		return "", errors.New("inventory summary report is invalid")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", errors.New("inventory summary serialization failed")
	}
	defer zero(data)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", errors.New("inventory summary output directory could not be created")
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return "", errors.New("inventory summary output ID could not be created")
	}
	name := "inventory-summary-" + report.CreatedAt.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(id) + ".json"
	path := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".inventory-summary-*.tmp")
	if err != nil {
		return "", errors.New("inventory summary temporary file could not be created")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return "", errors.New("inventory summary permissions could not be restricted")
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", errors.New("inventory summary write failed")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", errors.New("inventory summary flush failed")
	}
	if err := tmp.Close(); err != nil {
		return "", errors.New("inventory summary close failed")
	}
	if _, err := os.Stat(path); err == nil {
		return "", errors.New("inventory summary output name collision")
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", errors.New("inventory summary publish failed")
	}
	return path, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
