//go:build unit

package service

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWokeyJSONDuplicateKeysRejectedAtBothBoundaries(t *testing.T) {
	image := `"data:image/png;base64,` + base64.StdEncoding.EncodeToString(testWokeyPNG) + `"`
	for _, tc := range []struct{ name, fields string }{
		{"prompt", `"prompt":"AUDITED-FIRST","prompt":"UNREVIEWED-LAST","image":` + image},
		{"prompt_reverse", `"prompt":"UNREVIEWED-LAST","prompt":"AUDITED-FIRST","image":` + image},
		{"escaped_prompt", `"prompt":"AUDITED-FIRST","pr\u006fmpt":"UNREVIEWED-LAST","image":` + image},
		{"model", `"model":"grok-imagine-image-quality","prompt":"edit","image":` + image},
		{"image", `"prompt":"edit","image":` + image + `,"image":` + image},
		{"images", `"prompt":"edit","images":[` + image + `],"images":[` + image + `]`},
		{"image_url", `"prompt":"edit","image":{"url":` + image + `,"url":` + image + `}`},
		{"image_image_url_url", `"prompt":"edit","image":{"image_url":{"url":` + image + `,"u\u0072l":` + image + `}}`},
		{"images_element_url", `"prompt":"edit","images":[{"url":` + image + `,"url":` + image + `}]`},
		{"secret_key", `"prompt":"edit","image":` + image + `,"SYNTHETIC_SECRET":1,"SYNTHETIC_SECRET":2`},
		{"unknown_nested", `"prompt":"edit","image":` + image + `,"unknown":{"x":1,"x":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"grok-imagine-image-quality",` + tc.fields + `}`)
			for _, boundary := range []string{"before_normalization", "final_multipart"} {
				t.Run(boundary, func(t *testing.T) {
					var out []byte
					var err error
					if boundary == "before_normalization" {
						out, _, err = prepareGrokMediaForwardBodyForAccount(hardeningGrokAccount(), GrokMediaEndpointImagesEdits, body, "application/json")
					} else {
						out, _, err = prepareWokeyGrokImageEditBody(body)
					}
					var inputErr *GrokImageEditInputValidationError
					require.ErrorAs(t, err, &inputErr)
					require.ErrorContains(t, err, "duplicate JSON object key")
					require.Nil(t, out)
					require.NotContains(t, err.Error(), "AUDITED-FIRST")
					require.NotContains(t, err.Error(), "UNREVIEWED-LAST")
					require.NotContains(t, err.Error(), "SYNTHETIC_SECRET")
				})
			}
		})
	}
}

func TestWokeyJSONBoundaryRequiresSingleObject(t *testing.T) {
	for _, body := range []string{`[]`, `null`, `"text"`, `1`, `{"model":"a"}{"model":"b"}`, `{"model":"a"`, `{"model":"a",}`, `{"model":"a","x":"SYNTHETIC_SECRET`} {
		t.Run(fmt.Sprintf("%x", body), func(t *testing.T) {
			for _, final := range []bool{false, true} {
				var err error
				if final {
					_, _, err = prepareWokeyGrokImageEditBody([]byte(body))
				} else {
					_, _, err = prepareGrokMediaForwardBodyForAccount(hardeningGrokAccount(), GrokMediaEndpointImagesEdits, []byte(body), "application/json")
				}
				var inputErr *GrokImageEditInputValidationError
				require.ErrorAs(t, err, &inputErr)
				require.NotContains(t, err.Error(), "SYNTHETIC_SECRET")
			}
		})
	}
}

func TestWokeyJSONBoundaryPreservesOtherProviderContracts(t *testing.T) {
	body := []byte(`{"model":"grok-imagine-image-quality","prompt":"first","prompt":"last","image":"data:image/png;base64,eA=="}`)
	for _, tc := range []struct {
		name, base string
		endpoint   GrokMediaEndpoint
	}{
		{"official_edits", "https://api.x.ai/v1", GrokMediaEndpointImagesEdits},
		{"wokey_generation", "https://api.wokey.ai/v1", GrokMediaEndpointImagesGenerations},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := hardeningGrokAccount()
			account.Credentials["base_url"] = tc.base
			expected, expectedType, expectedErr := prepareGrokMediaForwardBody(tc.endpoint, body, "application/json")
			actual, actualType, actualErr := prepareGrokMediaForwardBodyForAccount(account, tc.endpoint, body, "application/json")
			require.Equal(t, expectedErr, actualErr)
			require.Equal(t, expectedType, actualType)
			require.Equal(t, expected, actual)
		})
	}
}

func TestWokeyJSONBoundaryPreservesValidScalarsAndDeepInput(t *testing.T) {
	image := `"data:image/png;base64,` + base64.StdEncoding.EncodeToString(testWokeyPNG) + `"`
	body := []byte(`{"model":"grok-imagine-image-quality","prompt":"edit","Prompt":"distinct","enabled":true,"large":12345678901234567890,"huge":1e9999,"image":` + image + `}`)
	prepared, _, err := prepareGrokMediaForwardBodyForAccount(hardeningGrokAccount(), GrokMediaEndpointImagesEdits, body, "application/json")
	require.NoError(t, err)
	out, contentType, err := prepareWokeyGrokImageEditBody(prepared)
	require.NoError(t, err)
	fields, _ := readWokeyImageEditMultipart(t, contentType, out)
	require.Equal(t, "distinct", fields["Prompt"])
	require.Equal(t, "true", fields["enabled"])
	require.Equal(t, "12345678901234567890", fields["large"])
	require.Equal(t, "1e9999", fields["huge"])
	// Different objects may reuse names; Unicode-equivalent spellings remain distinct.
	// These live inside the accepted image object and are not scalar form options.
	nested := `{"model":"grok-imagine-image-quality","prompt":"edit","image":{"url":` + image + `,"meta":[{"x":1},{"x":2}],"é":1,"e\u0301":2,"deep":` + strings.Repeat(`[`, 2000) + `0` + strings.Repeat(`]`, 2000) + `}}`
	_, _, err = prepareGrokMediaForwardBodyForAccount(hardeningGrokAccount(), GrokMediaEndpointImagesEdits, []byte(nested), "application/json")
	require.NoError(t, err, "validator must not introduce a smaller nesting or key-normalization limit")
	_, _, err = prepareWokeyGrokImageEditBody([]byte(nested))
	require.NoError(t, err)
}
