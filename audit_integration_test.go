package mosaic

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/farshidrezaei/mosaic/audit"
)

// TestAuditIntegrationWithRealFFmpeg verifies the auditor end to end against
// packages produced by Mosaic's real HLS and DASH encoders. It is gated by
// FFMPEG_INTEGRATION and never runs in the default unit-test suite.
//
// The 720p test source yields a two-rung ladder (720p + 360p), so each package
// is a genuine dual-bitrate output.
func TestAuditIntegrationWithRealFFmpeg(t *testing.T) {
	if os.Getenv("FFMPEG_INTEGRATION") == "" {
		t.Skip("Skipping audit integration test (FFMPEG_INTEGRATION not set)")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}

	tmp := t.TempDir()
	inputPath := filepath.Join(tmp, "input.mp4")
	if err := runCommand("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=1280x720:rate=30:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac", inputPath); err != nil {
		t.Fatalf("generate input: %v", err)
	}

	ctx := context.Background()

	t.Run("HLS package audits clean", func(t *testing.T) {
		out := filepath.Join(tmp, "hls")
		job := Job{Input: inputPath, OutputDir: out, Profile: ProfileVOD}
		if _, err := EncodeHls(ctx, job, WithLogLevel("error")); err != nil {
			t.Fatalf("EncodeHls: %v", err)
		}

		report, err := AuditPackage(out)
		if err != nil {
			t.Fatalf("AuditPackage setup error: %v", err)
		}
		if !report.OK() {
			t.Fatalf("fresh HLS package has audit issues: %+v", report.Issues)
		}
		if len(report.Renditions) != 2 {
			t.Fatalf("expected 2 renditions, got %d: %+v", len(report.Renditions), report.Renditions)
		}

		// Deleting one segment must surface as a missing-file error.
		target := report.Renditions[0].MediaSegments[0]
		if removeErr := os.Remove(filepath.Join(out, target)); removeErr != nil {
			t.Fatalf("remove segment: %v", removeErr)
		}
		faulty, err := AuditPackage(out)
		if err != nil {
			t.Fatalf("re-audit setup error: %v", err)
		}
		found := false
		for _, issue := range faulty.Issues {
			if issue.Code == audit.CodeMissingFile && issue.Path == target {
				found = true
			}
		}
		if !found {
			t.Fatalf("deleted segment %s not reported; issues=%+v", target, faulty.Issues)
		}
	})

	t.Run("DASH package audits clean", func(t *testing.T) {
		out := filepath.Join(tmp, "dash")
		job := Job{Input: inputPath, OutputDir: out, Profile: ProfileVOD}
		if _, err := EncodeDash(ctx, job, WithLogLevel("error")); err != nil {
			t.Fatalf("EncodeDash: %v", err)
		}

		report, err := AuditPackage(out)
		if err != nil {
			t.Fatalf("AuditPackage setup error: %v", err)
		}
		if !report.OK() {
			t.Fatalf("fresh DASH package has audit issues: %+v", report.Issues)
		}
		// Two video representations plus one audio representation.
		if len(report.Renditions) < 3 {
			t.Fatalf("expected >=3 renditions (video x2 + audio), got %d", len(report.Renditions))
		}

		// A bad subtitle BaseURL must be reported as a missing file.
		manifest := filepath.Join(out, "manifest.mpd")
		original, err := os.ReadFile(manifest)
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		subSet := `
		<AdaptationSet id="999" contentType="text" mimeType="text/vtt" lang="en">
			<Representation id="sub_missing" bandwidth="256">
				<BaseURL>does_not_exist.vtt</BaseURL>
			</Representation>
		</AdaptationSet>
	`
		mpd := string(original)
		idx := lastIndex(mpd, "</Period>")
		if idx < 0 {
			t.Fatal("fixture manifest has no </Period>")
		}
		mpd = mpd[:idx] + subSet + mpd[idx:]
		if writeErr := os.WriteFile(manifest, []byte(mpd), 0o644); writeErr != nil {
			t.Fatalf("rewrite manifest: %v", writeErr)
		}

		faulty, err := AuditPackage(out)
		if err != nil {
			t.Fatalf("re-audit setup error: %v", err)
		}
		found := false
		for _, issue := range faulty.Issues {
			if issue.Code == audit.CodeMissingFile && issue.Path == "does_not_exist.vtt" {
				found = true
			}
		}
		if !found {
			t.Fatalf("bad subtitle reference not reported; issues=%+v", faulty.Issues)
		}
	})

	t.Run("sentinel errors are usable via errors.Is", func(t *testing.T) {
		if _, err := AuditPackage(filepath.Join(tmp, "missing")); !errors.Is(err, audit.ErrDirNotFound) {
			t.Fatalf("expected ErrDirNotFound, got %v", err)
		}
		empty := filepath.Join(tmp, "empty")
		if err := os.MkdirAll(empty, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if _, err := AuditPackage(empty); !errors.Is(err, audit.ErrEmptyDirectory) {
			t.Fatalf("expected ErrEmptyDirectory, got %v", err)
		}
	})
}

// lastIndex returns the last index of substr, or -1.
func lastIndex(s, substr string) int {
	for i := len(s) - len(substr); i >= 0; i-- {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
