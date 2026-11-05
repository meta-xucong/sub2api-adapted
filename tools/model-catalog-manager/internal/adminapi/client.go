package adminapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sub2api-model-catalog-manager/internal/config"
)

const (
	maxResponseBytes = 8 << 20
	requestTimeout   = 30 * time.Second
	trustedBaseURL   = "https://404token.xyz"
)

var ErrUnexpectedBulkResult = errors.New("mapping update returned an unexpected account result")

type Client struct {
	base   *url.URL
	http   *http.Client
	apiKey []byte
}

type Account struct {
	ID                  int64             `json:"id"`
	Platform            string            `json:"platform"`
	Type                string            `json:"type"`
	Protocol            string            `json:"protocol"`
	BaseURL             string            `json:"base_url"`
	Status              string            `json:"status,omitempty"`
	ParentAccountID     *int64            `json:"parent_account_id,omitempty"`
	ModelMapping        map[string]string `json:"model_mapping"`
	ModelMappingPresent bool              `json:"-"`
	APIKeyPresent       *bool             `json:"-"`
}

// HasModelMapping distinguishes an explicit (possibly empty) mapping from an
// account whose native configuration omits the field entirely.
func (a Account) HasModelMapping() bool {
	return a.ModelMappingPresent || a.ModelMapping != nil
}

// AccountSummary is the only retained projection from the paginated account
// list. Any credentials, headers, extra fields, and unrelated account fields
// are consumed by the decoder and discarded.
type AccountSummary struct {
	ID              int64  `json:"id"`
	Platform        string `json:"platform,omitempty"`
	Type            string `json:"type,omitempty"`
	Status          string `json:"status,omitempty"`
	ParentAccountID *int64 `json:"parent_account_id,omitempty"`
}

type RefreshStatus struct {
	Enabled        bool               `json:"enabled"`
	Timezone       string             `json:"timezone"`
	RunStatus      string             `json:"last_run_status"`
	LastFinishedAt *time.Time         `json:"last_finished_at"`
	Accounts       []AccountRunStatus `json:"accounts"`
}

type AccountRunStatus struct {
	AccountID int64  `json:"account_id"`
	Status    string `json:"status"`
}

type Catalog struct {
	Models            []string         `json:"models"`
	Warnings          []CatalogWarning `json:"warnings,omitempty"`
	LiveListAvailable bool             `json:"live_list_available"`
}

type CatalogWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type BulkUpdateResult struct {
	SuccessIDs []int64 `json:"success_ids"`
	FailedIDs  []int64 `json:"failed_ids"`
}

type safeAPIError struct {
	status   int
	category string
}

func (e safeAPIError) Error() string {
	if e.status > 0 {
		return fmt.Sprintf("admin API request failed: status=%d category=%s", e.status, e.category)
	}
	return "admin API request failed: category=" + e.category
}

func New(baseURL string) (*Client, error) {
	if strings.TrimSuffix(strings.TrimSpace(baseURL), "/") != trustedBaseURL {
		return nil, errors.New("admin API destination is fixed to https://404token.xyz")
	}
	base, err := config.ValidateBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	return newClient(base), nil
}

func newClient(base *url.URL) *Client {
	transport := &http.Transport{
		Proxy:             nil, // Never forward administrator credentials through ambient proxy settings.
		DialContext:       (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
	}
	return &Client{
		base: base,
		http: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	zero(c.apiKey)
	c.apiKey = nil
	if tr, ok := c.http.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

func (c *Client) SetAPIKey(key []byte) error {
	if c == nil || len(key) == 0 || len(key) > 16*1024 {
		return errors.New("admin API key is unavailable")
	}
	for _, b := range key {
		if b < 0x21 || b > 0x7e {
			return errors.New("admin API key is invalid")
		}
	}
	zero(c.apiKey)
	c.apiKey = append(c.apiKey[:0], key...)
	return nil
}

func (c *Client) RefreshStatus(ctx context.Context) (RefreshStatus, error) {
	var out RefreshStatus
	err := c.request(ctx, http.MethodGet, "/api/v1/admin/model-catalog-refresh/status", nil, func(dec *json.Decoder) error {
		return decodeKnownObject(dec, map[string]func(*json.Decoder) error{
			"enabled":          func(d *json.Decoder) error { return d.Decode(&out.Enabled) },
			"timezone":         func(d *json.Decoder) error { return d.Decode(&out.Timezone) },
			"last_run_status":  func(d *json.Decoder) error { return d.Decode(&out.RunStatus) },
			"last_finished_at": func(d *json.Decoder) error { return d.Decode(&out.LastFinishedAt) },
			"accounts":         func(d *json.Decoder) error { return d.Decode(&out.Accounts) },
		})
	})
	if err != nil {
		return RefreshStatus{}, err
	}
	return out, nil
}

func (c *Client) Account(ctx context.Context, id int64) (Account, error) {
	if id <= 0 {
		return Account{}, errors.New("invalid account ID")
	}
	var out Account
	path := "/api/v1/admin/accounts/" + strconv.FormatInt(id, 10)
	err := c.request(ctx, http.MethodGet, path, nil, func(dec *json.Decoder) error {
		return decodeAccount(dec, &out)
	})
	if err != nil {
		return Account{}, err
	}
	if out.ID != id || out.Platform == "" || out.Type == "" {
		return Account{}, errors.New("account response did not satisfy the supported metadata contract")
	}
	return out, nil
}

// ListAllAccounts reads every account through the paginated admin list API.
// It is intentionally opt-in at the config/workflow layer because list items
// may include credential-shaped fields even though this client discards them.
func (c *Client) ListAllAccounts(ctx context.Context) ([]AccountSummary, error) {
	const pageSize = 1000
	const maxAccounts = 100000
	var all []AccountSummary
	seen := map[int64]bool{}
	var expectedTotal int64 = -1
	var lastID int64
	for page := 1; ; page++ {
		var result accountListPage
		query := url.Values{
			"page":       {strconv.Itoa(page)},
			"page_size":  {strconv.Itoa(pageSize)},
			"lite":       {"1"},
			"sort_by":    {"id"},
			"sort_order": {"asc"},
		}
		if err := c.requestWithQuery(ctx, http.MethodGet, "/api/v1/admin/accounts", query, nil, func(dec *json.Decoder) error {
			return decodeAccountListPage(dec, &result)
		}); err != nil {
			return nil, err
		}
		if result.Page != page || result.PageSize != pageSize || result.Total < 0 || !result.HasItems || !result.HasTotal || !result.HasPage || !result.HasPageSize || !result.HasPages {
			return nil, errors.New("admin account list response did not satisfy the complete pagination contract")
		}
		if expectedTotal < 0 {
			expectedTotal = result.Total
			if expectedTotal > maxAccounts {
				return nil, errors.New("admin account list exceeds the configured safety limit")
			}
			all = make([]AccountSummary, 0, int(expectedTotal))
		} else if expectedTotal != result.Total {
			return nil, errors.New("admin account list total changed during pagination")
		}
		expectedPages := 0
		if result.Total > 0 {
			expectedPages = int((result.Total + pageSize - 1) / pageSize)
		}
		if result.Pages != expectedPages {
			return nil, errors.New("admin account list page count changed or is inconsistent")
		}
		if expectedTotal == 0 {
			if page != 1 || len(result.Items) != 0 {
				return nil, errors.New("admin account list returned inconsistent empty pagination")
			}
			return all, nil
		}
		expectedItems := int64(pageSize)
		remaining := expectedTotal - int64(len(all))
		if remaining < int64(pageSize) {
			expectedItems = remaining
		}
		if int64(len(result.Items)) != expectedItems || len(result.Items) > pageSize {
			return nil, errors.New("admin account list pagination made no progress or exceeded its total")
		}
		for _, item := range result.Items {
			if item.ID <= 0 || seen[item.ID] || (lastID != 0 && item.ID <= lastID) {
				return nil, errors.New("admin account list contains an invalid, duplicate, or unordered ID")
			}
			seen[item.ID] = true
			lastID = item.ID
			all = append(all, item)
		}
		if int64(len(all)) == expectedTotal {
			if page != int((expectedTotal+pageSize-1)/pageSize) {
				return nil, errors.New("admin account list page count does not match its total")
			}
			return all, nil
		}
		if int64(len(all)) > expectedTotal || page >= maxAccounts/pageSize+1 {
			return nil, errors.New("admin account list pagination did not terminate safely")
		}
	}
}

// SyncUpstream performs the one explicitly side-effecting catalog endpoint.
// Callers must check the local allow_native_sync_metadata_write gate first.
func (c *Client) SyncUpstream(ctx context.Context, id int64) (Catalog, error) {
	if id <= 0 {
		return Catalog{}, errors.New("invalid account ID")
	}
	path := "/api/v1/admin/accounts/" + strconv.FormatInt(id, 10) + "/models/sync-upstream"
	var out Catalog
	err := c.request(ctx, http.MethodPost, path, []byte("{}"), func(dec *json.Decoder) error {
		return decodeKnownObject(dec, map[string]func(*json.Decoder) error{
			"models":              func(d *json.Decoder) error { return d.Decode(&out.Models) },
			"warnings":            func(d *json.Decoder) error { return d.Decode(&out.Warnings) },
			"live_list_available": func(d *json.Decoder) error { return d.Decode(&out.LiveListAvailable) },
		})
	})
	if err != nil {
		return Catalog{}, err
	}
	return out, nil
}

// BulkUpdateMapping is the only supported write method. It intentionally has
// no filters or generic account-update payload. Higher-level compatibility and
// human confirmation gates must be checked before calling it.
func (c *Client) BulkUpdateMapping(ctx context.Context, id int64, mapping map[string]string) (BulkUpdateResult, error) {
	if id <= 0 || mapping == nil {
		return BulkUpdateResult{}, errors.New("invalid mapping update")
	}
	body, err := json.Marshal(struct {
		AccountIDs  []int64 `json:"account_ids"`
		Credentials struct {
			ModelMapping map[string]string `json:"model_mapping"`
		} `json:"credentials"`
	}{AccountIDs: []int64{id}, Credentials: struct {
		ModelMapping map[string]string `json:"model_mapping"`
	}{ModelMapping: mapping}})
	if err != nil {
		return BulkUpdateResult{}, errors.New("mapping update could not be prepared")
	}
	defer zero(body)
	var result BulkUpdateResult
	err = c.request(ctx, http.MethodPost, "/api/v1/admin/accounts/bulk-update", body, func(dec *json.Decoder) error {
		return decodeKnownObject(dec, map[string]func(*json.Decoder) error{
			"success_ids": func(d *json.Decoder) error { return d.Decode(&result.SuccessIDs) },
			"failed_ids":  func(d *json.Decoder) error { return d.Decode(&result.FailedIDs) },
		})
	})
	if err != nil {
		return BulkUpdateResult{}, err
	}
	if len(result.SuccessIDs) != 1 || result.SuccessIDs[0] != id || len(result.FailedIDs) != 0 {
		return BulkUpdateResult{}, ErrUnexpectedBulkResult
	}
	return result, nil
}

func (c *Client) request(ctx context.Context, method, path string, body []byte, parseData func(*json.Decoder) error) error {
	return c.requestWithQuery(ctx, method, path, nil, body, parseData)
}

func (c *Client) requestWithQuery(ctx context.Context, method, path string, query url.Values, body []byte, parseData func(*json.Decoder) error) error {
	if c == nil || c.base == nil || c.http == nil || !allowedRequest(method, path, query) {
		return errors.New("admin API operation is outside the allowlist")
	}
	if len(c.apiKey) == 0 {
		return errors.New("admin API key is not configured")
	}
	u := *c.base
	u.Path = path
	if query != nil {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return safeAPIError{category: "request_create"}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("x-api-key", string(c.apiKey))
	resp, err := c.http.Do(req)
	if err != nil {
		return safeAPIError{category: "transport"}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return safeAPIError{status: resp.StatusCode, category: "http"}
	}
	limited := &countingReader{r: io.LimitReader(resp.Body, maxResponseBytes+1)}
	dec := json.NewDecoder(limited)
	if err := decodeEnvelope(dec, parseData); err != nil {
		return safeAPIError{status: resp.StatusCode, category: "invalid_response"}
	}
	if limited.n > maxResponseBytes {
		return safeAPIError{status: resp.StatusCode, category: "response_too_large"}
	}
	return nil
}

func allowed(method, path string) bool {
	return allowedRequest(method, path, nil)
}

func allowedRequest(method, path string, query url.Values) bool {
	if method == http.MethodGet && path == "/api/v1/admin/accounts" {
		return exactAccountListQuery(query)
	}
	if query != nil && len(query) != 0 {
		return false
	}
	if method == http.MethodGet && path == "/api/v1/admin/model-catalog-refresh/status" {
		return true
	}
	if method == http.MethodGet && accountPath(path) {
		return true
	}
	if method == http.MethodPost && syncPath(path) {
		return true
	}
	return method == http.MethodPost && path == "/api/v1/admin/accounts/bulk-update"
}

func exactAccountListQuery(query url.Values) bool {
	if len(query) != 5 || len(query["page"]) != 1 || len(query["page_size"]) != 1 || len(query["lite"]) != 1 || len(query["sort_by"]) != 1 || len(query["sort_order"]) != 1 {
		return false
	}
	page, err := strconv.Atoi(query.Get("page"))
	return err == nil && page > 0 && query.Get("page_size") == "1000" && query.Get("lite") == "1" && query.Get("sort_by") == "id" && query.Get("sort_order") == "asc"
}

type accountListPage struct {
	Items       []AccountSummary
	Total       int64
	Page        int
	PageSize    int
	Pages       int
	HasItems    bool
	HasTotal    bool
	HasPage     bool
	HasPageSize bool
	HasPages    bool
}

func decodeAccountListPage(dec *json.Decoder, out *accountListPage) error {
	return decodeUniqueKnownObject(dec, map[string]func(*json.Decoder) error{
		"items": func(d *json.Decoder) error {
			out.HasItems = true
			return decodeAccountSummaryArray(d, &out.Items)
		},
		"total": func(d *json.Decoder) error {
			out.HasTotal = true
			return d.Decode(&out.Total)
		},
		"page": func(d *json.Decoder) error {
			out.HasPage = true
			return d.Decode(&out.Page)
		},
		"page_size": func(d *json.Decoder) error {
			out.HasPageSize = true
			return d.Decode(&out.PageSize)
		},
		"pages": func(d *json.Decoder) error {
			out.HasPages = true
			return d.Decode(&out.Pages)
		},
	})
}

func decodeAccountSummaryArray(dec *json.Decoder, out *[]AccountSummary) error {
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('[') {
		return errors.New("admin account list items are not an array")
	}
	items := make([]AccountSummary, 0)
	for dec.More() {
		var item AccountSummary
		if err := decodeUniqueKnownObject(dec, map[string]func(*json.Decoder) error{
			"id":                func(d *json.Decoder) error { return d.Decode(&item.ID) },
			"platform":          func(d *json.Decoder) error { return d.Decode(&item.Platform) },
			"type":              func(d *json.Decoder) error { return d.Decode(&item.Type) },
			"status":            func(d *json.Decoder) error { return d.Decode(&item.Status) },
			"parent_account_id": func(d *json.Decoder) error { return d.Decode(&item.ParentAccountID) },
		}); err != nil {
			return err
		}
		items = append(items, item)
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	*out = items
	return nil
}

func decodeUniqueKnownObject(dec *json.Decoder, fields map[string]func(*json.Decoder) error) error {
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return errors.New("expected object")
	}
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok || seen[key] {
			return errors.New("object has an invalid or duplicate key")
		}
		seen[key] = true
		if read, ok := fields[key]; ok {
			if err := read(dec); err != nil {
				return err
			}
		} else if err := skipValue(dec); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func accountPath(path string) bool {
	const prefix = "/api/v1/admin/accounts/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	id := strings.TrimPrefix(path, prefix)
	if id == "" || strings.Contains(id, "/") {
		return false
	}
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}

func syncPath(path string) bool {
	const suffix = "/models/sync-upstream"
	if !strings.HasSuffix(path, suffix) {
		return false
	}
	return accountPath(strings.TrimSuffix(path, suffix))
}

type countingReader struct {
	r io.Reader
	n int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	return n, err
}

func decodeEnvelope(dec *json.Decoder, parseData func(*json.Decoder) error) error {
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return errors.New("bad envelope")
	}
	codeSeen, dataSeen := false, false
	var code int
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return errors.New("bad envelope key")
		}
		switch key {
		case "code":
			codeSeen = true
			if err := dec.Decode(&code); err != nil {
				return err
			}
		case "data":
			dataSeen = true
			if parseData == nil {
				if err := skipValue(dec); err != nil {
					return err
				}
			} else if err := parseData(dec); err != nil {
				return err
			}
		default:
			if err := skipValue(dec); err != nil {
				return err
			}
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing response data")
	}
	if !codeSeen || code != 0 || !dataSeen {
		return errors.New("unsuccessful response envelope")
	}
	return nil
}

func decodeKnownObject(dec *json.Decoder, fields map[string]func(*json.Decoder) error) error {
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return errors.New("expected object")
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return errors.New("invalid object key")
		}
		if read, ok := fields[key]; ok {
			if err := read(dec); err != nil {
				return err
			}
		} else if err := skipValue(dec); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func decodeAccount(dec *json.Decoder, out *Account) error {
	var credentials accountCredentialsProjection
	var topLevelMapping map[string]string
	hasTopLevelMapping := false
	err := decodeKnownObject(dec, map[string]func(*json.Decoder) error{
		"id":                func(d *json.Decoder) error { return d.Decode(&out.ID) },
		"platform":          func(d *json.Decoder) error { return d.Decode(&out.Platform) },
		"type":              func(d *json.Decoder) error { return d.Decode(&out.Type) },
		"protocol":          func(d *json.Decoder) error { return d.Decode(&out.Protocol) },
		"api_protocol":      func(d *json.Decoder) error { return d.Decode(&out.Protocol) },
		"base_url":          func(d *json.Decoder) error { return d.Decode(&out.BaseURL) },
		"status":            func(d *json.Decoder) error { return d.Decode(&out.Status) },
		"parent_account_id": func(d *json.Decoder) error { return d.Decode(&out.ParentAccountID) },
		"model_mapping": func(d *json.Decoder) error {
			if err := d.Decode(&topLevelMapping); err != nil {
				return err
			}
			if topLevelMapping == nil {
				topLevelMapping = map[string]string{}
			}
			hasTopLevelMapping = true
			return nil
		},
		"credentials":        func(d *json.Decoder) error { return decodeAccountCredentials(d, &credentials) },
		"credentials_status": func(d *json.Decoder) error { return decodeCredentialsStatus(d, &out.APIKeyPresent) },
	})
	if err != nil {
		return err
	}
	// The official account DTO keeps protocol, base URL, and model mapping inside
	// credentials. Prefer that nested projection when present while retaining the
	// historical top-level fields for older response shapes.
	if credentials.hasProtocol {
		out.Protocol = credentials.Protocol
	}
	if credentials.hasBaseURL {
		out.BaseURL = credentials.BaseURL
	}
	if credentials.hasModelMapping {
		out.ModelMapping = credentials.ModelMapping
		out.ModelMappingPresent = true
	} else if hasTopLevelMapping {
		out.ModelMapping = topLevelMapping
		out.ModelMappingPresent = true
	}
	return nil
}

// decodeCredentialsStatus intentionally extracts only the redacted boolean
// presence marker. No credential values or other status fields are retained.
func decodeCredentialsStatus(dec *json.Decoder, hasAPIKey **bool) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	if tok != json.Delim('{') {
		return errors.New("credentials_status is not an object")
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return errors.New("invalid credentials_status key")
		}
		if key == "has_api_key" {
			var present bool
			if err := dec.Decode(&present); err != nil {
				return err
			}
			*hasAPIKey = &present
		} else if err := skipValue(dec); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

type accountCredentialsProjection struct {
	Protocol        string
	BaseURL         string
	ModelMapping    map[string]string
	hasProtocol     bool
	hasBaseURL      bool
	hasModelMapping bool
}

// decodeAccountCredentials retains only the small non-secret projection needed
// by discovery and apply. Every other credential field is consumed without
// being materialized, including API keys and header overrides.
func decodeAccountCredentials(dec *json.Decoder, out *accountCredentialsProjection) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	if tok != json.Delim('{') {
		return errors.New("credentials is not an object")
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return errors.New("invalid credentials key")
		}
		switch key {
		case "api_protocol":
			if err := dec.Decode(&out.Protocol); err != nil {
				return err
			}
			out.hasProtocol = true
		case "base_url":
			if err := dec.Decode(&out.BaseURL); err != nil {
				return err
			}
			out.hasBaseURL = true
		case "model_mapping":
			var value map[string]string
			if err := dec.Decode(&value); err != nil {
				return err
			}
			if value == nil {
				value = map[string]string{}
			}
			out.ModelMapping = value
			out.hasModelMapping = true
		default:
			if err := skipValue(dec); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token()
	return err
}

func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for dec.More() {
			if _, err := dec.Token(); err != nil {
				return err
			}
			if err := skipValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := skipValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
