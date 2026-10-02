package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	modelSourceMaxPages      = 100
	modelSourcePageSize      = 100
	modelSourceMaxResponse   = 8 << 20
	modelSourceDeepseekPath  = "/models"
	modelSourceAnthropicPath = "/v1/models"
	modelSourceAliyunPath    = "/api/v1/models"
)

type upstreamModelPage struct {
	IDs          []string
	HasMore      bool
	NextCursor   string
	Total        int
	ShutdownDate map[string]string
}

func (s *AccountTestService) FetchUpstreamAvailabilityModels(ctx context.Context, account *Account) (UpstreamModelSourceProfile, []string, map[string]string, error) {
	profile := DetectUpstreamModelSourceProfile(account)
	if profile.ManualOnly {
		return profile, nil, nil, newUpstreamModelSyncUnsupportedError("Automatic availability refresh is not enabled for this source", nil)
	}
	if s == nil || s.httpUpstream == nil || account == nil {
		return profile, nil, nil, newUpstreamModelSyncConfigError("Upstream model refresh is not configured", nil)
	}
	apiKey := strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
	if profile.Kind == "anthropic" {
		apiKey = strings.TrimSpace(account.GetCredential("api_key"))
	}
	if apiKey == "" {
		return profile, nil, nil, newUpstreamModelSyncConfigError("Upstream model source credential is missing", nil)
	}
	if profile.Kind == "openai" || profile.Kind == "deepseek" {
		ids, body, err := s.fetchUnpaginatedModelSource(ctx, account, profile)
		if err != nil {
			return profile, nil, nil, err
		}
		shutdownDates := map[string]string(nil)
		if profile.Kind == "openai" {
			shutdownDates = extractOpenAIShutdownDates(body)
		}
		return profile, ids, shutdownDates, nil
	}

	var ids []string
	shutdownDates := make(map[string]string)
	cursor := ""
	totalBytes := int64(0)
	seenCursors := make(map[string]struct{})
	aliyunExpectedTotal := 0
	aliyunSeenIDs := make(map[string]struct{})
	for pageNumber := 1; pageNumber <= modelSourceMaxPages; pageNumber++ {
		request, err := buildModelSourcePageRequest(ctx, account, profile, pageNumber, cursor)
		if err != nil {
			return profile, nil, nil, err
		}
		page, bodyBytes, err := s.doModelSourcePage(ctx, account, profile, request)
		if err != nil {
			return profile, nil, nil, err
		}
		totalBytes += int64(len(bodyBytes))
		if totalBytes > modelSourceMaxResponse {
			return profile, nil, nil, newUpstreamModelSyncUpstreamError("Upstream model catalog exceeds the total response limit", nil)
		}
		if profile.Kind == "aliyun" {
			if page.Total <= 0 {
				return profile, nil, nil, newUpstreamModelSyncUpstreamError("Alibaba model list omitted its total count", nil)
			}
			if aliyunExpectedTotal == 0 {
				aliyunExpectedTotal = page.Total
			} else if page.Total != aliyunExpectedTotal {
				return profile, nil, nil, newUpstreamModelSyncUpstreamError("Alibaba model list total count changed during pagination", nil)
			}
			for _, id := range page.IDs {
				id = strings.TrimSpace(id)
				if id == "" {
					return profile, nil, nil, newUpstreamModelSyncUpstreamError("Alibaba model list contained an empty model ID", nil)
				}
				if _, exists := aliyunSeenIDs[id]; exists {
					return profile, nil, nil, newUpstreamModelSyncUpstreamError("Alibaba model list pagination repeated a model ID", nil)
				}
				aliyunSeenIDs[id] = struct{}{}
				ids = append(ids, id)
			}
			if len(ids) > upstreamAvailabilityMaxIDs {
				return profile, nil, nil, newUpstreamModelSyncUpstreamError("Upstream model catalog exceeds the model count limit", nil)
			}
			if len(ids) > aliyunExpectedTotal {
				return profile, nil, nil, newUpstreamModelSyncUpstreamError("Alibaba model list pagination exceeded its total count", nil)
			}
			if len(ids) == aliyunExpectedTotal {
				for id, date := range page.ShutdownDate {
					shutdownDates[id] = date
				}
				return profile, ids, shutdownDates, nil
			}
			if len(page.IDs) == 0 || len(ids) >= modelSourceMaxPages*modelSourcePageSize {
				return profile, nil, nil, newUpstreamModelSyncUpstreamError("Alibaba model list pagination was incomplete", nil)
			}
			for id, date := range page.ShutdownDate {
				shutdownDates[id] = date
			}
			continue
		}
		ids = append(ids, page.IDs...)
		if len(ids) > upstreamAvailabilityMaxIDs {
			return profile, nil, nil, newUpstreamModelSyncUpstreamError("Upstream model catalog exceeds the model count limit", nil)
		}
		for id, date := range page.ShutdownDate {
			shutdownDates[id] = date
		}
		if !page.HasMore {
			return profile, ids, shutdownDates, nil
		}
		cursor = strings.TrimSpace(page.NextCursor)
		if cursor == "" {
			return profile, nil, nil, newUpstreamModelSyncUpstreamError("Anthropic model list pagination cursor is missing", nil)
		}
		if _, repeated := seenCursors[cursor]; repeated {
			return profile, nil, nil, newUpstreamModelSyncUpstreamError("Anthropic model list pagination cursor repeated", nil)
		}
		seenCursors[cursor] = struct{}{}
	}
	return profile, nil, nil, newUpstreamModelSyncUpstreamError("Upstream model list exceeded the page limit", nil)
}

func (s *AccountTestService) fetchUnpaginatedModelSource(ctx context.Context, account *Account, profile UpstreamModelSourceProfile) ([]string, []byte, error) {
	baseURL := profile.BaseURL
	if profile.Kind == "deepseek" {
		baseURL = strings.TrimRight(baseURL, "/")
		if strings.HasSuffix(baseURL, "/v1") {
			baseURL = strings.TrimSuffix(baseURL, "/v1")
		}
		baseURL += modelSourceDeepseekPath
	} else {
		baseURL = buildOpenAIModelsURL(baseURL)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, nil, newUpstreamModelSyncConfigError("Invalid trusted model source URL", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.GetCredential("api_key")))
	return s.fetchModelIDsFromRequest(ctx, account, profile, request)
}

func buildModelSourcePageRequest(ctx context.Context, account *Account, profile UpstreamModelSourceProfile, page int, cursor string) (*http.Request, error) {
	var requestURL string
	switch profile.Kind {
	case "anthropic":
		parsed, err := url.Parse(profile.BaseURL)
		if err != nil {
			return nil, newUpstreamModelSyncConfigError("Invalid Anthropic model source URL", err)
		}
		parsed.Path = modelSourceAnthropicPath
		parsed.RawPath = ""
		query := parsed.Query()
		query.Set("limit", strconv.Itoa(modelSourcePageSize))
		if cursor != "" {
			query.Set("after_id", cursor)
		}
		parsed.RawQuery = query.Encode()
		requestURL = parsed.String()
	case "aliyun":
		parsed, err := url.Parse(profile.BaseURL)
		if err != nil {
			return nil, newUpstreamModelSyncConfigError("Invalid Alibaba model source URL", err)
		}
		parsed.Path = modelSourceAliyunPath
		parsed.RawPath = ""
		query := parsed.Query()
		query.Set("supports", "inference")
		query.Set("page_no", strconv.Itoa(page))
		query.Set("page_size", strconv.Itoa(modelSourcePageSize))
		parsed.RawQuery = query.Encode()
		requestURL = parsed.String()
	default:
		return nil, newUpstreamModelSyncUnsupportedError("Pagination is not supported for this model source", nil)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid model source URL", err)
	}
	request.Header.Set("Accept", "application/json")
	if profile.Kind == "anthropic" {
		request.Header.Set("x-api-key", strings.TrimSpace(account.GetCredential("api_key")))
		request.Header.Set("anthropic-version", "2023-06-01")
	} else {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.GetCredential("api_key")))
	}
	return request, nil
}

func (s *AccountTestService) doModelSourcePage(ctx context.Context, account *Account, profile UpstreamModelSourceProfile, request *http.Request) (upstreamModelPage, []byte, error) {
	ids, body, err := s.fetchModelIDsFromRequest(ctx, account, profile, request)
	if err != nil {
		return upstreamModelPage{}, nil, err
	}
	page, err := parseModelSourcePage(profile.Kind, body, ids)
	if err != nil {
		return upstreamModelPage{}, nil, newUpstreamModelSyncUpstreamError("Upstream model list response was incomplete or invalid", err)
	}
	return page, body, nil
}

func (s *AccountTestService) fetchModelIDsFromRequest(ctx context.Context, account *Account, profile UpstreamModelSourceProfile, request *http.Request) ([]string, []byte, error) {
	response, err := s.doUpstreamModelsRequest(request, upstreamModelsProxyURL(account), account)
	if err != nil {
		return nil, nil, newUpstreamModelSyncUpstreamError("Failed to request upstream model list", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.Request == nil {
		return nil, nil, newUpstreamModelSyncUpstreamError("Model source response origin could not be verified", nil)
	}
	if !sameURLOrigin(request.URL, response.Request.URL) {
		return nil, nil, newUpstreamModelSyncUpstreamError("Model source redirected to a different origin", nil)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, modelSourceMaxResponse+1))
	if err != nil {
		return nil, nil, newUpstreamModelSyncUpstreamError("Failed to read upstream model list", err)
	}
	if len(body) > modelSourceMaxResponse {
		return nil, nil, newUpstreamModelSyncUpstreamError("Upstream model list response exceeds the size limit", nil)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		kind := UpstreamModelSyncErrorUpstream
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
			kind = UpstreamModelSyncErrorUnsupported
		}
		return nil, nil, &UpstreamModelSyncError{Kind: kind, Message: fmt.Sprintf("Upstream model list request failed with HTTP %d", response.StatusCode), StatusCode: response.StatusCode}
	}
	if profile.Kind == "aliyun" {
		page, parseErr := parseModelSourcePage(profile.Kind, body, nil)
		if parseErr != nil {
			return nil, nil, newUpstreamModelSyncUpstreamError("Alibaba model list response was invalid", parseErr)
		}
		return page.IDs, body, nil
	}
	ids, err := extractUpstreamModelIDs(body)
	if err != nil {
		return nil, nil, newUpstreamModelSyncUpstreamError("Upstream model list response was not valid JSON", err)
	}
	return ids, body, nil
}

func parseModelSourcePage(kind string, body []byte, parsedIDs []string) (upstreamModelPage, error) {
	switch kind {
	case "anthropic":
		var result struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return upstreamModelPage{}, err
		}
		ids := make([]string, 0, len(result.Data))
		for _, item := range result.Data {
			ids = append(ids, item.ID)
		}
		return upstreamModelPage{IDs: ids, HasMore: result.HasMore, NextCursor: result.LastID}, nil
	case "aliyun":
		var result struct {
			Success *bool `json:"success"`
			Output  struct {
				Total  int `json:"total"`
				Models []struct {
					ID string `json:"model"`
				} `json:"models"`
			} `json:"output"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return upstreamModelPage{}, err
		}
		if result.Success != nil && !*result.Success {
			return upstreamModelPage{}, fmt.Errorf("provider reported an unsuccessful model list")
		}
		ids := make([]string, 0, len(result.Output.Models))
		for _, item := range result.Output.Models {
			ids = append(ids, item.ID)
		}
		return upstreamModelPage{IDs: ids, Total: result.Output.Total}, nil
	default:
		return upstreamModelPage{IDs: parsedIDs}, nil
	}
}

func extractOpenAIShutdownDates(body []byte) map[string]string {
	var response struct {
		Data []struct {
			ID           string  `json:"id"`
			ShutdownDate *string `json:"shutdown_date"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil
	}
	result := make(map[string]string)
	for _, model := range response.Data {
		if model.ShutdownDate != nil {
			result[model.ID] = strings.TrimSpace(*model.ShutdownDate)
		}
	}
	return result
}

func sameURLOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}
