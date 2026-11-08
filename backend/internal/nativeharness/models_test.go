package nativeharness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type forbiddenModelStore struct{}

func (forbiddenModelStore) Lookup(context.Context, int64, int64, string) (*Operation, error) {
	panic("model preflight must not touch operation store")
}
func (forbiddenModelStore) Freeze(context.Context, Operation) (*Operation, error) {
	panic("model preflight must not freeze")
}

func TestNativeModelsConfigurationOnly(t *testing.T) {
	s := newTestService("https://private-facade.invalid")
	s.Store = forbiddenModelStore{}
	s.Getenv = func(string) string { panic("model preflight must not resolve credentials") }
	trial := s.Config.Routes["synthetic-test"]
	trial.ExecutionMode = "workbuddy_local_trial"
	trial.ModelAlias = "workbuddy-test"
	trial.NativeModel = "private-native-id"
	s.Config.Routes[trial.ModelAlias] = trial
	bad := trial
	bad.ExecutionMode = "production"
	s.Config.Routes["invalid"] = bad
	result, err := s.ConfiguredModels(7, 9, true)
	if err != nil || len(result.Data) != 2 || result.Data[0].ID != "synthetic-test" || result.Data[1].ID != "workbuddy-test" || !result.NativeHarness.AcceptingNew || result.NativeHarness.LiveReadinessChecked {
		t.Fatalf("%+v %v", result, err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-facade", "private-native-id", "TEST_TOKEN", "ledger", "binding"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("configuration detail leaked", secret)
		}
	}
	for _, canExecute := range []bool{false, true} {
		s.Config.AcceptingNew = false
		result, err = s.ConfiguredModels(7, 9, canExecute)
		if err != nil || result.Data == nil || len(result.Data) != 0 || result.NativeHarness.AcceptingNew {
			t.Fatal("draining response", err)
		}
	}
	s.Config.AcceptingNew = true
	result, err = s.ConfiguredModels(7, 9, false)
	if err != nil || len(result.Data) != 0 || result.NativeHarness.AcceptingNew {
		t.Fatal("execution disabled", err)
	}
	_, err = s.ConfiguredModels(7, 10, true)
	requireCode(t, err, "NATIVE_FORBIDDEN")
	s.Config.Enabled = false
	_, err = s.ConfiguredModels(7, 9, true)
	requireCode(t, err, "NATIVE_DISABLED")
}
