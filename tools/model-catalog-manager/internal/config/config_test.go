package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		Target:     TargetConfig{BaseURL: "https://404token.xyz"},
		Accounts:   []AccountConfig{{ID: 9001, AcceptCredentialResponseMemoryExposure: boolPointer(true)}},
		Credential: CredentialConfig{WindowsCredentialTarget: "Sub2API model catalog"},
		StateDir:   "state", ReportDir: "reports", BackupDir: "backups", RulesFile: "rules.json",
	}
}

func TestValidateBaseURLPins404tokenHTTPSOrigin(t *testing.T) {
	for _, tc := range []struct {
		url string
		ok  bool
	}{
		{"https://404token.xyz", true},
		{"https://404token.xyz/", true},
		{"https://example.test", false},
		{"http://404token.xyz", false},
		{"https://404token.xyz:443", false},
		{"https://user:pass@404token.xyz", false},
		{"https://example.test/api", false},
		{"https://example.test?x=1", false},
		{"ftp://example.test", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			_, err := ValidateBaseURL(tc.url)
			if (err == nil) != tc.ok {
				t.Fatalf("ValidateBaseURL(%q) err=%v", tc.url, err)
			}
		})
	}
}

func TestConfigUsesFailClosedDefaultsAndExactAccountMemoryGate(t *testing.T) {
	cfg := validConfig()
	if cfg.ApplyModeValue() != "preview" || cfg.RuntimeCompatible() {
		t.Fatalf("defaults are not fail closed: mode=%q compatible=%v", cfg.ApplyModeValue(), cfg.RuntimeCompatible())
	}
	if err := cfg.ExposureGateFor([]int64{9001}); err != nil {
		t.Fatalf("explicitly accepted account rejected: %v", err)
	}
	cfg.Accounts[0].AcceptCredentialResponseMemoryExposure = boolPointer(false)
	if err := cfg.ExposureGateFor([]int64{9001}); err == nil {
		t.Fatal("explicit false privacy gate was accepted")
	}
	if err := cfg.ExposureGateFor([]int64{9002}); err == nil {
		t.Fatal("non-allowlisted account was accepted")
	}
}

func TestAllAccountScopeRequiresExplicitGlobalMemoryAcknowledgement(t *testing.T) {
	cfg := validConfig()
	cfg.AccountScope = "all"
	cfg.Accounts = nil
	if err := cfg.Validate(); err != nil {
		t.Fatalf("all-account config should be structurally valid before consent gate: %v", err)
	}
	if err := cfg.ExposureGateFor([]int64{9001}); err == nil {
		t.Fatal("all-account mode passed without explicit global memory acknowledgement")
	}
	cfg.AcceptAllCredentialResponseMemoryExposure = true
	if err := cfg.ExposureGateFor([]int64{9001, 9002}); err != nil {
		t.Fatalf("explicit global acknowledgement rejected: %v", err)
	}
	cfg.Accounts = []AccountConfig{{ID: 9001, AcceptCredentialResponseMemoryExposure: boolPointer(true)}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("all-account mode accepted a simultaneous static ID list")
	}
}

func TestTargetCompatibilityRequiresExactContractAndEvidence(t *testing.T) {
	cfg := validConfig()
	cfg.Target.RuntimeCompatibility = RuntimeCompatibility{BuildID: "image-sha", APIContractID: "admin-catalog-v1", Evidence: "target verification", ReviewedAt: "2026-10-11"}
	if !cfg.RuntimeCompatible() {
		t.Fatal("valid explicit target compatibility record was rejected")
	}
	cfg.Target.RuntimeCompatibility.APIContractID = "unknown"
	if cfg.RuntimeCompatible() {
		t.Fatal("unknown API contract passed compatibility gate")
	}
	cfg.Target.RuntimeCompatibility.APIContractID = "admin-catalog-v1"
	cfg.Target.RuntimeCompatibility.Evidence = ""
	if cfg.RuntimeCompatible() {
		t.Fatal("compatibility record without target evidence passed")
	}
}

func TestConfigLoadStrictJSONAndDefaultsPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"target":{"base_url":"https://404token.xyz","runtime_compatibility":{"build_id":"","api_contract_id":"","evidence":"","reviewed_at":""}},"accounts":[{"id":9001,"accept_credential_response_memory_exposure":false}],"allow_native_sync_metadata_write":false}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ApplyModeValue() != "preview" || !strings.HasSuffix(cfg.StateDir, "state") || !strings.HasSuffix(cfg.RulesFile, "model-rules.json") || cfg.Credential.WindowsCredentialTarget == "" {
		t.Fatalf("defaults were not applied: %#v", cfg)
	}
	if err := cfg.ExposureGateFor([]int64{9001}); err == nil {
		t.Fatal("false privacy gate should remain blocked")
	}

	if err := os.WriteFile(path, []byte(strings.TrimSuffix(body, "}")+`,"unexpected":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown config field was accepted")
	}
}

func TestApprovedRulesHashMustBeSHA256AndModeKnown(t *testing.T) {
	cfg := validConfig()
	cfg.ApplyMode = "something"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unknown apply mode was accepted")
	}
	cfg.ApplyMode = "approved_rules"
	cfg.ApprovedRulesSHA256 = "abc"
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid rule hash was accepted")
	}
	cfg.ApprovedRulesSHA256 = strings.Repeat("a", 64)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid hash rejected: %v", err)
	}
}

func boolPointer(v bool) *bool { return &v }
