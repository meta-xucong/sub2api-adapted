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
	var calls atomic.Int32
	var lastAdmission atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		lastAdmission.Store(r.Header.Get("X-Native-Admission-Allowed"))
		if r.Header.Get("X-Native-Tenant") != "user-7" || r.Header.Get("X-Native-Principal") != "key-9" {
			t.Error("identity not server derived")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, strings.ReplaceAll(completion, "synthetic-test", "workbuddy-test"))
	}))
	defer server.Close()
	s := newTestService(server.URL)
	route := s.Config.Routes["synthetic-test"]
	route.ExecutionMode = "workbuddy_local_trial"
	route.ModelAlias = "workbuddy-test"
	route.NativeModel = "test-model"
	delete(s.Config.Routes, "synthetic-test")
	s.Config.Routes[route.ModelAlias] = route
	raw := []byte(strings.ReplaceAll(request, "synthetic-test", "workbuddy-test"))
	// Multi-role history is not silently flattened or persisted.
	bad := []byte(strings.Replace(string(raw), `[{"role":"user"`, `[{"role":"assistant","content":"old"},{"role":"user"`, 1))
	_, err := s.Invoke(context.Background(), 7, 9, "bad", true, bad)
	requireCode(t, err, "LOCAL_TRIAL_SINGLE_USER_MESSAGE_REQUIRED")
	if len(s.Store.(*memoryStore).rows) != 0 || calls.Load() != 0 {
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
	op, _ := s.Store.Lookup(context.Background(), 7, 9, "op")
	encoded, _ := json.Marshal(op.Route)
	if !strings.Contains(string(encoded), `"execution_mode":"workbuddy_local_trial"`) {
		t.Fatal("mode not durably frozen")
	}
	// Current route removed: receipt lookup survives, but new execution permission cannot.
	delete(s.Config.Routes, route.ModelAlias)
	result, err = s.Invoke(context.Background(), 7, 9, "op", false, raw)
	if err != nil || result.Billing != "disabled-local-trial" || lastAdmission.Load() != "false" {
		t.Fatalf("replay %+v %v", result, err)
	}
	_, err = s.Invoke(context.Background(), 7, 9, "new", true, raw)
	requireCode(t, err, "MODEL_NOT_AVAILABLE")
	if calls.Load() != 2 {
		t.Fatal("unexpected egress/retry")
	}
}
