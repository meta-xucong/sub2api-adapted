# Media CSP fixture

`media-preview.mp4` is a generated 0.5-second, 16×16 black H.264 clip with no audio,
metadata or external content. It is used only by `../check_media_preview_csp.cjs`.
The browser check generates a silent WAV in memory for its audio cases.

Fixture: 1,653 bytes; SHA-256
`4526ddce1f5d407d86719a820b8502d7b8e12d6c57d60cda5261749ab9fdd186`.
Encoding with a different FFmpeg/libx264 version may produce different bytes.

Recreate the video with an existing FFmpeg installation:

```sh
ffmpeg -hide_banner -loglevel error \
  -f lavfi -i color=c=black:s=16x16:r=10:d=0.5 -an \
  -c:v libx264 -pix_fmt yuv420p -map_metadata -1 \
  -fflags +bitexact -flags:v +bitexact -movflags +faststart media-preview.mp4
```
