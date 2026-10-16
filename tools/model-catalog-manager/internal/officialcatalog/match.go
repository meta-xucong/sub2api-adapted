package officialcatalog

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

func matchObservations(observations []InputModel, catalogs []fetchedCatalog, _ time.Time) []Observation {
	byID := map[string][]Reference{}
	byAlias := map[string][]Reference{}
	for _, catalog := range catalogs {
		for _, model := range catalog.Models {
			base := Reference{
				Provider: model.Provider, ModelID: model.ModelID, Status: model.Status,
				Lifecycle: model.Lifecycle, DeprecatedAt: model.DeprecatedAt, RetiresAt: model.RetiresAt,
				ShutdownDate: model.ShutdownDate, OfflineTime: model.OfflineTime, Line: model.Line,
				Endpoints: append([]string{}, model.Endpoints...),
			}
			byID[model.ModelID] = append(byID[model.ModelID], base)
			for _, alias := range model.Aliases {
				ref := base
				ref.MatchKind = "official_alias_candidate"
				ref.Status = "official_alias_candidate"
				byAlias[alias] = append(byAlias[alias], ref)
			}
		}
	}
	byID = coalesceReferences(byID)
	byAlias = coalesceReferences(byAlias)
	out := make([]Observation, 0, len(observations))
	for _, item := range observations {
		refs := make([]Reference, 0)
		for _, ref := range byID[item.ModelID] {
			ref.MatchKind = "exact_id"
			refs = append(refs, ref)
		}
		refs = append(refs, byAlias[item.ModelID]...)
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].Provider != refs[j].Provider {
				return refs[i].Provider < refs[j].Provider
			}
			if refs[i].ModelID != refs[j].ModelID {
				return refs[i].ModelID < refs[j].ModelID
			}
			return refs[i].MatchKind < refs[j].MatchKind
		})
		refs = uniqueReferences(refs)
		status := "unknown"
		if len(refs) == 1 {
			status = refs[0].Status
		}
		if len(refs) > 1 {
			status = "multiple_oem_references"
		}
		out = append(out, Observation{
			AccountID: item.AccountID, SourceFingerprint: item.SourceFingerprint, Platform: item.Platform,
			AccountType: item.AccountType, APIProtocol: item.APIProtocol, ModelID: item.ModelID,
			ObservedAs: item.ObservedAs, Status: status, MultipleOEMRefs: len(refs) > 1,
			Matches: refs, Warning: "same exact text only; route is unknown",
		})
	}
	return out
}

func coalesceReferences(input map[string][]Reference) map[string][]Reference {
	for key, refs := range input {
		byPair := map[string]Reference{}
		for _, ref := range refs {
			pair := ref.Provider + "\x00" + ref.ModelID + "\x00" + ref.MatchKind
			existing, ok := byPair[pair]
			if !ok {
				byPair[pair] = ref
				continue
			}
			existing.Endpoints = uniqueStrings(append(existing.Endpoints, ref.Endpoints...))
			if existing.Lifecycle == "" {
				existing.Lifecycle = ref.Lifecycle
			}
			if existing.DeprecatedAt == "" {
				existing.DeprecatedAt = ref.DeprecatedAt
			}
			if existing.RetiresAt == "" {
				existing.RetiresAt = ref.RetiresAt
			}
			if existing.ShutdownDate == "" {
				existing.ShutdownDate = ref.ShutdownDate
			}
			if existing.OfflineTime == "" {
				existing.OfflineTime = ref.OfflineTime
			}
			if existing.Line == "" {
				existing.Line = ref.Line
			}
			byPair[pair] = existing
		}
		merged := make([]Reference, 0, len(byPair))
		for _, ref := range byPair {
			merged = append(merged, ref)
		}
		sort.Slice(merged, func(i, j int) bool {
			if merged[i].Provider != merged[j].Provider {
				return merged[i].Provider < merged[j].Provider
			}
			if merged[i].ModelID != merged[j].ModelID {
				return merged[i].ModelID < merged[j].ModelID
			}
			return merged[i].MatchKind < merged[j].MatchKind
		})
		input[key] = merged
	}
	return input
}

func uniqueReferences(values []Reference) []Reference {
	if len(values) < 2 {
		return values
	}
	seen := map[string]bool{}
	out := make([]Reference, 0, len(values))
	for _, value := range values {
		key := value.Provider + "\x00" + value.ModelID + "\x00" + value.MatchKind
		if !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
}

func findNameShapeCandidates(inventory Inventory) []NameShapeCandidate {
	byAccount := make(map[int64]map[string]bool)
	for _, item := range inventory.Observations {
		if byAccount[item.AccountID] == nil {
			byAccount[item.AccountID] = map[string]bool{}
		}
		byAccount[item.AccountID][item.ModelID] = true
	}
	out := make([]NameShapeCandidate, 0)
	seen := map[string]bool{}
	for accountID, ids := range byAccount {
		for id := range ids {
			prefix, ok := dateShapedPrefix(id)
			if !ok || !ids[prefix] {
				continue
			}
			key := strings.Join([]string{itoa(accountID), prefix, id}, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, NameShapeCandidate{AccountID: accountID, FullModelID: id, ObservedPrefix: prefix, Label: "name_shape_candidate", Action: "none", Equivalence: "unproven"})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AccountID != out[j].AccountID {
			return out[i].AccountID < out[j].AccountID
		}
		if out[i].ObservedPrefix != out[j].ObservedPrefix {
			return out[i].ObservedPrefix < out[j].ObservedPrefix
		}
		return out[i].FullModelID < out[j].FullModelID
	})
	return out
}

func dateShapedPrefix(id string) (string, bool) {
	if len(id) >= 9 {
		suffix := id[len(id)-8:]
		if allDigits(suffix) && id[len(id)-9] == '-' && len(id) > 9 {
			return id[:len(id)-9], true
		}
	}
	if len(id) >= 12 {
		suffix := id[len(id)-11:]
		if suffix[0] == '-' && suffix[5] == '-' && suffix[8] == '-' && allDigits(suffix[1:5]) && allDigits(suffix[6:8]) && allDigits(suffix[9:11]) && len(id) > 11 {
			return id[:len(id)-11], true
		}
	}
	return "", false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

func sortReport(r *Report) {
	sort.Slice(r.ProviderResults, func(i, j int) bool { return r.ProviderResults[i].Provider < r.ProviderResults[j].Provider })
	for i := range r.ProviderResults {
		sort.Slice(r.ProviderResults[i].Endpoints, func(a, b int) bool {
			return r.ProviderResults[i].Endpoints[a].Endpoint < r.ProviderResults[i].Endpoints[b].Endpoint
		})
	}
	sort.Slice(r.Observations, func(i, j int) bool {
		a, b := r.Observations[i], r.Observations[j]
		if a.AccountID != b.AccountID {
			return a.AccountID < b.AccountID
		}
		if a.ModelID != b.ModelID {
			return a.ModelID < b.ModelID
		}
		return a.ObservedAs < b.ObservedAs
	})
	sort.Slice(r.NameShape, func(i, j int) bool {
		a, b := r.NameShape[i], r.NameShape[j]
		if a.AccountID != b.AccountID {
			return a.AccountID < b.AccountID
		}
		if a.FullModelID != b.FullModelID {
			return a.FullModelID < b.FullModelID
		}
		return a.ObservedPrefix < b.ObservedPrefix
	})
}
