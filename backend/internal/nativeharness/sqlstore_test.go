package nativeharness

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// This deliberately does not auto-create schema or accept a production DSN.
// The test operator supplies a disposable DB with migration 248 applied.
func TestSQLStorePostgres(t *testing.T) {
	dsn := os.Getenv("NATIVE_HARNESS_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit disposable PostgreSQL required")
	}
	db, e := sql.Open("postgres", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	store := &SQLStore{DB: db}
	key := fmt.Sprintf("postgres-%d", time.Now().UnixNano())
	route := configured("http://127.0.0.1:1234").Routes["synthetic-test"]
	if _, e = store.Lookup(ctx, 7, 9, key); e != ErrNotFound {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[string]bool{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			op, e := store.Freeze(ctx, Operation{7, 9, key, "digest", fmt.Sprintf("op%d_%s", i, key), route})
			if e != nil {
				t.Error(e)
				return
			}
			mu.Lock()
			ids[op.ID] = true
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if len(ids) != 1 {
		t.Fatalf("got %d operation ids", len(ids))
	}
	// A new DB handle models process restart, not an in-memory map reuse.
	db2, e := sql.Open("postgres", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db2.Close()
	op, e := (&SQLStore{DB: db2}).Lookup(ctx, 7, 9, key)
	if e != nil || !ids[op.ID] || op.Route != route {
		t.Fatal("frozen route lost", e)
	}
	if _, e = store.Lookup(ctx, 7, 10, key); e != ErrNotFound {
		t.Fatal("key scope leaked", e)
	}
	// A conflicting contender cannot replace either digest or route.
	changed := route
	changed.Origin = "http://127.0.0.1:5678"
	got, e := store.Freeze(ctx, Operation{7, 9, key, "different", "different-op-" + key, changed})
	if e != nil || got.Digest != "digest" || got.Route != route {
		t.Fatal("overwrite", e)
	}
	t.Log("100 concurrent real PostgreSQL inserts freeze one operation; new handle and key-scope checks pass")
}
