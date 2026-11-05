package app

import (
	"context"
	"errors"

	"sub2api-model-catalog-manager/internal/catalog"
	"sub2api-model-catalog-manager/internal/config"
)

type AccountScopeSnapshot struct {
	IDs    []int64
	Count  int
	SHA256 string
}

func resolveAccountScope(ctx context.Context, cfg config.Config, client AdminAPI) (AccountScopeSnapshot, error) {
	if !cfg.AllAccounts() {
		ids := cfg.EligibleAccountIDs()
		if len(ids) == 0 {
			return AccountScopeSnapshot{}, errors.New("no account passed the credential-response memory gate")
		}
		return AccountScopeSnapshot{IDs: ids}, nil
	}
	if !cfg.AcceptAllCredentialResponseMemoryExposure {
		return AccountScopeSnapshot{}, errors.New("all-account mode is blocked: credential-response memory exposure was not explicitly accepted for all accounts")
	}
	summaries, err := client.ListAllAccounts(ctx)
	if err != nil {
		return AccountScopeSnapshot{}, err
	}
	ids := make([]int64, len(summaries))
	for i := range summaries {
		ids[i] = summaries[i].ID
	}
	digest, err := catalog.AccountIDsDigest(ids)
	if err != nil {
		return AccountScopeSnapshot{}, err
	}
	return AccountScopeSnapshot{IDs: ids, Count: len(ids), SHA256: digest}, nil
}
