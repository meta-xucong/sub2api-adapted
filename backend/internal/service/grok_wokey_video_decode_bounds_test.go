package service

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDecodeWokeyVideoDataURLPreservesEncodingCompatibility(t *testing.T) {
	tests := []struct {
		name, input string
		want        []byte
		wantErr     bool
	}{
		{name: "base64", input: "data:image/png;base64,aGVsbG8=", want: []byte("hello")},
		{name: "wrapped base64", input: "data:image/png;base64, \taGVs\r\nbG8=\n ", want: []byte("hello")},
		{name: "escaped", input: "data:image/png,hello%00%FF+%20world", want: []byte("hello\x00\xff+ world")},
		{name: "invalid escape", input: "data:image/png,%GG", wantErr: true},
		{name: "truncated escape", input: "data:image/png,abc%", wantErr: true},
		{name: "invalid base64", input: "data:image/png;base64,aGVs!G8=", wantErr: true},
		{name: "unpadded base64", input: "data:image/png;base64,aGVsbG8", wantErr: true},
		{name: "trailing base64", input: "data:image/png;base64,aGVsbG8=AAAA", wantErr: true},
		{name: "empty", input: "data:image/png;base64,", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeWokeyVideoDataURL(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got.Data)
			require.Equal(t, "image/png", got.ContentType)
		})
	}
}

func TestDecodeWokeyVideoDataURLExactByteBoundary(t *testing.T) {
	for _, size := range []int{wokeyVideoReferenceImageMaxBytes, wokeyVideoReferenceImageMaxBytes + 1} {
		raw := strings.Repeat("a", size)
		for _, kind := range []string{"base64", "wrapped base64", "plain", "escaped"} {
			t.Run(kind+"/"+strconv.Itoa(size), func(t *testing.T) {
				input := "data:image/png," + raw
				switch kind {
				case "base64", "wrapped base64":
					encoded := base64.StdEncoding.EncodeToString([]byte(raw))
					if kind == "wrapped base64" {
						encoded = encoded[:4] + "\r\n" + encoded[4:]
					}
					input = "data:image/png;base64," + encoded
				case "escaped":
					input = "data:image/png," + strings.Repeat("%61", size)
				}
				got, err := decodeWokeyVideoDataURL(input)
				if size > wokeyVideoReferenceImageMaxBytes {
					require.ErrorContains(t, err, "exceeds")
					return
				}
				require.NoError(t, err)
				require.Len(t, got.Data, size)
				require.Equal(t, byte('a'), got.Data[0])
				require.Equal(t, byte('a'), got.Data[size-1])
			})
		}
	}
}

func TestDecodeWokeyVideoDataURLRejectsOversizeBeforeLargeAllocation(t *testing.T) {
	for _, kind := range []string{"base64", "plain", "escaped"} {
		t.Run(kind, func(t *testing.T) {
			if runWokeyVideoAllocationGuardInSubprocess(t) {
				return
			}
			// Prepare input before measuring: this checks decoder overhead, not the
			// request body's already-existing allocation or aggregate server memory.
			size := 2 * wokeyVideoReferenceImageMaxBytes
			input := "data:image/png," + strings.Repeat("a", size)
			switch kind {
			case "base64":
				input = "data:image/png;base64," + strings.Repeat("A", base64.StdEncoding.EncodedLen(size))
			case "escaped":
				input = "data:image/png," + strings.Repeat("%61", size)
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			got, err := decodeWokeyVideoDataURL(input)
			runtime.ReadMemStats(&after)
			runtime.KeepAlive(input)
			require.ErrorContains(t, err, "exceeds")
			require.Empty(t, got.Data)
			allocated := after.TotalAlloc - before.TotalAlloc
			t.Logf("decoder allocation bytes for oversized %s: %d", kind, allocated)
			require.Less(t, allocated, uint64(1<<20), "oversized input must be rejected before full decode allocation")
		})
	}
}

func TestDecodeWokeyVideoDataURLLineWrappingDoesNotInflateAllocation(t *testing.T) {
	if runWokeyVideoAllocationGuardInSubprocess(t) {
		return
	}
	input := "data:image/png;base64,YQ" + strings.Repeat("\r\n", wokeyVideoReferenceImageMaxBytes) + "=="
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := decodeWokeyVideoDataURL(input)
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(input)
	require.NoError(t, err)
	require.Equal(t, []byte("a"), got.Data)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("decoder allocation bytes for wrapped input: %d", allocated)
	require.Less(t, allocated, uint64(1<<20))
}

func runWokeyVideoAllocationGuardInSubprocess(t *testing.T) bool {
	t.Helper()
	// TotalAlloc is process-wide. Isolate each measurement from background
	// goroutines left by other service tests, without changing the byte limit.
	const childMarker = "SUB2API_WOKEY_VIDEO_ALLOCATION_TEST_CHILD"
	if os.Getenv(childMarker) == t.Name() {
		return false
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	// Go matches each slash-separated test-name component independently.
	parts := strings.Split(t.Name(), "/")
	for i := range parts {
		parts[i] = "^" + regexp.QuoteMeta(parts[i]) + "$"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary,
		"-test.run="+strings.Join(parts, "/"),
		"-test.count=1", "-test.timeout=45s", "-test.v")
	// The decoder needs no provider credentials or inherited service settings.
	cmd.Env = []string{childMarker + "=" + t.Name(), "GOMAXPROCS=2", "GOMEMLIMIT=2GiB"}
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "isolated allocation guard failed (context: %v):\n%s", ctx.Err(), output)
	t.Logf("isolated allocation guard:\n%s", output)
	return true
}
