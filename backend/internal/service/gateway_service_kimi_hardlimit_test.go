package service

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestIsKimiGatewayTargetModel(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  bool
	}{
		{name: "stable id", model: "kimi-for-coding", want: true},
		{name: "kimi prefix", model: "kimi-k2-thinking", want: true},
		{name: "case insensitive", model: "KIMI-FOR-CODING", want: true},
		{name: "other model", model: "gpt-5.4", want: false},
		{name: "empty", model: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isKimiGatewayTargetModel(tt.model)
			if got != tt.want {
				t.Fatalf("isKimiGatewayTargetModel(%q)=%v want=%v", tt.model, got, tt.want)
			}
		})
	}
}

func TestEnforceKimiGatewayHardLimit(t *testing.T) {
	t.Run("force by reqModel", func(t *testing.T) {
		body := []byte(`{"model":"kimi-for-coding","max_tokens":9000}`)
		out, changed := enforceKimiGatewayHardLimit("kimi-for-coding", body)
		if !changed {
			t.Fatal("expected body to be changed")
		}
		if got := gjson.GetBytes(out, "max_tokens").Int(); got != kimiGatewayHardMaxTokens {
			t.Fatalf("max_tokens=%d want=%d", got, kimiGatewayHardMaxTokens)
		}
	})

	t.Run("force by body model", func(t *testing.T) {
		body := []byte(`{"model":"kimi-k2-thinking","max_tokens":4096}`)
		out, changed := enforceKimiGatewayHardLimit("", body)
		if !changed {
			t.Fatal("expected body to be changed")
		}
		if got := gjson.GetBytes(out, "max_tokens").Int(); got != kimiGatewayHardMaxTokens {
			t.Fatalf("max_tokens=%d want=%d", got, kimiGatewayHardMaxTokens)
		}
	})

	t.Run("non kimi unchanged", func(t *testing.T) {
		body := []byte(`{"model":"gpt-5.4","max_tokens":2048}`)
		out, changed := enforceKimiGatewayHardLimit("gpt-5.4", body)
		if changed {
			t.Fatal("expected body not changed")
		}
		if string(out) != string(body) {
			t.Fatalf("body mutated unexpectedly: %s", string(out))
		}
	})

	t.Run("already hardened", func(t *testing.T) {
		body := []byte(`{"model":"kimi-for-coding","max_tokens":1600}`)
		out, changed := enforceKimiGatewayHardLimit("kimi-for-coding", body)
		if changed {
			t.Fatal("expected body not changed when max_tokens already hardened")
		}
		if string(out) != string(body) {
			t.Fatalf("body mutated unexpectedly: %s", string(out))
		}
	})
}
