# Troubleshooting

This document lists common failures and how to debug them.

## FFprobe Not Found

Symptom:

```text
exec: "ffprobe": executable file not found in $PATH
```

Fix:

Install FFmpeg and ensure `ffprobe` is on `PATH`.

```bash
ffprobe -version
ffmpeg -version
```

## FFmpeg Not Found

Symptom:

```text
exec: "ffmpeg": executable file not found in $PATH
```

Fix:

Install FFmpeg and ensure `ffmpeg` is on `PATH`.

## Remote URL Fails

Symptoms:

```text
Failed to resolve hostname
Input/output error
```

Likely causes:

- DNS blocked.
- Network egress blocked.
- URL expired.
- FFmpeg build lacks protocol support.

Debug:

```bash
ffprobe -v error -show_format -show_streams "https://example.com/video.mp4"
```

## Output Directory Permission Denied

Symptom:

```text
create output dir: mkdir /path: permission denied
```

Fix:

Use a writable output directory.

```bash
mkdir -p /tmp/mosaic-output
```

Mosaic creates directories automatically, but the process still needs permissions.

## Video Is Upside Down

Likely cause:

- Source rotation metadata was interpreted differently by the player.
- Orientation normalization was disabled.

Recommended option:

```go
mosaic.WithNormalizeOrientation()
```

Debug source metadata:

```bash
ffprobe -v error -select_streams v:0 \
  -show_entries stream=width,height:stream_tags=rotate:stream_side_data=rotation \
  -of json input.mp4
```

Debug output init segment:

```bash
ffprobe -v error -select_streams v:0 \
  -show_entries stream=width,height:stream_tags=rotate:stream_side_data=rotation \
  -of json /tmp/output/init_0.mp4
```

Output rotation metadata should be absent or `0`.

## Output Aspect Ratio Looks Wrong

Mosaic preserves display aspect ratio when building ladder dimensions.

Check source display dimensions:

```bash
ffprobe -v error -select_streams v:0 \
  -show_entries stream=width,height:stream_side_data=rotation:stream_tags=rotate \
  -of json input.mp4
```

Check output dimensions:

```bash
for f in /tmp/output/init*.mp4; do
  echo "$f"
  ffprobe -v error -select_streams v:0 \
    -show_entries stream=width,height \
    -of csv=p=0 "$f"
done
```

For a square input, outputs should be square.

For a portrait input, outputs should be portrait.

For a non-standard input, dimensions may differ by one or two pixels from the exact mathematical ratio because Mosaic forces even dimensions.

## Hardware Encoder Fails

Symptoms:

```text
Unknown encoder 'h264_nvenc'
No capable devices found
Error initializing output stream
```

Fix:

- Confirm FFmpeg supports the encoder:

```bash
ffmpeg -hide_banner -encoders | grep h264
```

- Confirm drivers and devices are available.
- Try CPU encoding by removing the GPU option.

## HLS Player Cannot Load Output

Check files:

```bash
find /tmp/output -maxdepth 1 -type f -printf '%f\n' | sort
```

Expected HLS files:

```text
master.m3u8
stream_0.m3u8
init*.mp4
seg_*.m4s
```

Serve the directory over HTTP for browser playback. Many browser/player combinations do not reliably load HLS from `file://`.

## DASH Player Cannot Load Output

Expected DASH files:

```text
manifest.mpd
init-stream*.m4s
chunk-stream*.m4s
```

Use an HTTP server for playback tests.

## Playback Goes Black Mid-Stream or a Segment Is Missing

Run the read-only packaging auditor against the output directory. It never re-encodes and pinpoints the exact file, rendition, and timeline position of the defect:

```bash
mosaic audit ./output/hls
mosaic audit --json ./output/dash
```

Common findings and their meaning:

- `missing_file`: a playlist/manifest references a segment, init segment, or subtitle payload that does not exist (the typical "black screen halfway through" cause).
- `timeline_gap` / `timeline_overlap`: segment numbering or the DASH SegmentTimeline is non-contiguous.
- `path_escape`: a reference points outside the output directory (for example `../../etc/x.m4s`) or uses an unsupported absolute form, so packaged uploads break.
- `duplicate_segment`: the same segment is referenced twice.
- `invalid_duration`: an EXTINF is zero/negative or exceeds the target duration.
- `manifest_too_large`, `rendition_limit_exceeded`, `segment_limit_exceeded`: a configured safety limit was exceeded; override with the `--max-*` flags if the package is legitimate.

The CLI exits `0` when no error-severity issues are found, `1` when defects are present, and `2` on setup failure (missing/empty directory or no manifest).

## Tests Fail Because Go Cache Is Read-Only

Symptom:

```text
open /home/user/.cache/go-build/...: read-only file system
```

Fix:

```bash
GOCACHE=/tmp/go-build go test ./...
```

## Collecting a Useful Bug Report

Include:

- input file or URL
- exact code snippet or command
- OS and architecture
- Go version
- FFmpeg version
- full error output
- source FFprobe JSON
- output init segment FFprobe JSON
- expected output dimensions
- actual output dimensions
