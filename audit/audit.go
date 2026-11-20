// Package audit provides read-only verification of packaged HLS and DASH CMAF
// output directories.
//
// An audit never re-encodes or modifies anything. It parses the generated
// manifests and playlists, then cross-checks every declared reference against
// the files on disk: rendition playlists, initialization segments, media
// segments, subtitle tracks, segment numbering, and timeline continuity.
//
// All findings are returned as a deterministically ordered Report so that the
// same input always produces the same output, which makes audits safe to use
// in CI pipelines and post-upload verification jobs.
package audit

import (
	"errors"
	"os"
	"sort"
	"strings"
)

// Format identifiers reported by an audit.
const (
	FormatHLS  = "hls"
	FormatDASH = "dash"
)

// Sentinel errors returned for setup failures that prevent an audit from
// running. Content problems discovered inside a valid package are reported as
// Issues instead.
var (
	// ErrDirNotFound is returned when the audited directory does not exist.
	ErrDirNotFound = errors.New("audit: output directory not found")
	// ErrEmptyDirectory is returned when the audited directory contains no files.
	ErrEmptyDirectory = errors.New("audit: output directory is empty")
	// ErrNoManifest is returned when neither an HLS master playlist nor a
	// DASH manifest can be found in the audited directory.
	ErrNoManifest = errors.New("audit: no HLS or DASH manifest found in directory")
)

// Severity classifies an Issue by operational impact.
type Severity string

const (
	// SeverityError describes a defect that will break playback or indicates
	// that the package is incomplete (missing files, escaping references,
	// broken timelines, exceeded safety limits).
	SeverityError Severity = "error"
	// SeverityWarning describes a deviation that players may tolerate but
	// should be investigated (duration drift, unreferenced leftover files).
	SeverityWarning Severity = "warning"
)

// Code is a stable machine-readable identifier for an Issue. The set of codes
// is part of the package API and is safe to switch on in calling code.
type Code string

const (
	// CodeManifestTooLarge: a manifest/playlist exceeds Options.MaxManifestBytes.
	CodeManifestTooLarge Code = "manifest_too_large"
	// CodeManifestUnreadable: a manifest/playlist exists but cannot be read.
	CodeManifestUnreadable Code = "manifest_unreadable"
	// CodeManifestInvalid: a manifest/playlist is malformed or missing required tags.
	CodeManifestInvalid Code = "manifest_invalid"
	// CodePathEscape: a reference resolves outside the audited directory.
	CodePathEscape Code = "path_escape"
	// CodeMissingFile: a manifest references a file that does not exist.
	CodeMissingFile Code = "missing_file"
	// CodeEmptyFile: a referenced segment or manifest exists but is zero bytes.
	CodeEmptyFile Code = "empty_file"
	// CodeMissingInitSegment: a fragmented-MP4 rendition declares no init segment
	// or its init segment is missing.
	CodeMissingInitSegment Code = "missing_init_segment"
	// CodeDuplicateSegment: the same segment is referenced more than once.
	CodeDuplicateSegment Code = "duplicate_segment"
	// CodeTimelineGap: a DASH SegmentTimeline leaves a gap between entries.
	CodeTimelineGap Code = "timeline_gap"
	// CodeTimelineOverlap: a DASH SegmentTimeline entry starts before the
	// previous entry ends, or an HLS playlist re-declares an occupied interval.
	CodeTimelineOverlap Code = "timeline_overlap"
	// CodeInvalidDuration: a declared segment duration is zero, negative, or
	// exceeds the rendition's target duration beyond tolerance.
	CodeInvalidDuration Code = "invalid_duration"
	// CodeDurationMismatch: renditions of the same track kind declare
	// materially different total durations.
	CodeDurationMismatch Code = "duration_mismatch"
	// CodeTrackDeclarationInvalid: a master/manifest track declaration is
	// incomplete, inconsistent, or unsupported.
	CodeTrackDeclarationInvalid Code = "track_declaration_invalid"
	// CodeUnreferencedSegment: a media file on disk is not referenced by any
	// manifest or playlist.
	CodeUnreferencedSegment Code = "unreferenced_segment"
	// CodeRenditionLimitExceeded: more renditions than Options.MaxRenditions.
	CodeRenditionLimitExceeded Code = "rendition_limit_exceeded"
	// CodeSegmentLimitExceeded: a rendition declares more segments than
	// Options.MaxSegmentsPerRendition.
	CodeSegmentLimitExceeded Code = "segment_limit_exceeded"
	// CodeEmptyRendition: a rendition playlist/representation declares no media.
	CodeEmptyRendition Code = "empty_rendition"
)

// Default safety limits. They protect auditors from pathological or hostile
// packages; any override must stay positive.
const (
	DefaultMaxManifestBytes        int64 = 1 << 20 // 1 MiB per manifest/playlist
	DefaultMaxRenditions                 = 32
	DefaultMaxSegmentsPerRendition       = 10_000
	DefaultMaxIssues                     = 1_000
	DefaultTimelineTolerance             = 0.25 // seconds
	DefaultDurationDriftTolerance        = 1.0  // seconds between renditions
)

// Options configures an Audit. Zero values are replaced by the defaults; use
// the With* functional options to override individual fields.
type Options struct {
	// Format forces a packaging format: FormatHLS, FormatDASH, or "" for
	// automatic detection.
	Format string
	// MaxManifestBytes is the maximum accepted size of a single manifest or
	// playlist. Larger files are flagged and skipped.
	MaxManifestBytes int64
	// MaxRenditions caps how many renditions (including subtitle tracks) are audited.
	MaxRenditions int
	// MaxSegmentsPerRendition caps the number of media segments per rendition.
	MaxSegmentsPerRendition int
	// MaxIssues caps the number of issues collected in a Report.
	MaxIssues int
	// TimelineTolerance is the maximum acceptable DASH timeline discontinuity
	// in seconds before it is reported as a gap or overlap.
	TimelineTolerance float64
	// DurationDriftTolerance is the maximum acceptable total-duration drift
	// between renditions of the same track kind.
	DurationDriftTolerance float64
}

// Option customizes an Audit.
type Option func(*Options)

// WithFormat forces the packaging format ("hls" or "dash"). An empty string
// restores automatic detection.
func WithFormat(format string) Option {
	return func(o *Options) { o.Format = strings.ToLower(strings.TrimSpace(format)) }
}

// WithMaxManifestBytes overrides the per-manifest size limit.
func WithMaxManifestBytes(n int64) Option {
	return func(o *Options) { o.MaxManifestBytes = n }
}

// WithMaxRenditions overrides the rendition count limit.
func WithMaxRenditions(n int) Option {
	return func(o *Options) { o.MaxRenditions = n }
}

// WithMaxSegmentsPerRendition overrides the per-rendition segment limit.
func WithMaxSegmentsPerRendition(n int) Option {
	return func(o *Options) { o.MaxSegmentsPerRendition = n }
}

// WithMaxIssues overrides the issue collection cap.
func WithMaxIssues(n int) Option {
	return func(o *Options) { o.MaxIssues = n }
}

// WithTimelineTolerance overrides the DASH timeline discontinuity tolerance.
func WithTimelineTolerance(seconds float64) Option {
	return func(o *Options) { o.TimelineTolerance = seconds }
}

// WithDurationDriftTolerance overrides the cross-rendition duration drift tolerance.
func WithDurationDriftTolerance(seconds float64) Option {
	return func(o *Options) { o.DurationDriftTolerance = seconds }
}

func (o *Options) normalize() {
	if o.MaxManifestBytes <= 0 {
		o.MaxManifestBytes = DefaultMaxManifestBytes
	}
	if o.MaxRenditions <= 0 {
		o.MaxRenditions = DefaultMaxRenditions
	}
	if o.MaxSegmentsPerRendition <= 0 {
		o.MaxSegmentsPerRendition = DefaultMaxSegmentsPerRendition
	}
	if o.MaxIssues <= 0 {
		o.MaxIssues = DefaultMaxIssues
	}
	if o.TimelineTolerance <= 0 {
		o.TimelineTolerance = DefaultTimelineTolerance
	}
	if o.DurationDriftTolerance <= 0 {
		o.DurationDriftTolerance = DefaultDurationDriftTolerance
	}
	o.Format = strings.ToLower(strings.TrimSpace(o.Format))
}

// Issue describes a single packaging defect. All fields are stable across runs
// for the same input so reports can be diffed.
type Issue struct {
	// Severity is SeverityError or SeverityWarning.
	Severity Severity `json:"severity"`
	// Code is the stable machine-readable defect identifier.
	Code Code `json:"code"`
	// Format is the packaging format ("hls" or "dash").
	Format string `json:"format"`
	// Manifest is the path of the manifest declaring the problem, relative to
	// the audited directory.
	Manifest string `json:"manifest"`
	// Rendition is the playlist name or DASH Representation ID. Empty for
	// package-level problems.
	Rendition string `json:"rendition,omitempty"`
	// Track is the track kind: "video", "audio", or "subtitle".
	Track string `json:"track,omitempty"`
	// Path is the offending referenced path, relative to the audited
	// directory when resolvable.
	Path string `json:"path,omitempty"`
	// Message is a human-readable explanation.
	Message string `json:"message"`
	// Time is the timeline position of the problem in seconds, or 0 when the
	// problem is not tied to a timeline position.
	Time float64 `json:"time,omitempty"`
}

// RenditionReport summarizes one audited rendition playlist (HLS) or
// Representation (DASH).
type RenditionReport struct {
	// Name is the playlist file name or Representation ID.
	Name string `json:"name"`
	// Track is the track kind: "video", "audio", or "subtitle".
	Track string `json:"track"`
	// Manifest is the playlist or manifest path relative to the root.
	Manifest string `json:"manifest"`
	// InitSegment is the initialization segment path relative to the root,
	// empty for container formats without one.
	InitSegment string `json:"init_segment,omitempty"`
	// MediaSegments lists the referenced media segment paths relative to the
	// root, in playlist order.
	MediaSegments []string `json:"media_segments"`
	// SegmentCount is len(MediaSegments).
	SegmentCount int `json:"segment_count"`
	// Duration is the sum of declared segment durations in seconds.
	Duration float64 `json:"duration_seconds"`
}

// Report is the result of an Audit.
type Report struct {
	// Format is the detected or forced packaging format.
	Format string `json:"format"`
	// RootDir is the audited directory.
	RootDir string `json:"root_dir"`
	// Manifests lists every audited manifest/playlist relative to RootDir, sorted.
	Manifests []string `json:"manifests"`
	// Renditions summarizes every audited rendition, sorted by name.
	Renditions []RenditionReport `json:"renditions"`
	// Issues holds every defect, sorted by severity, manifest, rendition, time,
	// code, and path.
	Issues []Issue `json:"issues"`
	// ErrorCount is the number of SeverityError issues.
	ErrorCount int `json:"error_count"`
	// WarningCount is the number of SeverityWarning issues.
	WarningCount int `json:"warning_count"`
}

// OK reports whether the package has no issues at all.
func (r *Report) OK() bool { return len(r.Issues) == 0 }

// HasErrors reports whether the package contains any severity-error defect.
func (r *Report) HasErrors() bool { return r.ErrorCount > 0 }

// Audit inspects an existing HLS or DASH output directory without modifying it.
//
// Setup failures (missing or empty directory, no recognizable manifest) return
// a sentinel error and a nil report. Every packaging defect found in a
// readable package is returned inside the Report as an Issue instead.
func Audit(rootDir string, opts ...Option) (*Report, error) {
	options := Options{}
	for _, opt := range opts {
		opt(&options)
	}
	options.normalize()

	info, err := os.Stat(rootDir)
	if err != nil || !info.IsDir() {
		return nil, ErrDirNotFound
	}

	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil, ErrDirNotFound
	}
	if len(entries) == 0 {
		return nil, ErrEmptyDirectory
	}

	auditor := &auditor{
		root:    rootDir,
		options: options,
		report: &Report{
			RootDir: rootDir,
		},
		referenced: make(map[string]struct{}),
		seenFiles:  make(map[string]struct{}),
	}

	switch options.Format {
	case FormatHLS:
		auditor.report.Format = FormatHLS
		if !auditor.fileExists(hlsMasterName) {
			return nil, ErrNoManifest
		}
		auditor.auditHLS()
	case FormatDASH:
		auditor.report.Format = FormatDASH
		if !auditor.fileExists(dashManifestName) {
			return nil, ErrNoManifest
		}
		auditor.auditDASH()
	default:
		switch {
		case auditor.fileExists(hlsMasterName):
			auditor.report.Format = FormatHLS
			auditor.auditHLS()
		case auditor.fileExists(dashManifestName):
			auditor.report.Format = FormatDASH
			auditor.auditDASH()
		default:
			return nil, ErrNoManifest
		}
	}

	auditor.checkUnreferenced()
	auditor.finalize()
	return auditor.report, nil
}

// auditor carries shared state for one audit run.
// auditor carries shared state for one audit run.
type auditor struct {
	seenFiles  map[string]struct{} // absolute paths of regular files on disk
	referenced map[string]struct{} // absolute paths referenced by manifests
	report     *Report
	root       string
	options    Options
}

// addIssue records an issue, honoring the MaxIssues cap.
func (a *auditor) addIssue(issue Issue) {
	if len(a.report.Issues) >= a.options.MaxIssues {
		return
	}
	if issue.Severity == SeverityError {
		a.report.ErrorCount++
	} else {
		a.report.WarningCount++
	}
	a.report.Issues = append(a.report.Issues, issue)
}

// finalize sorts every report collection for deterministic, diff-friendly output.
func (a *auditor) finalize() {
	sort.Strings(a.report.Manifests)
	sort.Slice(a.report.Renditions, func(i, j int) bool {
		if a.report.Renditions[i].Name != a.report.Renditions[j].Name {
			return a.report.Renditions[i].Name < a.report.Renditions[j].Name
		}
		return a.report.Renditions[i].Track < a.report.Renditions[j].Track
	})
	sort.SliceStable(a.report.Issues, func(i, j int) bool {
		x, y := a.report.Issues[i], a.report.Issues[j]
		if x.Severity != y.Severity {
			return x.Severity == SeverityError // errors first
		}
		if x.Manifest != y.Manifest {
			return x.Manifest < y.Manifest
		}
		if x.Rendition != y.Rendition {
			return x.Rendition < y.Rendition
		}
		if x.Track != y.Track {
			return x.Track < y.Track
		}
		if x.Time != y.Time {
			return x.Time < y.Time
		}
		if x.Code != y.Code {
			return x.Code < y.Code
		}
		return x.Path < y.Path
	})
}
