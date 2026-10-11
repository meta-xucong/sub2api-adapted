package securecred

import (
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"sub2api-model-catalog-manager/internal/config"
)

var ErrUnsupportedSource = errors.New("configured credential source is unavailable on this operating system")

type Value struct {
	APIKey []byte
}

func Read(c config.CredentialConfig) (Value, error) {
	for _, name := range []string{"CODEX_404TOKEN_ADMIN_API_KEY", "SUB2API_ADMIN_API_KEY"} {
		if value, ok := os.LookupEnv(name); ok && value != "" {
			key := []byte(value)
			if !validAPIKey(key) {
				wipe(key)
				return Value{}, errors.New("admin API key environment variable is invalid")
			}
			return Value{APIKey: key}, nil
		}
	}
	return readOSCredential(c)
}

func (v *Value) Clear() {
	wipe(v.APIKey)
	v.APIKey = nil
}

func validAPIKey(key []byte) bool {
	if len(key) == 0 || len(key) > 16*1024 || strings.TrimSpace(string(key)) != string(key) {
		return false
	}
	for _, b := range key {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return true
}

func decodeWindowsAPIKey(username string, blob []byte) ([]byte, error) {
	if username != "x-api-key" || len(blob) == 0 || len(blob) > 16*1024 || len(blob)%2 != 0 {
		return nil, errors.New("Windows Credential Manager entry has an invalid API-key shape")
	}
	words := make([]uint16, len(blob)/2)
	for i := range words {
		words[i] = binary.LittleEndian.Uint16(blob[i*2 : i*2+2])
	}
	runes := utf16.Decode(words)
	key := make([]byte, 0, len(blob))
	for i, char := range runes {
		key = utf8.AppendRune(key, char)
		runes[i] = 0
	}
	for i := range words {
		words[i] = 0
	}
	if !validAPIKey(key) {
		wipe(key)
		return nil, errors.New("Windows Credential Manager API key is invalid")
	}
	return key, nil
}

func wipe(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
