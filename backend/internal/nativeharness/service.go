// Package nativeharness implements an opt-in, free synthetic channel seam.
// It persists routing decisions, never tasks or executable dispatch work.
package nativeharness

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const Path = "/v1/native-harness/chat/completions"
const MaxBody = 262144
const MaxRequest = 65536

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
var ErrNotFound = errors.New("native operation not found")

type Error struct {
	Code   string
	Status int
}

func (e *Error) Error() string           { return e.Code }
func fail(code string, status int) error { return &Error{code, status} }

type Operation struct {
	UserID    int64
	APIKeyID  int64
	ClientKey string
	Digest    string
	ID        string
	Route     config.NativeHarnessRoute
}

type Store interface {
	Lookup(context.Context, int64, int64, string) (*Operation, error)
	Freeze(context.Context, Operation) (*Operation, error)
}

type Service struct {
	Config config.NativeHarnessConfig
	Store  Store
	// Getenv is a secret-reference resolver; secrets are never persisted in Store.
	Getenv func(string) string
	client *http.Client
}

func New(cfg config.NativeHarnessConfig, store Store) *Service {
	// A fresh transport: no environment proxy, no redirect, and no implicit
	// idempotency-key retry. Do NOT forward the client's Idempotency-Key header.
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 35 * time.Second, DisableKeepAlives: true}
	return &Service{Config: cfg, Store: store, Getenv: os.Getenv, client: &http.Client{Transport: tr,
		Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func ValidRoute(alias string, r config.NativeHarnessRoute) bool {
	u, err := url.Parse(r.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" || u.Hostname() == "localhost")) {
		return false
	}
	if r.Revision < 1 || alias != r.ModelAlias || !strings.HasPrefix(alias, "synthetic-") || !strings.HasPrefix(r.NativeModel, "fake-") || !envName.MatchString(r.CredentialEnv) {
		return false
	}
	for _, v := range []string{r.Source, r.LedgerInstance, r.BindingID, r.ModelAlias, r.NativeModel} {
		if !identifier.MatchString(v) {
			return false
		}
	}
	return true
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func canonical(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// decode forbids duplicate keys throughout the bounded JSON object. The normal
// gateway's lenient/gjson parser is deliberately not used for this contract.
func decode(raw []byte) (map[string]any, error) {
	if len(raw) > MaxBody || !json.Valid(raw) || !validUnicode(raw) {
		return nil, fail("INVALID_REQUEST", 400)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var parse func(int) (any, error)
	parse = func(depth int) (any, error) {
		if depth > 16 {
			return nil, errors.New("depth")
		}
		t, e := d.Token()
		if e != nil {
			return nil, e
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return t, nil
		}
		switch delim {
		case '{':
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				key, ok := k.(string)
				if !ok {
					return nil, errors.New("key")
				}
				if _, ok := m[key]; ok {
					return nil, errors.New("duplicate")
				}
				v, e := parse(depth + 1)
				if e != nil {
					return nil, e
				}
				m[key] = v
			}
			_, e := d.Token()
			return m, e
		case '[':
			a := []any{}
			for d.More() {
				v, e := parse(depth + 1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			_, e := d.Token()
			return a, e
		}
		return nil, errors.New("delimiter")
	}
	v, e := parse(0)
	if e != nil {
		return nil, fail("INVALID_REQUEST", 400)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fail("INVALID_REQUEST", 400)
	}
	return m, nil
}

func normalize(raw []byte) (map[string]any, error) {
	if len(raw) > MaxRequest {
		return nil, fail("BODY_TOO_LARGE", 413)
	}
	m, e := decode(raw)
	if e != nil {
		return nil, e
	}
	for k := range m {
		switch k {
		case "model", "messages", "n", "stream", "user":
		default:
			return nil, fail("UNSUPPORTED_FIELD", 400)
		}
	}
	model, ok := m["model"].(string)
	if !ok || !identifier.MatchString(model) || !strings.HasPrefix(model, "synthetic-") {
		return nil, fail("MODEL_NOT_AVAILABLE", 400)
	}
	if n, ok := m["n"]; ok && n != json.Number("1") {
		return nil, fail("UNSUPPORTED_N", 400)
	}
	if s, ok := m["stream"]; ok && s != false {
		return nil, fail("UNSUPPORTED_STREAM", 400)
	}
	if u, exists := m["user"]; exists {
		tag, ok := u.(string)
		if !ok || len([]rune(tag)) > 256 {
			return nil, fail("INVALID_USER_TAG", 400)
		}
	}
	a, ok := m["messages"].([]any)
	if !ok || len(a) == 0 || len(a) > 64 {
		return nil, fail("INVALID_MESSAGES", 400)
	}
	for i, v := range a {
		msg, ok := v.(map[string]any)
		if !ok || len(msg) != 2 {
			return nil, fail("INVALID_MESSAGES", 400)
		}
		role, rok := msg["role"].(string)
		text, tok := msg["content"].(string)
		if !rok || !tok || (role != "user" && role != "assistant") || strings.TrimSpace(text) == "" || (i == len(a)-1 && role != "user") {
			return nil, fail("INVALID_MESSAGES", 400)
		}
	}
	result := map[string]any{"model": model, "messages": a, "n": 1, "stream": false}
	if len(canonical(result)) > MaxRequest {
		return nil, fail("BODY_TOO_LARGE", 413)
	}
	return result, nil
}

type Response struct {
	Status int
	Body   []byte
	Replay string
}

func (s *Service) Invoke(ctx context.Context, userID, keyID int64, clientKey string, canExecute bool, raw []byte) (*Response, error) {
	cfg := s.Config
	if !cfg.Enabled {
		return nil, fail("NATIVE_DISABLED", 503)
	}
	if userID <= 0 || keyID <= 0 || userID != cfg.AllowedUserID || keyID != cfg.AllowedAPIKeyID {
		return nil, fail("NATIVE_FORBIDDEN", 403)
	}
	if !identifier.MatchString(clientKey) {
		return nil, fail("IDEMPOTENCY_KEY_REQUIRED", 400)
	}
	req, err := normalize(raw)
	if err != nil {
		return nil, err
	}
	body := canonical(req)
	digest := hash(body)
	if s.Store == nil {
		return nil, fail("ROUTE_STORE_UNAVAILABLE", 503)
	}
	op, err := s.Store.Lookup(ctx, userID, keyID, clientKey)
	if errors.Is(err, ErrNotFound) {
		if !cfg.AcceptingNew || !canExecute {
			return nil, fail("NEW_ADMISSION_DISABLED", 403)
		}
		alias := req["model"].(string)
		route, ok := cfg.Routes[alias]
		if !ok || !ValidRoute(alias, route) {
			return nil, fail("MODEL_NOT_AVAILABLE", 400)
		}
		id := make([]byte, 16)
		if _, err = rand.Read(id); err != nil {
			return nil, fail("ROUTE_STORE_UNAVAILABLE", 503)
		}
		op, err = s.Store.Freeze(ctx, Operation{userID, keyID, clientKey, digest, "op_" + hex.EncodeToString(id), route})
	}
	if err != nil || op == nil {
		return nil, fail("ROUTE_STORE_UNAVAILABLE", 503)
	}
	if op.UserID != userID || op.APIKeyID != keyID || op.ClientKey != clientKey || !identifier.MatchString(op.ID) {
		return nil, fail("ROUTE_INTEGRITY_ERROR", 503)
	}
	if op.Digest != digest {
		return nil, fail("IDEMPOTENCY_CONFLICT", 409)
	}
	if !ValidRoute(req["model"].(string), op.Route) {
		return nil, fail("ROUTE_INTEGRITY_ERROR", 503)
	}
	token := s.Getenv(op.Route.CredentialEnv)
	if len(token) < 16 || strings.ContainsAny(token, "\r\n") {
		return nil, fail("ROUTE_CREDENTIAL_UNAVAILABLE", 503)
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, op.Route.Origin+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fail("ROUTE_INTEGRITY_ERROR", 503)
	}
	// Prevent net/http from treating a body as safely replayable on a transport error.
	r.GetBody = nil
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Native-Tenant", fmt.Sprintf("user-%d", userID))
	r.Header.Set("X-Native-Principal", fmt.Sprintf("key-%d", keyID))
	r.Header.Set("X-Native-Operation", op.ID)
	r.Header.Set("X-Native-Source", op.Route.Source)
	r.Header.Set("X-Native-Ledger", op.Route.LedgerInstance)
	binding := map[string]any{"binding_id": op.Route.BindingID, "revision": op.Route.Revision, "model_alias": op.Route.ModelAlias, "native_model": op.Route.NativeModel}
	r.Header.Set("X-Native-Binding-Digest", hash(canonical(binding)))
	// Re-check current permission even if the earlier receipt is unknown. A false
	// flag only allows Facade lookup/replay; it can never create missing work.
	currentRoute, currentlyConfigured := cfg.Routes[op.Route.ModelAlias]
	admission := cfg.AcceptingNew && canExecute && currentlyConfigured && currentRoute == op.Route
	r.Header.Set("X-Native-Admission-Allowed", strconv.FormatBool(admission))
	response, err := s.client.Do(r)
	if err != nil {
		return nil, fail("NATIVE_RECEIPT_UNKNOWN", 502)
	}
	defer response.Body.Close()
	lengths := response.Header.Values("Content-Length")
	if len(lengths) != 1 {
		return nil, fail("NATIVE_RECEIPT_UNKNOWN", 502)
	}
	if response.ContentLength < 0 || response.ContentLength > MaxBody || len(response.TransferEncoding) != 0 {
		return nil, fail("NATIVE_RECEIPT_UNKNOWN", 502)
	}
	result, err := io.ReadAll(io.LimitReader(response.Body, MaxBody+1))
	if err != nil || int64(len(result)) != response.ContentLength {
		return nil, fail("NATIVE_RECEIPT_UNKNOWN", 502)
	}
	decoded, err := decode(result)
	if err != nil {
		return nil, fail("NATIVE_RECEIPT_UNKNOWN", 502)
	}
	if response.StatusCode != http.StatusOK {
		// Do not expose arbitrary upstream bodies or enter ordinary failover logic.
		code := "NATIVE_UPSTREAM_ERROR"
		if e, ok := decoded["error"].(map[string]any); ok {
			if c, ok := e["code"].(string); ok && identifier.MatchString(c) {
				code = c
			}
		}
		status := response.StatusCode
		if status < 400 || status > 599 {
			status = 502
		}
		return nil, fail(code, status)
	}
	if !validCompletion(decoded, op.Route.ModelAlias) {
		return nil, fail("NATIVE_INVALID_COMPLETION", 502)
	}

	return &Response{200, result, response.Header.Get("X-Native-Replay")}, nil
}

// Exact allowlists reject unsupported capabilities even when their values are
// null. In particular legacy function_call and tool_calls must never pass through
// this text/refusal-only channel. No arbitrary upstream fields are returned.
func exactFields(m map[string]any, required []string, optional ...string) bool {
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		if _, ok := m[key]; !ok {
			return false
		}
		allowed[key] = true
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range m {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func validCompletion(value map[string]any, alias string) bool {
	if !exactFields(value, []string{"id", "object", "created", "model", "choices"}) || value["object"] != "chat.completion" || value["model"] != alias {
		return false
	}
	id, ok := value["id"].(string)
	if !ok || !strings.HasPrefix(id, "chatcmpl-") || !identifier.MatchString(strings.TrimPrefix(id, "chatcmpl-")) {
		return false
	}
	created, ok := value["created"].(json.Number)
	if !ok || len(created) == 0 {
		return false
	}
	// Reject exponent/fraction, negative values (including -0), and overflow.
	for _, c := range string(created) {
		if c < '0' || c > '9' {
			return false
		}
	}
	if _, err := strconv.ParseInt(string(created), 10, 64); err != nil {
		return false
	}
	choices, ok := value["choices"].([]any)
	if !ok || len(choices) != 1 {
		return false
	}
	choice, ok := choices[0].(map[string]any)
	if !ok || !exactFields(choice, []string{"index", "message", "finish_reason"}) || choice["index"] != json.Number("0") || choice["finish_reason"] != "stop" {
		return false
	}
	message, ok := choice["message"].(map[string]any)
	if !ok || !exactFields(message, []string{"role", "content"}, "refusal") || message["role"] != "assistant" {
		return false
	}
	content := ""
	if message["content"] != nil {
		var ok bool
		content, ok = message["content"].(string)
		if !ok {
			return false
		}
	}
	refusal := ""
	if v, exists := message["refusal"]; exists {
		var ok bool
		refusal, ok = v.(string)
		if !ok || strings.TrimSpace(refusal) == "" {
			return false
		}
	}
	return strings.TrimSpace(content) != "" || strings.TrimSpace(refusal) != ""
}

// Go's encoding/json replaces malformed UTF-8 and lone UTF-16 surrogate
// escapes. Reject instead of silently changing a request before its digest.
func validUnicode(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		v, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if v >= 0xdc00 && v <= 0xdfff {
			return false
		}
		if v >= 0xd800 && v <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

// RequestModel validates the same exact request contract used for routing, so
// the handler can apply current group execution permission without a lenient
// second parser. Replay itself remains allowed by Invoke when permission=false.
func RequestModel(raw []byte) (string, error) {
	m, e := normalize(raw)
	if e != nil {
		return "", e
	}
	return m["model"].(string), nil
}
