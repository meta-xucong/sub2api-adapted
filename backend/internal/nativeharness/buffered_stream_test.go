package nativeharness

import (
	"bytes"
	"testing"
)

func TestNativeStreamCanonicalSameOperation(t *testing.T) {
	raw := []byte(`{"model":"synthetic-test","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	prepared, stream, err := PrepareNativeStream(raw)
	if err != nil || !stream {
		t.Fatal(err)
	}
	expected, err := normalize([]byte(`{"model":"synthetic-test","messages":[{"role":"user","content":"hello"}],"stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prepared, canonical(expected)) {
		t.Fatal("transport changes ledger digest")
	}
	for _, bad := range []string{`{"stream":1}`, `{"stream":true,"stream":false}`, `{"model":"synthetic-test","messages":[{"role":"user","content":"hi"}],"stream":true,"tools":[]}`, `{"model":"synthetic-test","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{}}`} {
		if _, _, err := PrepareNativeStream([]byte(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
