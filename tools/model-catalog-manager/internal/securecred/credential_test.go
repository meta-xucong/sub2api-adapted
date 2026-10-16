package securecred

import (
	"encoding/binary"
	"testing"
	"unicode/utf16"

	"sub2api-model-catalog-manager/internal/config"
)

func TestReadUsesSignerEnvironmentVariablePrecedence(t *testing.T) {
	t.Setenv("CODEX_404TOKEN_ADMIN_API_KEY", "first-test-key")
	t.Setenv("SUB2API_ADMIN_API_KEY", "second-test-key")
	value, err := Read(config.CredentialConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if string(value.APIKey) != "first-test-key" {
		t.Fatal("signer primary environment key did not take precedence")
	}
	value.Clear()

	t.Setenv("CODEX_404TOKEN_ADMIN_API_KEY", "")
	value, err = Read(config.CredentialConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if string(value.APIKey) != "second-test-key" {
		t.Fatal("signer fallback environment key was not used")
	}
	value.Clear()
}

func TestReadRejectsMalformedEnvironmentKeyWithoutFallback(t *testing.T) {
	t.Setenv("CODEX_404TOKEN_ADMIN_API_KEY", "bad\nkey")
	t.Setenv("SUB2API_ADMIN_API_KEY", "fallback-test-key")
	if _, err := Read(config.CredentialConfig{}); err == nil {
		t.Fatal("malformed primary environment key silently fell back")
	}
}

func TestCredentialClearZerosAPIKey(t *testing.T) {
	value := Value{APIKey: []byte("clear-me")}
	underlying := value.APIKey
	value.Clear()
	if value.APIKey != nil {
		t.Fatal("cleared key reference was retained")
	}
	for _, b := range underlying {
		if b != 0 {
			t.Fatal("credential buffer was not overwritten")
		}
	}
}

func TestWindowsCredentialDecoderAcceptsDedicatedAPIKeyOnly(t *testing.T) {
	encode := func(value string) []byte {
		words := utf16.Encode([]rune(value))
		blob := make([]byte, len(words)*2)
		for i, word := range words {
			binary.LittleEndian.PutUint16(blob[i*2:], word)
		}
		return blob
	}
	key, err := decodeWindowsAPIKey("x-api-key", encode("windows-test-api-key"))
	if err != nil || string(key) != "windows-test-api-key" {
		t.Fatalf("dedicated Windows API key was rejected: key=%q err=%v", key, err)
	}
	wipe(key)
	for _, tc := range []struct {
		name string
		user string
		blob []byte
	}{
		{name: "old login credential target", user: "admin@example.test", blob: encode("old-password")},
		{name: "wrong username marker", user: "password", blob: encode("old-password")},
		{name: "odd UTF-16 blob", user: "x-api-key", blob: []byte{0x41}},
		{name: "empty", user: "x-api-key", blob: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := decodeWindowsAPIKey(tc.user, tc.blob); err == nil {
				wipe(got)
				t.Fatal("invalid Windows credential shape was accepted")
			}
		})
	}
}
