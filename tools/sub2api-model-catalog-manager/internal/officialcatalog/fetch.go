package officialcatalog

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	StartupSyncTimeout  = 60 * time.Second
	ProviderSyncTimeout = 10 * time.Second
	MaxHTTPGets         = 87
	MaxRedirects        = 2
	MaxAlibabaNotices   = 16
	MaxXAIMigrations    = 8
	MaxParsedLinks      = 4096
)

var sourceOrder = []string{"openai", "anthropic", "deepseek", "alibaba", "xai"}

type sourceSpec struct {
	id          string
	provider    string
	rootURL     string
	limitation  string
	maxBody     int64
	maxChildren int
}

var sourceManifest = []sourceSpec{
	{id: "openai-api-deprecations", provider: "openai", rootURL: "https://developers.openai.com/api/docs/deprecations", limitation: "Official OpenAI API deprecation evidence only; ChatGPT product changes and proxy routes may differ.", maxBody: 2 << 20},
	{id: "anthropic-model-deprecations", provider: "anthropic", rootURL: "https://platform.claude.com/docs/en/about-claude/model-deprecations", limitation: "Anthropic-operated API lifecycle only; partner-platform routes may differ.", maxBody: 2 << 20},
	{id: "deepseek-api-updates", provider: "deepseek", rootURL: "https://api-docs.deepseek.com/updates/", limitation: "Only explicit lifecycle notices on the official API change log; unmentioned IDs remain unknown.", maxBody: 2 << 20},
	{id: "alibaba-model-deprecation", provider: "alibaba", rootURL: "https://help.aliyun.com/zh/model-studio/model-depreciation", limitation: "Official Model Studio lifecycle notices only; proxy routes may differ.", maxBody: 2 << 20, maxChildren: MaxAlibabaNotices},
	{id: "xai-api-release-notes", provider: "xai", rootURL: "https://docs.x.ai/developers/release-notes", limitation: "Official xAI API release notes only; compatibility redirects do not prove third-party routing.", maxBody: 2 << 20, maxChildren: MaxXAIMigrations},
}

var (
	tagRE                    = regexp.MustCompile(`(?is)<[^>]+>`)
	spaceRE                  = regexp.MustCompile(`[\s\x{00a0}]+`)
	scriptRE                 = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>|<style\b[^>]*>.*?</style>|<nav\b[^>]*>.*?</nav>|<footer\b[^>]*>.*?</footer>`)
	titleRE                  = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)
	h1RE                     = regexp.MustCompile(`(?is)<h1\b[^>]*>(.*?)</h1>`)
	headingRE                = regexp.MustCompile(`(?is)<h[1-6]\b[^>]*>(.*?)</h[1-6]>`)
	rowRE                    = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	cellRE                   = regexp.MustCompile(`(?is)<t[dh]\b[^>]*>(.*?)</t[dh]>`)
	tableRE                  = regexp.MustCompile(`(?is)<table\b[^>]*>.*?</table>`)
	listItemRE               = regexp.MustCompile(`(?is)<li\b[^>]*>(.*?)</li>`)
	listBlockRE              = regexp.MustCompile(`(?is)<(?:ul|ol)\b[^>]*>.*?</(?:ul|ol)>`)
	linkRE                   = regexp.MustCompile(`(?is)<a\b[^>]*href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)
	modelTokenRE             = regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9]*(?:[._:-][a-zA-Z0-9]+)+\b`)
	dateISORe                = regexp.MustCompile(`\b(20\d{2}-\d{2}-\d{2})\b`)
	dateUSRe                 = regexp.MustCompile(`(?i)\b(January|February|March|April|May|June|July|August|September|October|November|December)\s+([0-3]?\d),?\s+(20\d{2})\b`)
	dateCNRe                 = regexp.MustCompile(`(20\d{2})\s*年\s*(\d{1,2})\s*月\s*(\d{1,2})\s*日`)
	monthDayRe               = regexp.MustCompile(`(?i)\b(January|February|March|April|May|June|July|August|September|October|November|December)\s+([0-3]?\d)\b`)
	aliyunPath               = regexp.MustCompile(`^/notice/([0-9]+)$`)
	aliyunID                 = regexp.MustCompile(`^[0-9]+$`)
	alibabaLifecycleAnchorRE = regexp.MustCompile(`不再提供|停止服务|下线|退役|停服`)
	xaiPath                  = regexp.MustCompile(`^/developers/migration/[a-z0-9-]+$`)
)

const alibabaBulletinRenderer = "https://t.aliyun.com/abs/bulletin/bulletinDetail"

type alibabaBulletinResponse struct {
	Data struct {
		Success bool `json:"success"`
		Info    struct {
			Title      string `json:"title"`
			DetailList []struct {
				ContentHTML string `json:"contentHtml"`
			} `json:"detailList"`
		} `json:"info"`
	} `json:"data"`
}

type fetchFailure struct{ code string }

func (f fetchFailure) Error() string { return f.code }

type requestBudget struct{ used atomic.Int64 }

func (b *requestBudget) reserve() bool {
	for {
		current := b.used.Load()
		if current >= MaxHTTPGets {
			return false
		}
		if b.used.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (b *requestBudget) count() int { return int(b.used.Load()) }

func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{Proxy: anonymousEnvironmentProxy, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
		Timeout:   ProviderSyncTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func anonymousEnvironmentProxy(req *http.Request) (*url.URL, error) {
	proxy, err := resolveAnonymousProxy(req, http.ProxyFromEnvironment)
	return proxy, err
}

func resolveAnonymousProxy(req *http.Request, resolve func(*http.Request) (*url.URL, error)) (*url.URL, error) {
	if resolve == nil {
		return nil, errors.New("proxy resolver unavailable")
	}
	proxy, err := resolve(req)
	if err != nil || proxy == nil {
		return proxy, err
	}
	if proxy.User != nil {
		return nil, errors.New("credentialed proxy is disabled")
	}
	return proxy, nil
}

func validateConfiguredProxy(resolve func(*http.Request) (*url.URL, error)) error {
	if resolve == nil {
		resolve = http.ProxyFromEnvironment
	}
	urls := make([]string, 0, 5+MaxAlibabaNotices+MaxXAIMigrations)
	for _, spec := range sourceManifest {
		urls = append(urls, spec.rootURL)
		for i := 0; i < spec.maxChildren; i++ {
			if spec.provider == "alibaba" {
				urls = append(urls, alibabaBulletinURL(fmt.Sprintf("%d", 1+i)))
			} else if spec.provider == "xai" {
				urls = append(urls, fmt.Sprintf("https://docs.x.ai/developers/migration/proxy-check-%d", i))
			}
		}
	}
	for _, raw := range urls {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			return errors.New("proxy_configuration_rejected")
		}
		if _, err := resolveAnonymousProxy(req, resolve); err != nil {
			return errors.New("proxy_configuration_rejected")
		}
	}
	return nil
}

type parsedPage struct {
	Title    string
	Links    []string
	Evidence []LifecycleEvidence
}

func sourceForProvider(provider string) (sourceSpec, bool) {
	for _, spec := range sourceManifest {
		if spec.provider == provider {
			return spec, true
		}
	}
	return sourceSpec{}, false
}

func validateSourceURL(spec sourceSpec, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" || u.ForceQuery {
		return fetchFailure{code: "source_url_rejected"}
	}
	switch spec.provider {
	case "openai":
		if u.Host != "developers.openai.com" || u.Path != "/api/docs/deprecations" || u.RawQuery != "" {
			return fetchFailure{code: "source_url_rejected"}
		}
	case "anthropic":
		if u.Host != "platform.claude.com" || u.Path != "/docs/en/about-claude/model-deprecations" || u.RawQuery != "" {
			return fetchFailure{code: "source_url_rejected"}
		}
	case "deepseek":
		if u.Host != "api-docs.deepseek.com" || u.Path != "/updates" && u.Path != "/updates/" || u.RawQuery != "" {
			return fetchFailure{code: "source_url_rejected"}
		}
	case "alibaba":
		if u.Host == "help.aliyun.com" && u.Path == "/zh/model-studio/model-depreciation" {
			if u.RawQuery == "" {
				return nil
			}
		}
		if !isAlibabaRendererURL(u) {
			return fetchFailure{code: "source_url_rejected"}
		}
	case "xai":
		if u.Host != "docs.x.ai" || (u.Path != "/developers/release-notes" && !xaiPath.MatchString(u.Path)) || u.RawQuery != "" {
			return fetchFailure{code: "source_url_rejected"}
		}
	default:
		return fetchFailure{code: "source_url_rejected"}
	}
	return nil
}

func aliyunNoticeIDFromIndexURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "www.aliyun.com" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", false
	}
	match := aliyunPath.FindStringSubmatch(u.Path)
	if len(match) != 2 || !aliyunID.MatchString(match[1]) {
		return "", false
	}
	return match[1], true
}

func isAlibabaNoticeLandingURL(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && u.Host == "www.aliyun.com" && u.User == nil && u.Port() == "" && u.Path == "/notice/" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

func alibabaBulletinURL(id string) string {
	if !aliyunID.MatchString(id) {
		return ""
	}
	return fmt.Sprintf("%s?id=%s&language=zh&website=cn", alibabaBulletinRenderer, id)
}

func isAlibabaRendererURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.Host != "t.aliyun.com" || u.User != nil || u.Port() != "" || u.Path != "/abs/bulletin/bulletinDetail" || u.Fragment != "" {
		return false
	}
	if !strings.HasPrefix(u.RawQuery, "id=") {
		return false
	}
	parts := strings.Split(u.RawQuery, "&")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "id=") || !aliyunID.MatchString(strings.TrimPrefix(parts[0], "id=")) {
		return false
	}
	return parts[1] == "language=zh" && parts[2] == "website=cn"
}

func isAlibabaRendererRequestURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && isAlibabaRendererURL(u)
}

func fetchSource(ctx context.Context, spec sourceSpec, prior *CatalogSnapshot, doer HTTPDoer, budget *requestBudget, now func() time.Time) (CatalogSnapshot, error) {
	if ctx == nil || doer == nil || budget == nil {
		return CatalogSnapshot{}, fetchFailure{code: "source_fetch_unavailable"}
	}
	if now == nil {
		now = time.Now
	}
	oldPages := make(map[string]CachedPage)
	if prior != nil {
		for _, page := range prior.Pages {
			oldPages[page.URL] = page
		}
	}
	urls := []string{spec.rootURL}
	pages := make(map[string]CachedPage, 1+spec.maxChildren)
	for len(urls) > 0 {
		current := urls[0]
		urls = urls[1:]
		if _, exists := pages[current]; exists {
			continue
		}
		if err := validateSourceURL(spec, current); err != nil {
			return CatalogSnapshot{}, err
		}
		old, hasOld := oldPages[current]
		page, err := fetchAndParsePage(ctx, spec, current, old, hasOld, doer, budget, now)
		if err != nil {
			return CatalogSnapshot{}, err
		}
		pages[current] = page
		if current == spec.rootURL {
			children, err := validateChildren(spec, current, page.Links)
			if err != nil {
				return CatalogSnapshot{}, err
			}
			urls = append(urls, children...)
		}
	}
	ordered := make([]CachedPage, 0, len(pages))
	for _, page := range pages {
		ordered = append(ordered, page)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].URL < ordered[j].URL })
	all := make([]LifecycleEvidence, 0)
	for _, page := range ordered {
		all = append(all, page.Evidence...)
	}
	all = normalizeEvidence(all)
	checked := now().UTC()
	fetched := time.Time{}
	for _, page := range ordered {
		if page.FetchedAt.After(fetched) {
			fetched = page.FetchedAt
		}
	}
	if fetched.IsZero() {
		fetched = checked
	}
	digest, err := snapshotDigest(ordered)
	if err != nil {
		return CatalogSnapshot{}, fetchFailure{code: "source_parse_error"}
	}
	return CatalogSnapshot{Provider: spec.provider, Pages: ordered, Evidence: all, FetchedAt: fetched, CheckedAt: checked, ContentSHA256: digest, Complete: true}, nil
}

func fetchAndParsePage(ctx context.Context, spec sourceSpec, rawURL string, prior CachedPage, hasPrior bool, doer HTTPDoer, budget *requestBudget, now func() time.Time) (CachedPage, error) {
	if err := validateSourceURL(spec, rawURL); err != nil {
		return CachedPage{}, err
	}
	current := rawURL
	for redirects := 0; redirects <= MaxRedirects; redirects++ {
		if !budget.reserve() {
			return CachedPage{}, fetchFailure{code: "source_request_budget_exceeded"}
		}
		reqCtx, cancel := context.WithTimeout(ctx, ProviderSyncTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, current, nil)
		if err != nil {
			cancel()
			return CachedPage{}, fetchFailure{code: "source_request_invalid"}
		}
		req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.1")
		req.Header.Set("User-Agent", "sub2api-model-catalog-manager/official-lifecycle-review")
		if spec.provider == "alibaba" && isAlibabaRendererRequestURL(current) {
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Origin", "https://www.aliyun.com")
		}
		if current == rawURL && hasPrior {
			if prior.ETag != "" {
				req.Header.Set("If-None-Match", prior.ETag)
			}
			if prior.LastModified != "" {
				req.Header.Set("If-Modified-Since", prior.LastModified)
			}
		}
		resp, err := doer.Do(req)
		if err != nil || resp == nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			cancel()
			return CachedPage{}, fetchFailure{code: "http_error"}
		}
		if isRedirect(resp.StatusCode) {
			location := resp.Header.Get("Location")
			_ = resp.Body.Close()
			cancel()
			if redirects >= MaxRedirects || location == "" {
				return CachedPage{}, fetchFailure{code: "redirect_limit_exceeded"}
			}
			base, _ := url.Parse(current)
			ref, parseErr := url.Parse(location)
			if parseErr != nil {
				return CachedPage{}, fetchFailure{code: "redirect_rejected"}
			}
			current = base.ResolveReference(ref).String()
			if isAlibabaRendererRequestURL(rawURL) && current != rawURL {
				return CachedPage{}, fetchFailure{code: "redirect_rejected"}
			}
			if err := validateSourceURL(spec, current); err != nil {
				return CachedPage{}, fetchFailure{code: "redirect_rejected"}
			}
			continue
		}
		if resp.StatusCode == http.StatusNotModified {
			_ = resp.Body.Close()
			cancel()
			if !hasPrior || prior.URL != rawURL || prior.ContentSHA256 == "" || prior.Title == "" {
				return CachedPage{}, fetchFailure{code: "invalid_not_modified_response"}
			}
			return prior, nil
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			cancel()
			return CachedPage{}, fetchFailure{code: "http_error"}
		}
		limit := spec.maxBody
		if rawURL != spec.rootURL {
			limit = 1 << 20
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		closeErr := resp.Body.Close()
		cancel()
		if readErr != nil || closeErr != nil {
			return CachedPage{}, fetchFailure{code: "http_error"}
		}
		if len(body) == 0 || int64(len(body)) > limit {
			return CachedPage{}, fetchFailure{code: "response_too_large_or_empty"}
		}
		parsed, parseErr := parseSourcePage(spec, rawURL, body, now().UTC())
		if parseErr != nil {
			return CachedPage{}, fetchFailure{code: "source_parse_error"}
		}
		hash := sha256.Sum256(body)
		return CachedPage{
			URL: rawURL, Title: parsed.Title, ContentSHA256: hex.EncodeToString(hash[:]), FetchedAt: now().UTC(),
			ETag: safeHeader(resp.Header.Get("ETag")), LastModified: safeHeader(resp.Header.Get("Last-Modified")),
			Links: parsed.Links, Evidence: parsed.Evidence,
		}, nil
	}
	return CachedPage{}, fetchFailure{code: "redirect_limit_exceeded"}
}

func validateChildren(spec sourceSpec, root string, links []string) ([]string, error) {
	if spec.maxChildren == 0 {
		return nil, nil
	}
	rootURL, _ := url.Parse(root)
	set := make(map[string]bool)
	children := make([]string, 0)
	for _, href := range links {
		parsed, err := url.Parse(href)
		if err != nil {
			return nil, fetchFailure{code: "source_parse_error"}
		}
		resolved := rootURL.ResolveReference(parsed).String()
		if spec.provider == "alibaba" {
			id, validNotice := aliyunNoticeIDFromIndexURL(resolved)
			if !validNotice {
				candidateURL, _ := url.Parse(resolved)
				if candidateURL != nil && strings.HasPrefix(candidateURL.Path, "/notice/") {
					if isAlibabaNoticeLandingURL(candidateURL) {
						continue
					}
					return nil, fetchFailure{code: "source_link_rejected"}
				}
				continue
			}
			target := alibabaBulletinURL(id)
			if !set[target] {
				set[target] = true
				children = append(children, target)
			}
			continue
		}
		if validateSourceURL(spec, resolved) != nil {
			candidateURL, _ := url.Parse(resolved)
			alibabaNoticeCandidate := candidateURL != nil && strings.HasPrefix(candidateURL.Path, "/notice/") && candidateURL.Path != "/notice/"
			xaiMigrationCandidate := candidateURL != nil && strings.Contains(candidateURL.Path, "/developers/migration/")
			if alibabaNoticeCandidate || xaiMigrationCandidate {
				return nil, fetchFailure{code: "source_link_rejected"}
			}
			continue
		}
		if resolved == root || set[resolved] {
			continue
		}
		set[resolved] = true
		children = append(children, resolved)
	}
	sort.Strings(children)
	if len(children) > spec.maxChildren {
		return nil, fetchFailure{code: "source_link_limit_exceeded"}
	}
	return children, nil
}

func parseAlibabaBulletin(spec sourceSpec, sourceURL string, body []byte, retrievedAt time.Time) (parsedPage, error) {
	var response alibabaBulletinResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return parsedPage{}, errors.New("Alibaba bulletin response is not valid JSON")
	}
	if !response.Data.Success {
		return parsedPage{}, errors.New("Alibaba bulletin response reports failure")
	}
	title := strings.TrimSpace(response.Data.Info.Title)
	if title == "" {
		return parsedPage{}, errors.New("Alibaba bulletin title is empty")
	}
	contents := make([]string, 0, len(response.Data.Info.DetailList))
	for _, detail := range response.Data.Info.DetailList {
		if strings.TrimSpace(detail.ContentHTML) != "" {
			contents = append(contents, detail.ContentHTML)
		}
	}
	if len(contents) == 0 {
		return parsedPage{}, errors.New("Alibaba bulletin has no rendered content")
	}
	page := parsedPage{Title: shortText(title, 200)}
	hash := sha256.Sum256(body)
	for _, contentHTML := range contents {
		visible := extractVisibleText(contentHTML)
		proseHTML := tableRE.ReplaceAllString(contentHTML, " ")
		proseHTML = listBlockRE.ReplaceAllString(proseHTML, " ")
		prose := extractVisibleText(proseHTML)
		page.Evidence = append(page.Evidence, alibabaProseEvidence(prose, sourceURL, page.Title, retrievedAt, hex.EncodeToString(hash[:]))...)
		page.Evidence = append(page.Evidence, alibabaListEvidence(contentHTML, visible, sourceURL, page.Title, retrievedAt, hex.EncodeToString(hash[:]))...)
		page.Evidence = append(page.Evidence, alibabaTableEvidence(contentHTML, visible, sourceURL, page.Title, retrievedAt, hex.EncodeToString(hash[:]))...)
	}
	page.Evidence = normalizeEvidence(page.Evidence)
	return page, nil
}

func alibabaProseEvidence(prose, sourceURL, title string, retrievedAt time.Time, digest string) []LifecycleEvidence {
	var evidence []LifecycleEvidence
	for _, anchor := range alibabaLifecycleAnchorRE.FindAllStringIndex(prose, -1) {
		start := strings.LastIndexAny(prose[:anchor[0]], "。！？；：:\n") + 1
		prefix := strings.TrimSpace(prose[start:anchor[0]])
		after := strings.TrimSpace(prose[anchor[0]:])
		if containsAny(prefix, "清单", "名单") || strings.HasPrefix(after, "下线清单") || strings.HasPrefix(after, "下线名单") {
			continue
		}
		ids := extractAlibabaModelIDs([]string{prefix})
		if len(ids) == 0 {
			// Support explicit titles such as “下线模型：qwen-x”; do not
			// treat a navigation/list label or replacement paragraph as a model.
			end := strings.IndexAny(after, "。！？；：:\n")
			if end < 0 || end > 100 {
				end = len(after)
				if end > 100 {
					end = 100
				}
			}
			candidate := after[:end]
			if !containsAny(candidate, "清单", "名单", "如下", "推荐", "替换", "方案") {
				ids = extractAlibabaModelIDs([]string{candidate})
			}
		}
		if len(ids) == 0 {
			continue
		}
		contextEnd := len(prose)
		if next := strings.IndexAny(prose[anchor[1]:], "。！？；\n"); next >= 0 {
			contextEnd = anchor[1] + next
		}
		context := strings.TrimSpace(prose[start:contextEnd])
		status := "announced_deprecated"
		if containsAny(context, "已下线", "已退役", "已停止服务", "已停服", "已不再提供") {
			status = "retired"
		}
		for _, id := range ids {
			evidence = append(evidence, LifecycleEvidence{
				Provider: "alibaba", ModelID: id, Status: status, EffectiveAt: findDate(context),
				SourceURL: sourceURL, SourceTitle: title, RetrievedAt: retrievedAt,
				ContentSHA256: digest, Excerpt: shortText(context, 360),
			})
		}
	}
	return evidence
}

func alibabaListEvidence(contentHTML, visible, sourceURL, title string, retrievedAt time.Time, digest string) []LifecycleEvidence {
	lower := strings.ToLower(visible)
	labels := []string{"具体下线清单如下", "下线清单如下", "下线名单如下", "下线模型清单如下"}
	ends := []string{"推荐替换方案", "推荐替换模型", "推荐替代方案", "替换方案", "替代模型"}
	var evidence []LifecycleEvidence
	for _, label := range labels {
		start := strings.Index(lower, strings.ToLower(label))
		if start < 0 {
			continue
		}
		start += len(label)
		end := len(visible)
		for _, marker := range ends {
			if pos := strings.Index(lower[start:], strings.ToLower(marker)); pos >= 0 && start+pos < end {
				end = start + pos
			}
		}
		segment := strings.TrimSpace(visible[start:end])
		ids := make([]string, 0)
		for _, itemMatch := range listItemRE.FindAllStringSubmatch(contentHTML, -1) {
			if len(itemMatch) != 2 {
				continue
			}
			item := cleanFragment(itemMatch[1])
			compactSegment := strings.ToLower(strings.ReplaceAll(segment, " ", ""))
			compactItem := strings.ToLower(strings.ReplaceAll(item, " ", ""))
			if item == "" || !strings.Contains(compactSegment, compactItem) {
				continue
			}
			for _, separator := range []string{"：", ":"} {
				if colon := strings.Index(item, separator); colon >= 0 {
					item = strings.TrimSpace(item[colon+len(separator):])
					break
				}
			}
			ids = append(ids, extractAlibabaModelIDs([]string{item})...)
		}
		ids = uniqueStrings(ids)
		if len(ids) == 0 {
			continue
		}
		prefix := visible[:start]
		if len(prefix) > 300 {
			prefix = prefix[len(prefix)-300:]
		}
		contextText := prefix + " " + segment
		status := "announced_deprecated"
		if containsAny(contextText, "已下线", "已退役", "已停止服务", "已停服") {
			status = "retired"
		}
		for _, id := range ids {
			evidence = append(evidence, LifecycleEvidence{
				Provider: "alibaba", ModelID: id, Status: status, EffectiveAt: findDate(contextText),
				SourceURL: sourceURL, SourceTitle: title, RetrievedAt: retrievedAt,
				ContentSHA256: digest, Excerpt: shortText(segment, 360),
			})
		}
		break
	}
	return evidence
}

func alibabaTableEvidence(contentHTML, visible, sourceURL, title string, retrievedAt time.Time, digest string) []LifecycleEvidence {
	if !containsAny(visible, "下线", "退役", "停止服务", "停服") || !containsAny(visible, "下表", "下线清单", "下线名单") {
		return nil
	}
	var evidence []LifecycleEvidence
	for _, tableMatch := range tableRE.FindAllStringIndex(contentHTML, -1) {
		table := contentHTML[tableMatch[0]:tableMatch[1]]
		rows := extractTableRows(table)
		if len(rows) < 2 || !containsAny(strings.Join(rows[0], " "), "模型名称", "模型名") {
			continue
		}
		replacementColumn := -1
		for index, header := range rows[0] {
			if isAlibabaReplacementHeader(header) {
				replacementColumn = index
				break
			}
		}
		prefixStart := tableMatch[0] - 1200
		if prefixStart < 0 {
			prefixStart = 0
		}
		prefix := extractVisibleText(contentHTML[prefixStart:tableMatch[0]])
		if !containsAny(prefix, "下表", "下线清单", "下线名单") || !containsAny(prefix, "下线", "退役", "停止服务", "停服") {
			continue
		}
		for _, row := range rows[1:] {
			if len(row) == 0 {
				continue
			}
			ids := extractAlibabaModelIDs([]string{row[0]})
			status := "announced_deprecated"
			rowText := strings.Join(row, " | ")
			if containsAny(prefix+" "+rowText, "已下线", "已退役", "已停止服务", "已停服") {
				status = "retired"
			}
			date := findDate(rowText)
			if date == "" {
				date = findDate(prefix)
			}
			for _, id := range ids {
				replacement := ""
				if replacementColumn >= 0 && replacementColumn < len(row) {
					if replacementIDs := extractAlibabaModelIDs([]string{row[replacementColumn]}); len(replacementIDs) == 1 && replacementIDs[0] != id {
						replacement = replacementIDs[0]
					}
				}
				evidence = append(evidence, LifecycleEvidence{
					Provider: "alibaba", ModelID: id, Status: status, EffectiveAt: date,
					Replacement: replacement,
					SourceURL:   sourceURL, SourceTitle: title, RetrievedAt: retrievedAt,
					ContentSHA256: digest, Excerpt: shortText(rowText, 360),
				})
			}
		}
	}
	return evidence
}

func isAlibabaReplacementHeader(header string) bool {
	header = strings.ToLower(strings.TrimSpace(header))
	return containsAny(header, "推荐替换模型", "推荐替代模型", "推荐替换型号", "推荐替代型号", "替换模型", "替代模型", "replacement model", "recommended replacement")
}

func openAIDeprecationRows(source string) ([][]string, bool) {
	var rows [][]string
	foundTable := false
	for _, table := range tableRE.FindAllString(source, -1) {
		tableRows := extractTableRows(table)
		if len(tableRows) == 0 {
			continue
		}
		shutdownIndex, modelIndex, replacementIndex := -1, -1, -1
		for index, rawHeader := range tableRows[0] {
			header := strings.ToLower(strings.TrimSpace(rawHeader))
			switch header {
			case "shutdown date":
				shutdownIndex = index
			case "model", "model / system", "model/system":
				modelIndex = index
			case "recommended replacement":
				replacementIndex = index
			}
		}
		if shutdownIndex < 0 || modelIndex < 0 || replacementIndex < 0 {
			continue
		}
		foundTable = true
		for _, row := range tableRows[1:] {
			if shutdownIndex >= len(row) || modelIndex >= len(row) || replacementIndex >= len(row) {
				continue
			}
			rows = append(rows, []string{row[shutdownIndex], row[modelIndex], row[replacementIndex]})
		}
	}
	return rows, foundTable
}

func parseSourcePage(spec sourceSpec, sourceURL string, body []byte, retrievedAt time.Time) (parsedPage, error) {
	if validateSourceURL(spec, sourceURL) != nil {
		return parsedPage{}, errors.New("source URL is outside the fixed manifest")
	}
	if spec.provider == "alibaba" && sourceURL != spec.rootURL {
		return parseAlibabaBulletin(spec, sourceURL, body, retrievedAt)
	}
	text := extractVisibleText(string(body))
	title := extractPageTitle(string(body))
	headings := extractHeadings(string(body))
	if title == "" {
		return parsedPage{}, errors.New("source title missing")
	}
	page := parsedPage{Title: title, Links: extractLinks(sourceURL, string(body))}
	if len(linkRE.FindAllString(string(body), MaxParsedLinks+1)) > MaxParsedLinks {
		return parsedPage{}, errors.New("source has too many links")
	}
	addEvidence := func(e LifecycleEvidence) {
		e.Provider, e.SourceURL, e.SourceTitle, e.RetrievedAt = spec.provider, sourceURL, title, retrievedAt
		hash := sha256.Sum256(body)
		e.ContentSHA256 = hex.EncodeToString(hash[:])
		e.Excerpt = shortText(e.Excerpt, 360)
		page.Evidence = append(page.Evidence, e)
	}
	switch spec.provider {
	case "openai":
		apiRows, hasAPIDeprecationTable := openAIDeprecationRows(string(body))
		if !strings.Contains(strings.ToLower(title), "deprecations") || !strings.Contains(text, "Upcoming deprecations") || !strings.Contains(text, "Past deprecations") || !hasAPIDeprecationTable {
			return parsedPage{}, errors.New("OpenAI page structure changed")
		}
		for _, row := range apiRows {
			if len(row) < 3 || !looksLikeDate(row[0]) || !validLifecycleModelCell(row[1]) {
				continue
			}
			effective := normalizeDate(row[0])
			status := "announced_deprecated"
			if parsed, err := parseAnyDate(effective); err == nil && parsed.Before(retrievedAt) {
				status = "retired"
			}
			replacementIDs := splitModelIDs(row[2])
			replacement := ""
			if len(replacementIDs) == 1 {
				replacement = replacementIDs[0]
			}
			for _, id := range splitModelIDs(row[1]) {
				addEvidence(LifecycleEvidence{ModelID: id, Status: status, EffectiveAt: effective, Replacement: replacement, Excerpt: strings.Join(row, " | ")})
			}
		}
	case "anthropic":
		if !strings.Contains(strings.ToLower(title), "model deprecations") || !tableHasHeaders(string(body), "API model name", "Current state", "Deprecated", "Tentative retirement date") {
			return parsedPage{}, errors.New("Anthropic page structure changed")
		}
		states := map[string][]LifecycleEvidence{}
		for _, row := range extractTableRows(string(body)) {
			if len(row) < 4 || !validModelID(strings.TrimSpace(row[0])) {
				continue
			}
			state := strings.ToLower(strings.TrimSpace(row[1]))
			status := map[string]string{"deprecated": "announced_deprecated", "retired": "retired", "legacy": "legacy"}[state]
			if status == "" {
				continue
			}
			e := LifecycleEvidence{ModelID: strings.TrimSpace(row[0]), Status: status, AnnouncedAt: normalizeDate(row[2]), EffectiveAt: normalizeDate(row[3]), Excerpt: strings.Join(row, " | ")}
			states[e.ModelID] = append(states[e.ModelID], e)
		}
		for _, row := range extractTableRows(string(body)) {
			if len(row) < 3 || !strings.Contains(strings.ToLower(strings.Join(row, " ")), "claude-") || !looksLikeDate(row[0]) {
				continue
			}
			for _, id := range splitModelIDs(row[1]) {
				stateEvidence := states[id]
				if len(stateEvidence) == 0 {
					status := "announced_deprecated"
					if parsed, err := parseAnyDate(normalizeDate(row[0])); err == nil && parsed.Before(retrievedAt) {
						status = "retired"
					}
					stateEvidence = []LifecycleEvidence{{ModelID: id, Status: status}}
				}
				for _, base := range stateEvidence {
					e := base
					if date := normalizeDate(row[0]); date != "" {
						e.EffectiveAt = date
					}
					e.Replacement = safeText(row[2])
					e.Excerpt = strings.Join(row, " | ")
					states[id] = append(states[id], e)
				}
			}
		}
		for _, records := range states {
			for _, e := range records {
				addEvidence(e)
			}
		}
	case "deepseek":
		if !strings.EqualFold(strings.TrimSpace(strings.TrimSuffix(title, "| DeepSeek API Docs")), "Change Log") || !regexp.MustCompile(`(?i)Date:\s*20\d{2}-\d{2}-\d{2}`).MatchString(text) {
			return parsedPage{}, errors.New("DeepSeek page structure changed")
		}
		for _, sentence := range splitSentences(text) {
			lower := strings.ToLower(sentence)
			if !containsAny(lower, "deprecated", "retired", "discontinued", "will be discontinued", "end of life", "sunset") {
				continue
			}
			ids := lifecycleSubjects(sentence, "deepseek-", "deprecated", "retired", "discontinued", "end of life", "sunset")
			if len(ids) == 0 {
				continue
			}
			status := "announced_deprecated"
			if strings.Contains(lower, "point to") || strings.Contains(lower, "routed to") || strings.Contains(lower, "redirect") {
				status = "retired_with_compatibility_route"
			} else if date := findDate(sentence); date != "" {
				if parsed, err := parseAnyDate(date); err == nil && parsed.Before(retrievedAt) {
					status = "retired"
				}
			}
			replacement := ""
			if index := firstIndex(lower, "point to", "routed to", "redirected to"); index >= 0 {
				for _, id := range filterModelIDs(modelTokenRE.FindAllString(sentence, -1), "deepseek-") {
					if strings.Index(strings.ToLower(sentence), strings.ToLower(id)) > index {
						replacement = id
						break
					}
				}
			}
			for _, id := range ids {
				addEvidence(LifecycleEvidence{ModelID: id, Status: status, EffectiveAt: findDate(sentence), Replacement: replacement, Excerpt: sentence})
			}
		}
	case "alibaba":
		if !strings.Contains(title, "模型下线机制说明") || !strings.Contains(text, "下线模型列表") || !dateCNRe.MatchString(text) || !containsAny(text, "将下线", "已下线") {
			return parsedPage{}, errors.New("Alibaba page structure changed")
		}
		for _, row := range extractTableRows(string(body)) {
			joined := strings.Join(row, " ")
			if !containsAny(joined, "将下线", "已下线", "下线") {
				continue
			}
			date := findDate(joined)
			for _, id := range extractAlibabaModelIDs(row) {
				status := "announced_deprecated"
				if strings.Contains(joined, "已下线") {
					status = "retired"
				}
				addEvidence(LifecycleEvidence{ModelID: id, Status: status, EffectiveAt: date, Excerpt: joined})
			}
		}
	case "xai":
		if sourceURL == spec.rootURL {
			if !strings.Contains(strings.ToLower(title), "release notes") || len(headings) < 2 || !hasMonthHeading(headings) || findDate(text) == "" {
				return parsedPage{}, errors.New("xAI release notes page structure changed")
			}
		} else if !hasHeadingContaining(headings, "migration") {
			return parsedPage{}, errors.New("xAI migration page structure changed")
		}
		sentences := splitSentences(text)
		lifecycle := make(map[string][]LifecycleEvidence)
		type routeRecord struct {
			replacement string
			excerpt     string
		}
		routes := make(map[string][]routeRecord)
		for sentenceIndex, sentence := range sentences {
			lower := strings.ToLower(sentence)
			allIDs := filterModelIDs(modelTokenRE.FindAllString(sentence, -1), "grok-")
			if containsAny(lower, "deprecated", "retired", "end of life", "retirement") {
				ids := lifecycleSubjects(sentence, "grok-", "deprecated", "retired", "end of life", "retirement")
				status := "announced_deprecated"
				if date := findDate(sentence); date != "" {
					if parsed, err := parseAnyDate(date); err == nil && parsed.Before(retrievedAt) {
						status = "retired"
					}
				}
				for _, id := range ids {
					evidence := LifecycleEvidence{ModelID: id, Status: status, EffectiveAt: findDate(sentence), Excerpt: sentence}
					lifecycle[id] = append(lifecycle[id], evidence)
				}
			}
			if routeIndex := firstIndex(lower, "routed to", "served by", "redirected to"); routeIndex >= 0 {
				var sourceIDs, replacementIDs []string
				for _, id := range allIDs {
					idIndex := strings.Index(lower, strings.ToLower(id))
					if idIndex < routeIndex {
						sourceIDs = append(sourceIDs, id)
					} else if idIndex > routeIndex {
						replacementIDs = append(replacementIDs, id)
					}
				}
				if len(sourceIDs) == 0 && len(replacementIDs) > 0 && sentenceIndex > 0 {
					previous := sentences[sentenceIndex-1]
					for _, id := range lifecycleSubjects(previous, "grok-", "deprecated", "retired", "end of life", "retirement") {
						if len(lifecycle[id]) > 0 {
							sourceIDs = append(sourceIDs, id)
						}
					}
				}
				if len(replacementIDs) > 0 {
					for _, sourceID := range sourceIDs {
						routes[sourceID] = append(routes[sourceID], routeRecord{replacement: replacementIDs[0], excerpt: sentence})
					}
				}
			}
		}
		for id, records := range lifecycle {
			for _, evidence := range records {
				if routeRecords := routes[id]; len(routeRecords) > 0 {
					for _, route := range routeRecords {
						routed := evidence
						routed.Status = "retired_with_compatibility_route"
						routed.Replacement = route.replacement
						routed.Excerpt += " " + route.excerpt
						addEvidence(routed)
					}
					continue
				}
				addEvidence(evidence)
			}
		}
	}
	page.Evidence = normalizeEvidence(page.Evidence)
	return page, nil
}

func extractVisibleText(source string) string {
	source = scriptRE.ReplaceAllString(source, " ")
	source = regexp.MustCompile(`(?i)</(h[1-6]|p|tr|td|th|li|section|article|br|div|summary)>`).ReplaceAllString(source, "\n")
	source = tagRE.ReplaceAllString(source, " ")
	source = html.UnescapeString(source)
	return strings.TrimSpace(spaceRE.ReplaceAllString(source, " "))
}

func extractPageTitle(source string) string {
	if match := h1RE.FindStringSubmatch(source); len(match) == 2 {
		if title := cleanFragment(match[1]); title != "" {
			return shortText(title, 200)
		}
	}
	if match := titleRE.FindStringSubmatch(source); len(match) == 2 {
		return shortText(cleanFragment(match[1]), 200)
	}
	return ""
}

func extractHeadings(source string) []string {
	out := make([]string, 0)
	for _, match := range headingRE.FindAllStringSubmatch(source, -1) {
		if len(match) == 2 {
			if heading := cleanFragment(match[1]); heading != "" {
				out = append(out, heading)
			}
		}
	}
	return out
}

func hasMonthHeading(headings []string) bool {
	for _, heading := range headings {
		if monthDayRe.MatchString(heading) || regexp.MustCompile(`(?i)\b(January|February|March|April|May|June|July|August|September|October|November|December)\b`).MatchString(heading) {
			return true
		}
	}
	return false
}

func hasHeadingContaining(headings []string, token string) bool {
	for _, heading := range headings {
		if strings.Contains(strings.ToLower(heading), strings.ToLower(token)) {
			return true
		}
	}
	return false
}

func extractTableRows(source string) [][]string {
	rows := make([][]string, 0)
	for _, row := range rowRE.FindAllStringSubmatch(source, -1) {
		cells := make([]string, 0)
		for _, cell := range cellRE.FindAllStringSubmatch(row[1], -1) {
			cells = append(cells, cleanFragment(cell[1]))
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	return rows
}

func tableHasHeaders(source string, wanted ...string) bool {
	for _, row := range extractTableRows(source) {
		joined := strings.ToLower(strings.Join(row, " | "))
		all := true
		for _, header := range wanted {
			if !strings.Contains(joined, strings.ToLower(header)) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func extractLinks(base, source string) []string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil
	}
	set := make(map[string]bool)
	for _, link := range linkRE.FindAllStringSubmatch(source, -1) {
		if len(link) != 3 {
			continue
		}
		href := html.UnescapeString(strings.TrimSpace(link[1]))
		parsed, err := url.Parse(href)
		if err != nil {
			continue
		}
		resolved := baseURL.ResolveReference(parsed)
		set[resolved.String()] = true
	}
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func cleanFragment(s string) string {
	s = tagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
	return s
}

func splitModelIDs(cell string) []string {
	ids := make([]string, 0)
	for _, chunk := range strings.FieldsFunc(cell, func(r rune) bool { return r == '|' || r == ',' || r == ';' || r == '\n' }) {
		chunk = strings.TrimSpace(chunk)
		if validModelID(chunk) && modelTokenRE.MatchString(chunk) {
			ids = append(ids, chunk)
		}
	}
	return uniqueStrings(ids)
}

func validLifecycleModelCell(s string) bool { return len(splitModelIDs(s)) > 0 }

func filterModelIDs(ids []string, prefix string) []string {
	out := make([]string, 0)
	for _, id := range ids {
		id = strings.Trim(strings.TrimSpace(id), "`*_()[]{}.,;:")
		if strings.HasPrefix(strings.ToLower(id), prefix) && validModelID(id) {
			out = append(out, id)
		}
	}
	return uniqueStrings(out)
}

func lifecycleSubjects(sentence, prefix string, markers ...string) []string {
	all := filterModelIDs(modelTokenRE.FindAllString(sentence, -1), prefix)
	lower := strings.ToLower(sentence)
	anchor := firstIndex(lower, markers...)
	if anchor < 0 {
		return all
	}
	subjects := make([]string, 0, len(all))
	for _, id := range all {
		at := strings.Index(lower, strings.ToLower(id))
		if at >= 0 && at < anchor {
			subjects = append(subjects, id)
		}
	}
	if len(subjects) == 0 {
		// Some notices lead with the lifecycle term ("Retirement of model-x").
		for _, id := range all {
			at := strings.Index(lower, strings.ToLower(id))
			if at >= anchor && at-anchor < 80 {
				subjects = append(subjects, id)
				break
			}
		}
	}
	return uniqueStrings(subjects)
}

func extractAlibabaModelIDs(parts []string) []string {
	joined := strings.Join(parts, " ")
	allowed := []string{"qwen", "deepseek", "glm", "kimi", "wanx", "paraformer", "cosyvoice", "sambert", "tongyi", "qwq", "qvq", "qwen-vl", "qwen-audio", "gte"}
	ids := make([]string, 0)
	for _, token := range modelTokenRE.FindAllString(joined, -1) {
		low := strings.ToLower(token)
		for _, prefix := range allowed {
			if strings.HasPrefix(low, prefix) && validModelID(token) {
				ids = append(ids, token)
				break
			}
		}
	}
	return uniqueStrings(ids)
}

func normalizeEvidence(evidence []LifecycleEvidence) []LifecycleEvidence {
	byKey := make(map[string]LifecycleEvidence)
	for _, item := range evidence {
		item.ModelID = strings.TrimSpace(item.ModelID)
		if !validModelID(item.ModelID) || !validLifecycleStatus(item.Status) {
			continue
		}
		key := strings.Join([]string{item.Provider, item.ModelID, item.Status, item.AnnouncedAt, item.EffectiveAt, item.Replacement, item.SourceURL}, "\x00")
		if _, exists := byKey[key]; !exists {
			byKey[key] = item
		}
	}
	out := make([]LifecycleEvidence, 0, len(byKey))
	for _, item := range byKey {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		if out[i].ModelID != out[j].ModelID {
			return out[i].ModelID < out[j].ModelID
		}
		if out[i].Status != out[j].Status {
			return out[i].Status < out[j].Status
		}
		return out[i].SourceURL < out[j].SourceURL
	})
	return out
}

func validLifecycleStatus(status string) bool {
	switch status {
	case "announced_deprecated", "retired", "retired_with_compatibility_route", "legacy":
		return true
	default:
		return false
	}
}

func containsAny(value string, choices ...string) bool {
	for _, choice := range choices {
		if strings.Contains(strings.ToLower(value), strings.ToLower(choice)) {
			return true
		}
	}
	return false
}

func looksLikeDate(s string) bool { return findDate(s) != "" }

func normalizeDate(s string) string {
	if match := dateISORe.FindString(s); match != "" {
		return match
	}
	if match := dateUSRe.FindStringSubmatch(s); len(match) == 4 {
		if parsed, err := time.Parse("January 2, 2006", strings.Title(strings.ToLower(match[1])+" "+match[2]+", "+match[3])); err == nil {
			return parsed.Format("2006-01-02")
		}
	}
	if match := dateCNRe.FindStringSubmatch(s); len(match) == 4 {
		parsed, err := time.Parse("2006-1-2", fmt.Sprintf("%s-%s-%s", match[1], match[2], match[3]))
		if err == nil {
			return parsed.Format("2006-01-02")
		}
	}
	return ""
}

func findDate(s string) string {
	if date := dateISORe.FindString(s); date != "" {
		return date
	}
	if date := normalizeDate(s); date != "" {
		return date
	}
	if match := monthDayRe.FindStringSubmatch(s); len(match) == 3 {
		return match[1] + " " + match[2]
	}
	return ""
}

func parseAnyDate(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "January 2, 2006", "January 2 2006"} {
		if parsed, err := time.Parse(layout, s); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("date is not a full date")
}

func splitSentences(text string) []string {
	text = strings.ReplaceAll(text, "\u200b", " ")
	parts := regexp.MustCompile(`(?i)[.!?]\s+|\n+`).Split(text, -1)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func textLines(text string) []string {
	return regexp.MustCompile(`\n+`).Split(text, -1)
}

func shortText(s string, max int) string {
	s = strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
	if len(s) > max {
		s = s[:max]
	}
	return s
}

func safeText(s string) string {
	s = strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
	if strings.EqualFold(s, "n/a") || strings.EqualFold(s, "none") || s == "—" {
		return ""
	}
	return shortText(s, 240)
}

func safeHeader(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 512 || strings.ContainsAny(s, "\r\n") {
		return ""
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstIndex(value string, choices ...string) int {
	best := -1
	for _, choice := range choices {
		if index := strings.Index(value, choice); index >= 0 && (best < 0 || index < best) {
			best = index
		}
	}
	return best
}

func isRedirect(status int) bool {
	return status == http.StatusMovedPermanently || status == http.StatusFound || status == http.StatusSeeOther || status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect
}

func snapshotDigest(pages []CachedPage) (string, error) {
	parts := make([]string, 0, len(pages))
	for _, page := range pages {
		if page.URL == "" || page.ContentSHA256 == "" || page.Title == "" || page.FetchedAt.IsZero() {
			return "", errors.New("invalid page snapshot")
		}
		parts = append(parts, page.URL+"\x00"+page.ContentSHA256)
	}
	sort.Strings(parts)
	hash := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(hash[:]), nil
}

func failureCode(err error) string {
	var failure fetchFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	return "source_parse_error"
}
