package nativeharness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSQLStoreStatementContract(t *testing.T) {
	db, mock, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	store := &SQLStore{DB: db}
	ctx := context.Background()
	route := configured("http://127.0.0.1:1234").Routes["synthetic-test"]
	raw, _ := json.Marshal(route)
	mock.ExpectQuery(regexp.QuoteMeta(lookupSQL)).WithArgs(int64(7), int64(9), "key").WillReturnError(sql.ErrNoRows)
	if _, e = store.Lookup(ctx, 7, 9, "key"); e != ErrNotFound {
		t.Fatal(e)
	}
	// A losing insert must SELECT the existing operation, not use its candidate.
	mock.ExpectExec(`INSERT INTO native_harness_operations`).WithArgs(int64(7), int64(9), "key", "new-digest", "new-op", string(raw)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(lookupSQL)).WithArgs(int64(7), int64(9), "key").WillReturnRows(sqlmock.NewRows([]string{"request_digest", "operation_id", "route"}).AddRow("old-digest", "old-op", raw))
	op, e := store.Freeze(ctx, Operation{7, 9, "key", "new-digest", "new-op", route})
	if e != nil || op.ID != "old-op" || op.Digest != "old-digest" || op.Route != route {
		t.Fatal(op, e)
	}
	// An uncertain insert result must stop before SELECT/HTTP, not retry insert.
	mock.ExpectExec(`INSERT INTO native_harness_operations`).WillReturnError(errors.New("connection lost"))
	if _, e = store.Freeze(ctx, Operation{7, 9, "other", "digest", "op", route}); e == nil {
		t.Fatal("DB error hidden")
	}
	if e = mock.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
