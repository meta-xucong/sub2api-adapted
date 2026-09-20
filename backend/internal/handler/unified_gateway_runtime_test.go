package handler

import (
	"bytes"
	"mime/multipart"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUnifiedGatewayRequestModelReadsJSONAndMultipart(t *testing.T) {
	model, err := unifiedGatewayRequestModel("application/json", []byte(`{"model":"gpt-6-astra"}`))
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", model)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("prompt", "turn this into a painting"))
	require.NoError(t, writer.WriteField("model", "image-model"))
	require.NoError(t, writer.Close())

	model, err = unifiedGatewayRequestModel(writer.FormDataContentType(), body.Bytes())
	require.NoError(t, err)
	require.Equal(t, "image-model", model)
}

func TestUnifiedGatewayRequestModelRejectsMalformedOrMissingModel(t *testing.T) {
	_, err := unifiedGatewayRequestModel("application/json", []byte(`{"prompt":"missing"}`))
	require.ErrorIs(t, err, service.ErrUnifiedGatewayInvalidRequest)

	_, err = unifiedGatewayRequestModel("multipart/form-data; boundary=missing", []byte("not-a-multipart-body"))
	require.ErrorIs(t, err, service.ErrUnifiedGatewayInvalidRequest)
}

func TestEstimateUnifiedGatewayUnitsTreatsImageEditsAsImageDelivery(t *testing.T) {
	require.Equal(t, float64(1), estimateUnifiedGatewayUnits(service.UnifiedGatewayEndpointImageEdits, []byte(`{}`)))
}
