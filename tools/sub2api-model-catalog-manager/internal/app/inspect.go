package app

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"sub2api-model-catalog-manager/internal/adminapi"
	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/config"
	"sub2api-model-catalog-manager/internal/report"
	"sub2api-model-catalog-manager/internal/securecred"
)

type CredentialReader func(config.CredentialConfig) (securecred.Value, error)
type AdminAPI interface {
	SetAPIKey([]byte) error
	RefreshStatus(context.Context) (adminapi.RefreshStatus, error)
	ListAllAccounts(context.Context) ([]adminapi.AccountSummary, error)
	Account(context.Context, int64) (adminapi.Account, error)
	SyncUpstream(context.Context, int64) (adminapi.Catalog, error)
	BulkUpdateMapping(context.Context, int64, map[string]string) (adminapi.BulkUpdateResult, error)
	Close()
}

type publishedSnapshotReader interface {
	AccountWithPublishedSnapshot(context.Context, int64, string, string) (adminapi.Account, error)
}

type ClientFactory func(string) (AdminAPI, error)

type Inspector struct {
	ReadCredential CredentialReader
	NewClient      ClientFactory
}

// Preflight validates all local configuration and credential-exposure gates
// before any credential read or remote request.
func (i Inspector) Preflight(cfg config.Config) error {
	if cfg.AllAccounts() && !cfg.RuntimeCompatible() {
		return errors.New("all-account inspection blocked: target account-list API contract is not verified")
	}
	eligible := cfg.EligibleAccountIDs()
	if cfg.AllAccounts() && !cfg.AcceptAllCredentialResponseMemoryExposure {
		return errors.New("all-account mode is blocked: credential-response memory exposure was not explicitly accepted for all accounts")
	}
	if !cfg.AllAccounts() && len(eligible) > 0 {
		if err := cfg.ExposureGateFor(eligible); err != nil {
			return err
		}
	}
	if len(eligible) > 0 && (i.ReadCredential == nil || i.NewClient == nil) {
		return errors.New("inspection dependencies are unavailable")
	}
	return nil
}

func (i Inspector) Inspect(ctx context.Context, cfg config.Config) (report.Document, error) {
	if err := i.Preflight(cfg); err != nil {
		return report.Document{}, err
	}
	doc, err := report.New("inspect")
	if err != nil {
		return report.Document{}, err
	}
	doc.TargetFingerprint = cfg.Target.RuntimeCompatibility.BuildID
	doc.AccountScope = cfg.AccountScopeValue()
	if cfg.AllAccounts() {
		doc.AccountScanStatus = "complete"
	}
	eligible := cfg.EligibleAccountIDs()
	if !cfg.AllAccounts() && len(eligible) == 0 {
		for _, account := range cfg.Accounts {
			doc.Accounts = append(doc.Accounts, report.Account{ID: account.ID, InspectionStatus: "blocked_credential_memory_exposure_not_accepted", Mappings: []report.Mapping{}})
		}
		doc.Sort()
		return doc, nil
	}
	cred, err := i.ReadCredential(cfg.Credential)
	if err != nil {
		return report.Document{}, errors.New("administrator credential could not be read from the configured OS store")
	}
	defer cred.Clear()
	client, err := i.NewClient(cfg.Target.BaseURL)
	if err != nil {
		return report.Document{}, err
	}
	defer client.Close()
	if err := client.SetAPIKey(cred.APIKey); err != nil {
		return report.Document{}, err
	}
	status, err := client.RefreshStatus(ctx)
	if err != nil {
		return report.Document{}, err
	}
	doc.RefreshRunStatus = safeStatus(status.RunStatus)
	doc.RefreshFinishedAt = status.LastFinishedAt
	scope, err := resolveAccountScope(ctx, cfg, client)
	if err != nil {
		return report.Document{}, err
	}
	doc.AccountInventoryCount = scope.Count
	doc.AccountInventorySHA256 = scope.SHA256
	for _, id := range scope.IDs {
		account, err := accountForCatalogDiagnostics(ctx, client, id, cfg)
		if err != nil {
			if cfg.AllAccounts() {
				doc.AccountScanStatus = "partial"
				doc.Accounts = append(doc.Accounts, report.Account{ID: id, InspectionStatus: "account_read_failed", Mappings: []report.Mapping{}})
				continue
			}
			return report.Document{}, err
		}
		row := report.Account{
			ID: account.ID, Platform: account.Platform, Type: account.Type,
			Protocol: account.Protocol, BaseURL: safeBaseURL(account.BaseURL),
			Status: account.Status, InspectionStatus: "inspected", CredentialShadow: account.ParentAccountID != nil,
		}
		if account.HasModelMapping() {
			row.MappingStatus = "explicit_nonempty"
			row.MappingSHA256, err = catalog.MappingFingerprint(account.ModelMapping)
			if err != nil {
				return report.Document{}, errors.New("account mapping hash could not be computed")
			}
			if len(account.ModelMapping) == 0 {
				row.MappingStatus = "explicit_empty"
			}
		} else {
			row.MappingStatus = "no_explicit_mapping"
		}
		if source, sourceErr := catalog.SourceForAccount(account.Platform, account.Type, account.Protocol, account.BaseURL, account.ID); sourceErr == nil {
			row.SourceFingerprint = catalog.SourceFingerprint(source)
		}
		for publicID, upstreamID := range account.ModelMapping {
			disposition := "unreviewed"
			if strings.ContainsAny(publicID, "*?") {
				disposition = "manual_or_wildcard_preserved"
			}
			row.Mappings = append(row.Mappings, report.Mapping{PublicID: publicID, UpstreamID: upstreamID, Disposition: disposition})
		}
		row.CatalogDiagnostics = catalogDiagnostics(account.ModelMapping, account.PublishedSnapshot, nil, false, doc.CreatedAt)
		doc.Accounts = append(doc.Accounts, row)
	}
	if !cfg.AllAccounts() {
		for _, account := range cfg.Accounts {
			if !cfg.AcceptsCredentialMemoryExposure(account.ID) {
				doc.Accounts = append(doc.Accounts, report.Account{ID: account.ID, InspectionStatus: "blocked_credential_memory_exposure_not_accepted", Mappings: []report.Mapping{}})
			}
		}
	}
	doc.Sort()
	return doc, nil
}

func accountForCatalogDiagnostics(ctx context.Context, client AdminAPI, id int64, cfg config.Config) (adminapi.Account, error) {
	if reader, ok := client.(publishedSnapshotReader); ok {
		return reader.AccountWithPublishedSnapshot(ctx, id, cfg.Target.RuntimeCompatibility.BuildID, cfg.Target.RuntimeCompatibility.APIContractID)
	}
	account, err := client.Account(ctx, id)
	if err != nil {
		return adminapi.Account{}, err
	}
	if account.PublishedSnapshot == nil {
		account.PublishedSnapshot = &adminapi.PublishedSnapshot{ContractStatus: "not_read"}
	}
	return account, nil
}

func catalogDiagnostics(mapping map[string]string, snapshot *adminapi.PublishedSnapshot, live []string, liveAvailable bool, reportAt time.Time) *report.CatalogDiagnostics {
	d := &report.CatalogDiagnostics{
		SnapshotContractStatus: "not_read",
		MappingCount:           len(mapping),
		HasLiveCatalog:         liveAvailable,
		Observation:            "observations_only_no_mapping_change_proposed",
	}
	if liveAvailable {
		d.LiveCount = len(live)
	}
	if snapshot == nil {
		return d
	}
	d.SnapshotContractStatus = snapshot.ContractStatus
	d.SnapshotRawCount = snapshot.RawCount
	d.SnapshotPublicCount = snapshot.PublicCount
	d.SnapshotPublicToUpstreamCount = snapshot.PublicToUpstreamCount
	d.UnmappedTargetCount = snapshot.UnmappedTargetCount
	if snapshot.ContractStatus != adminapi.PublishedSnapshotContractStatusSupported {
		d.Observation = "snapshot_ids_withheld_due_to_unsupported_or_incomplete_contract"
		return d
	}
	if !validSnapshotDiagnosticMetadata(snapshot) {
		d.SnapshotContractStatus = adminapi.PublishedSnapshotContractStatusIncomplete
		d.Observation = "snapshot_ids_withheld_due_to_unsupported_or_incomplete_contract"
		return d
	}
	d.SnapshotStatus = snapshot.Status
	d.SnapshotLastAttemptAt = snapshot.LastAttemptAt
	d.SnapshotLastSuccessAt = snapshot.LastSuccessAt
	d.SnapshotNextRefreshAt = snapshot.NextRefreshAt
	d.SnapshotRawDigest = snapshot.RawDigest
	if !reportAt.IsZero() {
		due := !reportAt.Before(*snapshot.NextRefreshAt)
		d.SnapshotRefreshOverdue = &due
	}
	d.DuplicateRawIDs = append([]string{}, snapshot.DuplicateRawIDs...)
	d.DuplicatePublicIDs = append([]string{}, snapshot.DuplicatePublicIDs...)
	if snapshot.Status == "unsupported" || snapshot.Status == "error" {
		d.Observation = "snapshot_is_not_current_publication_evidence"
		return d
	}
	public := uniqueIDs(snapshot.PublicModels)
	raw := uniqueIDs(snapshot.RawModels)
	mappedPublic := make([]string, 0, len(mapping))
	mappedUpstream := make([]string, 0, len(mapping))
	for publicID, upstreamID := range mapping {
		mappedPublic = append(mappedPublic, publicID)
		mappedUpstream = append(mappedUpstream, upstreamID)
	}
	if snapshot.Status == "fresh" || snapshot.Status == "stale" {
		d.SnapshotEffectivePublicCount = len(public)
	} else {
		d.Observation = "snapshot_is_historical_and_not_current_publication_evidence"
	}
	d.MappingPublicOnlyVsPublic = difference(mappedPublic, public)
	d.PublicOnlyVsMappingPublic = difference(public, mappedPublic)
	d.MappingUpstreamOnlyVsRaw = difference(mappedUpstream, raw)
	d.RawOnlyVsMappingUpstream = difference(raw, mappedUpstream)
	d.RawOnlyVsPublic = difference(raw, public)
	d.PublicOnlyVsRaw = difference(public, raw)
	if liveAvailable {
		liveSet := uniqueIDs(live)
		d.LiveOnlyVsPublic = difference(liveSet, public)
		d.PublicOnlyVsLive = difference(public, liveSet)
	}
	return d
}

func validSnapshotDiagnosticMetadata(snapshot *adminapi.PublishedSnapshot) bool {
	if snapshot == nil || snapshot.Source != "upstream_models_endpoint" || snapshot.LastAttemptAt == nil || snapshot.NextRefreshAt == nil || !validSnapshotDiagnosticDigest(snapshot.RawDigest) {
		return false
	}
	switch snapshot.Status {
	case "fresh", "stale", "expired", "unsupported", "error":
		return true
	default:
		return false
	}
}

func validSnapshotDiagnosticDigest(value string) bool {
	const prefix = "sha256:"
	if len(value) != len(prefix)+64 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, r := range value[len(prefix):] {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func uniqueIDs(values []string) []string {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		seen[value] = true
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func difference(left, right []string) []string {
	set := make(map[string]bool, len(right))
	for _, value := range right {
		set[value] = true
	}
	out := make([]string, 0)
	for _, value := range left {
		if !set[value] {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func safeBaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

func safeStatus(s string) string {
	switch s {
	case "completed", "succeeded", "partial_failure", "running", "never_run", "disabled", "unavailable", "stale_running":
		return s
	default:
		return "unknown"
	}
}
