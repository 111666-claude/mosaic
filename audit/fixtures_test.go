package audit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// writeFile creates a file (with parent directories) containing content.
func writeFile(t dirTB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// dirTB is the subset of testing.T used by the fixture builders.
type dirTB interface {
	Helper()
	TempDir() string
	Fatalf(format string, args ...any)
}

// mediaPlaceholder is a non-empty stand-in for a binary media segment.
const mediaPlaceholder = "FAKE-MEDIA-CONTENT"

// hlsFixture builds a healthy two-rendition HLS fMP4 package mirroring the
// layout FFmpeg produces with Mosaic's HLS muxer settings:
// master.m3u8, stream_0.m3u8 / stream_1.m3u8, init_{0,1}.mp4 and
// seg_{v}_{n}.m4s segments of 5s each (last segment 2s), 12s total.
func hlsFixture(t dirTB) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "master.m3u8"), `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-STREAM-INF:BANDWIDTH=449455,AVERAGE-BANDWIDTH=391151,RESOLUTION=1280x720,CODECS="avc1.f4001f,mp4a.40.2"
stream_0.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=446443,AVERAGE-BANDWIDTH=397417,RESOLUTION=854x480,CODECS="avc1.f4001f,mp4a.40.2"
stream_1.m3u8
`)

	for v := 0; v < 2; v++ {
		writeFile(t, filepath.Join(root, "init_"+strconv.Itoa(v)+".mp4"), mediaPlaceholder)
		var b strings.Builder
		b.WriteString("#EXTM3U\n")
		b.WriteString("#EXT-X-VERSION:7\n")
		b.WriteString("#EXT-X-TARGETDURATION:5\n")
		b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
		b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
		b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
		b.WriteString("#EXT-X-MAP:URI=\"init_" + strconv.Itoa(v) + ".mp4\"\n")
		for n := 0; n < 3; n++ {
			dur := 5.0
			if n == 2 {
				dur = 2.0
			}
			b.WriteString("#EXTINF:" + strconv.FormatFloat(dur, 'f', 6, 64) + ",\n")
			seg := "seg_" + strconv.Itoa(v) + "_" + strconv.Itoa(n) + ".m4s"
			b.WriteString(seg + "\n")
			writeFile(t, filepath.Join(root, seg), mediaPlaceholder)
		}
		b.WriteString("#EXT-X-ENDLIST\n")
		writeFile(t, filepath.Join(root, "stream_"+strconv.Itoa(v)+".m3u8"), b.String())
	}
	return root
}

// hlsSubtitleFixture adds a healthy English subtitle track (m3u8 + vtt) and
// rewrites the master to declare it, dropping the second variant so the
// fixture stays small and focused.
func hlsSubtitleFixture(t dirTB) string {
	t.Helper()
	root := hlsFixture(t)

	// The rewritten master below keeps only variant 0, so remove variant 1's
	// files to avoid intentional unreferenced-segment warnings.
	removeFile(t, root, "stream_1.m3u8")
	removeFile(t, root, "init_1.mp4")
	for n := 0; n < 3; n++ {
		removeFile(t, root, "seg_1_"+strconv.Itoa(n)+".m4s")
	}

	writeFile(t, filepath.Join(root, "sub_en.vtt"), "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nHi\n")
	writeFile(t, filepath.Join(root, "sub_en.m3u8"), `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:13
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:12.000,
sub_en.vtt
#EXT-X-ENDLIST
`)
	writeFile(t, filepath.Join(root, "master.m3u8"), `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",DEFAULT=YES,AUTOSELECT=YES,FORCED=NO,LANGUAGE="en",URI="sub_en.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=449455,RESOLUTION=1280x720,SUBTITLES="subs"
stream_0.m3u8
`)
	return root
}

// dashFixture builds a healthy two-video-rendition plus one audio DASH CMAF
// package mirroring FFmpeg's dash muxer output: manifest.mpd with
// SegmentTemplate/SegmentTimeline, init-streamN.m4s and chunk-streamN-K.m4s.
func dashFixture(t dirTB) string {
	t.Helper()
	root := t.TempDir()

	manifest := `<?xml version="1.0" encoding="utf-8"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT12.0S" maxSegmentDuration="PT5.0S" minBufferTime="PT10.0S">
	<Period id="0" start="PT0.0S">
		<AdaptationSet id="0" contentType="video" segmentAlignment="true" bitstreamSwitching="true">
			<Representation id="0" mimeType="video/mp4" codecs="avc1.f4001f" bandwidth="261548" width="1280" height="720">
				<SegmentTemplate timescale="15360" initialization="init-stream$RepresentationID$.m4s" media="chunk-stream$RepresentationID$-$Number$.m4s" startNumber="1">
					<SegmentTimeline>
						<S t="0" d="76800" r="1" />
						<S d="30720" />
					</SegmentTimeline>
				</SegmentTemplate>
			</Representation>
			<Representation id="1" mimeType="video/mp4" codecs="avc1.f4001f" bandwidth="267816" width="854" height="480">
				<SegmentTemplate timescale="15360" initialization="init-stream$RepresentationID$.m4s" media="chunk-stream$RepresentationID$-$Number$.m4s" startNumber="1">
					<SegmentTimeline>
						<S t="0" d="76800" r="1" />
						<S d="30720" />
					</SegmentTimeline>
				</SegmentTemplate>
			</Representation>
		</AdaptationSet>
		<AdaptationSet id="1" contentType="audio" segmentAlignment="true">
			<Representation id="2" mimeType="audio/mp4" codecs="mp4a.40.2" bandwidth="128000" audioSamplingRate="44100">
				<SegmentTemplate timescale="44100" initialization="init-stream$RepresentationID$.m4s" media="chunk-stream$RepresentationID$-$Number$.m4s" startNumber="1">
					<SegmentTimeline>
						<S t="0" d="220160" />
						<S d="221184" />
						<S d="87856" />
					</SegmentTimeline>
				</SegmentTemplate>
			</Representation>
		</AdaptationSet>
	</Period>
</MPD>
`
	writeFile(t, filepath.Join(root, "manifest.mpd"), manifest)

	for _, id := range []string{"0", "1", "2"} {
		writeFile(t, filepath.Join(root, "init-stream"+id+".m4s"), mediaPlaceholder)
		for n := 1; n <= 3; n++ {
			writeFile(t, filepath.Join(root, "chunk-stream"+id+"-"+strconv.Itoa(n)+".m4s"), mediaPlaceholder)
		}
	}
	return root
}

// dashSubtitleFixture adds a text AdaptationSet with a BaseURL payload.
func dashSubtitleFixture(t dirTB) string {
	t.Helper()
	root := dashFixture(t)
	manifest := filepath.Join(root, "manifest.mpd")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	injected := strings.Replace(string(data), "\t</Period>", `		<AdaptationSet id="100" contentType="text" mimeType="text/vtt" lang="en">
			<Representation id="sub_en" bandwidth="256">
				<BaseURL>sub_en.vtt</BaseURL>
			</Representation>
		</AdaptationSet>
	</Period>`, 1)
	writeFile(t, manifest, injected)
	writeFile(t, filepath.Join(root, "sub_en.vtt"), "WEBVTT\n")
	return root
}

// removeFile deletes a fixture file.
func removeFile(t dirTB, root, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatalf("remove %s: %v", name, err)
	}
}

// replaceInFile performs a literal string replacement inside a fixture file.
func replaceInFile(t dirTB, root, name, old, new string) {
	t.Helper()
	p := filepath.Join(root, name)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	updated := strings.Replace(string(data), old, new, 1)
	if updated == string(data) {
		t.Fatalf("replacement target %q not found in %s", old, name)
	}
	if err := os.WriteFile(p, []byte(updated), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
