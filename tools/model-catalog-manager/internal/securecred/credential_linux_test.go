//go:build linux

package securecred

import (
	"os"
	"path/filepath"
	"testing"

	"sub2api-model-catalog-manager/internal/config"
)

func TestLinuxCredentialOnlyReadsInjectedSystemdCredentialDirectory(t *testing.T) {
	t.Setenv("CODEX_404TOKEN_ADMIN_API_KEY", "")
	t.Setenv("SUB2API_ADMIN_API_KEY", "")
	previous, hadPrevious := os.LookupEnv("CREDENTIALS_DIRECTORY")
	t.Cleanup(func() {
		if hadPrevious {
			_ = os.Setenv("CREDENTIALS_DIRECTORY", previous)
		} else {
			_ = os.Unsetenv("CREDENTIALS_DIRECTORY")
		}
	})
	_ = os.Unsetenv("CREDENTIALS_DIRECTORY")
	cfg := config.CredentialConfig{SystemdCredentialName: "admin-api-key"}
	if _, err := Read(cfg); err == nil {
		t.Fatal("credential read succeeded without systemd-injected directory")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "admin-api-key"), []byte(`{"api_key":"ephemeral-test-api-key"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("CREDENTIALS_DIRECTORY", dir); err != nil {
		t.Fatal(err)
	}
	value, err := Read(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(value.APIKey) != "ephemeral-test-api-key" {
		t.Fatal("systemd credential contents were not read correctly")
	}
	value.Clear()
	if len(value.APIKey) != 0 {
		t.Fatal("credential buffer was not cleared")
	}
	if err := os.WriteFile(filepath.Join(dir, "admin-api-key"), []byte(`{"username":"admin","password":"old-login-password"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(cfg); err == nil {
		t.Fatal("legacy username/password systemd credential was accepted as an API key")
	}
}

func TestLinuxCredentialRejectsUnsafeNamesAndPlaintextConfigSource(t *testing.T) {
	t.Setenv("CODEX_404TOKEN_ADMIN_API_KEY", "")
	t.Setenv("SUB2API_ADMIN_API_KEY", "")
	for _, name := range []string{"../admin", "a/b", `a\b`, "a:b", ""} {
		if _, err := Read(config.CredentialConfig{SystemdCredentialName: name}); err == nil {
			t.Errorf("unsafe credential name accepted: %q", name)
		}
	}
	if _, err := Read(config.CredentialConfig{WindowsCredentialTarget: "not-on-linux"}); err == nil {
		t.Fatal("Windows Credential Manager source unexpectedly worked on Linux")
	}
}
