package service

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestAlchemyBrainSchemaFingerprintUsesCanonicalSchemaOnly(t *testing.T) {
	body := []byte(`{"messages":[{"content":"fixture prompt"}],"output_config":{"format":{"type":"json_schema","schema":{"type":"object","required":["1"],"properties":{"1":{"type":"string"}}}}}}`)
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(`{"properties":{"1":{"type":"string"}},"required":["1"],"type":"object"}`)))
	if got := alchemyBrainSchemaFingerprint(body); got != want {
		t.Fatalf("schema digest = %q, want %q", got, want)
	}
	if got := alchemyBrainSchemaFingerprint([]byte(`{"output_config":{"effort":"high"}}`)); got != "" {
		t.Fatalf("missing schema has digest %q", got)
	}
}
