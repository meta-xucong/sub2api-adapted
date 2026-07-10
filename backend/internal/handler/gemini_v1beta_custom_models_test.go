package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGeminiModelIDsFromModelsListBody(t *testing.T) {
	body := []byte(`{"models":[{"name":"models/gemini-2.5-flash"},{"name":"models/gemini-2.5-pro"}]}`)
	require.Equal(t, []string{"gemini-2.5-flash", "gemini-2.5-pro"}, geminiModelIDsFromModelsListBody(body))
}

func TestGeminiCustomModelsListAllows(t *testing.T) {
	group := &service.Group{ModelsListConfig: service.GroupModelsListConfig{
		Enabled: true,
		Models:  []string{"gemini-2.5-flash", "gemini-3-*"},
	}}
	require.True(t, geminiCustomModelsListAllows(group, "models/gemini-2.5-flash"))
	require.True(t, geminiCustomModelsListAllows(group, "gemini-3-pro"))
	require.False(t, geminiCustomModelsListAllows(group, "gemini-2.5-pro"))
}
