package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelsListCandidateIDsOpenAIUsesAdminSelectableCatalog(t *testing.T) {
	got := defaultModelsListCandidateIDs(PlatformOpenAI)

	require.Equal(t, []string{
		"gpt-5.4-mini",
		"gpt-5.4",
		"gpt-5.5",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-image-2",
	}, got)
}
