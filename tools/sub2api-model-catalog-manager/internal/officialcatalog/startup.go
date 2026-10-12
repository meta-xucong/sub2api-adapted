package officialcatalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const cacheVersion = 2

type diskSnapshot struct {
	Version  int             `json:"version"`
	SourceID string          `json:"source_id"`
	Snapshot CatalogSnapshot `json:"snapshot"`
}

var (
	startupLocalLocks sync.Map
)

func SyncStartup(ctx context.Context, options StartupSyncOptions) StartupSyncResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.CacheDir == "" {
		cacheDir := options.userCacheDir
		if cacheDir == nil {
			cacheDir = os.UserCacheDir
		}
		if base, err := cacheDir(); err == nil {
			options.CacheDir = filepath.Join(base, "sub2api-model-catalog-manager", "official-lifecycle")
		}
	}
	result := StartupSyncResult{Providers: make([]ProviderSync, len(sourceManifest))}
	for i, spec := range sourceManifest {
		result.Providers[i] = ProviderSync{Provider: spec.provider, Status: "unavailable", SourceURL: spec.rootURL, Limitation: spec.limitation}
	}
	if options.Offline {
		for i, spec := range sourceManifest {
			result.Providers[i] = loadOffline(spec, options)
		}
		return result
	}
	if options.CacheDir == "" {
		for i, spec := range sourceManifest {
			result.Providers[i].ErrorCode = "private_cache_unavailable"
			result.Providers[i].Provider = spec.provider
		}
		return result
	}
	if err := validateConfiguredProxy(options.proxyResolver); err != nil {
		for i, spec := range sourceManifest {
			result.Providers[i].Provider = spec.provider
			result.Providers[i].ErrorCode = "proxy_configuration_rejected"
		}
		return result
	}
	globalTimeout := options.startupTimeout
	if globalTimeout <= 0 {
		globalTimeout = StartupSyncTimeout
	}
	global, cancel := context.WithTimeout(ctx, globalTimeout)
	defer cancel()
	if err := preparePersistentCache(global, options.CacheDir, options.acquireInitLock); err != nil {
		for i, spec := range sourceManifest {
			result.Providers[i].Provider = spec.provider
			result.Providers[i].ErrorCode = "private_cache_unavailable"
		}
		return result
	}
	factory := options.NewDoer
	if factory == nil {
		factory = func() HTTPDoer { return NewHTTPClient() }
	}
	doer := factory()
	if doer == nil {
		for i := range result.Providers {
			result.Providers[i].ErrorCode = "http_client_unavailable"
		}
		return result
	}
	budget := &requestBudget{}
	semaphore := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, spec := range sourceManifest {
		wg.Add(1)
		go func(index int, source sourceSpec) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-global.Done():
				result.Providers[index] = failureWithLastGood(source, options, "timeout")
				return
			}
			defer func() { <-semaphore }()
			result.Providers[index] = syncOneSource(global, source, options, doer, budget)
		}(i, spec)
	}
	wg.Wait()
	return result
}

func syncOneSource(ctx context.Context, spec sourceSpec, options StartupSyncOptions, doer HTTPDoer, budget *requestBudget) ProviderSync {
	started := options.Now().UTC()
	lockFn := options.acquireScopeLock
	if lockFn == nil {
		lockFn = acquireOSLock
	}
	lockCtx, cancel := context.WithTimeout(ctx, StartupSyncTimeout)
	defer cancel()
	lockPath := filepath.Join(options.CacheDir, spec.id, "sync.lock")
	if err := securePrivateDir(filepath.Dir(lockPath)); err != nil && !os.IsNotExist(err) {
		return failureWithLastGood(spec, options, "private_cache_unavailable")
	}
	before, beforeErr := readSourceSnapshot(options.CacheDir, spec)
	lock, err := lockFn(lockCtx, lockPath)
	if err != nil {
		return failureWithLastGood(spec, options, "cache_lock_timeout")
	}
	defer lock.Close()
	if err := securePrivateDir(filepath.Dir(lockPath)); err != nil {
		return failureWithLastGood(spec, options, "private_cache_unavailable")
	}
	prior, priorErr := readSourceSnapshot(options.CacheDir, spec)
	if lock.Waited() && priorErr == nil {
		changed := errors.Is(beforeErr, os.ErrNotExist) || beforeErr == nil && (prior.CheckedAt.After(before.CheckedAt) || prior.ContentSHA256 != before.ContentSHA256)
		if changed && !prior.CheckedAt.Before(started) {
			return providerSyncFromSnapshot(spec, prior, true, true, "coalesced")
		}
	}
	if errors.Is(priorErr, os.ErrNotExist) {
		prior = CatalogSnapshot{}
		priorErr = nil
	}
	if priorErr != nil {
		return ProviderSync{Provider: spec.provider, Status: "unavailable", ErrorCode: "cache_invalid", SourceURL: spec.rootURL, Limitation: spec.limitation}
	}
	var priorPtr *CatalogSnapshot
	if prior.Complete {
		priorPtr = &prior
	}
	snapshot, fetchErr := fetchSource(ctx, spec, priorPtr, doer, budget, options.Now)
	if fetchErr != nil {
		return statusFromError(spec, priorPtr, failureCode(fetchErr))
	}
	if err := validateSnapshot(spec, snapshot); err != nil {
		return statusFromError(spec, priorPtr, "source_parse_error")
	}
	status := "updated"
	if priorPtr != nil && prior.ContentSHA256 == snapshot.ContentSHA256 {
		status = "unchanged"
	}
	if err := writeSourceSnapshot(options.CacheDir, spec, snapshot); err != nil {
		return ProviderSync{Provider: spec.provider, Status: status, ErrorCode: "private_cache_write_failed", FetchedAt: timePtr(snapshot.FetchedAt), CheckedAt: timePtr(snapshot.CheckedAt), EvidenceCount: len(snapshot.Evidence), Snapshot: &snapshot, SourceURL: spec.rootURL, Limitation: spec.limitation}
	}
	return providerSyncFromSnapshot(spec, snapshot, true, false, status)
}

func providerSyncFromSnapshot(spec sourceSpec, snapshot CatalogSnapshot, persisted, coalesced bool, status string) ProviderSync {
	return ProviderSync{Provider: spec.provider, Status: status, FetchedAt: timePtr(snapshot.FetchedAt), CheckedAt: timePtr(snapshot.CheckedAt), EvidenceCount: len(snapshot.Evidence), CachePersisted: persisted, Coalesced: coalesced, Snapshot: &snapshot, SourceURL: spec.rootURL, ContentSHA256: snapshot.ContentSHA256, PageTitle: firstPageTitle(snapshot.Pages), Limitation: spec.limitation}
}

func firstPageTitle(pages []CachedPage) string {
	for _, page := range pages {
		if page.Title != "" {
			return page.Title
		}
	}
	return ""
}

func statusFromError(spec sourceSpec, prior *CatalogSnapshot, code string) ProviderSync {
	if prior != nil && validateSnapshot(spec, *prior) == nil {
		out := providerSyncFromSnapshot(spec, *prior, true, false, "stale")
		out.ErrorCode = code
		out.CheckedAt = nil
		return out
	}
	return ProviderSync{Provider: spec.provider, Status: "unavailable", ErrorCode: code, SourceURL: spec.rootURL, Limitation: spec.limitation}
}

func failureWithLastGood(spec sourceSpec, options StartupSyncOptions, code string) ProviderSync {
	if options.Offline {
		return loadOffline(spec, options)
	}
	snapshot, err := readSourceSnapshot(options.CacheDir, spec)
	if err == nil && validateSnapshot(spec, snapshot) == nil {
		out := providerSyncFromSnapshot(spec, snapshot, true, false, "stale")
		out.ErrorCode = code
		return out
	}
	return ProviderSync{Provider: spec.provider, Status: "unavailable", ErrorCode: code, SourceURL: spec.rootURL, Limitation: spec.limitation}
}

func loadOffline(spec sourceSpec, options StartupSyncOptions) ProviderSync {
	if options.CacheDir == "" {
		if cacheDir := options.userCacheDir; cacheDir != nil {
			if base, err := cacheDir(); err == nil {
				options.CacheDir = filepath.Join(base, "sub2api-model-catalog-manager", "official-lifecycle")
			}
		} else if base, err := os.UserCacheDir(); err == nil {
			options.CacheDir = filepath.Join(base, "sub2api-model-catalog-manager", "official-lifecycle")
		}
	}
	snapshot, err := readSourceSnapshot(options.CacheDir, spec)
	if err != nil || validateSnapshot(spec, snapshot) != nil {
		return ProviderSync{Provider: spec.provider, Status: "offline_cache_missing", ErrorCode: "cache_missing", SourceURL: spec.rootURL, Limitation: spec.limitation}
	}
	return providerSyncFromSnapshot(spec, snapshot, true, false, "offline_cached")
}

func preparePersistentCache(ctx context.Context, root string, acquire func(context.Context, string) (osFileLock, error)) error {
	if ctx == nil || root == "" {
		return errors.New("private cache unavailable")
	}
	if err := secureCacheTree(root); err != nil {
		return err
	}
	if acquire == nil {
		acquire = acquireOSLock
	}
	lock, err := acquire(ctx, filepath.Join(root, ".init.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := secureCacheTree(root); err != nil {
		return err
	}
	for _, spec := range sourceManifest {
		dir := filepath.Join(root, spec.id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := securePrivateDir(dir); err != nil {
			return err
		}
	}
	return nil
}

func readSourceSnapshot(root string, spec sourceSpec) (CatalogSnapshot, error) {
	if root == "" {
		return CatalogSnapshot{}, errors.New("cache unavailable")
	}
	dir := filepath.Join(root, spec.id)
	if err := securePrivateDir(dir); err != nil {
		return CatalogSnapshot{}, err
	}
	path := filepath.Join(dir, "snapshot.json")
	if info, err := os.Lstat(path); err != nil {
		return CatalogSnapshot{}, err
	} else if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 8<<20 {
		return CatalogSnapshot{}, errors.New("cache file is invalid")
	}
	if err := securePrivateFile(path); err != nil {
		return CatalogSnapshot{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil || len(body) == 0 || len(body) > 8<<20 {
		return CatalogSnapshot{}, errors.New("cache file is invalid")
	}
	var disk diskSnapshot
	if json.Unmarshal(body, &disk) != nil || disk.Version != cacheVersion || disk.SourceID != spec.id || validateSnapshot(spec, disk.Snapshot) != nil {
		return CatalogSnapshot{}, errors.New("cache file is invalid")
	}
	return disk.Snapshot, nil
}

func writeSourceSnapshot(root string, spec sourceSpec, snapshot CatalogSnapshot) error {
	if root == "" || validateSnapshot(spec, snapshot) != nil {
		return errors.New("invalid source snapshot")
	}
	dir := filepath.Join(root, spec.id)
	if err := securePrivateDir(dir); err != nil {
		return err
	}
	body, err := json.Marshal(diskSnapshot{Version: cacheVersion, SourceID: spec.id, Snapshot: snapshot})
	if err != nil || len(body) > 8<<20 {
		return errors.New("source snapshot exceeds cache limit")
	}
	defer zero(body)
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := securePrivateFile(tmpPath); err != nil {
		return err
	}
	if err := atomicReplace(tmpPath, filepath.Join(dir, "snapshot.json")); err != nil {
		return err
	}
	return securePrivateFile(filepath.Join(dir, "snapshot.json"))
}

func validateSnapshot(spec sourceSpec, snapshot CatalogSnapshot) error {
	if !snapshot.Complete || snapshot.Provider != spec.provider || snapshot.FetchedAt.IsZero() || snapshot.CheckedAt.IsZero() || len(snapshot.Pages) == 0 || len(snapshot.Pages) > 1+spec.maxChildren {
		return errors.New("snapshot incomplete")
	}
	urls := map[string]bool{}
	for _, page := range snapshot.Pages {
		if validateSourceURL(spec, page.URL) != nil || page.Title == "" || page.ContentSHA256 == "" || page.FetchedAt.IsZero() || urls[page.URL] {
			return errors.New("snapshot page invalid")
		}
		urls[page.URL] = true
		if len(page.Links) > 1000 || len(page.Evidence) > MaxTotalModels {
			return errors.New("snapshot page exceeds limit")
		}
		for _, evidence := range page.Evidence {
			if evidence.Provider != spec.provider || !validModelID(evidence.ModelID) || !validLifecycleStatus(evidence.Status) || evidence.SourceURL != page.URL || evidence.SourceTitle != page.Title || evidence.ContentSHA256 != page.ContentSHA256 || len(evidence.Excerpt) > 360 {
				return errors.New("snapshot evidence invalid")
			}
		}
	}
	digest, err := snapshotDigest(snapshot.Pages)
	if err != nil || digest != snapshot.ContentSHA256 {
		return errors.New("snapshot hash mismatch")
	}
	return nil
}

func snapshotResults(results StartupSyncResult) ([]ProviderResult, []CatalogSnapshot) {
	providerResults := make([]ProviderResult, 0, len(sourceManifest))
	snapshots := make([]CatalogSnapshot, 0, len(sourceManifest))
	byProvider := make(map[string]ProviderSync, len(results.Providers))
	for _, result := range results.Providers {
		byProvider[result.Provider] = result
	}
	for _, spec := range sourceManifest {
		result, ok := byProvider[spec.provider]
		if !ok {
			result = ProviderSync{Provider: spec.provider, Status: "unavailable", ErrorCode: "sync_result_missing", SourceURL: spec.rootURL, Limitation: spec.limitation}
		}
		pr := ProviderResult{Provider: spec.provider, Status: result.Status, SyncStatus: result.Status, CachePersisted: result.CachePersisted, LastFetchedAt: result.FetchedAt, LastCheckedAt: result.CheckedAt, EvidenceCount: result.EvidenceCount, ErrorCode: result.ErrorCode, Limitation: spec.limitation, SourceURL: spec.rootURL, PageTitle: result.PageTitle, ContentSHA256: result.ContentSHA256}
		providerResults = append(providerResults, pr)
		if result.Snapshot != nil && result.Snapshot.Complete {
			snapshots = append(snapshots, *result.Snapshot)
		}
	}
	return providerResults, snapshots
}

func BuildReviewFromStartup(inventory Inventory, syncResult StartupSyncResult, now func() time.Time) (Report, error) {
	if inventory.Digest == "" || inventory.Mode != "inspect" && inventory.Mode != "discover" {
		return Report{}, errors.New("official review dependencies or inventory are invalid")
	}
	if now == nil {
		now = time.Now
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Report{}, errors.New("official review ID could not be created")
	}
	created := now().UTC()
	providerResults, snapshots := snapshotResults(syncResult)
	report := Report{
		SchemaVersion: SchemaVersion, ReportID: hex.EncodeToString(id[:]), CreatedAt: created,
		InputReportMode: inventory.Mode, InputDigest: inventory.Digest,
		ProviderResults: providerResults, Observations: matchObservations(inventory.Observations, snapshots),
		NameShape:     findNameShapeCandidates(inventory),
		GlobalWarning: "Official lifecycle documents are evidence only. No notice found means unknown, not active. This does not prove an intermediary route is retired or available; no mappings are changed automatically.",
	}
	sortReport(&report)
	return report, nil
}

func (r StartupSyncResult) Summary() string {
	parts := make([]string, 0, len(r.Providers))
	for _, provider := range r.Providers {
		part := fmt.Sprintf("%s=%s", provider.Provider, provider.Status)
		if provider.ErrorCode != "" {
			part += "(" + provider.ErrorCode + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

func timePtr(t time.Time) *time.Time { return &t }

func snapshotPageURLs(snapshot CatalogSnapshot) []string {
	urls := make([]string, 0, len(snapshot.Pages))
	for _, page := range snapshot.Pages {
		urls = append(urls, page.URL)
	}
	sort.Strings(urls)
	return urls
}
