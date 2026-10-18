package app

import (
	"context"
	"errors"
	"net/url"
	"strings"

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

type ClientFactory func(string) (AdminAPI, error)

type Inspector struct {
	ReadCredential CredentialReader
	NewClient      ClientFactory
}

func (i Inspector) Inspect(ctx context.Context, cfg config.Config) (report.Document, error) {
	doc, err := report.New("inspect")
	if err != nil {
		return report.Document{}, err
	}
	doc.TargetFingerprint = cfg.Target.RuntimeCompatibility.BuildID
	doc.AccountScope = cfg.AccountScopeValue()
	if cfg.AllAccounts() {
		doc.AccountScanStatus = "complete"
	}
	if cfg.AllAccounts() && !cfg.RuntimeCompatible() {
		return report.Document{}, errors.New("all-account inspection blocked: target account-list API contract is not verified")
	}
	eligible := cfg.EligibleAccountIDs()
	if !cfg.AllAccounts() && len(eligible) == 0 {
		for _, account := range cfg.Accounts {
			doc.Accounts = append(doc.Accounts, report.Account{ID: account.ID, InspectionStatus: "blocked_credential_memory_exposure_not_accepted", Mappings: []report.Mapping{}})
		}
		doc.Sort()
		return doc, nil
	}
	if cfg.AllAccounts() && !cfg.AcceptAllCredentialResponseMemoryExposure {
		return report.Document{}, errors.New("all-account mode is blocked: credential-response memory exposure was not explicitly accepted for all accounts")
	}
	// This is deliberately checked before credential retrieval and before any network operation.
	if !cfg.AllAccounts() {
		if err := cfg.ExposureGateFor(eligible); err != nil {
			return report.Document{}, err
		}
	}
	if i.ReadCredential == nil || i.NewClient == nil {
		return report.Document{}, errors.New("inspection dependencies are unavailable")
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
		account, err := client.Account(ctx, id)
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
