package nativeharness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLocalTrialRouteRequiresExplicitMode(t *testing.T) {
	r := configured("http://127.0.0.1:1234").Routes["synthetic-test"]
	r.ModelAlias = "workbuddy-test"
	r.NativeModel = "test-model"
	if ValidRoute(r.ModelAlias, r) {
		t.Fatal("real binding without explicit trial mode")
	}
	r.ExecutionMode = "workbuddy_local_trial"
	if !ValidRoute(r.ModelAlias, r) {
		t.Fatal("explicit single-owner trial route rejected")
	}
	for _, mode := range []string{"production", "synthetic", "typo"} {
		r.ExecutionMode = mode
		if ValidRoute(r.ModelAlias, r) {
			t.Fatal(mode)
		}
	}
	r.ExecutionMode = "workbuddy_local_trial"
	r.NativeModel = "fake-text"
	if ValidRoute(r.ModelAlias, r) {
		t.Fatal("fake output mislabeled real")
	}
}

func TestLocalTrialAdmissionReplayAndFrozenMode(t *testing.T) {
	for _, initial := range []struct{ mode, alias, nextMode string }{
		{"workbuddy_local_trial", "workbuddy-test", "harness_local_trial"},
		{"harness_local_trial", "codex-test", "workbuddy_local_trial"},
		{"harness_local_trial", "workbuddy-test", "synthetic"},
	} {
		t.Run(initial.mode+"/"+initial.alias, func(t *testing.T) {
			var calls atomic.Int32
			var lastAdmission atomic.Value
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				lastAdmission.Store(r.Header.Get("X-Native-Admission-Allowed"))
				if r.Header.Get("X-Native-Tenant") != "user-7" || r.Header.Get("X-Native-Principal") != "key-9" {
					t.Error("identity not server derived")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, strings.ReplaceAll(completion, "synthetic-test", initial.alias))
			}))
			defer server.Close()
			s := newTestService(server.URL)
			route := s.Config.Routes["synthetic-test"]
			route.ExecutionMode = initial.mode
			route.ModelAlias = initial.alias
			route.NativeModel = "test-model"
			delete(s.Config.Routes, "synthetic-test")
			s.Config.Routes[route.ModelAlias] = route
			raw := []byte(strings.ReplaceAll(request, "synthetic-test", initial.alias))
			// Multi-role history is not silently flattened or persisted.
			bad := []byte(strings.Replace(string(raw), `[{"role":"user"`, `[{"role":"assistant","content":"old"},{"role":"user"`, 1))
			_, err := s.Invoke(context.Background(), 7, 9, "bad", true, bad)
			requireCode(t, err, "LOCAL_TRIAL_SINGLE_USER_MESSAGE_REQUIRED")
			store, ok := s.Store.(*memoryStore)
			if !ok {
				t.Fatal("unexpected store fixture")
			}
			if len(store.rows) != 0 || calls.Load() != 0 {
				t.Fatal("bad prompt admitted")
			}
			_, err = s.Invoke(context.Background(), 8, 9, "op", true, raw)
			requireCode(t, err, "NATIVE_FORBIDDEN")
			result, err := s.Invoke(context.Background(), 7, 9, "op", true, raw)
			if err != nil || result.Billing != "disabled-local-trial" {
				t.Fatalf("%+v %v", result, err)
			}
			if lastAdmission.Load() != "true" {
				t.Fatal("new admission")
			}
			op, lookupErr := s.Store.Lookup(context.Background(), 7, 9, "op")
			if lookupErr != nil || op == nil {
				t.Fatalf("lookup: %v", lookupErr)
			}
			encoded, marshalErr := json.Marshal(op.Route)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if !strings.Contains(string(encoded), `"execution_mode":"`+initial.mode+`"`) {
				t.Fatal("mode not durably frozen")
			}
			// An altered live route must never replace the frozen route on replay.
			changed := route
			changed.ExecutionMode = initial.nextMode
			changed.Origin = "http://127.0.0.1:1"
			s.Config.Routes[route.ModelAlias] = changed
			result, err = s.Invoke(context.Background(), 7, 9, "op", false, raw)
			if err != nil || result.Billing != "disabled-local-trial" || lastAdmission.Load() != "false" {
				t.Fatalf("cross-mode replay %+v %v", result, err)
			}
			// Current route removed: receipt lookup survives, but new execution permission cannot.
			delete(s.Config.Routes, route.ModelAlias)
			result, err = s.Invoke(context.Background(), 7, 9, "op", false, raw)
			if err != nil || result.Billing != "disabled-local-trial" || lastAdmission.Load() != "false" {
				t.Fatalf("replay %+v %v", result, err)
			}
			_, err = s.Invoke(context.Background(), 7, 9, "new", true, raw)
			requireCode(t, err, "MODEL_NOT_AVAILABLE")
			if calls.Load() != 3 {
				t.Fatal("unexpected egress/retry")
			}
		})
	}
}

func TestMultiHarnessTrialAliases(t *testing.T) {
	r := configured("http://127.0.0.1:1234").Routes["synthetic-test"]
	r.NativeModel = "real-model"
	for _, alias := range []string{"codex-test", "workbuddy-test"} {
		r.ModelAlias = alias
		r.ExecutionMode = "harness_local_trial"
		if !ValidRoute(alias, r) {
			t.Fatal("generic trial alias rejected", alias)
		}
		r.ExecutionMode = "workbuddy_local_trial"
		if ValidRoute(alias, r) != strings.HasPrefix(alias, "workbuddy-") {
			t.Fatal("legacy mode expanded", alias)
		}
	}
	r.ExecutionMode = "harness_local_trial"
	r.NativeModel = "fake-text"
	if ValidRoute(r.ModelAlias, r) {
		t.Fatal("fake model admitted")
	}
}
