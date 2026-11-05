package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateAtomicSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := Empty()
	s.Accounts["9001"] = Account{ManagedMappings: map[string]ManagedMapping{"public": {OriginalValue: "old", LastAppliedValue: "upstream", SourceFingerprint: "source-hash"}}, AbsenceCounts: map[string]int{}}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Accounts["9001"].ManagedMappings["public"].LastAppliedValue != "upstream" {
		t.Fatalf("unexpected state: %#v", got)
	}
}

func TestCorruptOrUnsupportedStateFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"accounts":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unsupported schema to block")
	}
	if err := os.WriteFile(path, []byte(`not json`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected corrupt state to block")
	}
}

func TestExclusiveLockBlocksSecondAcquisition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(path); err == nil {
		t.Fatal("expected second lock acquisition to fail")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("lock should be reusable after release: %v", err)
	}
	_ = second.Release()
}
