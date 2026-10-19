package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const (
	tavilySearchEndpoint   = "https://api.tavily.com/search"
	tavilyProviderName     = "tavily"
	tavilySearchDepthBasic = "basic"
)

// TavilyProvider implements web search via the Tavily Search API.
type TavilyProvider struct {
	apiKey     string
	httpClient *http.Client
}

// NewTavilyProvider creates a Tavily Search provider.
// The caller is responsible for configuring the http.Client with proxy/timeouts.
func NewTavilyProvider(apiKey string, httpClient *http.Client) *TavilyProvider {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &TavilyProvider{apiKey: strings.TrimSpace(apiKey), httpClient: httpClient}
}

func (t *TavilyProvider) Name() string { return tavilyProviderName }

func (t *TavilyProvider) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	if !ValidAPIKey(t.apiKey) {
		return nil, &ProviderError{Provider: tavilyProviderName, Kind: FailureInvalidKey}
	}
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}

	payload := tavilyRequest{
		Query:       req.Query,
		MaxResults:  maxResults,
		SearchDepth: tavilySearchDepthBasic,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, providerRequestError(tavilyProviderName, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilySearchEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, providerRequestError(tavilyProviderName, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// https://docs.tavily.com/documentation/api-reference/endpoint/search
	httpReq.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, providerRequestError(tavilyProviderName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, providerRequestError(tavilyProviderName, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, providerStatusError(tavilyProviderName, resp.StatusCode, body)
	}

	var raw tavilyResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, &ProviderError{Provider: tavilyProviderName, Kind: FailureResponse}
	}

	results := make([]SearchResult, 0, len(raw.Results))
	for _, r := range raw.Results {
		results = append(results, SearchResult{
			URL:     r.URL,
			Title:   r.Title,
			Snippet: r.Content,
		})
	}

	return &SearchResponse{Results: results, Query: req.Query}, nil
}

type tavilyRequest struct {
	Query       string `json:"query"`
	MaxResults  int    `json:"max_results"`
	SearchDepth string `json:"search_depth"`
}

type tavilyResponse struct {
	Results []tavilyResult `json:"results"`
}

type tavilyResult struct {
	URL     string  `json:"url"`
	Title   string  `json:"title"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}
