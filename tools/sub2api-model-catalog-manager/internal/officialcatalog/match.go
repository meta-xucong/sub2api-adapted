package officialcatalog

import (
	"sort"
	"strconv"
	"strings"
)

func matchObservations(observations []InputModel, snapshots []CatalogSnapshot) []Observation {
	byModel := make(map[string][]Reference)
	byProviderModel := make(map[string][]LifecycleEvidence)
	for _, snapshot := range snapshots {
		for _, evidence := range snapshot.Evidence {
			if evidence.Provider != snapshot.Provider || !validModelID(evidence.ModelID) || !validLifecycleStatus(evidence.Status) {
				continue
			}
			key := evidence.Provider + "\x00" + evidence.ModelID
			byProviderModel[key] = append(byProviderModel[key], evidence)
		}
	}
	conflict := make(map[string]bool)
	for key, evidence := range byProviderModel {
		variants := map[string]bool{}
		for _, item := range evidence {
			variants[strings.Join([]string{item.Status, item.AnnouncedAt, item.EffectiveAt, item.Replacement}, "\x00")] = true
		}
		conflict[key] = len(variants) > 1
	}
	for key, evidence := range byProviderModel {
		for _, item := range evidence {
			byModel[item.ModelID] = append(byModel[item.ModelID], Reference{
				Provider: item.Provider, ModelID: item.ModelID, MatchKind: "exact_id", Status: item.Status,
				AnnouncedAt: item.AnnouncedAt, EffectiveAt: item.EffectiveAt, Replacement: item.Replacement,
				SourceURL: item.SourceURL, SourceTitle: item.SourceTitle, RetrievedAt: item.RetrievedAt,
				ContentSHA256: item.ContentSHA256, Excerpt: item.Excerpt, Conflict: conflict[key],
			})
		}
	}
	out := make([]Observation, 0, len(observations))
	for _, item := range observations {
		refs := append([]Reference{}, byModel[item.ModelID]...)
		sort.Slice(refs, func(i, j int) bool {
			a, b := refs[i], refs[j]
			if a.Provider != b.Provider {
				return a.Provider < b.Provider
			}
			if a.Status != b.Status {
				return a.Status < b.Status
			}
			if a.EffectiveAt != b.EffectiveAt {
				return a.EffectiveAt < b.EffectiveAt
			}
			return a.SourceURL < b.SourceURL
		})
		refs = uniqueReferenceEvidence(refs)
		status := "unknown/no_official_notice_found"
		if len(refs) == 1 {
			status = refs[0].Status
		}
		if len(refs) > 1 {
			status = "multiple_official_references"
		}
		for _, ref := range refs {
			if ref.Conflict {
				status = "conflicting_official_lifecycle_evidence"
				break
			}
		}
		out = append(out, Observation{
			AccountID: item.AccountID, SourceFingerprint: item.SourceFingerprint, Platform: item.Platform,
			AccountType: item.AccountType, APIProtocol: item.APIProtocol, ModelID: item.ModelID,
			ObservedAs: item.ObservedAs, Status: status, MultipleRefs: len(refs) > 1,
			Matches: refs,
			Warning: "Exact model ID match to official lifecycle evidence only; this does not establish intermediary availability and does not change mappings.",
		})
	}
	return out
}

func uniqueReferenceEvidence(values []Reference) []Reference {
	seen := map[string]bool{}
	out := make([]Reference, 0, len(values))
	for _, value := range values {
		key := strings.Join([]string{value.Provider, value.ModelID, value.Status, value.AnnouncedAt, value.EffectiveAt, value.Replacement, value.SourceURL}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
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
	for accountID, ids := range byAccount {
		for id := range ids {
			prefix, ok := dateShapedPrefix(id)
			if !ok || !ids[prefix] {
				continue
			}
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
