package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportWritesOnlyPublicMappingData(t *testing.T) {
	dir := t.TempDir()
	doc := Document{RunID: "abc", CreatedAt: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), Mode: "inspect", Accounts: []Account{{
		ID: 9001, Platform: "openai", Type: "apikey", Mappings: []Mapping{{PublicID: "public", UpstreamID: "upstream", Disposition: "unreviewed"}},
	}}}
	path, err := Write(dir, doc)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"api_key", "header_overrides", "Authorization", "password", "token"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("report contains forbidden field %q", forbidden)
		}
	}
	if !strings.Contains(text, "upstream") {
		t.Fatal("report missing public model mapping")
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("unexpected report path: %s", path)
	}
}
