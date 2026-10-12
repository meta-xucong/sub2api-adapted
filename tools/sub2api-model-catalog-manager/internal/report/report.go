package report

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Document struct {
	RunID                    string             `json:"run_id"`
	CreatedAt                time.Time          `json:"created_at"`
	Mode                     string             `json:"mode"`
	AccountScope             string             `json:"account_scope,omitempty"`
	AccountScanStatus        string             `json:"account_scan_status,omitempty"`
	AccountInventoryCount    int                `json:"account_inventory_count,omitempty"`
	AccountInventorySHA256   string             `json:"account_inventory_sha256,omitempty"`
	TargetFingerprint        string             `json:"target_build_fingerprint,omitempty"`
	RefreshRunStatus         string             `json:"refresh_run_status,omitempty"`
	RefreshFinishedAt        *time.Time         `json:"refresh_finished_at,omitempty"`
	NativeSyncSideEffect     bool               `json:"native_sync_side_effect,omitempty"`
	ManualNativeSyncOverride bool               `json:"manual_native_sync_override,omitempty"`
	Accounts                 []Account          `json:"accounts"`
	Discovery                []DiscoveryAccount `json:"discovery,omitempty"`
}

type Account struct {
	ID                 int64               `json:"account_id"`
	Platform           string              `json:"platform"`
	Type               string              `json:"account_type"`
	Protocol           string              `json:"api_protocol,omitempty"`
	BaseURL            string              `json:"base_url,omitempty"`
	SourceFingerprint  string              `json:"source_fingerprint,omitempty"`
	MappingSHA256      string              `json:"mapping_sha256"`
	MappingStatus      string              `json:"mapping_status"`
	Status             string              `json:"status,omitempty"`
	InspectionStatus   string              `json:"inspection_status"`
	CredentialShadow   bool                `json:"credential_shadow"`
	Mappings           []Mapping           `json:"mappings"`
	CatalogDiagnostics *CatalogDiagnostics `json:"catalog_diagnostics,omitempty"`
}

type Mapping struct {
	PublicID    string `json:"public_id"`
	UpstreamID  string `json:"upstream_id"`
	Disposition string `json:"disposition"`
}

type DiscoveryAccount struct {
	ID                    int64               `json:"account_id"`
	NativeRefreshProfile  string              `json:"native_refresh_source_profile_id,omitempty"`
	NativeRefreshStatus   string              `json:"native_refresh_account_status,omitempty"`
	SourceFingerprint     string              `json:"source_fingerprint,omitempty"`
	SourceProfileStatus   string              `json:"source_profile_status"`
	IdentityAddPolicy     string              `json:"identity_add_policy,omitempty"`
	IdentityCatalogPolicy string              `json:"identity_catalog_policy,omitempty"`
	Status                string              `json:"status"`
	Reason                string              `json:"reason,omitempty"`
	CatalogHash           string              `json:"catalog_hash,omitempty"`
	CatalogObservedAt     *time.Time          `json:"catalog_observed_at,omitempty"`
	CatalogCompleteness   string              `json:"catalog_completeness"`
	LiveListAvailable     bool                `json:"live_list_available"`
	Warnings              []string            `json:"warning_codes,omitempty"`
	Models                []string            `json:"models,omitempty"`
	Changes               []MappingChange     `json:"proposed_changes,omitempty"`
	Conflicts             []string            `json:"conflicts,omitempty"`
	UnreviewedModels      []string            `json:"unreviewed_models,omitempty"`
	CatalogDiagnostics    *CatalogDiagnostics `json:"catalog_diagnostics,omitempty"`
}

// CatalogDiagnostics is a read-only comparison of account mapping IDs, the
// native stored snapshot, and (when present) this run's live directory. Lists
// use exact, case-sensitive IDs and are sorted before report serialization.
type CatalogDiagnostics struct {
	SnapshotContractStatus        string     `json:"snapshot_contract_status"`
	SnapshotStatus                string     `json:"snapshot_status,omitempty"`
	SnapshotLastAttemptAt         *time.Time `json:"snapshot_last_attempt_at,omitempty"`
	SnapshotLastSuccessAt         *time.Time `json:"snapshot_last_success_at,omitempty"`
	SnapshotNextRefreshAt         *time.Time `json:"snapshot_next_refresh_at,omitempty"`
	SnapshotRawDigest             string     `json:"snapshot_raw_digest,omitempty"`
	MappingCount                  int        `json:"mapping_count"`
	SnapshotRawCount              int        `json:"snapshot_raw_count"`
	SnapshotPublicCount           int        `json:"snapshot_public_count"`
	SnapshotPublicToUpstreamCount int        `json:"snapshot_public_to_upstream_count"`
	SnapshotEffectivePublicCount  int        `json:"snapshot_effective_public_count"`
	SnapshotRefreshOverdue        *bool      `json:"snapshot_refresh_overdue,omitempty"`
	LiveCount                     int        `json:"live_count,omitempty"`
	HasLiveCatalog                bool       `json:"has_live_catalog"`
	DuplicateRawIDs               []string   `json:"duplicate_raw_ids,omitempty"`
	DuplicatePublicIDs            []string   `json:"duplicate_public_ids,omitempty"`
	MappingPublicOnlyVsPublic     []string   `json:"mapping_public_only_vs_public,omitempty"`
	PublicOnlyVsMappingPublic     []string   `json:"public_only_vs_mapping_public,omitempty"`
	MappingUpstreamOnlyVsRaw      []string   `json:"mapping_upstream_only_vs_raw,omitempty"`
	RawOnlyVsMappingUpstream      []string   `json:"raw_only_vs_mapping_upstream,omitempty"`
	RawOnlyVsPublic               []string   `json:"raw_only_vs_public,omitempty"`
	PublicOnlyVsRaw               []string   `json:"public_only_vs_raw,omitempty"`
	LiveOnlyVsPublic              []string   `json:"live_only_vs_public,omitempty"`
	PublicOnlyVsLive              []string   `json:"public_only_vs_live,omitempty"`
	UnmappedTargetCount           int        `json:"unmapped_public_to_upstream_target_count"`
	Observation                   string     `json:"observation"`
}

type MappingChange struct {
	Action   string `json:"action"`
	PublicID string `json:"public_id"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after,omitempty"`
	RuleKind string `json:"rule_kind"`
	Evidence string `json:"evidence,omitempty"`
	Reason   string `json:"reason"`
}

func New(mode string) (Document, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return Document{}, errors.New("could not create report ID")
	}
	return Document{RunID: hex.EncodeToString(b), CreatedAt: time.Now().UTC(), Mode: mode}, nil
}

func (d *Document) Sort() {
	sort.Slice(d.Accounts, func(i, j int) bool { return d.Accounts[i].ID < d.Accounts[j].ID })
	sort.Slice(d.Discovery, func(i, j int) bool { return d.Discovery[i].ID < d.Discovery[j].ID })
	for i := range d.Accounts {
		sort.Slice(d.Accounts[i].Mappings, func(a, b int) bool { return d.Accounts[i].Mappings[a].PublicID < d.Accounts[i].Mappings[b].PublicID })
	}
}

func Write(dir string, d Document) (string, error) {
	if d.RunID == "" || d.Mode == "" {
		return "", errors.New("invalid report")
	}
	d.Sort()
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", errors.New("report serialization failed")
	}
	defer zero(data)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", errors.New("report directory could not be created")
	}
	name := d.Mode + "-" + d.CreatedAt.UTC().Format("20060102T150405Z") + "-" + d.RunID + ".json"
	path := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".report-*.tmp")
	if err != nil {
		return "", errors.New("temporary report file could not be created")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return "", errors.New("report permissions could not be restricted")
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", errors.New("report write failed")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", errors.New("report flush failed")
	}
	if err := tmp.Close(); err != nil {
		return "", errors.New("report close failed")
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("report atomic publish failed: %w", err)
	}
	return path, nil
}

func WritePlan(dir string, body []byte) (string, string, error) {
	if len(body) == 0 || len(body) > 4<<20 {
		return "", "", errors.New("plan payload is empty or too large")
	}
	var plan map[string]json.RawMessage
	if err := json.Unmarshal(body, &plan); err != nil || plan == nil {
		return "", "", errors.New("plan payload is invalid")
	}
	var version int
	if err := json.Unmarshal(plan["schema_version"], &version); err != nil || version == 0 {
		return "", "", errors.New("plan payload is missing a schema version")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", errors.New("plan directory could not be created")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", "", errors.New("could not create plan file ID")
	}
	name := "plan-" + time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(id) + ".json"
	path := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".plan-*.tmp")
	if err != nil {
		return "", "", errors.New("temporary plan file could not be created")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return "", "", errors.New("plan permissions could not be restricted")
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return "", "", errors.New("plan write failed")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", "", errors.New("plan flush failed")
	}
	if err := tmp.Close(); err != nil {
		return "", "", errors.New("plan close failed")
	}
	if _, err := os.Stat(path); err == nil {
		return "", "", errors.New("plan filename collision")
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", "", errors.New("plan atomic publish failed")
	}
	h := sha256.Sum256(body)
	return path, hex.EncodeToString(h[:]), nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
