package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGrokCustomVoiceOwnershipMigration(t *testing.T) {
	content, err := FS.ReadFile("247_grok_custom_voice_ownership.sql")
	require.NoError(t, err)
	ddl := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, ddl, "CREATE TABLE IF NOT EXISTS grok_custom_voices")
	require.Contains(t, ddl, "voice_id TEXT PRIMARY KEY")
	require.Contains(t, ddl, "CHECK (voice_id ~ '^[A-Za-z0-9_-]{1,128}$')")
	require.Contains(t, ddl, "user_id BIGINT NOT NULL CHECK (user_id > 0)")
	require.Contains(t, ddl, "account_id BIGINT NOT NULL CHECK (account_id > 0)")
	require.Contains(t, ddl, "deleted_at TIMESTAMPTZ")
	require.Contains(t, ddl, "CHECK (octet_length(profile::text) <= 16384)")
	require.Contains(t, ddl, "WHERE deleted_at IS NULL")
	require.NotContains(t, ddl, "ON DELETE CASCADE")
}
