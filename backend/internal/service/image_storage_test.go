package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A real image header is required; a MIME signature alone is not an image.
var pngBytes = func() []byte {
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		panic(err)
	}
	return out.Bytes()
}()

type savedImage struct {
	key         string
	contentType string
	data        []byte
}

type fakeImageStorage struct {
	saved []savedImage
	url   string
	err   error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (f *fakeImageStorage) Save(_ context.Context, key, contentType string, data []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.saved = append(f.saved, savedImage{key: key, contentType: contentType, data: append([]byte(nil), data...)})
	if f.url != "" {
		return f.url, nil
	}
	return "https://cdn.test/" + key, nil
}

func TestImageResultUploaderRewritesB64JSON(t *testing.T) {
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)

	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"created":1,"data":[{"b64_json":"` + b64 + `","revised_prompt":"a cat"}]}`)

	out, err := uploader.Rewrite(context.Background(), "imgtask_abc", result)
	require.NoError(t, err)

	require.Len(t, storage.saved, 1)
	require.Equal(t, "images/imgtask_abc-0.png", storage.saved[0].key)
	require.Equal(t, "image/png", storage.saved[0].contentType)
	require.Equal(t, pngBytes, storage.saved[0].data)

	var parsed struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out, &parsed))
	require.Len(t, parsed.Data, 1)
	require.JSONEq(t, `"https://cdn.test/images/imgtask_abc-0.png"`, string(parsed.Data[0]["url"]))
	_, hasB64 := parsed.Data[0]["b64_json"]
	require.False(t, hasB64, "b64_json must be stripped after offload")
	require.JSONEq(t, `"a cat"`, string(parsed.Data[0]["revised_prompt"]), "unrelated fields preserved")
}

func TestImageResultUploaderRewritesURL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	defer upstream.Close()

	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, upstream.Client())

	result := json.RawMessage(`{"created":1,"data":[{"url":"` + upstream.URL + `/pic.png"}]}`)
	out, err := uploader.Rewrite(context.Background(), "imgtask_xyz", result)
	require.NoError(t, err)

	require.Len(t, storage.saved, 1)
	require.Equal(t, pngBytes, storage.saved[0].data)
	require.Equal(t, "image/png", storage.saved[0].contentType)

	var parsed struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out, &parsed))
	require.JSONEq(t, `"https://cdn.test/images/imgtask_xyz-0.png"`, string(parsed.Data[0]["url"]))
}

func TestImageResultUploaderRewritesImageDataURLWithoutHTTP(t *testing.T) {
	httpCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return nil, errors.New("HTTP must not be called for data URLs")
	})}
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, client)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"data":[{"url":"DATA:image/jpeg;name=photo.jpg;BaSe64,` + b64 + `","revised_prompt":"kept"}]}`)

	out, err := uploader.Rewrite(context.Background(), "imgtask_data", result)
	require.NoError(t, err)
	require.Zero(t, httpCalls)
	require.Len(t, storage.saved, 1)
	require.Equal(t, pngBytes, storage.saved[0].data)
	require.Equal(t, "image/png", storage.saved[0].contentType, "detected bytes take precedence over a conflicting declaration")
	require.Equal(t, "images/imgtask_data-0.png", storage.saved[0].key)

	var parsed struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out, &parsed))
	require.JSONEq(t, `"https://cdn.test/images/imgtask_data-0.png"`, string(parsed.Data[0]["url"]))
	require.JSONEq(t, `"kept"`, string(parsed.Data[0]["revised_prompt"]))
}

func TestImageResultUploaderDataURLValidation(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{name: "missing comma", url: "data:image/png;base64", wantErr: "missing comma"},
		{name: "non image", url: "data:text/plain;base64,aGVsbG8=", wantErr: "is not an image"},
		{name: "non base64", url: "data:image/png,raw", wantErr: "not base64"},
		{name: "invalid base64", url: "data:image/png;base64,%%%", wantErr: "base64 payload"},
		{name: "invalid media type", url: "data:image/png;bad parameter;base64,aGVsbG8=", wantErr: "invalid media type"},
		{name: "parameter after base64", url: "data:image/png;base64;name=photo.png,aGVsbG8=", wantErr: "base64 marker must be the final header token"},
		{name: "duplicate base64 marker", url: "data:image/png;base64;base64,aGVsbG8=", wantErr: "duplicate base64 marker"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpCalls := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				httpCalls++
				return nil, errors.New("HTTP must not be called for data URLs")
			})}
			uploader := NewImageResultUploader(&fakeImageStorage{}, "images/", 0, client)
			result, err := json.Marshal(map[string]any{"data": []map[string]string{{"url": tt.url}}})
			require.NoError(t, err)

			_, err = uploader.Rewrite(context.Background(), "imgtask_bad", result)
			require.ErrorContains(t, err, tt.wantErr)
			require.Zero(t, httpCalls)
		})
	}
}

func TestImageResultUploaderRejectsOversizedImageDataURL(t *testing.T) {
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 3, nil)
	payload := base64.StdEncoding.EncodeToString([]byte("four"))
	result := json.RawMessage(`{"data":[{"url":"data:image/png;base64,` + payload + `"}]}`)

	_, err := uploader.Rewrite(context.Background(), "imgtask_large", result)
	require.ErrorContains(t, err, "decoded image data URL exceeds 3 bytes")
	require.Empty(t, storage.saved)
}

func TestImageResultUploaderB64JSONTakesPrecedenceOverDataURL(t *testing.T) {
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"data":[{"b64_json":"` + b64 + `","url":"data:text/plain,not-an-image"}]}`)

	_, err := uploader.Rewrite(context.Background(), "imgtask_precedence", result)
	require.NoError(t, err)
	require.Len(t, storage.saved, 1)
	require.Equal(t, pngBytes, storage.saved[0].data)
}

func TestImageResultUploaderPropagatesStorageError(t *testing.T) {
	storage := &fakeImageStorage{err: errors.New("bucket unreachable")}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)

	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"data":[{"b64_json":"` + b64 + `"}]}`)

	_, err := uploader.Rewrite(context.Background(), "imgtask_err", result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bucket unreachable")
}

func TestImageResultUploaderNilStoragePassthrough(t *testing.T) {
	var uploader *ImageResultUploader
	result := json.RawMessage(`{"data":[{"url":"https://example.test/x.png"}]}`)
	out, err := uploader.Rewrite(context.Background(), "imgtask_nil", result)
	require.NoError(t, err)
	require.JSONEq(t, string(result), string(out))
}

func TestImageTaskServiceCompleteOffloadsToStorage(t *testing.T) {
	store := &imageTaskMemoryStore{}
	storage := &fakeImageStorage{}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	svc := NewImageTaskServiceWithUploader(store, uploader, time.Hour, time.Minute)
	require.True(t, svc.Enabled())

	owner := ImageTaskOwner{UserID: 1, APIKeyID: 2}
	created, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)

	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"created":1,"data":[{"b64_json":"` + b64 + `"}]}`)
	require.NoError(t, svc.Complete(context.Background(), created.ID, http.StatusOK, result))

	got, err := svc.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusCompleted, got.Status)
	require.Equal(t, "https://cdn.test/images/"+created.ID+"-0.png", got.ImageURL)
	require.NotContains(t, string(got.Result), "b64_json", "large base64 must not be persisted to Redis")
	require.Len(t, storage.saved, 1)
}

func TestImageTaskServiceCompleteOffloadFailureMarksFailed(t *testing.T) {
	store := &imageTaskMemoryStore{}
	storage := &fakeImageStorage{err: errors.New("bucket unreachable")}
	uploader := NewImageResultUploader(storage, "images/", 0, nil)
	svc := NewImageTaskServiceWithUploader(store, uploader, time.Hour, time.Minute)

	owner := ImageTaskOwner{UserID: 1, APIKeyID: 2}
	created, err := svc.Create(context.Background(), owner)
	require.NoError(t, err)

	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	result := json.RawMessage(`{"data":[{"b64_json":"` + b64 + `"}]}`)
	require.NoError(t, svc.Complete(context.Background(), created.ID, http.StatusOK, result))

	got, err := svc.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	require.Equal(t, ImageTaskStatusFailed, got.Status)
	require.Equal(t, http.StatusBadGateway, got.HTTPStatus)
	require.Contains(t, string(got.Error), "object storage")
	require.NotContains(t, string(got.Result), "b64_json", "failed offload must not persist base64 to Redis")
}

func TestImageResultUploaderDefaultRejectsPrivateDestinations(t *testing.T) {
	var reached atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		_, _ = w.Write(pngBytes)
	}))
	defer server.Close()
	for _, target := range []string{server.URL, "http://localhost/image", "http://10.1.2.3/image", "http://169.254.169.254/latest/meta-data", "http://[::1]/image", "http://[fe80::1]/image", "file:///etc/passwd", "ftp://example.invalid/image", "https://user:password@example.invalid/image"} {
		t.Run(target, func(t *testing.T) {
			storage := &fakeImageStorage{}
			uploader := NewImageResultUploader(storage, "images/", 0, nil)
			_, _, err := uploader.download(context.Background(), target)
			require.Error(t, err)
			require.Empty(t, storage.saved)
		})
	}
	require.Zero(t, reached.Load(), "private requests must never reach the local server")
}

func TestImageResultUploaderDefaultValidatesEveryRedirect(t *testing.T) {
	clearImageDownloadProxyEnvironment(t)
	for _, target := range []string{"http://127.0.0.1/image", "http://169.254.169.254/meta", "http://[::1]/image", "https://user:password@example.invalid/image", "file:///etc/passwd"} {
		t.Run(target, func(t *testing.T) {
			client := defaultImageDownloadHTTPClient()
			guard, ok := client.Transport.(*imageDownloadTransport)
			require.True(t, ok)
			calls := 0
			guard.base = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "https://public.invalid/signed?token=unchanged", req.URL.String())
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})
			_, err := client.Get("https://public.invalid/signed?token=unchanged")
			require.Error(t, err)
			require.Equal(t, 1, calls, "redirect must be rejected before reaching the base transport")
		})
	}
}

func TestImageResultUploaderRejectsNonImagesInEveryEncoding(t *testing.T) {
	for _, data := range [][]byte{[]byte("SYNTHETIC_INTERNAL_CANARY"), []byte("<html>error</html>"), []byte("\x89PNG\r\n\x1a\nfake-png-payload"), nil} {
		b64 := base64.StdEncoding.EncodeToString(data)
		for _, item := range []map[string]string{{"b64_json": b64}, {"url": "data:image/png;base64," + b64}, {"url": "https://public.invalid/image"}} {
			storage := &fakeImageStorage{}
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
			})}
			uploader := NewImageResultUploader(storage, "images/", 0, client)
			result, err := json.Marshal(map[string]any{"data": []map[string]string{item}})
			require.NoError(t, err)
			_, err = uploader.Rewrite(context.Background(), "unsafe", result)
			require.Error(t, err)
			require.Empty(t, storage.saved)
		}
	}
}

func TestImageResultUploaderBoundsEveryEncoding(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	for _, item := range []map[string]string{{"b64_json": b64}, {"url": "data:image/png;base64," + b64}, {"url": "https://public.invalid/image"}} {
		storage := &fakeImageStorage{}
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(pngBytes)), Request: req}, nil
		})}
		uploader := NewImageResultUploader(storage, "images/", int64(len(pngBytes)-1), client)
		result, err := json.Marshal(map[string]any{"data": []map[string]string{item}})
		require.NoError(t, err)
		_, err = uploader.Rewrite(context.Background(), "oversized", result)
		require.ErrorContains(t, err, "exceeds")
		require.Empty(t, storage.saved)
	}
}

func TestImageResultUploaderUsesImageBytesInsteadOfMIME(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/jpeg"}}, Body: io.NopCloser(bytes.NewReader(pngBytes)), Request: req}, nil
	})}
	uploader := NewImageResultUploader(&fakeImageStorage{}, "", int64(len(pngBytes)), client)
	data, contentType, err := uploader.download(context.Background(), "https://public.invalid/image")
	require.NoError(t, err)
	require.Equal(t, "image/png", contentType)
	require.Equal(t, pngBytes, data)
}

func TestImageResultUploaderRejectsHugeGeometryWithoutPixelDecode(t *testing.T) {
	for _, dimensions := range [][2]uint32{{32768, 1}, {8192, 8192}} {
		data := imageStoragePNGHeaderDimensions(dimensions[0], dimensions[1])
		_, err := validatedStorageImageContentType(data)
		require.ErrorContains(t, err, "dimensions exceed")
	}
}

// These fixtures exercise DecodeConfig's header/geometry contract only. The
// compressed pixels remain tiny, so no test allocates a full 4K image or claims
// to validate a complete compressed pixel stream.
func imageStoragePNGHeaderDimensions(width, height uint32) []byte {
	data := bytes.Clone(pngBytes)
	binary.BigEndian.PutUint32(data[16:20], width)
	binary.BigEndian.PutUint32(data[20:24], height)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	return data
}

func TestImageResultUploaderAcceptsDocumentedGemini4KGeometry(t *testing.T) {
	// Google resolution tables, verified 2026-10-19:
	// https://ai.google.dev/gemini-api/docs/generate-content/image-generation
	for _, dimensions := range [][2]uint32{
		{4096, 4096}, {2048, 8192}, {8192, 2048},
		{1536, 12288}, {12288, 1536},
		{3392, 5056}, {5056, 3392}, {3584, 4800}, {4800, 3584},
		{3712, 4608}, {4608, 3712}, {3072, 5504}, {5504, 3072},
		{6336, 2688},
	} {
		data := imageStoragePNGHeaderDimensions(dimensions[0], dimensions[1])
		contentType, err := validatedStorageImageContentType(data)
		require.NoError(t, err, "documented 4K output %dx%d", dimensions[0], dimensions[1])
		require.Equal(t, "image/png", contentType)
	}
}

func TestImageResultUploaderAcceptsGemini4KInEveryEncoding(t *testing.T) {
	data := imageStoragePNGHeaderDimensions(1536, 12288)
	b64 := base64.StdEncoding.EncodeToString(data)
	for _, item := range []map[string]string{{"b64_json": b64}, {"url": "data:image/png;base64," + b64}, {"url": "https://public.invalid/image"}} {
		storage := &fakeImageStorage{}
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
		})}
		uploader := NewImageResultUploader(storage, "images/", 0, client)
		result, err := json.Marshal(map[string]any{"data": []map[string]string{item}})
		require.NoError(t, err)
		out, err := uploader.Rewrite(context.Background(), "gemini4k", result)
		require.NoError(t, err)
		require.Len(t, storage.saved, 1)
		require.Equal(t, data, storage.saved[0].data)
		require.NotContains(t, string(out), "b64_json")
	}
}

func TestImageResultUploaderGeometryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height uint32
		allowed       bool
	}{
		{"pixel limit", 6144, 4096, true},
		{"over pixel limit", 6144, 4097, false},
		{"width limit", 16384, 1, true},
		{"over width limit", 16385, 1, false},
		{"height limit", 1, 16384, true},
		{"over height limit", 1, 16385, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validatedStorageImageContentType(imageStoragePNGHeaderDimensions(tc.width, tc.height))
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "dimensions exceed")
			}
		})
	}
}

func clearImageDownloadProxyEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
}

func TestImageResultUploaderEnvironmentProxyFailsClosed(t *testing.T) {
	clearImageDownloadProxyEnvironment(t)
	t.Setenv("HTTPS_PROXY", "http://proxy-user:synthetic-proxy-secret@proxy.invalid:8080")
	client := defaultImageDownloadHTTPClient()
	guard, ok := client.Transport.(*imageDownloadTransport)
	require.True(t, ok)
	calls := 0
	guard.base = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(pngBytes)), Request: req}, nil
	})
	_, err := client.Get("https://public.invalid/image")
	require.ErrorContains(t, err, "environment proxy")
	require.NotContains(t, err.Error(), "synthetic-proxy-secret")
	require.Zero(t, calls, "strict downloader must not silently switch to direct")
	t.Setenv("NO_PROXY", "public.invalid")
	resp, err := client.Get("https://public.invalid/image")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, 1, calls, "explicit NO_PROXY destinations may use the pinned direct transport")
}

func TestImageResultUploaderRedirectRechecksEnvironmentProxy(t *testing.T) {
	clearImageDownloadProxyEnvironment(t)
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:8080")
	t.Setenv("NO_PROXY", "first.invalid")
	client := defaultImageDownloadHTTPClient()
	guard, ok := client.Transport.(*imageDownloadTransport)
	require.True(t, ok)
	calls := 0
	guard.base = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://second.invalid/image"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})
	_, err := client.Get("https://first.invalid/image")
	require.ErrorContains(t, err, "environment proxy")
	require.Equal(t, 1, calls)
}
