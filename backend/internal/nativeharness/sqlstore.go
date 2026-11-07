package nativeharness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// SQLStore uses the application's existing PostgreSQL database. Rows are durable
// routing tombstones, NOT executable jobs, and are never expired or re-selected.
type SQLStore struct{ DB *sql.DB }

const lookupSQL = `SELECT request_digest, operation_id, route FROM native_harness_operations WHERE user_id=$1 AND api_key_id=$2 AND client_key=$3`

func (s *SQLStore) Lookup(ctx context.Context, userID, keyID int64, key string) (*Operation, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("missing database")
	}
	op := &Operation{UserID: userID, APIKeyID: keyID, ClientKey: key}
	var raw []byte
	err := s.DB.QueryRowContext(ctx, lookupSQL, userID, keyID, key).Scan(&op.Digest, &op.ID, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &op.Route); err != nil {
		return nil, err
	}
	return op, nil
}
func (s *SQLStore) Freeze(ctx context.Context, candidate Operation) (*Operation, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("missing database")
	}
	raw, err := json.Marshal(candidate.Route)
	if err != nil {
		return nil, err
	}
	// Each statement commits before egress. PostgreSQL's unique constraint makes
	// concurrent losers wait for the winning insert; the following SELECT reads it.
	_, err = s.DB.ExecContext(ctx, `INSERT INTO native_harness_operations
 (user_id,api_key_id,client_key,request_digest,operation_id,route)
 VALUES ($1,$2,$3,$4,$5,$6::jsonb)
 ON CONFLICT (user_id,api_key_id,client_key) DO NOTHING`, candidate.UserID, candidate.APIKeyID, candidate.ClientKey, candidate.Digest, candidate.ID, string(raw))
	if err != nil {
		return nil, err
	}
	return s.Lookup(ctx, candidate.UserID, candidate.APIKeyID, candidate.ClientKey)
}
