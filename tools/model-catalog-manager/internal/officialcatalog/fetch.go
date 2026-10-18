package officialcatalog

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	anthropicOrigin = "https://api.anthropic.com"
	openAIOrigin    = "https://api.openai.com"
	deepSeekOrigin  = "https://api.deepseek.com"
	xAIOrigin       = "https://api.x.ai"
)

var workspacePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type fetchFailure struct{ code string }

func (f fetchFailure) Error() string { return f.code }

func NewHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:           nil,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Review fetches direct OEM catalog references. It never reads a Sub2API config,
// account credential or endpoint; keys are caller-supplied OEM keys only.
func Review(ctx context.Context, inventory Inventory, keys map[string][]byte, options Options, doer HTTPDoer, now func() time.Time) (Report, error) {
	defer func() {
		for _, key := range keys {
			zero(key)
		}
	}()
	if ctx == nil || doer == nil || inventory.Digest == "" || (inventory.Mode != "inspect" && inventory.Mode != "discover") {
		return Report{}, errors.New("official review dependencies or inventory are invalid")
	}
	if now == nil {
		now = time.Now
	}
	if keys == nil {
		keys = map[string][]byte{}
	}
	reportID := make([]byte, 16)
	if _, err := rand.Read(reportID); err != nil {
		return Report{}, errors.New("official review ID could not be created")
	}
	created := now().UTC()
	r := Report{
		SchemaVersion: SchemaVersion, ReportID: hex.EncodeToString(reportID), CreatedAt: created,
		InputReportMode: inventory.Mode, InputDigest: inventory.Digest,
		ProviderResults: []ProviderResult{}, Observations: []Observation{}, NameShape: []NameShapeCandidate{},
		GlobalWarning: "Official OEM references are advisory only. An exact match does not identify the Sub2API route, provider, availability, or compatibility.",
	}

	catalogs := make([]fetchedCatalog, 0, 5)
	for _, spec := range []struct{ name, limitation string }{
		{"anthropic", "Anthropic lifecycle applies to Anthropic-operated API only; partner routes can differ."},
		{"openai", "The list is API-key scoped; absence is not a shutdown or third-party route conclusion."},
		{"deepseek", "Current listed IDs only; no lifecycle or historical retirement is inferred."},
		{"alibaba", "The list is region/workspace/key scoped; estimated offline metadata is not proof about a third-party route."},
		{"xai", "The lists are API-key scoped; aliases are OEM request aliases and do not establish a proxy mapping."},
	} {
		key := keys[spec.name]
		if len(key) == 0 {
			r.ProviderResults = append(r.ProviderResults, ProviderResult{Provider: spec.name, Status: "unavailable", Evidence: evidenceFor(spec.name), ErrorCode: "missing_credentials", Limitation: spec.limitation})
			continue
		}
		if !validCredential(key) {
			r.ProviderResults = append(r.ProviderResults, ProviderResult{Provider: spec.name, Status: "unavailable", Evidence: evidenceFor(spec.name), ErrorCode: "invalid_credentials", Limitation: spec.limitation})
			continue
		}
		var result fetchedCatalog
		var err error
		switch spec.name {
		case "anthropic":
			result, err = fetchAnthropic(ctx, key, doer, now)
		case "openai":
			result, err = fetchOpenAI(ctx, key, doer, now)
		case "deepseek":
			result, err = fetchDeepSeek(ctx, key, doer, now)
		case "alibaba":
			result, err = fetchAlibaba(ctx, key, options, doer, now)
		case "xai":
			result, err = fetchXAI(ctx, key, doer, now)
		}
		if err != nil {
			code := failureCode(err)
			pr := result.Result
			if pr.Provider == "" {
				pr = ProviderResult{Provider: spec.name, Evidence: evidenceFor(spec.name)}
			}
			pr.Status, pr.ErrorCode, pr.Limitation = "unavailable", code, spec.limitation
			r.ProviderResults = append(r.ProviderResults, pr)
			continue
		}
		result.Result.Limitation = spec.limitation
		r.ProviderResults = append(r.ProviderResults, result.Result)
		catalogs = append(catalogs, result)
	}
	r.Observations = matchObservations(inventory.Observations, catalogs, created)
	r.NameShape = findNameShapeCandidates(inventory)
	sortReport(&r)
	return r, nil
}

func evidenceFor(provider string) string {
	switch provider {
	case "anthropic":
		return "official_lifecycle_catalog"
	case "openai":
		return "official_key_scoped_list"
	case "deepseek":
		return "official_listed_ids_only"
	case "alibaba":
		return "official_region_workspace_scoped_list"
	case "xai":
		return "official_key_scoped_lists_and_explicit_aliases"
	default:
		return "unknown"
	}
}

func fetchAnthropic(ctx context.Context, key []byte, doer HTTPDoer, now func() time.Time) (fetchedCatalog, error) {
	out := fetchedCatalog{Result: ProviderResult{Provider: "anthropic", Evidence: evidenceFor("anthropic"), Status: "available", Endpoints: []EndpointResult{}}}
	models := make([]modelRecord, 0)
	seenIDs, seenCursors := map[string]bool{}, map[string]bool{}
	after := ""
	for page := 0; page < MaxPages; page++ {
		u, _ := url.Parse(anthropicOrigin + "/v1/models")
		q := u.Query()
		q.Set("limit", "1000")
		for _, lifecycle := range []string{"active", "deprecated", "retired"} {
			q.Add("lifecycle", lifecycle)
		}
		if after != "" {
			q.Set("after_id", after)
		}
		u.RawQuery = q.Encode()
		body, _, err := requestJSON(ctx, doer, u.String(), key, "x-api-key", "anthropic", true)
		if err != nil {
			return out, err
		}
		var pageBody struct {
			Data []struct {
				ID           string  `json:"id"`
				Lifecycle    string  `json:"lifecycle"`
				DeprecatedAt *string `json:"deprecated_at"`
				RetiresAt    *string `json:"retires_at"`
				Line         *string `json:"line"`
			} `json:"data"`
			HasMore *bool   `json:"has_more"`
			LastID  *string `json:"last_id"`
		}
		if json.Unmarshal(body, &pageBody) != nil || pageBody.Data == nil || pageBody.HasMore == nil || len(pageBody.Data) == 0 {
			zero(body)
			return out, fetchFailure{code: "invalid_response"}
		}
		zero(body)
		pageModels := make([]modelRecord, 0, len(pageBody.Data))
		for _, item := range pageBody.Data {
			if !validModelID(item.ID) || seenIDs[item.ID] || (item.Lifecycle != "active" && item.Lifecycle != "deprecated" && item.Lifecycle != "retired") {
				return out, fetchFailure{code: "invalid_response"}
			}
			if !validOptionalDate(item.DeprecatedAt, time.RFC3339) || !validOptionalDate(item.RetiresAt, time.RFC3339) || (item.Line != nil && !validInventoryText(*item.Line)) {
				return out, fetchFailure{code: "invalid_response"}
			}
			seenIDs[item.ID] = true
			m := modelRecord{Provider: "anthropic", ModelID: item.ID, Status: anthropicStatus(item.Lifecycle), Lifecycle: item.Lifecycle, Endpoints: []string{"/v1/models"}}
			if item.DeprecatedAt != nil {
				m.DeprecatedAt = *item.DeprecatedAt
			}
			if item.RetiresAt != nil {
				m.RetiresAt = *item.RetiresAt
			}
			if item.Line != nil {
				m.Line = *item.Line
			}
			pageModels = append(pageModels, m)
		}
		if *pageBody.HasMore {
			if pageBody.LastID == nil || *pageBody.LastID == "" || seenCursors[*pageBody.LastID] || seenIDs[*pageBody.LastID] == false || pageModels[len(pageModels)-1].ModelID != *pageBody.LastID {
				return out, fetchFailure{code: "pagination_invalid"}
			}
			seenCursors[*pageBody.LastID] = true
			if after != "" && *pageBody.LastID == after {
				return out, fetchFailure{code: "pagination_invalid"}
			}
			after = *pageBody.LastID
		} else if pageBody.LastID != nil && *pageBody.LastID != "" && pageModels[len(pageModels)-1].ModelID != *pageBody.LastID {
			return out, fetchFailure{code: "pagination_invalid"}
		}
		models = append(models, pageModels...)
		if len(models) > MaxTotalModels {
			return out, fetchFailure{code: "catalog_limit"}
		}
		out.Result.Endpoints = append(out.Result.Endpoints, EndpointResult{Endpoint: "/v1/models", Status: "available", ModelCount: len(pageModels), RetrievedAt: timePtr(now().UTC())})
		if !*pageBody.HasMore {
			break
		}
		if page == MaxPages-1 {
			return out, fetchFailure{code: "pagination_limit"}
		}
	}
	if len(models) == 0 {
		return out, fetchFailure{code: "empty_catalog"}
	}
	out.Models = models
	out.Result.ModelCount = len(models)
	out.Result.RetrievedAt = timePtr(now().UTC())
	return out, nil
}

func fetchOpenAI(ctx context.Context, key []byte, doer HTTPDoer, now func() time.Time) (fetchedCatalog, error) {
	out := fetchedCatalog{Result: ProviderResult{Provider: "openai", Evidence: evidenceFor("openai"), Status: "available", Endpoints: []EndpointResult{}}}
	body, _, err := requestJSON(ctx, doer, openAIOrigin+"/v1/models", key, "Authorization", "bearer", true)
	if err != nil {
		return out, err
	}
	defer zero(body)
	var response struct {
		Object string `json:"object"`
		Data   []struct {
			ID           string  `json:"id"`
			ShutdownDate *string `json:"shutdown_date"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || response.Object != "list" || response.Data == nil {
		return out, fetchFailure{code: "invalid_response"}
	}
	if len(response.Data) > MaxTotalModels {
		return out, fetchFailure{code: "catalog_limit"}
	}
	models := make([]modelRecord, 0, len(response.Data))
	seen := map[string]bool{}
	for _, item := range response.Data {
		if !validModelID(item.ID) || seen[item.ID] {
			return out, fetchFailure{code: "invalid_response"}
		}
		seen[item.ID] = true
		m := modelRecord{Provider: "openai", ModelID: item.ID, Status: "official_reference_match", Endpoints: []string{"/v1/models"}}
		if item.ShutdownDate != nil {
			if _, err := time.Parse("2006-01-02", *item.ShutdownDate); err != nil {
				return out, fetchFailure{code: "invalid_response"}
			}
			m.ShutdownDate = *item.ShutdownDate
			shutdown, _ := time.Parse("2006-01-02", *item.ShutdownDate)
			today := now().UTC().Format("2006-01-02")
			if shutdown.Format("2006-01-02") < today {
				m.Status = "official_shutdown_date_passed_candidate"
			} else {
				m.Status = "official_shutdown_date_upcoming"
			}
		}
		models = append(models, m)
	}
	if len(models) == 0 {
		return out, fetchFailure{code: "empty_catalog"}
	}
	out.Models = models
	out.Result.ModelCount, out.Result.RetrievedAt = len(models), timePtr(now().UTC())
	out.Result.Endpoints = []EndpointResult{{Endpoint: "/v1/models", Status: "available", ModelCount: len(models), RetrievedAt: out.Result.RetrievedAt}}
	return out, nil
}

func fetchDeepSeek(ctx context.Context, key []byte, doer HTTPDoer, now func() time.Time) (fetchedCatalog, error) {
	out := fetchedCatalog{Result: ProviderResult{Provider: "deepseek", Evidence: evidenceFor("deepseek"), Status: "available", Endpoints: []EndpointResult{}}}
	body, _, err := requestJSON(ctx, doer, deepSeekOrigin+"/models", key, "Authorization", "bearer", true)
	if err != nil {
		return out, err
	}
	defer zero(body)
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return out, fetchFailure{code: "invalid_response"}
	}
	for _, marker := range []string{"has_more", "has_next", "next", "next_page", "next_cursor", "cursor", "pagination", "page", "page_no", "total"} {
		if _, ok := root[marker]; ok {
			return out, fetchFailure{code: "unexpected_pagination"}
		}
	}
	var response struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || response.Object != "list" || response.Data == nil {
		return out, fetchFailure{code: "invalid_response"}
	}
	if len(response.Data) > MaxTotalModels {
		return out, fetchFailure{code: "catalog_limit"}
	}
	models := make([]modelRecord, 0, len(response.Data))
	seen := map[string]bool{}
	for _, item := range response.Data {
		if !validModelID(item.ID) || seen[item.ID] {
			return out, fetchFailure{code: "invalid_response"}
		}
		seen[item.ID] = true
		models = append(models, modelRecord{Provider: "deepseek", ModelID: item.ID, Status: "official_reference_match", Endpoints: []string{"/models"}})
	}
	if len(models) == 0 {
		return out, fetchFailure{code: "empty_catalog"}
	}
	out.Models = models
	out.Result.ModelCount, out.Result.RetrievedAt = len(models), timePtr(now().UTC())
	out.Result.Endpoints = []EndpointResult{{Endpoint: "/models", Status: "available", ModelCount: len(models), RetrievedAt: out.Result.RetrievedAt}}
	return out, nil
}

func fetchAlibaba(ctx context.Context, key []byte, options Options, doer HTTPDoer, now func() time.Time) (fetchedCatalog, error) {
	out := fetchedCatalog{Result: ProviderResult{Provider: "alibaba", Evidence: evidenceFor("alibaba"), Status: "available", Endpoints: []EndpointResult{}}}
	base, code := alibabaBase(options)
	if code != "" {
		return out, fetchFailure{code: code}
	}
	models := make([]modelRecord, 0)
	seenIDs := map[string]bool{}
	var stableTotal int
	for page := 1; page <= MaxPages; page++ {
		u, _ := url.Parse(base)
		q := u.Query()
		q.Set("page_no", strconv.Itoa(page))
		q.Set("page_size", "100")
		u.RawQuery = q.Encode()
		body, _, err := requestJSON(ctx, doer, u.String(), key, "Authorization", "bearer", false)
		if err != nil {
			return out, err
		}
		var response struct {
			Success *bool `json:"success"`
			Output  *struct {
				Total  *int `json:"total"`
				PageNo *int `json:"page_no"`
				Models []struct {
					Model   string `json:"model"`
					Offline *struct {
						OfflineTime *string `json:"offline_time"`
					} `json:"inference_offline_info"`
				} `json:"models"`
			} `json:"output"`
		}
		if json.Unmarshal(body, &response) != nil || response.Success == nil || !*response.Success || response.Output == nil || response.Output.Total == nil || response.Output.PageNo == nil || response.Output.Models == nil {
			zero(body)
			return out, fetchFailure{code: "invalid_response"}
		}
		zero(body)
		output := response.Output
		if *output.PageNo != page || *output.Total < 0 {
			return out, fetchFailure{code: "pagination_invalid"}
		}
		if page == 1 {
			stableTotal = *output.Total
		} else if *output.Total != stableTotal {
			return out, fetchFailure{code: "pagination_changed_total"}
		}
		if stableTotal > MaxPages*100 {
			return out, fetchFailure{code: "catalog_limit"}
		}
		if stableTotal == 0 {
			if page != 1 || len(output.Models) != 0 {
				return out, fetchFailure{code: "pagination_invalid"}
			}
			return out, fetchFailure{code: "empty_catalog"}
		}
		if len(output.Models) == 0 {
			return out, fetchFailure{code: "pagination_no_progress"}
		}
		pageModels := make([]modelRecord, 0, len(output.Models))
		for _, item := range output.Models {
			if !validModelID(item.Model) || seenIDs[item.Model] {
				return out, fetchFailure{code: "invalid_response"}
			}
			seenIDs[item.Model] = true
			m := modelRecord{Provider: "alibaba", ModelID: item.Model, Status: "official_reference_match", Endpoints: []string{"/api/v1/models"}}
			if item.Offline != nil && item.Offline.OfflineTime != nil {
				if len(*item.Offline.OfflineTime) > 100 || hasControl(*item.Offline.OfflineTime) {
					return out, fetchFailure{code: "invalid_response"}
				}
				m.OfflineTime = *item.Offline.OfflineTime
				if m.OfflineTime != "" {
					m.Status = "official_offline_time_candidate"
				}
			}
			pageModels = append(pageModels, m)
		}
		if len(models)+len(pageModels) > stableTotal {
			return out, fetchFailure{code: "pagination_invalid"}
		}
		models = append(models, pageModels...)
		out.Result.Endpoints = append(out.Result.Endpoints, EndpointResult{Endpoint: "/api/v1/models", Status: "available", ModelCount: len(pageModels), RetrievedAt: timePtr(now().UTC())})
		if len(models) == stableTotal {
			break
		}
		if page == MaxPages {
			return out, fetchFailure{code: "pagination_limit"}
		}
	}
	if len(models) == 0 || len(models) != stableTotal {
		return out, fetchFailure{code: "pagination_incomplete"}
	}
	out.Models = models
	out.Result.ModelCount, out.Result.RetrievedAt = len(models), timePtr(now().UTC())
	return out, nil
}

func fetchXAI(ctx context.Context, key []byte, doer HTTPDoer, now func() time.Time) (fetchedCatalog, error) {
	out := fetchedCatalog{Result: ProviderResult{Provider: "xai", Evidence: evidenceFor("xai"), Endpoints: []EndpointResult{}}}
	allModels := make([]modelRecord, 0)
	endpointSpecs := []struct{ path, shape string }{
		{"/v1/models", "data"}, {"/v1/image-generation-models", "models"}, {"/v1/video-generation-models", "models"},
	}
	successes, failures := 0, 0
	for _, endpoint := range endpointSpecs {
		body, _, err := requestJSON(ctx, doer, xAIOrigin+endpoint.path, key, "Authorization", "bearer", true)
		if err != nil {
			failures++
			out.Result.Endpoints = append(out.Result.Endpoints, EndpointResult{Endpoint: endpoint.path, Status: "unavailable", ErrorCode: failureCode(err), RetrievedAt: timePtr(now().UTC())})
			continue
		}
		models, parseErr := parseXAIList(body, endpoint.shape, endpoint.path)
		zero(body)
		if parseErr != nil {
			failures++
			out.Result.Endpoints = append(out.Result.Endpoints, EndpointResult{Endpoint: endpoint.path, Status: "unavailable", ErrorCode: failureCode(parseErr), RetrievedAt: timePtr(now().UTC())})
			continue
		}
		successes++
		allModels = append(allModels, models...)
		out.Result.Endpoints = append(out.Result.Endpoints, EndpointResult{Endpoint: endpoint.path, Status: "available", ModelCount: len(models), RetrievedAt: timePtr(now().UTC())})
	}
	if successes == 0 {
		out.Result.Status = "unavailable"
		return out, fetchFailure{code: "all_endpoints_unavailable"}
	}
	if len(allModels) > MaxTotalModels {
		out.Result.Status = "unavailable"
		return out, fetchFailure{code: "catalog_limit"}
	}
	out.Models = allModels
	out.Result.ModelCount = len(allModels)
	out.Result.RetrievedAt = timePtr(now().UTC())
	if failures == 0 {
		out.Result.Status = "available"
	} else {
		out.Result.Status = "partial"
	}
	return out, nil
}

func parseXAIList(body []byte, shape, endpoint string) ([]modelRecord, error) {
	var rows []struct {
		ID      string    `json:"id"`
		Aliases *[]string `json:"aliases"`
	}
	if shape == "data" {
		var root struct {
			Object string `json:"object"`
			Data   []struct {
				ID      string    `json:"id"`
				Aliases *[]string `json:"aliases"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &root) != nil || root.Object != "list" || root.Data == nil {
			return nil, fetchFailure{code: "invalid_response"}
		}
		rows = root.Data
	} else {
		var root struct {
			Models []struct {
				ID      string    `json:"id"`
				Aliases *[]string `json:"aliases"`
			} `json:"models"`
		}
		if json.Unmarshal(body, &root) != nil || root.Models == nil {
			return nil, fetchFailure{code: "invalid_response"}
		}
		rows = root.Models
	}
	if len(rows) == 0 {
		return nil, fetchFailure{code: "empty_catalog"}
	}
	seen := map[string]bool{}
	out := make([]modelRecord, 0, len(rows))
	for _, item := range rows {
		if !validModelID(item.ID) || seen[item.ID] || item.Aliases == nil {
			return nil, fetchFailure{code: "invalid_response"}
		}
		seen[item.ID] = true
		aliases := make([]string, 0, len(*item.Aliases))
		aliasSeen := map[string]bool{}
		for _, alias := range *item.Aliases {
			if !validModelID(alias) || alias == item.ID || aliasSeen[alias] {
				return nil, fetchFailure{code: "invalid_response"}
			}
			aliasSeen[alias] = true
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		out = append(out, modelRecord{Provider: "xai", ModelID: item.ID, Status: "official_reference_match", Aliases: aliases, Endpoints: []string{endpoint}})
	}
	return out, nil
}

func requestJSON(ctx context.Context, doer HTTPDoer, rawURL string, key []byte, authHeader, authMode string, anthropic bool) ([]byte, string, error) {
	if doer == nil {
		return nil, "", fetchFailure{code: "request_unavailable"}
	}
	if !isFixedURL(rawURL) {
		return nil, "", fetchFailure{code: "endpoint_not_allowed"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fetchFailure{code: "request_failed"}
	}
	req.Header.Set("Accept", "application/json")
	if authMode == "bearer" {
		req.Header.Set(authHeader, "Bearer "+string(key))
	} else {
		req.Header.Set(authHeader, string(key))
	}
	if anthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, err := doer.Do(req)
	if err != nil {
		return nil, "", fetchFailure{code: "request_failed"}
	}
	if resp == nil || resp.Body == nil {
		return nil, "", fetchFailure{code: "invalid_response"}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, "", fetchFailure{code: "redirect_rejected"}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fetchFailure{code: "http_error"}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		zero(body)
		return nil, "", fetchFailure{code: "response_read_failed"}
	}
	if len(body) > MaxResponseBytes {
		zero(body)
		return nil, "", fetchFailure{code: "response_too_large"}
	}
	return body, "", nil
}

func isFixedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return false
	}
	if u.Host == "api.anthropic.com" {
		return u.Path == "/v1/models"
	}
	if u.Host == "api.openai.com" {
		return u.Path == "/v1/models"
	}
	if u.Host == "api.deepseek.com" {
		return u.Path == "/models"
	}
	if u.Host == "api.x.ai" {
		return u.Path == "/v1/models" || u.Path == "/v1/image-generation-models" || u.Path == "/v1/video-generation-models"
	}
	if strings.HasSuffix(u.Host, ".maas.aliyuncs.com") || u.Host == "dashscope-intl.aliyuncs.com" || u.Host == "cn-hongkong.dashscope.aliyuncs.com" {
		return u.Path == "/api/v1/models" && allowedAlibabaHost(u.Host)
	}
	return false
}

func allowedAlibabaHost(host string) bool {
	if host == "dashscope-intl.aliyuncs.com" || host == "cn-hongkong.dashscope.aliyuncs.com" {
		return true
	}
	parts := strings.Split(host, ".")
	if len(parts) != 5 || !workspacePattern.MatchString(parts[0]) || parts[1] != "cn-beijing" && parts[1] != "eu-central-1" && parts[1] != "ap-northeast-1" && parts[1] != "us-east-1" || parts[2] != "maas" || parts[3] != "aliyuncs" || parts[4] != "com" {
		return false
	}
	return true
}

func alibabaBase(options Options) (string, string) {
	var host string
	switch options.AlibabaRegion {
	case "beijing":
		host = options.AlibabaWorkspace + ".cn-beijing.maas.aliyuncs.com"
	case "singapore":
		host = "dashscope-intl.aliyuncs.com"
	case "hongkong":
		host = "cn-hongkong.dashscope.aliyuncs.com"
	case "frankfurt":
		host = options.AlibabaWorkspace + ".eu-central-1.maas.aliyuncs.com"
	case "tokyo":
		host = options.AlibabaWorkspace + ".ap-northeast-1.maas.aliyuncs.com"
	case "virginia":
		host = options.AlibabaWorkspace + ".us-east-1.maas.aliyuncs.com"
	default:
		return "", "unsupported_region"
	}
	needsWorkspace := options.AlibabaRegion == "beijing" || options.AlibabaRegion == "frankfurt" || options.AlibabaRegion == "tokyo" || options.AlibabaRegion == "virginia"
	if needsWorkspace && !workspacePattern.MatchString(options.AlibabaWorkspace) {
		return "", "invalid_workspace"
	}
	if !needsWorkspace && options.AlibabaWorkspace != "" {
		return "", "workspace_not_allowed"
	}
	return "https://" + host + "/api/v1/models", ""
}

func validCredential(key []byte) bool {
	if len(key) == 0 || len(key) > MaxCredentialBytes {
		return false
	}
	for _, b := range key {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return true
}

func validOptionalDate(value *string, layout string) bool {
	if value == nil || *value == "" {
		return true
	}
	_, err := time.Parse(layout, *value)
	return err == nil
}

func anthropicStatus(lifecycle string) string {
	switch lifecycle {
	case "retired":
		return "official_retired_candidate"
	case "deprecated":
		return "official_deprecated_candidate"
	default:
		return "official_reference_match"
	}
}

func failureCode(err error) string {
	var failure fetchFailure
	if errors.As(err, &failure) {
		if safeErrorCode(failure.code) {
			return failure.code
		}
	}
	return "fetch_failed"
}

func safeErrorCode(code string) bool {
	switch code {
	case "missing_credentials", "invalid_credentials", "unsupported_region", "invalid_workspace", "workspace_not_allowed", "request_unavailable", "endpoint_not_allowed", "request_failed", "http_error", "redirect_rejected", "invalid_response", "response_read_failed", "response_too_large", "pagination_invalid", "pagination_limit", "pagination_no_progress", "pagination_changed_total", "pagination_incomplete", "unexpected_pagination", "empty_catalog", "catalog_limit", "all_endpoints_unavailable":
		return true
	default:
		return false
	}
}

func timePtr(value time.Time) *time.Time { return &value }
