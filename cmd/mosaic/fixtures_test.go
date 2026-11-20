package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// hlsCLIFixture builds a small healthy HLS package in a temp dir for the
// `mosaic audit` CLI tests. It mirrors the audit package fixture layout
// without importing the package's test-only helpers.
func hlsCLIFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, "master.m3u8"), "#EXTM3U\n#EXT-X-VERSION:7\n"+
		"#EXT-X-STREAM-INF:BANDWIDTH=400000,RESOLUTION=1280x720\nstream_0.m3u8\n"+
		"#EXT-X-STREAM-INF:BANDWIDTH=200000,RESOLUTION=854x480\nstream_1.m3u8\n")

	for v := 0; v < 2; v++ {
		mustWrite(t, filepath.Join(root, "init_"+strconv.Itoa(v)+".mp4"), "INIT")
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:5\n")
		b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n")
		b.WriteString("#EXT-X-MAP:URI=\"init_" + strconv.Itoa(v) + ".mp4\"\n")
		for n := 0; n < 3; n++ {
			b.WriteString("#EXTINF:5.000000,\n")
			seg := "seg_" + strconv.Itoa(v) + "_" + strconv.Itoa(n) + ".m4s"
			b.WriteString(seg + "\n")
			mustWrite(t, filepath.Join(root, seg), "MEDIA")
		}
		b.WriteString("#EXT-X-ENDLIST\n")
		mustWrite(t, filepath.Join(root, "stream_"+strconv.Itoa(v)+".m3u8"), b.String())
	}
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func removeCLIFIxtureFile(t *testing.T, root, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatalf("remove %s: %v", name, err)
	}
}
