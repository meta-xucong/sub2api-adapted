package service

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	accountPrioritySchedulingSettingCacheTTL  = 5 * time.Second
	accountPrioritySchedulingSettingDBTimeout = 2 * time.Second
)

type cachedAccountPrioritySchedulingSetting struct {
	enabled   bool
	expiresAt int64
}

var accountPrioritySchedulingSettingCache atomic.Pointer[cachedAccountPrioritySchedulingSetting]
var accountPrioritySchedulingSettingSF singleflight.Group

func storeAccountPrioritySchedulingEnabled(enabled bool) {
	accountPrioritySchedulingSettingSF.Forget(SettingKeyAccountPrioritySchedulingEnabled)
	accountPrioritySchedulingSettingCache.Store(&cachedAccountPrioritySchedulingSetting{
		enabled: enabled, expiresAt: time.Now().Add(accountPrioritySchedulingSettingCacheTTL).UnixNano(),
	})
}

func accountPrioritySchedulingEnabled(ctx context.Context, repo SettingRepository) bool {
	if cached := accountPrioritySchedulingSettingCache.Load(); cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cached.enabled
	}
	if repo == nil {
		return false
	}
	result, _, _ := accountPrioritySchedulingSettingSF.Do(SettingKeyAccountPrioritySchedulingEnabled, func() (any, error) {
		if cached := accountPrioritySchedulingSettingCache.Load(); cached != nil && time.Now().UnixNano() < cached.expiresAt {
			return cached.enabled, nil
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountPrioritySchedulingSettingDBTimeout)
		defer cancel()
		value, err := repo.GetValue(dbCtx, SettingKeyAccountPrioritySchedulingEnabled)
		if err != nil {
			storeAccountPrioritySchedulingEnabled(false)
			return false, err
		}
		enabled := strings.EqualFold(strings.TrimSpace(value), "true")
		storeAccountPrioritySchedulingEnabled(enabled)
		return enabled, nil
	})
	enabled, _ := result.(bool)
	return enabled
}

func accountPrioritySchedulingEnabledForService(ctx context.Context, settings *SettingService) bool {
	if settings == nil {
		return false
	}
	return accountPrioritySchedulingEnabled(ctx, settings.settingRepo)
}

func splitPrioritySchedulingAccounts(accounts []*Account, enabled bool) (preferred, regular []*Account) {
	if !enabled {
		return nil, accounts
	}
	preferred = make([]*Account, 0, len(accounts))
	regular = make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if account != nil && account.PriorityScheduling {
			preferred = append(preferred, account)
		} else {
			regular = append(regular, account)
		}
	}
	return preferred, regular
}

func prioritySchedulingCandidateSubset(accounts []*Account, enabled bool) []*Account {
	preferred, _ := splitPrioritySchedulingAccounts(accounts, enabled)
	if len(preferred) > 0 {
		return preferred
	}
	return accounts
}
