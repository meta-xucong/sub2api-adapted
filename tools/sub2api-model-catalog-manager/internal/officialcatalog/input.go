package officialcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	inputTokenPattern  = regexp.MustCompile(`^[A-Za-z0-9._:-]{0,80}$`)
	fingerprintPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type sourceDocument struct {
	Mode                   string            `json:"mode"`
	CreatedAt              json.RawMessage   `json:"created_at"`
	AccountScope           json.RawMessage   `json:"account_scope"`
	AccountScanStatus      json.RawMessage   `json:"account_scan_status"`
	AccountInventoryCount  json.RawMessage   `json:"account_inventory_count"`
	AccountInventorySHA256 json.RawMessage   `json:"account_inventory_sha256"`
	TargetBuildFingerprint json.RawMessage   `json:"target_build_fingerprint"`
	Accounts               []sourceAccount   `json:"accounts"`
	Discovery              []sourceDiscovery `json:"discovery"`
}

type sourceAccount struct {
	ID                int64           `json:"account_id"`
	SourceFingerprint string          `json:"source_fingerprint"`
	Platform          string          `json:"platform"`
	AccountType       string          `json:"account_type"`
	APIProtocol       string          `json:"api_protocol"`
	Mappings          []sourceMapping `json:"mappings"`
}

type sourceMapping struct {
	PublicID   string `json:"public_id"`
	UpstreamID string `json:"upstream_id"`
}

type sourceDiscovery struct {
	ID                int64    `json:"account_id"`
	SourceFingerprint string   `json:"source_fingerprint"`
	Status            string   `json:"status"`
	LiveListAvailable bool     `json:"live_list_available"`
	Models            []string `json:"models"`
}

// ParseInventory extracts only the explicitly approved inventory fields. Unknown
// fields (including base_url, query strings, headers and credentials) are ignored.
func ParseInventory(r io.Reader) (Inventory, error) {
	var result Inventory
	if r == nil {
		return result, errors.New("input report is unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(r, MaxInputReportBytes+1))
	if err != nil || len(body) == 0 || len(body) > MaxInputReportBytes {
		zero(body)
		return result, errors.New("input report is empty, unreadable, or exceeds the size limit")
	}
	defer zero(body)
	dec := json.NewDecoder(bytes.NewReader(body))
	var source sourceDocument
	if err := dec.Decode(&source); err != nil {
		return result, errors.New("input report is not valid JSON")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return result, errors.New("input report contains trailing data")
	}
	if source.Mode != "inspect" && source.Mode != "discover" {
		return result, errors.New("input report mode must be inspect or discover")
	}
	if len(source.Accounts) > MaxAccounts || len(source.Discovery) > MaxAccounts {
		return result, errors.New("input report exceeds the account limit")
	}

	accounts := make(map[int64]*inputAccount, len(source.Accounts)+len(source.Discovery))
	accountRowIDs := make([]int64, 0, len(source.Accounts))
	discoveryRowIDs := make([]int64, 0, len(source.Discovery))
	discoveryRecords := make([]DiscoveryRecord, 0, len(source.Discovery))
	for _, row := range source.Accounts {
		accountRowIDs = append(accountRowIDs, row.ID)
		if row.ID <= 0 || !validInventoryText(row.Platform) || !validInventoryText(row.AccountType) || !validInventoryText(row.APIProtocol) || !validFingerprint(row.SourceFingerprint) {
			return result, errors.New("input report contains invalid account identity fields")
		}
		account, err := mergeInputAccount(accounts, row.ID, row.SourceFingerprint, row.Platform, row.AccountType, row.APIProtocol)
		if err != nil {
			return result, err
		}
		if len(row.Mappings) > MaxModelsPerAccount {
			return result, errors.New("input report exceeds the per-account model limit")
		}
		for _, mapping := range row.Mappings {
			if mapping.PublicID == "" || mapping.UpstreamID == "" || !validModelID(mapping.PublicID) || !validModelID(mapping.UpstreamID) {
				return result, errors.New("input report contains an invalid mapping model ID")
			}
			account.Mappings = append(account.Mappings, inputMapping{PublicID: mapping.PublicID, UpstreamID: mapping.UpstreamID})
		}
	}
	for _, row := range source.Discovery {
		discoveryRowIDs = append(discoveryRowIDs, row.ID)
		if row.ID <= 0 || !validFingerprint(row.SourceFingerprint) {
			return result, errors.New("input report contains invalid discovery identity fields")
		}
		account, err := mergeInputAccount(accounts, row.ID, row.SourceFingerprint, "", "", "")
		if err != nil {
			return result, err
		}
		if len(row.Models) > MaxModelsPerAccount {
			return result, errors.New("input report exceeds the per-account model limit")
		}
		for _, modelID := range row.Models {
			if !validModelID(modelID) {
				return result, errors.New("input report contains an invalid discovered model ID")
			}
			account.DiscoveredModels = append(account.DiscoveredModels, modelID)
		}
		discoveryRecords = append(discoveryRecords, DiscoveryRecord{
			AccountID: row.ID, SourceFingerprint: row.SourceFingerprint, Status: row.Status,
			LiveListAvailable: row.LiveListAvailable, Models: uniqueStrings(row.Models),
		})
	}
	if len(accounts) == 0 {
		return result, errors.New("input report contains no account inventory")
	}

	canonical := make([]inputAccount, 0, len(accounts))
	for _, account := range accounts {
		account.Mappings = uniqueMappings(account.Mappings)
		account.DiscoveredModels = uniqueStrings(account.DiscoveredModels)
		canonical = append(canonical, *account)
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].ID < canonical[j].ID })
	canonicalBytes, err := json.Marshal(struct {
		Mode     string         `json:"mode"`
		Accounts []inputAccount `json:"accounts"`
	}{Mode: source.Mode, Accounts: canonical})
	if err != nil {
		return result, errors.New("input whitelist digest could not be computed")
	}
	h := sha256.Sum256(canonicalBytes)
	zero(canonicalBytes)

	observations := make([]InputModel, 0)
	for _, account := range canonical {
		seen := map[string]bool{}
		for _, mapping := range account.Mappings {
			for _, item := range []struct{ id, role string }{{mapping.PublicID, "public_id"}, {mapping.UpstreamID, "upstream_id"}} {
				key := item.role + "\x00" + item.id
				if !seen[key] {
					seen[key] = true
					observations = append(observations, InputModel{AccountID: account.ID, SourceFingerprint: account.SourceFingerprint, Platform: account.Platform, AccountType: account.AccountType, APIProtocol: account.APIProtocol, ModelID: item.id, ObservedAs: item.role})
				}
			}
		}
		for _, modelID := range account.DiscoveredModels {
			key := "discovered_model\x00" + modelID
			if !seen[key] {
				seen[key] = true
				observations = append(observations, InputModel{AccountID: account.ID, SourceFingerprint: account.SourceFingerprint, Platform: account.Platform, AccountType: account.AccountType, APIProtocol: account.APIProtocol, ModelID: modelID, ObservedAs: "discovered_model"})
			}
		}
	}
	if len(observations) > MaxTotalModels {
		return result, errors.New("input report exceeds the total model observation limit")
	}
	sort.Slice(observations, func(i, j int) bool {
		a, b := observations[i], observations[j]
		if a.AccountID != b.AccountID {
			return a.AccountID < b.AccountID
		}
		if a.ModelID != b.ModelID {
			return a.ModelID < b.ModelID
		}
		return a.ObservedAs < b.ObservedAs
	})
	metadata := InputMetadata{
		CreatedAt:              decodeRawString(source.CreatedAt),
		AccountScope:           decodeRawString(source.AccountScope),
		AccountScanStatus:      decodeRawString(source.AccountScanStatus),
		AccountInventoryCount:  decodeRawInt(source.AccountInventoryCount),
		AccountInventorySHA256: decodeRawString(source.AccountInventorySHA256),
		TargetBuildFingerprint: decodeRawString(source.TargetBuildFingerprint),
	}
	inputAccounts := make([]InputAccount, 0, len(canonical))
	mappingEdges := make([]MappingEdge, 0)
	for _, account := range canonical {
		inputAccounts = append(inputAccounts, InputAccount{
			ID: account.ID, SourceFingerprint: account.SourceFingerprint, Platform: account.Platform,
			AccountType: account.AccountType, APIProtocol: account.APIProtocol,
		})
		for _, mapping := range account.Mappings {
			mappingEdges = append(mappingEdges, MappingEdge{
				AccountID: account.ID, SourceFingerprint: account.SourceFingerprint, Platform: account.Platform,
				AccountType: account.AccountType, APIProtocol: account.APIProtocol,
				PublicID: mapping.PublicID, UpstreamID: mapping.UpstreamID,
			})
		}
	}
	sort.Slice(mappingEdges, func(i, j int) bool {
		a, b := mappingEdges[i], mappingEdges[j]
		if a.AccountID != b.AccountID {
			return a.AccountID < b.AccountID
		}
		if a.PublicID != b.PublicID {
			return a.PublicID < b.PublicID
		}
		return a.UpstreamID < b.UpstreamID
	})
	sort.Slice(discoveryRecords, func(i, j int) bool { return discoveryRecords[i].AccountID < discoveryRecords[j].AccountID })
	return Inventory{
		Mode: source.Mode, Digest: hex.EncodeToString(h[:]), Observations: observations,
		Metadata: metadata, AccountRowIDs: accountRowIDs, DiscoveryRowIDs: discoveryRowIDs,
		Accounts: inputAccounts, Mappings: mappingEdges, DiscoveryRecords: discoveryRecords,
	}, nil
}

func decodeRawString(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func decodeRawInt(raw json.RawMessage) int {
	var value int
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return 0
	}
	return value
}

func mergeInputAccount(accounts map[int64]*inputAccount, id int64, fingerprint, platform, accountType, protocol string) (*inputAccount, error) {
	account := accounts[id]
	if account == nil {
		if len(accounts) >= MaxAccounts {
			return nil, errors.New("input report exceeds the account limit")
		}
		account = &inputAccount{ID: id, SourceFingerprint: fingerprint, Platform: platform, AccountType: accountType, APIProtocol: protocol, Mappings: []inputMapping{}, DiscoveredModels: []string{}}
		accounts[id] = account
		return account, nil
	}
	for _, pair := range []struct {
		dst      *string
		incoming string
	}{{&account.SourceFingerprint, fingerprint}, {&account.Platform, platform}, {&account.AccountType, accountType}, {&account.APIProtocol, protocol}} {
		if pair.incoming == "" {
			continue
		}
		if *pair.dst != "" && *pair.dst != pair.incoming {
			return nil, errors.New("input report has conflicting whitelisted account fields")
		}
		*pair.dst = pair.incoming
	}
	return account, nil
}

func validInventoryText(s string) bool { return len(s) <= 80 && inputTokenPattern.MatchString(s) }

func validFingerprint(s string) bool { return s == "" || fingerprintPattern.MatchString(s) }

func validModelID(s string) bool {
	if s == "" || len(s) > MaxModelIDBytes || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func uniqueMappings(values []inputMapping) []inputMapping {
	if len(values) == 0 {
		return []inputMapping{}
	}
	seen := make(map[string]bool, len(values))
	out := make([]inputMapping, 0, len(values))
	for _, value := range values {
		key := value.PublicID + "\x00" + value.UpstreamID
		if !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PublicID != out[j].PublicID {
			return out[i].PublicID < out[j].PublicID
		}
		return out[i].UpstreamID < out[j].UpstreamID
	})
	return out
}

func hasControl(s string) bool { return strings.IndexFunc(s, unicode.IsControl) >= 0 }
