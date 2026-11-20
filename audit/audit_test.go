package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findIssue returns the first issue matching the given code, or nil.
func findIssue(report *Report, code Code) *Issue {
	for i := range report.Issues {
		if report.Issues[i].Code == code {
			return &report.Issues[i]
		}
	}
	return nil
}

// hasCode reports whether the report contains an issue with the given code.
func hasCode(report *Report, code Code) bool {
	return findIssue(report, code) != nil
}

// mustAudit fails the test when the audit itself returns a setup error.
func mustAudit(t *testing.T, root string, opts ...Option) *Report {
	t.Helper()
	report, err := Audit(root, opts...)
	if err != nil {
		t.Fatalf("Audit(%s) unexpected setup error: %v", root, err)
	}
	return report
}

// ---------------------------------------------------------------------------
// Healthy packages
// ---------------------------------------------------------------------------

func TestAuditHLS_HealthyFixture(t *testing.T) {
	root := hlsFixture(t)
	report := mustAudit(t, root)

	if report.Format != FormatHLS {
		t.Fatalf("format = %q, want hls", report.Format)
	}
	if !report.OK() {
		t.Fatalf("healthy fixture reported issues: %+v", report.Issues)
	}
	if len(report.Renditions) != 2 {
		t.Fatalf("renditions = %d, want 2", len(report.Renditions))
	}
	for _, rr := range report.Renditions {
		if rr.Track != "video" {
			t.Errorf("rendition %s track = %q, want video", rr.Name, rr.Track)
		}
		if rr.SegmentCount != 3 {
			t.Errorf("rendition %s segments = %d, want 3", rr.Name, rr.SegmentCount)
		}
		if rr.Duration != 12.0 {
			t.Errorf("rendition %s duration = %v, want 12", rr.Name, rr.Duration)
		}
		if !strings.HasPrefix(rr.InitSegment, "init_") {
			t.Errorf("rendition %s init = %q, want init_*", rr.Name, rr.InitSegment)
		}
	}
	if len(report.Manifests) != 3 {
		t.Errorf("manifests = %v, want master + 2 playlists", report.Manifests)
	}
}

func TestAuditDASH_HealthyFixture(t *testing.T) {
	root := dashFixture(t)
	report := mustAudit(t, root)

	if report.Format != FormatDASH {
		t.Fatalf("format = %q, want dash", report.Format)
	}
	if !report.OK() {
		t.Fatalf("healthy fixture reported issues: %+v", report.Issues)
	}
	if len(report.Renditions) != 3 {
		t.Fatalf("renditions = %d, want 3 (2 video + 1 audio)", len(report.Renditions))
	}

	tracks := map[string]int{}
	for _, rr := range report.Renditions {
		tracks[rr.Track]++
		if rr.SegmentCount != 3 {
			t.Errorf("rendition %s segments = %d, want 3", rr.Name, rr.SegmentCount)
		}
		if rr.Duration != 12.0 {
			t.Errorf("rendition %s duration = %v, want 12", rr.Name, rr.Duration)
		}
		if rr.InitSegment == "" {
			t.Errorf("rendition %s has no init segment", rr.Name)
		}
	}
	if tracks["video"] != 2 || tracks["audio"] != 1 {
		t.Errorf("track distribution = %v, want 2 video + 1 audio", tracks)
	}
}

// ---------------------------------------------------------------------------
// Setup failures
// ---------------------------------------------------------------------------

func TestAudit_MissingDirectory(t *testing.T) {
	_, err := Audit(filepath.Join(t.TempDir(), "does-not-exist"))
	if !errors.Is(err, ErrDirNotFound) {
		t.Fatalf("err = %v, want ErrDirNotFound", err)
	}
}

func TestAudit_EmptyDirectory(t *testing.T) {
	_, err := Audit(t.TempDir())
	if !errors.Is(err, ErrEmptyDirectory) {
		t.Fatalf("err = %v, want ErrEmptyDirectory", err)
	}
}

func TestAudit_NoManifest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "random.txt"), "hello")
	_, err := Audit(root)
	if !errors.Is(err, ErrNoManifest) {
		t.Fatalf("err = %v, want ErrNoManifest", err)
	}
}

func TestAudit_ForcedFormatMismatch(t *testing.T) {
	root := hlsFixture(t)
	_, err := Audit(root, WithFormat(FormatDASH))
	if !errors.Is(err, ErrNoManifest) {
		t.Fatalf("err = %v, want ErrNoManifest for forced dash on HLS dir", err)
	}
}

// ---------------------------------------------------------------------------
// HLS defects
// ---------------------------------------------------------------------------

func TestAuditHLS_MissingSegment(t *testing.T) {
	root := hlsFixture(t)
	removeFile(t, root, "seg_0_1.m4s")
	report := mustAudit(t, root)

	issue := findIssue(report, CodeMissingFile)
	if issue == nil {
		t.Fatalf("expected missing_file issue, got %+v", report.Issues)
	}
	if issue.Severity != SeverityError || issue.Path != "seg_0_1.m4s" {
		t.Errorf("unexpected issue: %+v", issue)
	}
}

func TestAuditHLS_EmptySegmentFile(t *testing.T) {
	root := hlsFixture(t)
	if err := os.WriteFile(filepath.Join(root, "seg_1_0.m4s"), nil, 0o644); err != nil {
		t.Fatalf("truncate segment: %v", err)
	}
	report := mustAudit(t, root)
	if !hasCode(report, CodeEmptyFile) {
		t.Fatalf("expected empty_file issue, got %+v", report.Issues)
	}
}

func TestAuditHLS_MissingInitSegmentFile(t *testing.T) {
	root := hlsFixture(t)
	removeFile(t, root, "init_0.mp4")
	report := mustAudit(t, root)

	issue := findIssue(report, CodeMissingFile)
	if issue == nil || issue.Path != "init_0.mp4" {
		t.Fatalf("expected missing init file, got %+v", report.Issues)
	}
}

func TestAuditHLS_MissingInitSegmentDeclaration(t *testing.T) {
	root := hlsFixture(t)
	replaceInFile(t, root, "stream_0.m3u8", "#EXT-X-MAP:URI=\"init_0.mp4\"\n", "")
	report := mustAudit(t, root)
	if !hasCode(report, CodeMissingInitSegment) {
		t.Fatalf("expected missing_init_segment issue, got %+v", report.Issues)
	}
}

func TestAuditHLS_PathEscape(t *testing.T) {
	root := hlsFixture(t)
	replaceInFile(t, root, "stream_1.m3u8", "seg_1_0.m4s", "../../etc/evil.m4s")
	report := mustAudit(t, root)

	issue := findIssue(report, CodePathEscape)
	if issue == nil {
		t.Fatalf("expected path_escape issue, got %+v", report.Issues)
	}
	if issue.Severity != SeverityError {
		t.Errorf("path escape severity = %q, want error", issue.Severity)
	}
}

func TestAuditHLS_AbsolutePathRejected(t *testing.T) {
	root := hlsFixture(t)
	replaceInFile(t, root, "stream_1.m3u8", "seg_1_0.m4s", "/etc/passwd")
	report := mustAudit(t, root)
	if !hasCode(report, CodePathEscape) {
		t.Fatalf("expected path_escape for absolute path, got %+v", report.Issues)
	}
}

func TestAuditHLS_DuplicateSegment(t *testing.T) {
	root := hlsFixture(t)
	replaceInFile(t, root, "stream_1.m3u8", "seg_1_2.m4s", "seg_1_1.m4s")
	report := mustAudit(t, root)

	if !hasCode(report, CodeDuplicateSegment) {
		t.Fatalf("expected duplicate_segment issue, got %+v", report.Issues)
	}
	if !hasCode(report, CodeTimelineOverlap) {
		t.Fatalf("expected timeline_overlap from backwards numbering, got %+v", report.Issues)
	}
}

func TestAuditHLS_TimelineGap(t *testing.T) {
	root := hlsFixture(t)
	// Renumber the middle reference to 9 and provide that file, isolating the
	// numbering gap from the missing-file defect.
	writeFile(t, filepath.Join(root, "seg_0_9.m4s"), mediaPlaceholder)
	replaceInFile(t, root, "stream_0.m3u8", "seg_0_1.m4s", "seg_0_9.m4s")
	report := mustAudit(t, root)

	if !hasCode(report, CodeTimelineGap) {
		t.Fatalf("expected timeline_gap issue, got %+v", report.Issues)
	}
}

func TestAuditHLS_InvalidDuration(t *testing.T) {
	root := hlsFixture(t)
	replaceInFile(t, root, "stream_0.m3u8", "#EXTINF:5.000000,", "#EXTINF:9.500000,")
	report := mustAudit(t, root)

	issue := findIssue(report, CodeInvalidDuration)
	if issue == nil {
		t.Fatalf("expected invalid_duration issue, got %+v", report.Issues)
	}
	if !hasCode(report, CodeDurationMismatch) {
		t.Fatalf("expected duration_mismatch after drift, got %+v", report.Issues)
	}
}

func TestAuditHLS_NonPositiveDuration(t *testing.T) {
	root := hlsFixture(t)
	replaceInFile(t, root, "stream_0.m3u8", "#EXTINF:5.000000,", "#EXTINF:0,")
	report := mustAudit(t, root)
	if !hasCode(report, CodeInvalidDuration) {
		t.Fatalf("expected invalid_duration for zero EXTINF, got %+v", report.Issues)
	}
}

func TestAuditHLS_EmptyRendition(t *testing.T) {
	root := hlsFixture(t)
	writeFile(t, filepath.Join(root, "empty.m3u8"), "#EXTM3U\n#EXT-X-TARGETDURATION:5\n#EXT-X-ENDLIST\n")
	replaceInFile(t, root, "master.m3u8", "stream_1.m3u8", "empty.m3u8")
	report := mustAudit(t, root)
	if !hasCode(report, CodeEmptyRendition) {
		t.Fatalf("expected empty_rendition issue, got %+v", report.Issues)
	}
}

func TestAuditHLS_MalformedMaster(t *testing.T) {
	root := hlsFixture(t)
	writeFile(t, filepath.Join(root, "master.m3u8"), "not a playlist\n")
	report := mustAudit(t, root)
	if !hasCode(report, CodeManifestInvalid) {
		t.Fatalf("expected manifest_invalid for bad header, got %+v", report.Issues)
	}
}

func TestAuditHLS_MissingVariantPlaylist(t *testing.T) {
	root := hlsFixture(t)
	removeFile(t, root, "stream_0.m3u8")
	report := mustAudit(t, root)
	if !hasCode(report, CodeMissingFile) {
		t.Fatalf("expected missing_file for variant playlist, got %+v", report.Issues)
	}
}

func TestAuditHLS_UnreferencedSegment(t *testing.T) {
	root := hlsFixture(t)
	writeFile(t, filepath.Join(root, "seg_0_99.m4s"), mediaPlaceholder)
	report := mustAudit(t, root)

	issue := findIssue(report, CodeUnreferencedSegment)
	if issue == nil || issue.Path != "seg_0_99.m4s" {
		t.Fatalf("expected unreferenced warning for seg_0_99.m4s, got %+v", report.Issues)
	}
	if issue.Severity != SeverityWarning {
		t.Errorf("unreferenced severity = %q, want warning", issue.Severity)
	}
}

func TestAuditHLS_BadSubtitleReference(t *testing.T) {
	root := hlsSubtitleFixture(t)

	// Healthy baseline: subtitle track and its VTT payload validate cleanly.
	if report := mustAudit(t, root); !report.OK() {
		t.Fatalf("healthy subtitle fixture reported issues: %+v", report.Issues)
	}

	removeFile(t, root, "sub_en.vtt")
	report := mustAudit(t, root)
	issue := findIssue(report, CodeMissingFile)
	if issue == nil || issue.Path != "sub_en.vtt" || issue.Track != "subtitle" {
		t.Fatalf("expected missing subtitle payload, got %+v", report.Issues)
	}
}

func TestAuditHLS_SubtitleDeclarationWithoutURI(t *testing.T) {
	root := hlsFixture(t)
	writeFile(t, filepath.Join(root, "master.m3u8"), `#EXTM3U
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",LANGUAGE="en"
#EXT-X-STREAM-INF:BANDWIDTH=400000
stream_0.m3u8
`)
	report := mustAudit(t, root)
	if !hasCode(report, CodeTrackDeclarationInvalid) {
		t.Fatalf("expected track_declaration_invalid for URI-less subtitle, got %+v", report.Issues)
	}
}

// ---------------------------------------------------------------------------
// DASH defects
// ---------------------------------------------------------------------------

func TestAuditDASH_MissingChunk(t *testing.T) {
	root := dashFixture(t)
	removeFile(t, root, "chunk-stream0-2.m4s")
	report := mustAudit(t, root)

	issue := findIssue(report, CodeMissingFile)
	if issue == nil || issue.Path != "chunk-stream0-2.m4s" {
		t.Fatalf("expected missing chunk, got %+v", report.Issues)
	}
}

func TestAuditDASH_EmptyChunk(t *testing.T) {
	root := dashFixture(t)
	if err := os.WriteFile(filepath.Join(root, "chunk-stream2-1.m4s"), nil, 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	report := mustAudit(t, root)
	if !hasCode(report, CodeEmptyFile) {
		t.Fatalf("expected empty_file, got %+v", report.Issues)
	}
}

func TestAuditDASH_MissingInitSegment(t *testing.T) {
	root := dashFixture(t)
	removeFile(t, root, "init-stream1.m4s")
	report := mustAudit(t, root)

	issue := findIssue(report, CodeMissingFile)
	if issue == nil || issue.Path != "init-stream1.m4s" {
		t.Fatalf("expected missing init segment, got %+v", report.Issues)
	}
}

func TestAuditDASH_InitSegmentDeclarationMissing(t *testing.T) {
	root := dashFixture(t)
	replaceInFile(t, root, "manifest.mpd",
		"initialization=\"init-stream$RepresentationID$.m4s\" ", "")
	report := mustAudit(t, root)
	if !hasCode(report, CodeMissingInitSegment) {
		t.Fatalf("expected missing_init_segment, got %+v", report.Issues)
	}
}

func TestAuditDASH_TimelineGap(t *testing.T) {
	root := dashFixture(t)
	replaceInFile(t, root, "manifest.mpd",
		"<S d=\"30720\" />", "<S t=\"200000\" d=\"30720\" />")
	report := mustAudit(t, root)

	issue := findIssue(report, CodeTimelineGap)
	if issue == nil {
		t.Fatalf("expected timeline_gap, got %+v", report.Issues)
	}
	if issue.Time != 10.0 {
		t.Errorf("gap time = %v, want 10s", issue.Time)
	}
}

func TestAuditDASH_TimelineOverlap(t *testing.T) {
	root := dashFixture(t)
	// Second video entry starts at 50000 units while the first entry ends at
	// 76800 (two repeats of 76800 start at 0, so cursor is 153600); any
	// explicit t below the cursor is an overlap.
	replaceInFile(t, root, "manifest.mpd",
		"<S d=\"30720\" />", "<S t=\"50000\" d=\"30720\" />")
	report := mustAudit(t, root)
	if !hasCode(report, CodeTimelineOverlap) {
		t.Fatalf("expected timeline_overlap, got %+v", report.Issues)
	}
}

func TestAuditDASH_DurationMismatch(t *testing.T) {
	root := dashFixture(t)
	// Stretch rendition 1's first timeline entry so its total drifts past the
	// tolerance relative to rendition 0 (both video tracks are compared).
	old := `<Representation id="1" mimeType="video/mp4" codecs="avc1.f4001f" bandwidth="267816" width="854" height="480">
				<SegmentTemplate timescale="15360" initialization="init-stream$RepresentationID$.m4s" media="chunk-stream$RepresentationID$-$Number$.m4s" startNumber="1">
					<SegmentTimeline>
						<S t="0" d="76800" r="1" />`
	new := strings.Replace(old, `d="76800"`, `d="153600"`, 1)
	replaceInFile(t, root, "manifest.mpd", old, new)
	report := mustAudit(t, root)
	if !hasCode(report, CodeDurationMismatch) {
		t.Fatalf("expected duration_mismatch, got %+v", report.Issues)
	}
}

func TestAuditDASH_BadSubtitleReference(t *testing.T) {
	root := dashSubtitleFixture(t)

	if report := mustAudit(t, root); !report.OK() {
		t.Fatalf("healthy DASH subtitle fixture reported issues: %+v", report.Issues)
	}

	removeFile(t, root, "sub_en.vtt")
	report := mustAudit(t, root)
	issue := findIssue(report, CodeMissingFile)
	if issue == nil || issue.Path != "sub_en.vtt" || issue.Track != "subtitle" {
		t.Fatalf("expected missing DASH subtitle payload, got %+v", report.Issues)
	}
}

func TestAuditDASH_SubtitleWithoutBaseURL(t *testing.T) {
	root := dashFixture(t)
	replaceInFile(t, root, "manifest.mpd", "\t</Period>", `		<AdaptationSet id="100" contentType="text" mimeType="text/vtt">
			<Representation id="sub_en" bandwidth="256" />
		</AdaptationSet>
	</Period>`)
	report := mustAudit(t, root)
	if !hasCode(report, CodeTrackDeclarationInvalid) {
		t.Fatalf("expected track_declaration_invalid, got %+v", report.Issues)
	}
}

func TestAuditDASH_MalformedManifest(t *testing.T) {
	root := dashFixture(t)
	writeFile(t, filepath.Join(root, "manifest.mpd"), "<MPD><unclosed>")
	report := mustAudit(t, root)
	if !hasCode(report, CodeManifestInvalid) {
		t.Fatalf("expected manifest_invalid for malformed XML, got %+v", report.Issues)
	}
}

func TestAuditDASH_UnknownContentType(t *testing.T) {
	root := dashFixture(t)
	replaceInFile(t, root, "manifest.mpd",
		`contentType="audio"`, `contentType="hologram"`)
	report := mustAudit(t, root)
	if !hasCode(report, CodeTrackDeclarationInvalid) {
		t.Fatalf("expected track_declaration_invalid, got %+v", report.Issues)
	}
}

// ---------------------------------------------------------------------------
// Limits
// ---------------------------------------------------------------------------

func TestAudit_ManifestTooLarge(t *testing.T) {
	root := hlsFixture(t)
	report := mustAudit(t, root, WithMaxManifestBytes(50))
	if !hasCode(report, CodeManifestTooLarge) {
		t.Fatalf("expected manifest_too_large, got %+v", report.Issues)
	}
}

func TestAudit_RenditionLimit(t *testing.T) {
	root := hlsFixture(t)
	report := mustAudit(t, root, WithMaxRenditions(1))
	if !hasCode(report, CodeRenditionLimitExceeded) {
		t.Fatalf("expected rendition_limit_exceeded, got %+v", report.Issues)
	}
}

func TestAudit_SegmentLimit(t *testing.T) {
	root := hlsFixture(t)
	report := mustAudit(t, root, WithMaxSegmentsPerRendition(2))
	if !hasCode(report, CodeSegmentLimitExceeded) {
		t.Fatalf("expected segment_limit_exceeded, got %+v", report.Issues)
	}
}

func TestAudit_MaxIssuesCap(t *testing.T) {
	root := hlsFixture(t)
	// Every segment becomes an issue; the cap must bound the report.
	removeFile(t, root, "seg_0_0.m4s")
	removeFile(t, root, "seg_0_1.m4s")
	removeFile(t, root, "seg_0_2.m4s")
	removeFile(t, root, "seg_1_0.m4s")
	report := mustAudit(t, root, WithMaxIssues(2))
	if len(report.Issues) != 2 {
		t.Fatalf("issues = %d, want capped at 2", len(report.Issues))
	}
}

// ---------------------------------------------------------------------------
// Determinism and rendering
// ---------------------------------------------------------------------------

func TestAudit_StableSortingAndDeterminism(t *testing.T) {
	root := hlsFixture(t)
	removeFile(t, root, "seg_0_1.m4s")
	removeFile(t, root, "init_1.mp4")
	writeFile(t, filepath.Join(root, "seg_0_99.m4s"), mediaPlaceholder)

	first := mustAudit(t, root)
	second := mustAudit(t, root)

	if len(first.Issues) < 3 {
		t.Fatalf("expected multiple issues, got %d", len(first.Issues))
	}
	// Errors sort before warnings.
	if first.Issues[0].Severity != SeverityError {
		t.Errorf("first issue severity = %q, want error", first.Issues[0].Severity)
	}
	last := first.Issues[len(first.Issues)-1]
	if last.Severity != SeverityWarning {
		t.Errorf("last issue severity = %q, want warning", last.Severity)
	}
	// Two runs produce byte-identical text and JSON rendering.
	if first.FormatText() != second.FormatText() {
		t.Error("text rendering is not deterministic across runs")
	}
	firstJSON, _ := first.FormatJSON()
	secondJSON, _ := second.FormatJSON()
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Error("JSON rendering is not deterministic across runs")
	}
}

func TestAudit_JSONRoundTrip(t *testing.T) {
	root := dashFixture(t)
	report := mustAudit(t, root)

	data, err := report.FormatJSON()
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal report JSON: %v", err)
	}
	if decoded.Format != FormatDASH || len(decoded.Renditions) != 3 {
		t.Fatalf("decoded report mismatch: %+v", decoded)
	}
}

func TestReport_TextMentionsCodes(t *testing.T) {
	root := hlsFixture(t)
	removeFile(t, root, "seg_0_0.m4s")
	report := mustAudit(t, root)

	text := report.FormatText()
	if !strings.Contains(text, string(CodeMissingFile)) {
		t.Errorf("text report missing code %q:\n%s", CodeMissingFile, text)
	}
	if !strings.Contains(text, "Mosaic Packaging Audit") {
		t.Errorf("text report missing header:\n%s", text)
	}
}

func TestReport_Codes(t *testing.T) {
	root := hlsFixture(t)
	removeFile(t, root, "seg_0_0.m4s")
	removeFile(t, root, "seg_1_0.m4s")
	report := mustAudit(t, root)

	codes := report.Codes()
	if len(codes) != 1 || codes[0] != CodeMissingFile {
		t.Fatalf("codes = %v, want only %s", codes, CodeMissingFile)
	}
}
