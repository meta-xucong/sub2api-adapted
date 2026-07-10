package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveOpenAIResponsesImageRoutingModel(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","tools":[{"type":"image_generation","model":"gpt-image-2"}]}`)
	require.Equal(t, "gpt-image-2", ResolveOpenAIResponsesImageRoutingModel("gpt-5.5", body))
	require.Equal(t, "gpt-5.5", ResolveOpenAIResponsesImageRoutingModel("gpt-5.5", []byte(`{"input":"hello"}`)))
}
