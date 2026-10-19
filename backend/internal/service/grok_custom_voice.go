package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type GrokCustomVoice struct {
	VoiceID   string
	UserID    int64
	AccountID int64
	Profile   json.RawMessage
	Deleted   bool
}
type GrokCustomVoiceRepository interface {
	CreateGrokCustomVoice(context.Context, *GrokCustomVoice) error
	GetGrokCustomVoice(context.Context, string) (*GrokCustomVoice, error)
	ListGrokCustomVoices(context.Context, int64, int64, string, int) ([]GrokCustomVoice, error)
	DeleteGrokCustomVoice(context.Context, string, int64) error
	UpdateGrokCustomVoice(context.Context, *GrokCustomVoice) error
}

var ErrGrokVoiceInvalidRequest = errors.New("invalid voice request")
var ErrGrokVoiceNotFound = errors.New("voice not found")
var errGrokVoiceUnmapped = fmt.Errorf("%w: unmapped", ErrGrokVoiceNotFound)
var ErrGrokVoiceStoreUnavailable = errors.New("voice ownership store unavailable")

type grokVoiceIdentityKey struct{}
type grokVoiceIdentity struct {
	userID  int64
	groupID int64
}

func WithGrokVoiceIdentity(ctx context.Context, userID int64, groupID *int64) context.Context {
	return context.WithValue(ctx, grokVoiceIdentityKey{}, grokVoiceIdentity{userID, derefGroupID(groupID)})
}
func (s *OpenAIGatewayService) grokVoiceStore() (GrokCustomVoiceRepository, error) {
	if s != nil {
		if r, ok := s.accountRepo.(GrokCustomVoiceRepository); ok {
			return r, nil
		}
	}
	return nil, ErrGrokVoiceStoreUnavailable
}
func (s *OpenAIGatewayService) ownedGrokVoice(ctx context.Context, id string) (*GrokCustomVoice, error) {
	identity, _ := ctx.Value(grokVoiceIdentityKey{}).(grokVoiceIdentity)
	if identity.userID <= 0 {
		return nil, ErrGrokVoiceNotFound
	}
	repo, err := s.grokVoiceStore()
	if err != nil {
		return nil, err
	}
	voice, err := repo.GetGrokCustomVoice(ctx, id)
	if err != nil {
		return nil, ErrGrokVoiceStoreUnavailable
	}
	if voice == nil {
		return nil, errGrokVoiceUnmapped
	}
	if voice.Deleted || voice.UserID != identity.userID {
		return nil, ErrGrokVoiceNotFound
	}
	account, err := s.accountRepo.GetByID(ctx, voice.AccountID)
	if err != nil || !grokVoiceAccountInGroup(account, identity.groupID) {
		return nil, ErrGrokVoiceNotFound
	}
	return voice, nil
}
func grokVoiceAccountInGroup(account *Account, groupID int64) bool {
	if account == nil || account.Platform != PlatformGrok {
		return false
	}
	for _, id := range account.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

// GrokVoicePinnedAccount resolves custom resource ownership before scheduling.
// Unknown TTS IDs are checked against the public built-in catalogue only after
// an account is selected; unknown resource IDs never reach an upstream.
func (s *OpenAIGatewayService) GrokVoicePinnedAccount(ctx context.Context, endpoint string, body []byte) (int64, error) {
	if strings.HasPrefix(endpoint, "custom-voices/") {
		if !ValidGrokCustomVoiceID(strings.Split(endpoint, "/")[1]) {
			return 0, ErrGrokVoiceNotFound
		}
		voice, err := s.ownedGrokVoice(ctx, strings.Split(endpoint, "/")[1])
		if err != nil {
			return 0, err
		}
		return voice.AccountID, nil
	}
	if endpoint != "tts" {
		return 0, nil
	}
	ids, err := grokVoiceIDs(body)
	if err != nil {
		return 0, err
	}
	var pinned int64
	for _, id := range ids {
		voice, err := s.ownedGrokVoice(ctx, id)
		if errors.Is(err, errGrokVoiceUnmapped) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if pinned != 0 && pinned != voice.AccountID {
			return 0, ErrGrokVoiceNotFound
		}
		pinned = voice.AccountID
	}
	return pinned, nil
}

// ListGrokCustomVoices never obtains a provider's account-wide inventory.
func (s *OpenAIGatewayService) ListGrokCustomVoices(ctx context.Context, c *gin.Context) error {
	identity, _ := ctx.Value(grokVoiceIdentityKey{}).(grokVoiceIdentity)
	if identity.userID <= 0 {
		return ErrGrokVoiceNotFound
	}
	repo, err := s.grokVoiceStore()
	if err != nil {
		return err
	}
	limit := 100
	if raw := c.Query("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 1000 {
			return fmt.Errorf("%w: limit must be between 1 and 1000", ErrGrokVoiceInvalidRequest)
		}
		limit = n
	}
	cursor := c.Query("pagination_token")
	voices, err := repo.ListGrokCustomVoices(ctx, identity.userID, identity.groupID, cursor, limit+1)
	if err != nil {
		return ErrGrokVoiceStoreUnavailable
	}
	profiles := make([]json.RawMessage, 0, len(voices))
	var token any
	for i, v := range voices {
		if i == limit {
			token = voices[i-1].VoiceID
			break
		}
		profiles = append(profiles, v.Profile)
	}
	c.JSON(http.StatusOK, gin.H{"voices": profiles, "pagination_token": token})
	return nil
}

// ValidGrokCustomVoiceID admits official eight-character xAI IDs and common
// UUID/dash/underscore relay aliases, but no escaped or normalized path syntax.
func ValidGrokCustomVoiceID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range []byte(id) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// parseGrokCustomVoiceResponseID binds exactly the canonical identity exposed
// to the client. Unrelated provider metadata is left untouched.
func parseGrokCustomVoiceResponseID(profile []byte) (string, error) {
	invalid := fmt.Errorf("invalid custom voice response identity")
	d := json.NewDecoder(bytes.NewReader(profile))
	root, err := d.Token()
	if err != nil || root != json.Delim('{') {
		return "", invalid
	}
	id := ""
	seen := false
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return "", invalid
		}
		key, ok := token.(string)
		if !ok {
			return "", invalid
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return "", invalid
		}
		if strings.EqualFold(key, "voice_id") {
			if key != "voice_id" || seen {
				return "", invalid
			}
			seen = true
			if json.Unmarshal(value, &id) != nil || !ValidGrokCustomVoiceID(id) {
				return "", invalid
			}
		}
	}
	if _, err := d.Token(); err != nil {
		return "", invalid
	}
	if _, err := d.Token(); err != io.EOF || !seen {
		return "", invalid
	}
	return id, nil
}

// grokVoiceIDs checks JSON structurally before looking at any voice field. This
// rejects duplicate keys so provider/parser disagreement cannot bypass checks.
func grokVoiceIDs(body []byte) (result []string, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w: malformed voice JSON", ErrGrokVoiceInvalidRequest)
		}
	}()
	d := json.NewDecoder(bytes.NewReader(body))
	var ids []string
	var read func(int, string) (any, error)
	read = func(depth int, path string) (any, error) {
		if depth > 64 {
			return nil, fmt.Errorf("voice JSON nesting exceeds limit")
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
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return nil, e
				}
				k, ok := key.(string)
				if !ok || seen[k] {
					return nil, fmt.Errorf("duplicate or invalid JSON field")
				}
				seen[k] = true
				fieldPath := k
				if path != "" {
					fieldPath = path + "." + k
				}
				value, e := read(depth+1, fieldPath)
				if e != nil {
					return nil, e
				}
				if grokVoiceFieldPath(fieldPath) {
					id, ok := value.(string)
					if !ok || strings.TrimSpace(id) == "" {
						return nil, fmt.Errorf("voice must be a non-empty string")
					}
					ids = append(ids, id)
				}
			}
		case '[':
			for d.More() {
				if _, e := read(depth+1, path+"[]"); e != nil {
					return nil, e
				}
			}
		default:
			return nil, fmt.Errorf("invalid voice JSON")
		}
		_, e = d.Token()
		return nil, e
	}
	first := bytes.TrimSpace(body)
	if len(first) == 0 || first[0] != '{' {
		return nil, fmt.Errorf("voice body must be a JSON object")
	}
	if _, err := read(0, ""); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("invalid trailing voice JSON")
	}
	return ids, nil
}

type grokVoiceCatalogueEntry struct {
	ids     map[string]bool
	expires time.Time
}
type grokVoiceCatalogueCache struct {
	mu      sync.Mutex
	entries map[string]grokVoiceCatalogueEntry
}

func (s *OpenAIGatewayService) validateGrokVoiceIDs(ctx context.Context, c *gin.Context, account *Account, body []byte) error {
	ids, err := grokVoiceIDs(body)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	// Bound ownership, credential and catalogue work for each voice-bearing
	// event. Non-voice audio frames need no extra timeout or Gin state.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, id := range ids {
		v, e := s.ownedGrokVoice(ctx, id)
		if e == nil {
			if v.AccountID != account.ID {
				return ErrGrokVoiceNotFound
			}
			continue
		}
		if !errors.Is(e, errGrokVoiceUnmapped) {
			return e
		}
		builtin, e := s.grokBuiltinVoice(ctx, c, account, id)
		if e != nil {
			return e
		}
		if !builtin {
			return ErrGrokVoiceNotFound
		}
	}
	return nil
}
func (s *OpenAIGatewayService) grokBuiltinVoice(ctx context.Context, c *gin.Context, account *Account, id string) (bool, error) {
	// The deadline includes credential lookup/refresh, not only HTTP I/O.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	target, err := buildGrokVoiceURL(account, s.cfg, "tts/voices")
	if err != nil {
		return false, err
	}
	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return false, err
	}
	// Hash credential material; never keep tokens in the catalogue key.
	key := fmt.Sprintf("%d:%s", account.ID, DeriveSessionHashFromSeed(target+":"+token))
	cache := &s.grokVoiceCatalogue
	cache.mu.Lock()
	entry, ok := cache.entries[key]
	cache.mu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return s.grokBuiltinVoiceUnmapped(ctx, grokBuiltinVoiceCatalogueID(entry.ids, id))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	account.ApplyHeaderOverrides(req.Header)
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		return false, fmt.Errorf("built-in voice catalogue unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("built-in voice catalogue unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return false, fmt.Errorf("invalid built-in voice catalogue")
	}
	var result struct {
		Voices []struct {
			ID string `json:"voice_id"`
		} `json:"voices"`
	}
	if json.Unmarshal(data, &result) != nil || len(result.Voices) == 0 {
		return false, fmt.Errorf("invalid built-in voice catalogue")
	}
	// Retain only small catalogues: at most 128 * 256 short IDs across
	// the service, rather than retaining 128 arbitrarily large JSON payloads.
	cacheable := len(result.Voices) <= 256
	matchedID := ""
	ambiguous := false
	entry = grokVoiceCatalogueEntry{ids: map[string]bool{}, expires: time.Now().Add(5 * time.Minute)}
	for _, voice := range result.Voices {
		if grokBuiltinVoiceIDEqual(voice.ID, id) {
			if matchedID != "" && matchedID != voice.ID {
				ambiguous = true
			}
			matchedID = voice.ID
		}
		if len(voice.ID) > 128 {
			cacheable = false
		}
		if cacheable && voice.ID != "" {
			entry.ids[voice.ID] = true
		}
	}
	if cacheable {
		cache.mu.Lock()
		if len(cache.entries) >= 128 {
			cache.entries = nil
		}
		if cache.entries == nil {
			cache.entries = map[string]grokVoiceCatalogueEntry{}
		}
		cache.entries[key] = entry
		cache.mu.Unlock()
	}
	if ambiguous {
		return false, nil
	}
	return s.grokBuiltinVoiceUnmapped(ctx, matchedID)
}

// Public built-in IDs are ASCII case-insensitive. Custom resource identities
// remain exact: neither this comparison nor a catalogue hit may alias a durable
// custom mapping, including a tombstone or a mapping owned by the caller.
func grokBuiltinVoiceIDEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

func grokBuiltinVoiceCatalogueID(ids map[string]bool, requested string) string {
	matched := ""
	for id := range ids {
		if grokBuiltinVoiceIDEqual(id, requested) {
			if matched != "" && matched != id {
				return "" // Ambiguous catalogue aliases fail closed.
			}
			matched = id
		}
	}
	return matched
}

func (s *OpenAIGatewayService) grokBuiltinVoiceUnmapped(ctx context.Context, canonicalID string) (bool, error) {
	if canonicalID == "" {
		return false, nil
	}
	repo, err := s.grokVoiceStore()
	if err != nil {
		return false, err
	}
	voice, err := repo.GetGrokCustomVoice(ctx, canonicalID)
	if err != nil {
		return false, ErrGrokVoiceStoreUnavailable
	}
	return voice == nil, nil
}

func (s *OpenAIGatewayService) authorizeGrokVoiceForward(ctx context.Context, c *gin.Context, account *Account, endpoint string, body []byte) error {
	if endpoint == "tts" {
		return s.validateGrokVoiceIDs(ctx, c, account, body)
	}
	if !strings.HasPrefix(endpoint, "custom-voices") {
		return nil
	}
	identity, _ := ctx.Value(grokVoiceIdentityKey{}).(grokVoiceIdentity)
	if identity.userID <= 0 || !grokVoiceAccountInGroup(account, identity.groupID) {
		return ErrGrokVoiceNotFound
	}
	if _, err := s.grokVoiceStore(); err != nil {
		return err
	}
	if endpoint == "custom-voices" {
		if c == nil || c.Request == nil || c.Request.Method != http.MethodPost {
			return ErrGrokVoiceNotFound
		}
		return nil
	}
	if !ValidGrokCustomVoiceID(strings.Split(endpoint, "/")[1]) {
		return ErrGrokVoiceNotFound
	}
	v, err := s.ownedGrokVoice(ctx, strings.Split(endpoint, "/")[1])
	if err != nil {
		return err
	}
	if v.AccountID != account.ID {
		return ErrGrokVoiceNotFound
	}
	return nil
}
func (s *OpenAIGatewayService) persistGrokVoiceResponse(ctx context.Context, c *gin.Context, account *Account, endpoint string, data []byte) error {
	// Upstream work is detached from client cancellation. Finish its durable
	// ownership transition even if the caller disconnects after acceptance.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if !strings.HasPrefix(endpoint, "custom-voices") {
		return nil
	}
	repo, err := s.grokVoiceStore()
	if err != nil {
		return err
	}
	identity, _ := ctx.Value(grokVoiceIdentityKey{}).(grokVoiceIdentity)
	if c.Request.Method == http.MethodDelete {
		return repo.DeleteGrokCustomVoice(ctx, strings.Split(endpoint, "/")[1], identity.userID)
	}
	if c.Request.Method != http.MethodPost && c.Request.Method != http.MethodPatch {
		return nil
	}
	if len(data) > 8192 {
		return fmt.Errorf("custom voice metadata exceeds limit")
	}
	id, parseErr := parseGrokCustomVoiceResponseID(data)
	if parseErr != nil {
		return fmt.Errorf("invalid custom voice response")
	}
	if endpoint != "custom-voices" && id != strings.Split(endpoint, "/")[1] {
		return fmt.Errorf("custom voice response ID mismatch")
	}
	v := &GrokCustomVoice{VoiceID: id, UserID: identity.userID, AccountID: account.ID, Profile: append(json.RawMessage(nil), data...)}
	if c.Request.Method == http.MethodPost {
		return repo.CreateGrokCustomVoice(ctx, v)
	}
	return repo.UpdateGrokCustomVoice(ctx, v)
}

// Only protocol configuration fields select voices. A tool schema or message
// payload may legitimately contain unrelated properties named "voice".
func grokVoiceFieldPath(path string) bool {
	switch path {
	case "voice", "voice_id", "session.voice", "session.voice_id", "session.audio.output.voice", "response.voice", "response.audio.output.voice":
		return true
	}
	return false
}
