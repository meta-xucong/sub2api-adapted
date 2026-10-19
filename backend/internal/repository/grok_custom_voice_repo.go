package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Keep ownership on the concrete production account repository, including when
// its constructor exposes the narrower AccountRepository interface.
var _ service.GrokCustomVoiceRepository = (*accountRepository)(nil)

func grokCustomVoiceProfile(voice *service.GrokCustomVoice) ([]byte, error) {
	if voice == nil || !service.ValidGrokCustomVoiceID(voice.VoiceID) || voice.UserID <= 0 || voice.AccountID <= 0 {
		return nil, fmt.Errorf("invalid custom voice ownership")
	}
	if len(voice.Profile) == 0 {
		return []byte(`{}`), nil
	}
	// Profiles contain bounded metadata only; never persist audio payloads.
	if len(voice.Profile) > 8192 {
		return nil, fmt.Errorf("custom voice profile exceeds metadata size limit")
	}
	if !json.Valid(voice.Profile) {
		return nil, fmt.Errorf("invalid custom voice profile")
	}
	return voice.Profile, nil
}

func (r *accountRepository) CreateGrokCustomVoice(ctx context.Context, voice *service.GrokCustomVoice) error {
	profile, err := grokCustomVoiceProfile(voice)
	if err != nil {
		return err
	}
	// No conflict update: provider IDs are not assumed globally unique. Any
	// collision, including with a deleted voice, must fail closed.
	_, err = r.sql.ExecContext(ctx, `INSERT INTO grok_custom_voices (voice_id, user_id, account_id, profile) VALUES ($1, $2, $3, $4::jsonb)`, voice.VoiceID, voice.UserID, voice.AccountID, string(profile))
	return err
}

func (r *accountRepository) GetGrokCustomVoice(ctx context.Context, voiceID string) (*service.GrokCustomVoice, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT voice_id, user_id, account_id, profile, (deleted_at IS NOT NULL) FROM grok_custom_voices WHERE voice_id = $1`, voiceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var voice service.GrokCustomVoice
	if err := rows.Scan(&voice.VoiceID, &voice.UserID, &voice.AccountID, &voice.Profile, &voice.Deleted); err != nil {
		return nil, err
	}
	return &voice, rows.Err()
}

func (r *accountRepository) ListGrokCustomVoices(ctx context.Context, userID, groupID int64, cursor string, limit int) ([]service.GrokCustomVoice, error) {
	if userID <= 0 || groupID <= 0 || limit < 1 || limit > 1001 {
		return nil, fmt.Errorf("invalid custom voice list scope or limit")
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT v.voice_id, v.user_id, v.account_id, v.profile FROM grok_custom_voices v JOIN accounts a ON a.id = v.account_id JOIN account_groups ag ON ag.account_id = a.id WHERE v.user_id = $1 AND ag.group_id = $2 AND v.voice_id > $3 AND a.platform = $4 AND a.deleted_at IS NULL AND v.deleted_at IS NULL ORDER BY v.voice_id LIMIT $5`, userID, groupID, cursor, "grok", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	voices := make([]service.GrokCustomVoice, 0)
	for rows.Next() {
		var voice service.GrokCustomVoice
		if err := rows.Scan(&voice.VoiceID, &voice.UserID, &voice.AccountID, &voice.Profile); err != nil {
			return nil, err
		}
		voices = append(voices, voice)
	}
	return voices, rows.Err()
}

func (r *accountRepository) DeleteGrokCustomVoice(ctx context.Context, voiceID string, userID int64) error {
	// Preserve the original tombstone timestamp on retries by the same owner.
	result, err := r.sql.ExecContext(ctx, `UPDATE grok_custom_voices SET deleted_at = COALESCE(deleted_at, NOW()) WHERE voice_id = $1 AND user_id = $2`, voiceID, userID)
	return grokCustomVoiceMutationResult(result, err)
}

func (r *accountRepository) UpdateGrokCustomVoice(ctx context.Context, voice *service.GrokCustomVoice) error {
	profile, err := grokCustomVoiceProfile(voice)
	if err != nil {
		return err
	}
	// Neither owner nor provider account can be changed through profile updates.
	result, err := r.sql.ExecContext(ctx, `UPDATE grok_custom_voices SET profile = $4::jsonb WHERE voice_id = $1 AND user_id = $2 AND account_id = $3 AND deleted_at IS NULL`, voice.VoiceID, voice.UserID, voice.AccountID, string(profile))
	return grokCustomVoiceMutationResult(result, err)
}

func grokCustomVoiceMutationResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
