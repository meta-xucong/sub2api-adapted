package service

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"github.com/stretchr/testify/require"
	"hash/crc32"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestAvatarCompressionChecksGeometryBeforeDecode(t *testing.T) {
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	raw := b.Bytes()
	binary.BigEndian.PutUint32(raw[16:20], 9000)
	binary.BigEndian.PutUint32(raw[20:24], 9000)
	binary.BigEndian.PutUint32(raw[29:33], crc32.ChecksumIEEE(raw[12:29]))
	_, _, err := compressInlineAvatar(raw)
	require.ErrorIs(t, err, ErrAvatarDimensions)
}

func TestAvatarEncodedByteCapBeforeAllocation(t *testing.T) {
	_, err := normalizeInlineUserAvatarInput("data:image/png;base64," + strings.Repeat("A", base64.StdEncoding.EncodedLen(maxInlineAvatarBytes)+4))
	require.ErrorIs(t, err, ErrAvatarTooLarge)
}

func TestAvatarCompressionRejectsMalformedImage(t *testing.T) {
	_, _, err := compressInlineAvatar([]byte("invalid image"))
	require.ErrorIs(t, err, ErrAvatarInvalid)
}

func TestAvatarWrappedBase64KeepsExistingCompatibility(t *testing.T) {
	raw := []byte("small-avatar")
	encoded := base64.StdEncoding.EncodeToString(raw)
	wrapped := encoded[:4] + "\r\n" + encoded[4:]
	result, err := normalizeInlineUserAvatarInput("data:image/png;base64," + wrapped)
	require.NoError(t, err)
	require.Equal(t, len(raw), result.ByteSize)
}
